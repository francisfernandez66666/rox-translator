// ============ 本文件职责中文说明 ============
// 订阅续费宽限期（★ #74，2026-09-23）调度层单测，复用 pay_renew_test.go 的 renewFixture：
//
//	A) 宽限期内不摘身份：到期后跑一轮订阅扫描，package_code / package_expires_at 原样保留，
//	   并落 grace_expires_at（= 到期时刻 + 宽限天数）、发一条「进入宽限期」站内信（重复扫描不重发）；
//	B) 宽限期结束摘身份：grace_expires_at 已越过的，扫描按原逻辑 ExpirePackage 并改发「宽限期结束」文案；
//	C) 关闭自动续费的租户不进宽限期：到期即刻摘除（保持 #74 之前的行为，不给无续费意愿者延长收费窗口）；
//	D) 宽限天数走配置：subscription_grace_days=0 关闭宽限期、=6 则按 6 天起算（环境变量 > 库配置 > 默认 3）；
//	E) 续费单重试同日不重复建单：pending 单被超时关单后同日再扫描也不补建，跨到下一阶梯日才补建；
//	F) /api/me/package 透出 in_grace / grace_expires（前端「已到期，宽限期至 X 日」提示的数据源）；
//	G) handleExpiredSubscription 逐条裁决：未开自动续费 false、宽限天数 0 false、
//	   首轮 true、次轮 true 且通知不重发（NotifiedGrace 去重）、窗口不逐轮重新起算；
//	H) 配置优先序与防呆：env SUBSCRIPTION_GRACE_DAYS > system_config > 默认 3，非法值（负数/365/非数字）
//	   回落默认，clampGraceDays 合法区间 [0,90]；renewalLeadDays 同口径（env > 库 > T-3）；
//	I) graceDeadline：上一期遗留值（g ≤ exp）判无效并按本期到期时刻重新起算（否则新一期宽限期形同虚设）；
//	J) 宽限期截止时刻落库失败 → 不给宽限期也不发通知（先落库、再通知的次序锁）；
//	K) 阶梯建单两层去重：窗口外不建单；同包 pending 单跳过且**不消耗**今日格子（关单后同日可补建）；
//	   真正建过单的那日格子已 created，重复触发与扫描都不再建；
//	L) 宽限期内按日补建：同日不堆单，跨日（renewalAttemptClock 桩）旧单关闭后补建一张且身份保留；
//	M) 建单失败路径：抢到格子后建单失败 → 格子落 failed + 原因非空，同日不重试、跨日才重试；
//	N) 续费到账（真实 MarkOrderPaid）后跑一轮扫描清掉遗留宽限期键，读侧口径同步；
//	O) subscriptionGraceState 纯函数口径：无包/试用/未到期/宽限期已过/遗留值一律 false。
//
// ★ 为什么宽限/摘除类断言一律用「第二家租户」（graceTenant）：runSubscriptionScan 按设计
//
//	跳过租户 1（平台宿主，无订阅概念），而 renewFixture 的测试租户恰好落在 ID=1；
//	直接用它会得到「扫描什么都不做」的假绿。
//
// ★ 单测自钉 sqlite 方言由 newRenewFixture 承担（AGENTS.md §4）；建单日期口径经
//
//	renewalAttemptClock 变量注入，否则无法稳定复现「同日二次触发」这条分支。
//
// ==========================================
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// graceTenant 在夹具库里再开一家企业租户（ID>1）+ 一名 tenant_admin，
// 并按给定到期时刻与自动续费开关预置订阅身份。
// 套餐沿用夹具里的平台包 renew_month（tenant_id=0，任意租户可订阅），断言金额/去重都够真。
func (f *renewFixture) graceTenant(t *testing.T, expires time.Time, autoRenew bool) int64 {
	t.Helper()
	perms := tenant.Perms{
		PackageCode: "renew_month", PackageExpires: expires.Format(time.RFC3339),
		AutoRenew: autoRenew, SentenceBalance: 100,
	}
	b, _ := json.Marshal(perms)
	seq := time.Now().UnixNano()
	co, err := f.srv.Ten.Create(fmt.Sprintf("t_grace_%d", seq), "宽限期测试公司", "", string(b))
	if err != nil {
		t.Fatalf("创建宽限期租户失败: %v", err)
	}
	if co.ID <= 1 {
		t.Fatalf("夹具租户 ID 必须 >1，否则订阅扫描会按设计跳过平台宿主租户（got %d）", co.ID)
	}
	if _, err := f.srv.Store.CreateUser(co.ID, fmt.Sprintf("grace_admin_%d", seq), "hash-grace",
		"宽限期管理员", store.RoleTenantAdmin, 1, 0); err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	return co.ID
}

// setPerms 改写某租户的订阅到期时刻（模拟「已过到期日」）。
func (f *renewFixture) setExpires(t *testing.T, tid int64, offset time.Duration) {
	t.Helper()
	p, err := f.srv.Store.GetTenantPerms(tid)
	if err != nil {
		t.Fatalf("读取权限失败: %v", err)
	}
	p.PackageExpires = time.Now().Add(offset).Format(time.RFC3339)
	if err := f.srv.Store.SaveTenantPerms(tid, p); err != nil {
		t.Fatalf("改写到期时刻失败: %v", err)
	}
}

// permsOf 读取某租户权限快照。
func (f *renewFixture) permsOf(t *testing.T, tid int64) *tenant.Perms {
	t.Helper()
	p, err := f.srv.Store.GetTenantPerms(tid)
	if err != nil {
		t.Fatalf("读取权限失败: %v", err)
	}
	return p
}

// injectExpiry 改写夹具租户自身的到期时刻（offset 为负=已到期）。
func (f *renewFixture) injectExpiry(t *testing.T, offset time.Duration) {
	t.Helper()
	f.setExpires(t, f.tid, offset)
}

// notifCount 按标题精确统计站内信条数（去重断言用）。
func (f *renewFixture) notifCount(t *testing.T, title string) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow("SELECT COUNT(1) FROM notifications WHERE title=?", title).Scan(&n); err != nil {
		t.Fatalf("统计站内信失败: %v", err)
	}
	return n
}

// notifCountPrefix 按标题前缀统计站内信条数（标题含日期等动态值时用）。
func (f *renewFixture) notifCountPrefix(t *testing.T, prefix string) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM notifications WHERE title LIKE ?", prefix+"%").Scan(&n); err != nil {
		t.Fatalf("统计站内信失败: %v", err)
	}
	return n
}

