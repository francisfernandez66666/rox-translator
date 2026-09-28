// ============ register_guard_f81_test.go · 职责说明 ============
// F-81（2026-09-28 〇-Z）「注册免费账号也要和免登录试用一样有防薅限制」的进程内回归锁。
//
// 用户点的是「和免费体验试用一样」，所以本文件判的不是「有没有 429」，而是四件具体的事：
//
//	A) 设备档真的按**浏览器设备号**记账：同一设备号在 24h 窗口内把 N 个免费账号发完之后
//	   第 N+1 个必须被拒，且拒绝**不建租户**（否则拒了个寂寞，额度照样发出去）；
//	B) 平台日预算档触顶时**留告警**（tenant_id=0、kind=register_budget）——没有这一条，
//	   刷号只表现为「官网注册按钮今天不能用」，运维要等客户投诉才知道；
//	C) 受邀加入已有企业**不占**这两档（那是客户自己发的一次性邀请码，
//	   算进平台名额等于把客户的员工入职打成「今日名额已满」），也不推进 reg_day 计数；
//	D) 记账纪律：闸拒掉的请求不吃格子；**占过格子但没做成的那笔也必须退格**
//	   （E 段：企业编码撞车导致建租户失败 ⇒ 计数回到原值），
//	   而坐实点在「额度真发出去」那一刻（不是等 HTTP 200），账号写入失败不退；
//	F) ★ 并发击穿锁（这条才是 F-81 的命门）：N 个请求同时打同一条上限时，
//	   真正拿到格子的笔数必须**恰好等于上限**——「先查后记」两步写法在这里会放行 limit+N 笔，
//	   而刷号脚本本来就是并发打的（C25 在邀请奖励上踩过的同一竞态形态）。
//
// 另有两档配置形态的锁：环境变量 > system_config > 代码默认（AGENTS §一·3），
// 以及「设备号缺失或脏值＝不判设备档，但照样记平台账」这一条**故意留下的软档**
// （理由见 register_guard.go reserveFreeAccount 的注释：公开注册接口的契约不能因为一个新字段打成 400）。
//
// ★ 方言自钉 sqlite（AGENTS §一·4）：夹具 f77Fixture 已做 config.C 的保存与恢复。
// ★ IP 档由 f77Register 每次放开（间隔 0／日限 200），本文件只判设备档与平台档，
//
//	免得两道闸的判据焊在同一根红线上。
//
// ==========================================================
package api

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// f81Body 组一次「新建独立免费租户」的注册载荷。
// 参数 user: 用户名（同时当邮箱前缀）；device: 浏览器设备号，空串则**不带**该字段
// （模拟老缓存包／脚本客户端），非空时走 personal 分支——这条分支不需要企业编码，
// 少一个必填字段就少一处「因为字段缺失先 400、后面那档闸根本没跑到」的假绿。
func f81Body(user, device string) map[string]any {
	m := map[string]any{
		"username": user, "password": "Passw0rd!", "type": "personal",
		"email": user + "@f81.example", "agreed": true,
	}
	if device != "" {
		m["device_id"] = device
	}
	return m
}

// f81Cnt 读 rate_limits 某一档的当前计数。**没有这一行时回 0**（不是空串）：
// 断言里「一行都没有」与「计数为 0」是同一件好事（没消耗配额），
// 判错方向就得每条自己写兜底，漏一处就是一条假红。
func f81Cnt(t *testing.T, f *renewFixture, scope, key string) int64 {
	t.Helper()
	var c int64
	if err := f.srv.Store.DB().QueryRow("SELECT count FROM rate_limits WHERE scope=? AND key=?", scope, key).Scan(&c); err != nil {
		return 0
	}
	return c
}

// f81Tenants 全站租户数（用「拒绝时没多出来一行」做等值锁）。
func f81Tenants(t *testing.T, f *renewFixture) int64 {
	t.Helper()
	var n int64
	if err := f.srv.Store.DB().QueryRow("SELECT COUNT(*) FROM tenants").Scan(&n); err != nil {
		t.Fatalf("数租户失败: %v", err)
	}
	return n
}

// f81Alerts 平台侧（tenant_id=0）某类 open 告警条数。
func f81Alerts(t *testing.T, f *renewFixture, kind string) int64 {
	t.Helper()
	var n int64
	if err := f.srv.Store.DB().QueryRow("SELECT COUNT(*) FROM alerts WHERE tenant_id=0 AND kind=? AND status='open'", kind).Scan(&n); err != nil {
		t.Fatalf("数告警失败: %v", err)
	}
	return n
}