// systemOrders 统计某租户由系统自动建的续费单总数（含已取消——「同日不重复建单」必须看总数）。
func (f *renewFixture) systemOrders(t *testing.T, tid int64) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM orders WHERE tenant_id=? AND created_by=0", tid).Scan(&n); err != nil {
		t.Fatalf("统计系统续费单失败: %v", err)
	}
	return n
}

// A：到期后进入宽限期——身份保留、宽限期截止时刻落库、通知只发一次。
func TestGraceKeepsIdentityWithinPeriod(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(-2*time.Hour), true) // 已到期 2 小时

	f.srv.runSubscriptionScan()

	after := f.permsOf(t, tid)
	if after.PackageCode != "renew_month" {
		t.Fatalf("宽限期内订阅身份必须保留，实际 package_code=%q", after.PackageCode)
	}
	if after.GraceExpiresAt == "" {
		t.Fatal("宽限期截止时刻未落库（grace_expires_at 为空）")
	}
	graceEnd, perr := time.Parse(time.RFC3339, after.GraceExpiresAt)
	if perr != nil {
		t.Fatalf("grace_expires_at 非 RFC3339: %q", after.GraceExpiresAt)
	}
	// 默认宽限 3 天：截止时刻 = 到期时刻 + 3 天（容 1 分钟，避开解析与时钟抖动）
	wantEnd := time.Now().Add(-2*time.Hour).AddDate(0, 0, 3)
	if d := graceEnd.Sub(wantEnd); d > time.Minute || d < -time.Minute {
		t.Fatalf("宽限期截止时刻应为到期时刻+3天：want %v got %v", wantEnd, graceEnd)
	}
	if n := f.notifCountPrefix(t, "订阅已到期，宽限期至"); n != 1 {
		t.Fatalf("进入宽限期应发一条站内信，实际 %d", n)
	}
	// 重复扫描（同一天再跑一轮）不得重发进入宽限期的通知，也不得把租户摘掉
	f.srv.runSubscriptionScan()
	if n2 := f.notifCountPrefix(t, "订阅已到期，宽限期至"); n2 != 1 {
		t.Fatalf("宽限期通知应去重，二次扫描后实际 %d 条", n2)
	}
	if p := f.permsOf(t, tid); p.PackageCode != "renew_month" {
		t.Fatal("二次扫描不应把宽限期内的租户摘掉")
	}
}

// B：宽限期结束仍未到账 → 摘身份并发「宽限期结束」文案（与未进宽限期的旧文案区分开）。
func TestGraceEndRemovesIdentity(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(-5*24*time.Hour), true) // 到期 5 天前，3 天宽限期早已结束
	// 补一个「本期有效但已跨过」的宽限期截止时刻（晚于到期时刻才算本期，graceDeadline 的判据）
	if err := f.srv.Store.SetSubscriptionGrace(tid, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("预置宽限期截止时刻失败: %v", err)
	}
	if p := f.permsOf(t, tid); p.GraceExpiresAt == "" {
		t.Fatal("预置宽限期截止时刻未落库")
	}
	f.srv.runSubscriptionScan()

	after := f.permsOf(t, tid)
	if after.PackageCode != "" {
		t.Fatalf("宽限期结束应摘除订阅身份，实际 package_code=%q", after.PackageCode)
	}
	if after.GraceExpiresAt != "" || after.NotifiedGrace {
		t.Fatalf("摘除时须一并清零宽限期键（否则下一期到期即刻被摘）: %+v", after)
	}
	if n := f.notifCount(t, "宽限期结束，订阅已失效"); n != 1 {
		t.Fatalf("宽限期结束应发一条站内信，实际 %d", n)
	}
	// 精确标题比对：宽限期那条文案是「订阅已到期，宽限期至 X」，用前缀会把两条混为一谈
	if n := f.notifCount(t, "订阅已到期"); n != 0 {
		t.Fatalf("经历过宽限期的租户不该收到旧文案「订阅已到期」，实际 %d", n)
	}
}

// C：关闭自动续费的租户不进宽限期（无续费意愿还延长收费窗口没有业务依据）。
func TestNoGraceWithoutAutoRenew(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(-2*time.Hour), false) // auto_renew=false
	f.srv.runSubscriptionScan()

	after := f.permsOf(t, tid)
	if after.PackageCode != "" {
		t.Fatalf("关闭自动续费者到期即应摘除，实际 package_code=%q", after.PackageCode)
	}
	if after.GraceExpiresAt != "" {
		t.Fatalf("关闭自动续费不应进宽限期，实际 grace_expires_at=%q", after.GraceExpiresAt)
	}
	if n := f.notifCount(t, "订阅已到期"); n != 1 {
		t.Fatalf("未进宽限期的摘除应发旧文案「订阅已到期」，实际 %d", n)
	}
}