// TestRegisterDeviceTierF81 A)＋D)：设备档按设备号计数、越线即拒，拒绝不建租户也不吃额度。
func TestRegisterDeviceTierF81(t *testing.T) {
	f := f77Fixture(t)
	if err := f.srv.Store.SetConfig("register_device_daily_limit", "2"); err != nil {
		t.Fatalf("配设备档上限失败: %v", err)
	}
	dev := "f81deviceAAAA01"

	for i := 1; i <= 2; i++ {
		code, m := f77Register(t, f.srv, "register.example", f81Body(fmt.Sprintf("f81dev%d", i), dev))
		if code != 200 || m["success"] != true {
			t.Fatalf("第 %d 次注册应放行：code=%d body=%v", i, code, m)
		}
	}
	if got := f81Cnt(t, f, regScopeDevice, dev); got != 2 {
		t.Fatalf("设备档计数=%d 期望 2（成功的两笔都要记账）", got)
	}

	base := f81Tenants(t, f)
	code, m := f77Register(t, f.srv, "register.example", f81Body("f81dev3", dev))
	if code != 429 {
		t.Fatalf("设备档越线应回 429，实得 %d body=%v", code, m)
	}
	if m["code"] != "RATE_LIMITED" {
		t.Errorf("错误码应为 RATE_LIMITED（前端按 code 分支），实得 %v", m["code"])
	}
	msg, _ := m["message"].(string)
	if !strings.Contains(msg, "该设备今天注册的账号已达上限") {
		t.Errorf("文案应点明是这台设备的上限，实得 %q", msg)
	}
	if d, ok := m["details"].(map[string]any); !ok || d["reason"] != "device" {
		t.Errorf("details.reason 应为 device（前端据此决定引导文案），实得 %v", m["details"])
	}
	if ra, ok := m["retry_after"]; !ok || ra == nil {
		t.Errorf("被拒应带 retry_after 字段，让前端做倒计时而不是从中文里抠数字，实得 %v", m["retry_after"])
	}
	if after := f81Tenants(t, f); after != base {
		t.Errorf("被拒的注册不应建出租户：before=%d after=%d", base, after)
	}
	if got := f81Cnt(t, f, regScopeDevice, dev); got != 2 {
		t.Errorf("被拒的请求不该推进设备档计数（与试用面「只有成功才计数」同口径），实得 %d", got)
	}

	// 换设备号立即放行：证明这一档真按设备号记，而不是把整站注册打死
	if c2, m2 := f77Register(t, f.srv, "register.example", f81Body("f81dev4", "f81deviceBBBB02")); c2 != 200 {
		t.Fatalf("别台设备应放行，实得 %d body=%v", c2, m2)
	}

	// 窗口过期后同一设备恢复额度（判据来自 windowActiveIn，防「隔夜不恢复」做成永久封）
	if _, err := f.srv.Store.DB().Exec("UPDATE rate_limits SET window_start=? WHERE scope=? AND key=?",
		time.Now().Add(-25*time.Hour).Unix(), regScopeDevice, dev); err != nil {
		t.Fatalf("改窗口起点失败: %v", err)
	}
	if c3, m3 := f77Register(t, f.srv, "register.example", f81Body("f81dev5", dev)); c3 != 200 {
		t.Fatalf("窗口过期后应恢复额度，实得 %d body=%v", c3, m3)
	}
}

// TestRegisterGlobalBudgetF81 B)＋C)：平台日预算档触顶要拒＋留告警；受邀加入不占这一档。
func TestRegisterGlobalBudgetF81(t *testing.T) {
	f := f77Fixture(t)
	// 先把平台档钉成「此刻已用满」：现读计数再写回，绝不对绝对值下手（本库前面已注册过若干笔）。
	// 注意 configIntTier 把 0 当非法值回落默认 500，所以计数为 0 时**必须**先造一笔真实注册，
	// 否则本段会在「从没触顶」的前提上跑完，判据恒真——那是本文件最不能接受的一种绿。
	used := f81Cnt(t, f, regScopeGlobal, "global")
	if used == 0 {
		if c, m := f77Register(t, f.srv, "register.example", f81Body("f81seed", "f81deviceCC03")); c != 200 {
			t.Fatalf("预置平台档计数失败: %d %v", c, m)
		}
		used = f81Cnt(t, f, regScopeGlobal, "global")
	}
	if used == 0 {
		t.Fatalf("预置后平台档计数仍为 0＝一次免费账号注册都没被记账（reserveFreeAccount/markSpent 没接到线，本段没有可判的前提）")
	}
	if err := f.srv.Store.SetConfig("register_global_daily_limit", fmt.Sprint(used)); err != nil {
		t.Fatalf("配平台档上限失败: %v", err)
	}

	base := f81Tenants(t, f)
	code, m := f77Register(t, f.srv, "register.example", f81Body("f81global1", "f81deviceDD04"))
	if code != 429 {
		t.Fatalf("平台日名额触顶应回 429，实得 %d body=%v", code, m)
	}
	if msg, _ := m["message"].(string); !strings.Contains(msg, "今日免费注册名额已用完") {
		t.Errorf("文案应说清是今天的全平台名额，实得 %q", msg)
	}
	if after := f81Tenants(t, f); after != base {
		t.Errorf("触顶拒绝不应建租户：before=%d after=%d", base, after)
	}
	if n := f81Alerts(t, f, "register_budget"); n < 1 {
		t.Errorf("触顶必须留一条 tenant_id=0 的 register_budget open 告警（否则刷号只表现为注册按钮失灵），实得 %d", n)
	}

	// C) 受邀加入已有企业：同样的触顶状态下照样放行，而且**不推进**平台档与设备档计数
	corp, err := f.srv.Ten.Create("f81corp", "F81 受邀企业", "", "{}")
	if err != nil {
		t.Fatalf("预置企业租户失败: %v", err)
	}
	if _, err := f.srv.Store.CreateInviteCode("f81invite01", corp.ID); err != nil {
		t.Fatalf("造邀请码失败: %v", err)
	}
	beforeGlobal := f81Cnt(t, f, regScopeGlobal, "global")
	c2, m2 := f77Register(t, f.srv, "register.example", map[string]any{
		"username": "f81member", "password": "Passw0rd!", "type": "enterprise",
		"role_choice": "member", "invite": "f81invite01",
		"email": "f81member@f81.example", "agreed": true, "device_id": "f81deviceEE05",
	})
	if c2 != 200 {
		t.Fatalf("受邀加入不应被平台日名额挡住（那是客户自己发的码）：code=%d body=%v", c2, m2)
	}
	if tid, _ := m2["tenant_id"].(float64); int64(tid) != corp.ID {
		t.Errorf("受邀加入应落在邀请码那家租户：期望 %d 实得 %v", corp.ID, m2["tenant_id"])
	}
	if after := f81Cnt(t, f, regScopeGlobal, "global"); after != beforeGlobal {
		t.Errorf("受邀加入不该占用平台免费账号名额：before=%d after=%d", beforeGlobal, after)
	}
	if got := f81Cnt(t, f, regScopeDevice, "f81deviceEE05"); got != 0 {
		t.Errorf("受邀加入不该推进设备档计数，实得 %d", got)
	}
}