// D：宽限天数走配置（subscription_grace_days：0=关闭、6=按 6 天起算）。
func TestGraceDaysFromConfig(t *testing.T) {
	f := newRenewFixture(t)
	// ① 配 0：显式关闭宽限期，行为回退到「到期即刻摘除」
	if err := f.srv.Store.SetConfig("subscription_grace_days", "0"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	tidOff := f.graceTenant(t, time.Now().Add(-2*time.Hour), true)
	f.srv.runSubscriptionScan()
	if p := f.permsOf(t, tidOff); p.PackageCode != "" {
		t.Fatalf("subscription_grace_days=0 应关闭宽限期即刻摘除，实际 package_code=%q", p.PackageCode)
	}
	if p := f.permsOf(t, tidOff); p.GraceExpiresAt != "" {
		t.Fatalf("关闭宽限期不应落 grace_expires_at，实际 %q", p.GraceExpiresAt)
	}
	// ② 配 6：宽限期按 6 天起算
	if err := f.srv.Store.SetConfig("subscription_grace_days", "6"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	tid6 := f.graceTenant(t, time.Now().Add(-3*time.Hour), true)
	f.srv.runSubscriptionScan()
	p6 := f.permsOf(t, tid6)
	if p6.PackageCode != "renew_month" {
		t.Fatalf("自定义 6 天宽限期内应保留身份，实际 package_code=%q", p6.PackageCode)
	}
	ge, err := time.Parse(time.RFC3339, p6.GraceExpiresAt)
	if err != nil {
		t.Fatalf("grace_expires_at 解析失败: %v (%q)", err, p6.GraceExpiresAt)
	}
	want := time.Now().Add(-3*time.Hour).AddDate(0, 0, 6)
	if d := ge.Sub(want); d > time.Minute || d < -time.Minute {
		t.Fatalf("宽限期截止应按配置 6 天算：want %v got %v", want, ge)
	}
}

// E：续费单按日重试，但同一天绝不重复建单（pending 单被超时关单也拦得住，靠的是同日唯一键）。
func TestRenewalRetryNotDuplicatedSameDay(t *testing.T) {
	f := newRenewFixture(t)
	// 剩 2 天：处于 T-3 阶梯内（扫描侧 daysLeft 整除截断后同样命中）
	tid := f.graceTenant(t, time.Now().Add(2*24*time.Hour), true)
	pkg, err := f.srv.Store.GetPackageByCode(tid, "renew_month")
	if err != nil || pkg == nil {
		t.Fatalf("读取平台包失败: %v", err)
	}

	f.srv.runSubscriptionScan()
	if n := f.systemOrders(t, tid); n != 1 {
		t.Fatalf("首个阶梯日应建 1 张系统续费单，实际 %d", n)
	}
	if got := f.srv.Store.RenewalAttemptsToday(tid, pkg.ID, time.Now().Format("2006-01-02")); got != 1 {
		t.Fatalf("今日建单台账应为 1 条，实际 %d", got)
	}
	// 挂单被超时关单（等价 CloseStalePendingOrders：此刻已无 pending，去重只能靠同日唯一键）
	if _, err := f.srv.Store.DB().Exec(
		"UPDATE orders SET status='cancelled' WHERE tenant_id=? AND created_by=0", tid); err != nil {
		t.Fatalf("关单失败: %v", err)
	}
	f.srv.runSubscriptionScan()
	if n := f.systemOrders(t, tid); n != 1 {
		t.Fatalf("同一天重复扫描不得补建（今日格子已消耗），实际 %d 张", n)
	}
	// 跨到下一个阶梯日：应补建第 2 张——这就是「未到账/失败按日重试」
	old := renewalAttemptClock
	renewalAttemptClock = func() time.Time { return time.Now().Add(24 * time.Hour) }
	t.Cleanup(func() { renewalAttemptClock = old })
	perms := f.permsOf(t, tid)
	if !f.srv.maybeCreateRenewalOrder(tid, perms, 1) {
		t.Fatal("次日应可重新建单（按日重试）")
	}
	if n := f.systemOrders(t, tid); n != 2 {
		t.Fatalf("次日应补建第 2 张，实际 %d", n)
	}
}

// F：/api/me/package 透出宽限期状态（前端「已到期，宽限期至 X 日」提示的数据源）。
// 本用例只验「读侧口径」，故直接预置 permissions 两键（夹具租户落在 ID=1，
// 订阅扫描按设计跳过平台宿主租户，靠扫描造状态交给 A/B/D/E 用 ID>1 的租户去验）。
func TestMyPackageExposesGraceState(t *testing.T) {
	f := newRenewFixture(t)
	f.subscribe(t)
	rec := f.do(t, http.MethodGet, "/api/me/package", nil)
	var before map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
		t.Fatal(err)
	}
	if before["in_grace"] == true || before["grace_expires"] != "" {
		t.Fatalf("未到期不应报宽限期: %v", before)
	}
	// 到期 + 宽限期截止在未来 → in_grace=true 并给出截止时刻
	f.setAutoRenew(t, true)
	f.injectExpiry(t, -2*time.Hour)
	if err := f.srv.Store.SetSubscriptionGrace(f.tid, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("预置宽限期失败: %v", err)
	}
	rec = f.do(t, http.MethodGet, "/api/me/package", nil)
	var after map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after["in_grace"] != true {
		t.Fatalf("宽限期内应透出 in_grace=true: %v", after)
	}
	if ge, _ := after["grace_expires"].(string); ge == "" {
		t.Fatalf("宽限期内应透出 grace_expires: %v", after)
	}
	if pc, _ := after["package_code"].(string); pc != "renew_month" {
		t.Fatalf("宽限期内订阅身份应仍可读出，实际 %q", pc)
	}
	// 宽限期已过（下一轮扫描会摘身份）：接口不再报宽限中，避免「界面说还在、后台已停」
	if err := f.srv.Store.SetSubscriptionGrace(f.tid, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("预置宽限期结束失败: %v", err)
	}
	rec = f.do(t, http.MethodGet, "/api/me/package", nil)
	var ended map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &ended); err != nil {
		t.Fatal(err)
	}
	if ended["in_grace"] == true || ended["grace_expires"] != "" {
		t.Fatalf("宽限期结束后不应再报宽限中: %v", ended)
	}
	// 遗留键失效兜底：上一期的 grace_expires_at 晚于「新一期到期时刻」以外的情形一律视为无效
	// （续费到账走 MarkOrderPaid 不清宽限期键，此处验读侧不把它当成新一期的宽限期）
	f.injectExpiry(t, 30*24*time.Hour) // 已续期：到期时刻被推到未来
	rec = f.do(t, http.MethodGet, "/api/me/package", nil)
	var renewed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &renewed); err != nil {
		t.Fatal(err)
	}
	if renewed["in_grace"] == true {
		t.Fatalf("已续期的订阅不得报宽限中: %v", renewed)
	}
}

// ---------- G~N：裁决函数本体、配置防呆、两层去重与到账清理 ----------

// graceNotifOf 按「租户 + 标题前缀」统计站内信（notifications.ref_type='tenant'、ref_id=tid）。
// WHY 带 ref 过滤：一个夹具库里会开多家宽限期租户，只按标题计数会把别家的通知算进来（假红/假绿都可能）。
func graceNotifOf(t *testing.T, f *renewFixture, tid int64, titlePrefix string) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM notifications WHERE ref_type='tenant' AND ref_id=? AND title LIKE ?",
		tid, titlePrefix+"%").Scan(&n); err != nil {
		t.Fatalf("统计站内信失败: %v", err)
	}
	return n
}

// graceCells 今日格子数（断言「pending 单去重不吃格子」这条阶梯口径）。
func graceCells(t *testing.T, f *renewFixture, tid, pkgID int64, day string) int {
	t.Helper()
	return f.srv.Store.RenewalAttemptsToday(tid, pkgID, day)
}