// TestRegisterNoDeviceStillCountsPlatform 软档形态锁：不带设备号／脏设备号都不吃设备档，但照样记平台账。
// 这条断言把「为什么允许不带设备号」写成可执行的口径——不是漏实现，是刻意：
// 公开注册接口不能因为加一个新字段就把老缓存包／脚本客户端打成 400（F-64、F-79 的同形教训）。
func TestRegisterNoDeviceStillCountsPlatform(t *testing.T) {
	f := f77Fixture(t)
	if err := f.srv.Store.SetConfig("register_device_daily_limit", "1"); err != nil {
		t.Fatalf("配设备档上限失败: %v", err)
	}
	if err := f.srv.Store.SetConfig("register_global_daily_limit", "50"); err != nil {
		t.Fatalf("配平台档上限失败: %v", err)
	}
	before := f81Cnt(t, f, regScopeGlobal, "global")
	for i := 1; i <= 2; i++ {
		code, m := f77Register(t, f.srv, "register.example", f81Body(fmt.Sprintf("f81nodev%d", i), ""))
		if code != 200 {
			t.Fatalf("不带设备号第 %d 次不应被设备档挡住：code=%d body=%v", i, code, m)
		}
	}
	if after := f81Cnt(t, f, regScopeGlobal, "global"); after != before+2 {
		t.Errorf("平台档应照实计两笔：before=%d after=%d", before, after)
	}

	// 脏设备号（非法字符集）同样按「没有设备号」处理：注册照做，但不许把脏串落进限流表当 key
	code, m := f77Register(t, f.srv, "register.example", f81Body("f81dirty", "!!!!bad key!!!!"))
	if code != 200 {
		t.Fatalf("脏设备号不该让注册失败（只是不计设备档），实得 %d %v", code, m)
	}
	rows, err := f.srv.Store.DB().Query("SELECT key FROM rate_limits WHERE scope=?", regScopeDevice)
	if err != nil {
		t.Fatalf("读设备档 key 失败: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatalf("扫 key 失败: %v", err)
		}
		if !trialDeviceIDRe.MatchString(k) {
			t.Errorf("设备档 key %q 未过白名单字符集（脏值会把限流表变成垃圾场）", k)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历设备档 key 出错: %v", err)
	}
}

// TestRegisterGuardTierPrecedence 配置优先序：环境变量 > system_config > 代码默认（AGENTS §一·3）。
// 一条锁同时抓两种手滑：env 名字拼错（env 支恒不生效，靠 system_config 蒙过去＝假绿），
// 以及优先序写反（管理台一改档就把机器上设的值顶掉，运维的 drop-in 静默失效）。
func TestRegisterGuardTierPrecedence(t *testing.T) {
	f := f77Fixture(t)
	if err := f.srv.Store.SetConfig("register_device_daily_limit", "9"); err != nil {
		t.Fatalf("配 system_config 档失败: %v", err)
	}
	if got := configIntTier(f.srv.Store, "REGISTER_DEVICE_DAILY", "register_device_daily_limit", regDeviceDailyDefault, false); got != 9 {
		t.Fatalf("库值应生效，实得 %d", got)
	}
	t.Setenv("REGISTER_DEVICE_DAILY", "1")
	if got := configIntTier(f.srv.Store, "REGISTER_DEVICE_DAILY", "register_device_daily_limit", regDeviceDailyDefault, false); got != 1 {
		t.Fatalf("环境变量应压过库值，实得 %d", got)
	}
	// 非法值（0 与非数字）回落下一级：额度配 0 多半是手滑，静默关掉注册入口比写错数字更难查
	t.Setenv("REGISTER_DEVICE_DAILY", "0")
	if got := configIntTier(f.srv.Store, "REGISTER_DEVICE_DAILY", "register_device_daily_limit", regDeviceDailyDefault, false); got != 9 {
		t.Fatalf("env=0 应回落库值 9，实得 %d", got)
	}
	t.Setenv("REGISTER_DEVICE_DAILY", "abc")
	if got := configIntTier(f.srv.Store, "REGISTER_DEVICE_DAILY", "register_device_daily_limit", regDeviceDailyDefault, false); got != 9 {
		t.Fatalf("env 非数字应回落库值 9，实得 %d", got)
	}
	// 最小间隔这一档 0 有合法含义（run_uat 与本地快跑靠配 0 放开间隔），必须走 allowZero=true；
	// 先把它真配成 0 再判——夹具里这一键默认没落库，直接判 0 得到的是代码默认 60（首跑就是这么红的）。
	if err := f.srv.Store.SetConfig("register_ip_min_interval_sec", "0"); err != nil {
		t.Fatalf("配最小间隔为 0 失败: %v", err)
	}
	if got := configIntTier(f.srv.Store, "REGISTER_IP_MIN_INTERVAL", "register_ip_min_interval_sec", regIPMinIntervalDef, true); got != 0 {
		t.Fatalf("最小间隔应读到库值 0（0＝不限间隔），实得 %d", got)
	}
	// 同一档若按 allowZero=false 解析，0 会被当非法值回落默认——这正是注册间隔与试用额度两档的语义差别
	if got := configIntTier(f.srv.Store, "REGISTER_IP_MIN_INTERVAL", "register_ip_min_interval_sec", regIPMinIntervalDef, false); got != regIPMinIntervalDef {
		t.Fatalf("allowZero=false 时 0 应回落默认 %d，实得 %d", regIPMinIntervalDef, got)
	}
	// 三处都没配 → 代码默认（防止有人把默认值写成 0，做出「默认即关闭」的事故形态）
	if got := configIntTier(f.srv.Store, "REGISTER_UNSET_ENV", "register_unset_cfg_key", regGlobalDailyDefault, false); got != regGlobalDailyDefault {
		t.Fatalf("未配置时应落代码默认，实得 %d", got)
	}
}

// TestRegisterGuardMemoryFallback Store 未就绪时的内存回退腿（R-M8 留的那条）。
// 为什么单独钉：持久化分支天天在跑，内存分支一年跑不到一次——它悄悄坏了（比如 key 前缀撞车、
// 或退格把计数写成负数）的现场表现是「首次启动期间注册不限流」，事后极难归因。
// 本段判四件事：占格即计数、拒格不吃格子、release 真退格、markSpent 之后不再退。
func TestRegisterGuardMemoryFallback(t *testing.T) {
	g := newRegisterGuard(nil)
	// memCnt 读内存账本（持锁读，与本文件其它判据同口径）。
	// 没有这一行时返回 0 而不是 panic：本段既有「该有格子」也有「该没格子」的判据，
	// 直接 g.data[key].count 会在键不存在时把测试打成空指针，报错点离根因十万八千里。
	memCnt := func(key string) int {
		g.mu.Lock()
		defer g.mu.Unlock()
		if a, ok := g.data[key]; ok {
			return a.count
		}
		return 0
	}

	// ① 占格：第一笔放行并把设备档推到 1（reserve 与旧的 allow 语义差别就在这——占格即计数）
	tk, wait, reason := g.reserveFreeAccount("devmem00000001", 1, 0)
	if tk == nil || reason != "" || wait != 0 {
		t.Fatalf("首笔应占格放行，实得 tk==nil?=%v reason=%q wait=%d", tk == nil, reason, wait)
	}
	if !tk.heldDevice {
		t.Errorf("内存回退腿没有真的占住设备档那一格（release 会变成空转，账本与实际脱钩）")
	}
	if c := memCnt("dev:devmem00000001"); c != 1 {
		t.Fatalf("占格后设备档计数应为 1，实得 %d", c)
	}
	// ② 上限 1 ⇒ 第二笔拒绝，且拒绝不推进计数（仍为 1）
	if tk2, w2, r2 := g.reserveFreeAccount("devmem00000001", 1, 0); tk2 != nil || r2 != "device" || w2 < 60 {
		t.Fatalf("第二笔应按设备拒绝，实得 tk!=nil?=%v reason=%q wait=%d", tk2 != nil, r2, w2)
	}
	if c := memCnt("dev:devmem00000001"); c != 1 {
		t.Errorf("被拒的请求不该推进计数，实得 %d", c)
	}
	// ③ 第一笔没做成 ⇒ release 退格 ⇒ 同一设备重新有额度
	tk.release()
	if c := memCnt("dev:devmem00000001"); c != 0 {
		t.Fatalf("退格后计数应回到 0，实得 %d（退格没接上线，用户会被白扣额度）", c)
	}
	if tk3, _, _ := g.reserveFreeAccount("devmem00000001", 1, 0); tk3 == nil {
		t.Fatalf("退格后同一设备应恢复额度")
	} else {
		// ④ 坐实之后 release 变空转（额度已发出，这一笔必须算数，哪怕随后账号写入失败）
		tk3.markSpent()
		tk3.release()
		if c := memCnt("dev:devmem00000001"); c != 1 {
			t.Fatalf("已坐实的一格不该被退回，实得 %d", c)
		}
		// release 幂等：重复调用不改变结果
		tk3.release()
		if c := memCnt("dev:devmem00000001"); c != 1 {
			t.Fatalf("release 二次调用不该多退一格，实得 %d", c)
		}
	}

	// ⑤ 平台档与设备档各记各的：换一个设备号不能把平台账也绕过
	tg, _, _ := g.reserveFreeAccount("devmem00000002", 1, 1)
	if tg == nil {
		t.Fatalf("平台档首笔应放行")
	}
	if _, _, r := g.reserveFreeAccount("devmem00000003", 1, 1); r != "global" {
		t.Fatalf("平台档触顶应拒绝新设备，实得 reason=%q", r)
	}
	// ⑥ 平台档拒绝时，先前占住的设备那一格必须退回（否则一次失败尝试把用户当日额度磨光）
	if c := memCnt("dev:devmem00000003"); c != 0 {
		t.Errorf("平台档触顶后设备档不该留格子（漏退格），实得 %d", c)
	}

	// ⑦ 内存 key 前缀不许与 IP 档撞车：同一串既当 IP 又当 dev: 会让两档互相顶数
	if ok3, _ := g.allow("devmem00000001", 1, 0); !ok3 {
		t.Errorf("IP 档不该看见设备档的计数（key 前缀必须隔离）")
	}
}

// TestRegisterReserveRefundOnFailure E)：占格成功后**注册没做成**的那一笔要退格。
// 现场形态：企业编码撞车 ⇒ 建租户 400。这一笔既没发额度也没账号，
// 如果把格子留在账上，用户重试三次就被「今天已达上限」锁死——
// 防薅机制误伤真人时，最难查的证据恰恰是「他自己说没注册成功过」。
func TestRegisterReserveRefundOnFailure(t *testing.T) {
	f := f77Fixture(t)
	if err := f.srv.Store.SetConfig("register_device_daily_limit", "2"); err != nil {
		t.Fatalf("配设备档上限失败: %v", err)
	}
	if err := f.srv.Store.SetConfig("register_global_daily_limit", "100"); err != nil {
		t.Fatalf("配平台档上限失败: %v", err)
	}
	dev := "f81refundAAA01"
	body := func(user, code string) map[string]any {
		return map[string]any{
			"username": user, "password": "Passw0rd!", "type": "enterprise",
			"role_choice": "admin", "code": code, "name": "F81 退格企业",
			"email": user + "@f81.example", "agreed": true, "device_id": dev,
		}
	}
	// 第一笔：占格 → 建租户 → 发额度 → 坐实（计数 +1，不该被退）
	if c, m := f77Register(t, f.srv, "register.example", body("f81rf1", "f81dupcode")); c != 200 {
		t.Fatalf("首笔企业注册应放行: code=%d body=%v", c, m)
	}
	if got := f81Cnt(t, f, regScopeDevice, dev); got != 1 {
		t.Fatalf("成功的一笔应占住 1 格，实得 %d", got)
	}
	before := f81Cnt(t, f, regScopeDevice, dev)
	beforeGlobal := f81Cnt(t, f, regScopeGlobal, regGlobalKey)
	base := f81Tenants(t, f)

	// 第二笔：企业编码撞车 ⇒ 400（失败发生在建租户那一步，格子已占、额度未发）
	c2, m2 := f77Register(t, f.srv, "register.example", body("f81rf2", "f81dupcode"))
	if c2 == 200 {
		t.Fatalf("重复企业编码本应失败，实得 200（本段前提没了）: %v", m2)
	}
	if after := f81Tenants(t, f); after != base {
		t.Errorf("失败的那笔不该建出租户：before=%d after=%d", base, after)
	}
	if got := f81Cnt(t, f, regScopeDevice, dev); got != before {
		t.Errorf("失败的那笔必须退格（设备档）：before=%d after=%d", before, got)
	}
	if got := f81Cnt(t, f, regScopeGlobal, regGlobalKey); got != beforeGlobal {
		t.Errorf("失败的那笔必须退格（平台档）：before=%d after=%d", beforeGlobal, got)
	}

	// 退格之后额度还在（上限 2、只坐实了 1 笔）：再注册一次必须成功，
	// 这条反向判据兜住「退格退过头把已坐实的那格也退了」
	if c3, m3 := f77Register(t, f.srv, "register.example", body("f81rf3", "f81thirdcode")); c3 != 200 {
		t.Fatalf("失败退格后应仍有额度，实得 code=%d body=%v", c3, m3)
	}
	if got := f81Cnt(t, f, regScopeDevice, dev); got != 2 {
		t.Errorf("两笔成功应占住 2 格，实得 %d", got)
	}
}

// TestRegisterReserveAtomicUnderConcurrency F)：并发打同一条上限时，拿到格子的笔数**恰好等于上限**。
// 这是 F-81 的命门，也是本批从「先判后记」改成「原子占格」的全部理由：
// 两步写法下 12 个 goroutine 会同时读到「还差一格」并全部放行，上限形同虚设，
// 而刷号脚本从来不是单线程的。
//
// ★ 判据只看「真占住格子的笔数」（tk.heldDevice），不看「放行笔数」：
// 数据库故障那一路是**刻意放行**（fail-open，见 reserveFreeAccount 注释），
// 那一笔不占格子也不该被算成"击穿"。把两者混成一个数，本段就会在 SQLite 偶发锁竞争下随机翻红。
func TestRegisterReserveAtomicUnderConcurrency(t *testing.T) {
	f := f77Fixture(t)
	const limit = 3
	g := f.srv.regGuard
	dev := "f81atomicAAA01"

	var wg sync.WaitGroup
	var mu sync.Mutex
	held := 0
	allowed := 0
	rejected := 0
	start := make(chan struct{})
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // 齐发：不等前一个跑完，否则测的是串行而不是竞态
			tk, _, _ := g.reserveFreeAccount(dev, limit, 0)
			mu.Lock()
			defer mu.Unlock()
			if tk == nil {
				rejected++
				return
			}
			allowed++
			if tk.heldDevice {
				held++
			}
		}(i)
	}
	close(start)
	wg.Wait()

	if held != limit {
		t.Errorf("并发占格：真占住设备档的笔数应恰好等于上限 %d，实得 %d（放行 %d、拒绝 %d）——大于上限＝击穿，小于上限＝有格子没人拿",
			limit, held, allowed, rejected)
	}
	if got := f81Cnt(t, f, regScopeDevice, dev); got != int64(limit) {
		t.Errorf("并发后设备档落库计数应等于上限 %d，实得 %d", limit, got)
	}
	// 反向对照：占住的格子全退回去之后，额度应重新可用（证明计数没被"拒绝的那些笔"污染）
	if _, err := f.srv.Store.DB().Exec("UPDATE rate_limits SET count=0 WHERE scope=? AND key=?", regScopeDevice, dev); err != nil {
		t.Fatalf("清零计数失败: %v", err)
	}
	if tk, _, _ := g.reserveFreeAccount(dev, limit, 0); tk == nil {
		t.Errorf("清零后应能再占一格（拒绝的那些笔不该留下计数）")
	} else {
		tk.release()
	}
}