// graceCellRow 读取指定格子（不存在即判失败）。
func graceCellRow(t *testing.T, f *renewFixture, tid, pkgID int64, day string) (status, orderNo, reason string) {
	t.Helper()
	if err := f.srv.Store.DB().QueryRow(
		"SELECT status, order_no, reason FROM renewal_attempts WHERE tenant_id=? AND package_id=? AND attempt_date=?",
		tid, pkgID, day).Scan(&status, &orderNo, &reason); err != nil {
		t.Fatalf("读取格子 (%d,%d,%s) 失败: %v", tid, pkgID, day, err)
	}
	return
}

// graceToday 今日日期口径（跟随 renewalAttemptClock 桩：桩挪到明天它就是明天）。
func graceToday() string { return renewalAttemptClock().Format("2006-01-02") }

// graceStubClock 替换今日时钟并在用例结束复原（包级变量不复原会污染同包后续用例与 -race）。
func graceStubClock(t *testing.T, fn func() time.Time) {
	t.Helper()
	old := renewalAttemptClock
	renewalAttemptClock = fn
	t.Cleanup(func() { renewalAttemptClock = old })
}

// graceSetenv 设环境变量并在用例结束复原（os.Setenv 是进程级状态）。
func graceSetenv(t *testing.T, key, val string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Setenv(key, val); err != nil {
		t.Fatalf("设置环境变量 %s 失败: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
			return
		}
		_ = os.Unsetenv(key)
	})
}

// gracePkgID 取该租户可见的续费包 ID（平台包 tenant_id=0，任意租户按码可取）。
func gracePkgID(t *testing.T, f *renewFixture, tid int64) int64 {
	t.Helper()
	pkg, err := f.srv.Store.GetPackageByCode(tid, "renew_month")
	if err != nil || pkg == nil {
		t.Fatalf("读取平台包失败: %v", err)
	}
	return pkg.ID
}

// graceCloseStalePending 走真实超时关单链路：把该租户 pending 单的 created_at 回拨 20 分钟
// （超过默认 15 分钟窗口）后调 CloseStalePendingOrders。
// WHY 不手改 status：手工 UPDATE 会绕过渠道豁免口径（manual/usdt 已点付的单不该关）。
func graceCloseStalePending(t *testing.T, f *renewFixture, tid int64) int64 {
	t.Helper()
	if _, err := f.srv.Store.DB().Exec(
		"UPDATE orders SET created_at=? WHERE tenant_id=? AND status='pending'",
		time.Now().UTC().Add(-20*time.Minute).Format(time.RFC3339), tid); err != nil {
		t.Fatalf("回拨订单创建时间失败: %v", err)
	}
	return f.srv.Store.CloseStalePendingOrders()
}

// graceExpiry 取快照里的本期到期时刻（不可解析即判失败）。
func graceExpiry(t *testing.T, p *tenant.Perms) time.Time {
	t.Helper()
	exp, err := time.Parse(time.RFC3339, p.PackageExpires)
	if err != nil {
		t.Fatalf("到期时刻不可解析: %q", p.PackageExpires)
	}
	return exp
}

// graceAdminToken 为 graceTenant 建出的那名租户管理员签 JWT（走 /api/me/package 出参断言用）。
// WHY 走真实 HTTP：in_grace/grace_expires 是前端渲染的唯一数据源，只验纯函数会漏掉 handler 侧装配。
func graceAdminToken(t *testing.T, f *renewFixture, tid int64) string {
	t.Helper()
	admins := f.srv.Store.ListUsersByRole(tid, store.RoleTenantAdmin)
	if len(admins) == 0 {
		t.Fatal("读取租户管理员失败：夹具租户没有 active 的 tenant_admin")
	}
	tk, serr := auth.Sign(admins[0], time.Hour)
	if serr != nil {
		t.Fatalf("签发 JWT 失败: %v", serr)
	}
	return tk
}

// G：handleExpiredSubscription 的三种裁决（未开自动续费 / 宽限天数为 0 / 首轮与次轮）。
// 逐条直调函数本体，避免只验扫描侧组合效果（出问题时分不清是调度还是判定错了）。
func TestGraceHandleExpiredVerdicts(t *testing.T) {
	f := newRenewFixture(t)
	now := time.Now()

	// ① 未开自动续费：直接 false，且不得落任何宽限期键
	tidOff := f.graceTenant(t, now.Add(-2*time.Hour), false)
	permsOff := f.permsOf(t, tidOff)
	if f.srv.handleExpiredSubscription(tidOff, permsOff, graceExpiry(t, permsOff), now) {
		t.Fatal("未开自动续费的已到期订阅不该进宽限期")
	}
	if p := f.permsOf(t, tidOff); p.GraceExpiresAt != "" || p.NotifiedGrace {
		t.Fatalf("被拒的一轮不得留下宽限期键: %+v", p)
	}
	if n := graceNotifOf(t, f, tidOff, "订阅已到期，宽限期至"); n != 0 {
		t.Fatalf("被拒的一轮不该发宽限期通知，实际 %d", n)
	}
	// ② nil 快照与 Store 缺失同样 false（防御性：不给「无凭证的免费」）
	if f.srv.handleExpiredSubscription(tidOff, nil, now, now) {
		t.Fatal("perms 为 nil 时应返回 false")
	}
	// ③ 首轮进宽限期 true；次轮仍 true 且通知不重发（NotifiedGrace 去重）
	tidOn := f.graceTenant(t, now.Add(-2*time.Hour), true)
	p1 := f.permsOf(t, tidOn)
	if !f.srv.handleExpiredSubscription(tidOn, p1, graceExpiry(t, p1), now) {
		t.Fatal("开启自动续费的已到期订阅首轮应进宽限期（true=保留身份）")
	}
	if !f.srv.handleExpiredSubscription(tidOn, f.permsOf(t, tidOn), graceExpiry(t, p1), now.Add(time.Minute)) {
		t.Fatal("宽限期内的第二轮仍应返回 true，否则中间几天会被摘掉")
	}
	if n := graceNotifOf(t, f, tidOn, "订阅已到期，宽限期至"); n != 1 {
		t.Fatalf("进入宽限期的通知每轮到期只发一次，实际 %d", n)
	}
	// ④ 宽限天数配 0：即便开了自动续费也 false（回退到「到期即刻摘除」）
	if err := f.srv.Store.SetConfig("subscription_grace_days", "0"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	tidZero := f.graceTenant(t, now.Add(-2*time.Hour), true)
	p4 := f.permsOf(t, tidZero)
	if f.srv.handleExpiredSubscription(tidZero, p4, graceExpiry(t, p4), now) {
		t.Fatal("宽限天数为 0 时应即刻摘除（返回 false）")
	}
	if p := f.permsOf(t, tidZero); p.GraceExpiresAt != "" {
		t.Fatalf("关闭宽限期不应落 grace_expires_at，实际 %q", p.GraceExpiresAt)
	}
	// ⑤ 尚未起算的快照不该被读成有效截止时刻；本期一旦起算，后续轮次必须沿用同一窗口，
	//    不得每轮重新起算（否则宽限期变成「永远还有 3 天」的滑动窗口，白送收费窗口）。
	if err := f.srv.Store.SetConfig("subscription_grace_days", "3"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	tidFuture := f.graceTenant(t, now.Add(48*time.Hour), true)
	pf := f.permsOf(t, tidFuture)
	if got, ok := graceDeadline(pf, graceExpiry(t, pf)); ok || !got.IsZero() {
		t.Fatal("未起算宽限期的快照不该有有效截止时刻")
	}
	tidCalc := f.graceTenant(t, now.Add(-10*time.Hour), true)
	pBefore := f.permsOf(t, tidCalc)
	if !f.srv.handleExpiredSubscription(tidCalc, pBefore, graceExpiry(t, pBefore), now) {
		t.Fatal("夹具前提：该租户应已进入宽限期")
	}
	first := f.permsOf(t, tidCalc).GraceExpiresAt
	if !f.srv.handleExpiredSubscription(tidCalc, f.permsOf(t, tidCalc), graceExpiry(t, pBefore), now.Add(time.Hour)) {
		t.Fatal("宽限期内后续轮次应仍返回 true")
	}
	if again := f.permsOf(t, tidCalc).GraceExpiresAt; again != first {
		t.Fatalf("宽限期窗口不得逐轮重新起算: first=%q again=%q", first, again)
	}
}

// H：宽限天数/续费提前量的配置优先序与非法值防呆（env > 库配置 > 默认，clamp [0,90]）。
func TestGraceDaysConfigPriorityAndClamp(t *testing.T) {
	f := newRenewFixture(t)
	// 默认 3 天（无 env、库无配置）
	if d := f.srv.subscriptionGraceDays(); d != defaultSubscriptionGraceDays {
		t.Fatalf("默认宽限天数应为 %d，实际 %d", defaultSubscriptionGraceDays, d)
	}
	// 库配置生效
	if err := f.srv.Store.SetConfig("subscription_grace_days", "6"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	if d := f.srv.subscriptionGraceDays(); d != 6 {
		t.Fatalf("库配置 6 天应生效，实际 %d", d)
	}
	// 环境变量压过库配置
	graceSetenv(t, "SUBSCRIPTION_GRACE_DAYS", "10")
	if d := f.srv.subscriptionGraceDays(); d != 10 {
		t.Fatalf("环境变量应压过库配置，实际 %d", d)
	}
	// 负数与超上限（365 等于白送一年）一律回落默认
	for _, bad := range []string{"-1", "365", "999"} {
		graceSetenv(t, "SUBSCRIPTION_GRACE_DAYS", bad)
		if d := f.srv.subscriptionGraceDays(); d != defaultSubscriptionGraceDays {
			t.Fatalf("宽限天数 %q 属非法值应回落默认 %d，实际 %d", bad, defaultSubscriptionGraceDays, d)
		}
	}
	// env 非数字＝未配置，继续落库配置；库里非法也回落默认
	graceSetenv(t, "SUBSCRIPTION_GRACE_DAYS", "abc")
	if d := f.srv.subscriptionGraceDays(); d != 6 {
		t.Fatalf("env 非数字应视为未配置并落到库配置 6，实际 %d", d)
	}
	if err := f.srv.Store.SetConfig("subscription_grace_days", "-5"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	if d := f.srv.subscriptionGraceDays(); d != defaultSubscriptionGraceDays {
		t.Fatalf("库配置 -5 应回落默认，实际 %d", d)
	}
	// clampGraceDays 边界：0 与 90 合法（0 是「关闭宽限期」这个正经语义，不是非法值）
	for _, tc := range []struct{ in, want int }{{0, 0}, {1, 1}, {90, 90}, {91, 3}, {-1, 3}, {365, 3}} {
		if got := clampGraceDays(tc.in, 3); got != tc.want {
			t.Fatalf("clampGraceDays(%d) 应为 %d，实际 %d", tc.in, tc.want, got)
		}
	}
	// 续费提前量：默认沿用 #41 的 T-3；env > 库 > 默认；非正数视为未配置
	if l := f.srv.renewalLeadDays(); l != autoRenewLeadDays {
		t.Fatalf("默认续费提前量应为 T-%d，实际 %d", autoRenewLeadDays, l)
	}
	if err := f.srv.Store.SetConfig("renewal_lead_days", "7"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	if l := f.srv.renewalLeadDays(); l != 7 {
		t.Fatalf("库配置提前量 7 天应生效，实际 %d", l)
	}
	graceSetenv(t, "RENEWAL_LEAD_DAYS", "12")
	if l := f.srv.renewalLeadDays(); l != 12 {
		t.Fatalf("环境变量提前量 12 天应生效，实际 %d", l)
	}
	graceSetenv(t, "RENEWAL_LEAD_DAYS", "0")
	if l := f.srv.renewalLeadDays(); l != 7 {
		t.Fatalf("提前量 0 非正数应视为未配置并落回库配置 7，实际 %d", l)
	}
}

// I：graceDeadline 判据——上一期遗留值（g ≤ exp）一律无效并按本期到期时刻重新起算。
// WHY：到账链路 MarkOrderPaid 在 store 冻结文件里不会来清宽限期键，遗留值只能靠这条判据天然失效。
func TestGraceDeadlineRejectsStaleValue(t *testing.T) {
	exp := time.Now().AddDate(0, 0, 5)
	for _, c := range []struct {
		name  string
		perms *tenant.Perms
	}{
		{"nil 快照", nil},
		{"空宽限期键", &tenant.Perms{PackageCode: "m", GraceExpiresAt: ""}},
		{"不可解析", &tenant.Perms{PackageCode: "m", GraceExpiresAt: "2026-13-45"}},
		{"早于本期到期", &tenant.Perms{PackageCode: "m", GraceExpiresAt: exp.Add(-24 * time.Hour).Format(time.RFC3339)}},
		// 相等也判无效：说明它是上一期遗留（本期还没起算），不能让新一期的宽限期形同虚设
		{"等于本期到期", &tenant.Perms{PackageCode: "m", GraceExpiresAt: exp.Format(time.RFC3339)}},
	} {
		if g, ok := graceDeadline(c.perms, exp); ok {
			t.Fatalf("%s：graceDeadline 应判无效，实际返回 %v", c.name, g)
		}
	}
	// 晚于本期到期时刻才是本期有效的宽限期
	still := &tenant.Perms{PackageCode: "m", GraceExpiresAt: exp.Add(72 * time.Hour).Format(time.RFC3339)}
	if _, ok := graceDeadline(still, exp); !ok {
		t.Fatal("晚于本期到期时刻的宽限期截止应判有效")
	}

	// 端到端：库里留着遗留值时本轮必须按本期 exp 重新起算，而不是当成「宽限期已过」直接摘除
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(-time.Hour), true)
	cur := f.permsOf(t, tid)
	stale := &tenant.Perms{}
	*stale = *cur
	stale.GraceExpiresAt = graceExpiry(t, cur).Add(-48 * time.Hour).Format(time.RFC3339)
	if err := f.srv.Store.SaveTenantPerms(tid, stale); err != nil {
		t.Fatalf("预置遗留宽限期值失败: %v", err)
	}
	if !f.srv.handleExpiredSubscription(tid, f.permsOf(t, tid), graceExpiry(t, cur), time.Now()) {
		t.Fatal("遗留值应判无效并重新起算本期宽限期，而不是拒给宽限期")
	}
	got := f.permsOf(t, tid)
	recomputed, perr := time.Parse(time.RFC3339, got.GraceExpiresAt)
	if perr != nil {
		t.Fatalf("重算后的宽限期截止时刻解析失败: %v (%q)", perr, got.GraceExpiresAt)
	}
	if recomputed.Format(time.RFC3339) == stale.GraceExpiresAt {
		t.Fatal("仍沿用遗留值起算，等于下一期到期即刻被摘")
	}
	want := graceExpiry(t, cur).AddDate(0, 0, defaultSubscriptionGraceDays)
	if d := recomputed.Sub(want); d > time.Minute || d < -time.Minute {
		t.Fatalf("应按本期到期时刻 +%d 天重算：want %v got %v", defaultSubscriptionGraceDays, want, recomputed)
	}
}

// J：宽限期截止时刻落库失败 → 不给宽限期也不发通知（先落库、再通知的次序锁）。
func TestGraceNotGrantedWhenPersistFails(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(-2*time.Hour), true)
	exp := graceExpiry(t, f.permsOf(t, tid))
	// 注入 tenants 写入故障：SetSubscriptionGrace 必然报错
	if _, err := f.srv.Store.DB().Exec(`CREATE TRIGGER grace_fail_tenants BEFORE UPDATE ON tenants
		  BEGIN SELECT RAISE(ABORT, '模拟 tenants 写入故障'); END`); err != nil {
		t.Fatalf("注入故障触发器失败: %v", err)
	}
	t.Cleanup(func() { _, _ = f.srv.Store.DB().Exec("DROP TRIGGER IF EXISTS grace_fail_tenants") })
	if f.srv.handleExpiredSubscription(tid, f.permsOf(t, tid), exp, time.Now()) {
		t.Fatal("状态没落住时不得返回 true（否则出现无凭证的长期免费）")
	}
	if n := graceNotifOf(t, f, tid, "订阅已到期，宽限期至"); n != 0 {
		t.Fatalf("落库失败的一轮不该发进入宽限期通知（次序是先落库再通知），实际 %d", n)
	}
	if p := f.permsOf(t, tid); p.GraceExpiresAt != "" || p.NotifiedGrace {
		t.Fatalf("失败轮不应留下半状态: %+v", p)
	}
	// 故障恢复后同一轮应能正常进宽限期（幂等重试，不要求人工介入）
	if _, err := f.srv.Store.DB().Exec("DROP TRIGGER grace_fail_tenants"); err != nil {
		t.Fatalf("撤除故障触发器失败: %v", err)
	}
	if !f.srv.handleExpiredSubscription(tid, f.permsOf(t, tid), exp, time.Now()) {
		t.Fatal("故障恢复后应可正常进入宽限期")
	}
	if p := f.permsOf(t, tid); p.GraceExpiresAt == "" || p.PackageCode != "renew_month" {
		t.Fatalf("重试用例后状态异常: %+v", p)
	}
}

// K：阶梯建单两层去重——pending 单存在时跳过且**不消耗**今日格子，
// 该单当天被超时关单后同一天仍可补建；真正建过单的那天（格子已 created）重复触发不再建。
func TestRenewalPendingOrderKeepsAttemptCell(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(2*24*time.Hour), true) // 剩 2 天：已进 T-3 阶梯
	pkgID := gracePkgID(t, f, tid)
	perms := f.permsOf(t, tid)

	// ① 窗口外（剩 10 天 > T-3）：不建单也不落格子
	if f.srv.maybeCreateRenewalOrder(tid, perms, 10) {
		t.Fatal("剩余 10 天不应提前建续费单")
	}
	if n := graceCells(t, f, tid, pkgID, graceToday()); n != 0 {
		t.Fatalf("窗口外不应消耗今日格子，实际 %d", n)
	}
	// ② 管理员自己在收银台下了单（created_by≠0）：本轮跳过，而且**不吃格子**
	pkg, err := f.srv.Store.GetPackage(pkgID)
	if err != nil || pkg == nil {
		t.Fatalf("读取套餐失败: %v", err)
	}
	if _, merr := f.srv.Store.CreatePackageOrder(tid, pkg, 4242, "mock"); merr != nil {
		t.Fatalf("预置人工订单失败: %v", merr)
	}
	if f.srv.maybeCreateRenewalOrder(tid, perms, 2) {
		t.Fatal("同包已有 pending 单时应跳过自动建单")
	}
	if n := graceCells(t, f, tid, pkgID, graceToday()); n != 0 {
		t.Fatalf("pending 单去重属于第一层，不应消耗今日格子，实际 %d 格", n)
	}
	if n := f.systemOrders(t, tid); n != 0 {
		t.Fatalf("人工单不应计入系统单，实际 %d", n)
	}
	// ③ 挂单被超时任务关掉 → 同一天再触发应能建单（正因为上一次没吃格子）
	if closed := graceCloseStalePending(t, f, tid); closed != 1 {
		t.Fatalf("预置人工单应被超时关单，实际关闭 %d 张", closed)
	}
	if !f.srv.maybeCreateRenewalOrder(tid, perms, 2) {
		t.Fatal("pending 单关闭后同日应可补建（格子未被消耗）")
	}
	if st, orderNo, reason := graceCellRow(t, f, tid, pkgID, graceToday()); st != store.RenewalAttemptCreated || orderNo == "" || reason != "" {
		t.Fatalf("成功格应回写 created+单号且无原因，实际 status=%q order_no=%q reason=%q", st, orderNo, reason)
	}
	// ④ 格子已 created：同日重复触发（含扫描）不再建单
	if f.srv.maybeCreateRenewalOrder(tid, perms, 2) {
		t.Fatal("今日已建过单，重复触发必须跳过")
	}
	f.srv.runSubscriptionScan()
	if n := f.systemOrders(t, tid); n != 1 {
		t.Fatalf("同日不应堆系统单，实际 %d", n)
	}
}

// L：宽限期内每日补建续费单——同日不堆单，跨日（今日时钟桩）旧单关闭后补建一张。
func TestRenewalOrderCreatedDailyWithinGrace(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(-2*time.Hour), true) // 已到期，进 3 天宽限期
	pkgID := gracePkgID(t, f, tid)

	f.srv.runSubscriptionScan()
	if n := f.systemOrders(t, tid); n != 1 {
		t.Fatalf("宽限期内首轮扫描应补建 1 张续费单，实际 %d", n)
	}
	if st, orderNo, _ := graceCellRow(t, f, tid, pkgID, graceToday()); st != store.RenewalAttemptCreated || orderNo == "" {
		t.Fatalf("宽限期首轮建单应落 created 格，实际 status=%q order_no=%q", st, orderNo)
	}
	// 同日再扫：pending 单还在（第一层）+ 格子已消耗（第二层），两道都挡
	f.srv.runSubscriptionScan()
	if n := f.systemOrders(t, tid); n != 1 {
		t.Fatalf("同一天重复扫描不应堆单，实际 %d", n)
	}
	// 挂单被 15 分钟超时关掉后，同日仍不补建（重试节奏交给下一个阶梯日）
	if closed := graceCloseStalePending(t, f, tid); closed != 1 {
		t.Fatalf("续费单应被超时关单，实际关闭 %d 张", closed)
	}
	f.srv.runSubscriptionScan()
	if n := f.systemOrders(t, tid); n != 1 {
		t.Fatalf("今日格子已消耗，不应再建单，实际 %d", n)
	}
	// 跨到下一阶梯日：应再建一张，且宽限期内身份仍保留
	graceStubClock(t, func() time.Time { return time.Now().Add(24 * time.Hour) })
	f.srv.runSubscriptionScan()
	if n := f.systemOrders(t, tid); n != 2 {
		t.Fatalf("次日应补建第 2 张续费单，实际 %d", n)
	}
	if p := f.permsOf(t, tid); p.PackageCode != "renew_month" {
		t.Fatalf("宽限期第 1 天身份应仍保留，实际 package_code=%q", p.PackageCode)
	}
	if n := graceCells(t, f, tid, pkgID, graceToday()); n != 1 {
		t.Fatalf("次日应新落 1 格，实际 %d", n)
	}
}

// M：抢到格子后建单失败 → 格子落 failed + 原因非空，同日不再重试，跨日才重试。
// WHY：「抢到即消耗」是刻意设计——否则包下架/DB 抖动这类必败错误会被刷成一串垃圾 pending 单。
// 构造手法：GetPackageByCode 之后用触发器打断 orders 写入（即「claim 之后建单失败」这一形态，
// 纯靠业务入参无法构造：包一旦下架就会在第一层就返回，走不到 claim）。
func TestRenewalAttemptFailedCellKeepsReason(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(2*24*time.Hour), true)
	pkgID := gracePkgID(t, f, tid)
	perms := f.permsOf(t, tid)

	if _, err := f.srv.Store.DB().Exec(`CREATE TRIGGER grace_fail_order BEFORE INSERT ON orders
		  BEGIN SELECT RAISE(ABORT, '模拟订单表写入故障'); END`); err != nil {
		t.Fatalf("注入订单写入故障失败: %v", err)
	}
	if f.srv.maybeCreateRenewalOrder(tid, perms, 2) {
		t.Fatal("建单失败时不应返回 true")
	}
	if _, err := f.srv.Store.DB().Exec("DROP TRIGGER grace_fail_order"); err != nil {
		t.Fatalf("撤除故障触发器失败: %v", err)
	}
	st, orderNo, reason := graceCellRow(t, f, tid, pkgID, graceToday())
	if st != store.RenewalAttemptFailed {
		t.Fatalf("建单失败的格子应落 failed，实际 %q", st)
	}
	if reason == "" {
		t.Fatal("失败格子必须留原因（运维排查「为什么这个客户没收到续费单」的唯一线索）")
	}
	if orderNo != "" {
		t.Fatalf("失败格子不应带订单号，实际 %q", orderNo)
	}
	if n := f.systemOrders(t, tid); n != 0 {
		t.Fatalf("失败路径不应留下订单，实际 %d", n)
	}
	// 同日重试：格子已消耗，故障恢复也不建单（防刷单）
	if f.srv.maybeCreateRenewalOrder(tid, perms, 2) {
		t.Fatal("失败格子同样消耗当日资格，同日不应重试建单")
	}
	// 跨日：故障已消失，应能建单并把新格子落成 created
	graceStubClock(t, func() time.Time { return time.Now().Add(24 * time.Hour) })
	if !f.srv.maybeCreateRenewalOrder(tid, perms, 2) {
		t.Fatal("次日（故障已恢复）应可重新建单")
	}
	if st2, no2, r2 := graceCellRow(t, f, tid, pkgID, graceToday()); st2 != store.RenewalAttemptCreated || no2 == "" || r2 != "" {
		t.Fatalf("次日格子应落 created+单号且无原因，实际 %q %q %q", st2, no2, r2)
	}
	if n := f.systemOrders(t, tid); n != 1 {
		t.Fatalf("次日应补建 1 张，实际 %d", n)
	}
}