// TestRateReserveAndReleaseSemantics 底层两把钳子（RateReserve／RateRelease）自身的判据锁。
//
// 为什么在业务面已经有并发锁的情况下还要钉这一层：F-81 的整道闸现在**完全建在这两个方法上**，
// 而它们同时服务试用面与登录面之外的新战场。业务测试能抓到"上限被击穿"，
// 抓不到这三类只有底层才会坏的东西：
//   - 触顶时没把现读状态带回去（retry_after 就会算成 0，前端把"明天再来"渲染成"立刻再试"，脚本打得更密）；
//   - 退格退到负数（计数下溢等于把后续每一笔都放行，是限流最坏的一种坏法）；
//   - 退格作用在**过期窗口**上（把一个已经该开新窗的行改成"还剩格子"，隔夜恢复额度的口径就没了）。
//
// ★ 这一段用独立 scope 无关的探针 key，不碰真实设备号，免得与上面几段互相顶计数。
func TestRateReserveAndReleaseSemantics(t *testing.T) {
	f := f77Fixture(t)
	st := f.srv.Store
	key := "f81probeKEY000001"
	rawCnt := func() int64 {
		t.Helper()
		var c int64
		if err := st.DB().QueryRow("SELECT count FROM rate_limits WHERE scope=? AND key=?", regScopeDevice, key).Scan(&c); err != nil {
			return -1 // 行不存在
		}
		return c
	}

	// ① 首笔：占格成功且把窗口起点一起带回（retry_after 靠它算）
	reserved, got, err := st.RateReserve(regScopeDevice, key, regWindowSec, 2)
	if err != nil || !reserved || got.Count != 1 || got.WindowStart <= 0 {
		t.Fatalf("首笔应占格成功并回带状态，实得 reserved=%v st=%+v err=%v", reserved, got, err)
	}
	// ② 第二笔占满上限
	if r2, s2, e2 := st.RateReserve(regScopeDevice, key, regWindowSec, 2); e2 != nil || !r2 || s2.Count != 2 {
		t.Fatalf("第二笔应占住最后一格，实得 reserved=%v st=%+v err=%v", r2, s2, e2)
	}
	// ③ 第三笔触顶：必须 reserved=false **且带回现读计数与窗口起点**（算等待秒数要用）
	r3, s3, e3 := st.RateReserve(regScopeDevice, key, regWindowSec, 2)
	if e3 != nil {
		t.Fatalf("触顶不该报错，实得 err=%v", e3)
	}
	if r3 {
		t.Fatalf("上限 2 已占满，第三笔仍被放行＝原子判据失效")
	}
	if s3.Count != 2 || s3.WindowStart <= 0 {
		t.Fatalf("触顶必须带回现读状态（否则 retry_after 只能算成 0，前端会引导\"立刻再试\"），实得 %+v", s3)
	}
	if w := regRetryWaitIn(s3.WindowStart); w < 60 || w > regWindowSec {
		t.Fatalf("等待秒数应落在 60~86400，实得 %d", w)
	}
	// 计数没被那笔失败的尝试污染
	if c := rawCnt(); c != 2 {
		t.Fatalf("触顶的那笔不该推进计数，实得 %d", c)
	}

	// ④ 退一格 ⇒ 重新可占
	if err := st.RateRelease(regScopeDevice, key, regWindowSec); err != nil {
		t.Fatalf("退格失败: %v", err)
	}
	if c := rawCnt(); c != 1 {
		t.Fatalf("退格后计数应为 1，实得 %d", c)
	}
	if r4, _, e4 := st.RateReserve(regScopeDevice, key, regWindowSec, 2); e4 != nil || !r4 {
		t.Fatalf("退格后应重新可占，实得 reserved=%v err=%v", r4, e4)
	}

	// ⑤ 退格不许下溢：连退五次，计数最低停在 0（负数＝把后续每一笔都放行）
	for i := 0; i < 5; i++ {
		if err := st.RateRelease(regScopeDevice, key, regWindowSec); err != nil {
			t.Fatalf("第 %d 次退格失败: %v", i+1, err)
		}
	}
	if c := rawCnt(); c != 0 {
		t.Fatalf("连退五次后计数应停在 0，实得 %d（负数会把这道闸整体关掉）", c)
	}

	// ⑥ 过期窗口上的退格必须是空转：不能把一个"该开新窗"的行改成"还剩格子"
	if _, err := st.DB().Exec("UPDATE rate_limits SET count=1, window_start=? WHERE scope=? AND key=?",
		time.Now().Add(-25*time.Hour).Unix(), regScopeDevice, key); err != nil {
		t.Fatalf("把窗口改到 25 小时前失败: %v", err)
	}
	if err := st.RateRelease(regScopeDevice, key, regWindowSec); err != nil {
		t.Fatalf("过期窗退格调用失败: %v", err)
	}
	if c := rawCnt(); c != 1 {
		t.Fatalf("过期窗不该被退格改动（隔夜恢复额度靠这条），实得 %d", c)
	}
	// 过期窗内重新占格 ⇒ 整行重置为本窗第一笔
	r6, s6, e6 := st.RateReserve(regScopeDevice, key, regWindowSec, 2)
	if e6 != nil || !r6 || s6.Count != 1 {
		t.Fatalf("过期窗重新开窗后计数应为 1，实得 reserved=%v st=%+v err=%v", r6, s6, e6)
	}
	// 首笔判据的反面：窗口起点必须真被刷新（还是 25 小时前的值就等于新窗没开）
	if !windowActiveIn(s6.WindowStart, regWindowSec) {
		t.Fatalf("开新窗后 window_start 应落在有效窗内，实得 %d", s6.WindowStart)
	}
}

// TestRegisterGuardScopeNames scope 与窗口字面值锁：冒烟脚本与排查文档按这些字面查 rate_limits，
// 改名必须连带改那边，所以钉在这里（改一个字就红灯，而不是等线上排查时扑空）。
func TestRegisterGuardScopeNames(t *testing.T) {
	if regScopeDevice != "reg_dev" || regScopeGlobal != "reg_day" {
		t.Errorf("scope 字面值漂移：reg_dev=%q reg_day=%q", regScopeDevice, regScopeGlobal)
	}
	if regWindowSec != 86400 || trialWindowSec != regWindowSec {
		t.Errorf("注册窗口应为 24h=86400 秒且与试用同长，实得 reg=%d trial=%d", regWindowSec, trialWindowSec)
	}
	if !windowActiveIn(time.Now().Unix()-86399, regWindowSec) || windowActiveIn(time.Now().Unix()-86401, regWindowSec) {
		t.Errorf("windowActiveIn 的窗口边界判错（差一秒就决定隔夜额度恢不恢复）")
	}
	if windowActiveIn(0, regWindowSec) {
		t.Errorf("windowStart=0（从未记过账）必须判为不在窗内")
	}
	// 试用侧那道同名判据必须走同一把尺子，不允许两处各抄一份实现
	if trialWindowActive(time.Now().Unix()-86401) || !trialWindowActive(time.Now().Unix()-10) {
		t.Errorf("trialWindowActive 与 windowActiveIn 判据不一致")
	}
}