// N：续费到账（真实 MarkOrderPaid）把到期时刻推到未来后，扫描清掉遗留宽限期键，
// 读侧口径同步（subscriptionGraceState 不再报 in_grace）——防「已续上的订阅仍显示宽限中」。
func TestPaidRenewalScanClearsGraceKeys(t *testing.T) {
	f := newRenewFixture(t)
	tid := f.graceTenant(t, time.Now().Add(-2*time.Hour), true)
	exp := graceExpiry(t, f.permsOf(t, tid))
	if !f.srv.handleExpiredSubscription(tid, f.permsOf(t, tid), exp, time.Now()) {
		t.Fatal("夹具前提：应已进入宽限期")
	}
	if p := f.permsOf(t, tid); p.GraceExpiresAt == "" {
		t.Fatal("夹具前提：宽限期截止时刻应已落库")
	}
	// 宽限期内生成续费单并支付到账
	if !f.srv.maybeCreateRenewalOrder(tid, f.permsOf(t, tid), -1) {
		t.Fatal("宽限期内应能生成续费单")
	}
	var orderID int64
	if err := f.srv.Store.DB().QueryRow(
		"SELECT id FROM orders WHERE tenant_id=? AND created_by=0 ORDER BY id DESC LIMIT 1", tid).Scan(&orderID); err != nil {
		t.Fatalf("读取系统续费单失败: %v", err)
	}
	if perr := f.srv.Store.MarkOrderPaid(orderID, tid); perr != nil {
		t.Fatalf("续费单支付失败: %v", perr)
	}
	paid := f.permsOf(t, tid)
	if graceExpiry(t, paid).Before(time.Now()) {
		t.Fatalf("到账后到期时刻应被推到未来，实际 %q", paid.PackageExpires)
	}
	// 到账链路（MarkOrderPaid 在 store 冻结文件里）只改订阅两键：宽限期键仍留在库里，
	// 清理必须落在扫描侧。读侧此刻已因「遗留值不晚于新一期到期时刻」判它无效（graceDeadline 兜底）。
	if paid.GraceExpiresAt == "" {
		t.Fatal("夹具前提：MarkOrderPaid 不应清宽限期键（清理责任在扫描侧，见 watchdog 未到期分支）")
	}
	if in, ge := subscriptionGraceState(paid); in || ge != "" {
		t.Fatalf("已续期的订阅读侧不该报宽限中，实际 in=%v grace=%q", in, ge)
	}
	// 扫描侧负责清理遗留键（MarkOrderPaid 在 store 冻结文件里，不改它）
	f.srv.runSubscriptionScan()
	after := f.permsOf(t, tid)
	if after.GraceExpiresAt != "" || after.NotifiedGrace {
		t.Fatalf("已续期的订阅应清掉遗留宽限期键，实际 %+v", after)
	}
	if after.PackageCode != "renew_month" {
		t.Fatalf("续期后订阅身份应保留，实际 %q", after.PackageCode)
	}
	if in, ge := subscriptionGraceState(after); in || ge != "" {
		t.Fatalf("清理后读侧不该再报宽限中，实际 in=%v grace=%q", in, ge)
	}
	// 接口侧同一口径（前端订阅页读的就是这两个字段）
	rec := f.do(t, http.MethodGet, "/api/me/package", nil, graceAdminToken(t, f, tid))
	var body map[string]any
	if jerr := json.Unmarshal(rec.Body.Bytes(), &body); jerr != nil {
		t.Fatalf("解析 /api/me/package 响应失败: %v (%s)", jerr, rec.Body.String())
	}
	if body["in_grace"] == true {
		t.Fatalf("已续期不该报 in_grace: %v", body)
	}
	if ge, _ := body["grace_expires"].(string); ge != "" {
		t.Fatalf("已续期不该带 grace_expires，实际 %q", ge)
	}
	if pc, _ := body["package_code"].(string); pc != "renew_month" {
		t.Fatalf("续期后接口仍应读出订阅身份，实际 %q", pc)
	}
}

// O：subscriptionGraceState（/api/me/package 出参判定）的纯函数口径全覆盖。
func TestGraceStateJudgement(t *testing.T) {
	now := time.Now()
	valid := now.Add(48 * time.Hour).Format(time.RFC3339)
	expired := now.Add(-time.Hour).Format(time.RFC3339)
	for _, p := range []*tenant.Perms{
		nil,
		{PackageCode: "", PackageExpires: expired, GraceExpiresAt: valid},
		{PackageCode: "trial", PackageExpires: expired, GraceExpiresAt: valid},
		{PackageCode: "m", PackageExpires: "", GraceExpiresAt: valid},
		{PackageCode: "m", PackageExpires: "非时间", GraceExpiresAt: valid},
		{PackageCode: "m", PackageExpires: expired, GraceExpiresAt: ""},
		// 未到期：还在有效期内，不该报宽限期
		{PackageCode: "m", PackageExpires: now.Add(24 * time.Hour).Format(time.RFC3339), GraceExpiresAt: valid},
		// 宽限期已过（下一轮扫描会摘身份）：口径必须与扫描侧一致
		{PackageCode: "m", PackageExpires: now.Add(-5 * time.Hour).Format(time.RFC3339),
			GraceExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)},
		// 上一期遗留值（早于本期到期时刻）
		{PackageCode: "m", PackageExpires: expired, GraceExpiresAt: now.Add(-72 * time.Hour).Format(time.RFC3339)},
	} {
		if in, ge := subscriptionGraceState(p); in || ge != "" {
			t.Fatalf("不该报宽限期，实际 in=%v grace=%q（入参 %+v）", in, ge, p)
		}
	}
	in, ge := subscriptionGraceState(&tenant.Perms{
		PackageCode: "m", PackageExpires: expired, GraceExpiresAt: valid})
	if !in || ge == "" {
		t.Fatalf("宽限期内应报 in_grace=true 并带截止时刻，实际 in=%v grace=%q", in, ge)
	}
	if _, perr := time.Parse(time.RFC3339, ge); perr != nil {
		t.Fatalf("grace_expires 应为 RFC3339（前端按时刻渲染）: %q", ge)
	}
}
