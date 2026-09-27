// ============ branding_paid_gate_test.go · 职责说明 ============
// F-75 品牌展示侧付费闸 + 到期 30 天宽限期（★ 2026-09-27 〇-X 用户拍板
// 「套餐到期后，宽限期 30 天＋站内信通知」）的后端单测，复用 pay_renew_test.go 的
// renewFixture（内存 SQLite + tenant.Store + 上架付费包 renew_month）：
//
//	A) 出栈三态走真实 GET /api/tenant/branding 匿名视角：付费在效＝展示、宽限期内＝展示、
//	   宽限结束＝八项视觉字段清空且 brand_paid=false（库里配置不动，续费即恢复）；
//	B) 视角豁免：同一时刻该租户管理员与平台超管仍拿真值——展示闸一旦污染管理台回显，
//	   客户点一次保存就把真配置覆盖成空（读写同源红线）；
//	C) 到期当轮由订阅扫描起算宽限：brand_grace_expires_at = 到期时刻 + 30 天、
//	   一条「进入宽限期」站内信，二次扫描不重发；
//	D) 四种不该起宽限的情形一律零键零通知：企业根租户／超管显式授权／没配过品牌／免费包到期；
//	E) 宽限结束：日扫发一条回收通知并清键，重跑不重发，展示侧同口径转隐藏；
//	F) 续费到账后日扫只清键、不通知；
//	G) 天数配置优先序 env BRAND_GRACE_DAYS > system_config.brand_grace_days > 30，
//	   非法值回落、上限 90；配 0＝当天直接按「已停止展示」告知；
//	H) 落库层：SetBrandGrace 会把两条去重标记复位（同一租户可能二次到期），
//	   MarkBrandGraceNotice 未知档位显式报错（既不当"已发"也不当"未发"）；
//	I) brandGraceDeadline 纯函数口径：nil/空/脏值/已过一律无效（宁可不展示，不给到期租户白送品牌）；
//	J) 次序锁：宽限键落库失败时不发通知（先落库、后触达，与 #74 同口径）。
//
// ★ 为什么断言一律落在 ID>1 的租户：runSubscriptionScan 按设计跳过租户 1（平台宿主），
//
//	renewFixture 自身的租户恰好是 ID=1，直接用它会得到「扫描什么都不做」的假绿。
//
// ★ 单测自钉 sqlite 方言由 newRenewFixture 承担（AGENTS.md §一·4）。
// ============================================================
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// brandGateMux 只挂品牌读接口（GET 公开、POST 保存），走真实 handler：
// 展示闸长在 brandingPayload 这个咽喉点上，接口面与 SPA 首屏注入面同源，
// 直调 payload 会漏掉 handler 侧的装配与鉴权分流。
func brandGateMux(s *Server) http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/tenant/branding", s.handleTenantBranding)
	return m
}

// brandGateGet 以指定视角读品牌接口（token 为空＝匿名访客），返回解析后的出栈 map。
func brandGateGet(t *testing.T, f *renewFixture, tid int64, token string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/tenant/branding?tenant_id=%d", tid), nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	brandGateMux(f.srv).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("品牌接口应 200，实际 %d（body=%s）", rec.Code, rec.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析品牌响应失败: %v (%s)", err, rec.Body.String())
	}
	if m["success"] != true {
		t.Fatalf("品牌接口应 success=true: %v", m)
	}
	return m
}

// brandGateTenant 造一家「个人身份 + 已配品牌视觉 + 持有指定套餐」的租户（ID 必 >1），
// 并给它配一名租户管理员。参数 expires=订阅到期时刻（过去＝已到期）。
func brandGateTenant(t *testing.T, f *renewFixture, code, pkgCode string, expires time.Time) int64 {
	t.Helper()
	perms := tenant.Perms{
		PackageCode: pkgCode, PackageExpires: expires.Format(time.RFC3339),
		SentenceBalance: 100,
	}
	b, _ := json.Marshal(perms)
	co, err := f.srv.Ten.Create(code, "品牌闸测试公司", "", string(b))
	if err != nil {
		t.Fatalf("创建品牌闸租户失败: %v", err)
	}
	if co.ID <= 1 {
		t.Fatalf("夹具租户 ID 必须 >1（否则订阅扫描按设计跳过平台宿主租户，got %d）", co.ID)
	}
	// 个人身份租户：品牌权益才由「付费套餐在效」决定（企业根租户天然解锁，见 tenantBrandingUnlocked）
	if err := f.srv.Ten.SetPersonal(co.ID, true); err != nil {
		t.Fatalf("置个人租户失败: %v", err)
	}
	if err := f.srv.Ten.SetBranding(co.ID, "极光汽车", "/brand/logo-aurora.png", code, ""); err != nil {
		t.Fatalf("预置品牌字段失败: %v", err)
	}
	if err := f.srv.Ten.SetBrandHomeBg(co.ID, "/brand/bg-aurora.png", "cover"); err != nil {
		t.Fatalf("预置品牌背景失败: %v", err)
	}
	if _, err := f.srv.Store.CreateUser(co.ID, fmt.Sprintf("bg_admin_%d", co.ID), "hash-bg",
		"品牌闸管理员", store.RoleTenantAdmin, 1, 0); err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	return co.ID
}

// brandGateAdminToken 取该租户管理员的 JWT（视角豁免断言用）。
func brandGateAdminToken(t *testing.T, f *renewFixture, tid int64) string {
	t.Helper()
	admins := f.srv.Store.ListUsersByRole(tid, store.RoleTenantAdmin)
	if len(admins) == 0 {
		t.Fatalf("夹具租户 %d 没有 active 的 tenant_admin", tid)
	}
	tk, err := auth.Sign(admins[0], time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	return tk
}

// brandGateSuperToken 建一名平台超管并签发 JWT（超管预览任意租户的视角）。
func brandGateSuperToken(t *testing.T, f *renewFixture) string {
	t.Helper()
	seq := time.Now().UnixNano()
	un := fmt.Sprintf("bg_super_%d", seq)
	if _, err := f.srv.Store.CreateUser(0, un, "hash-super", "品牌闸超管", store.RoleSuperAdmin, 0, 0); err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	u, err := f.srv.Store.GetUserByUsername(0, un)
	if err != nil {
		t.Fatalf("读取超管失败: %v", err)
	}
	tk, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发超管 JWT 失败: %v", err)
	}
	return tk
}

// brandGateVisualsShown 判定出栈是否带着品牌视觉（名 + Logo + 背景三样齐全才算展示）。
func brandGateVisualsShown(m map[string]any) bool {
	s, _ := m["brand_name"].(string)
	l, _ := m["brand_logo"].(string)
	bg, _ := m["brand_home_bg"].(string)
	return s != "" && l != "" && bg != ""
}

// brandGateNotif 按「租户 + 精确标题」统计站内信条数（去重断言用）。
// WHY 带 ref 过滤：一个夹具库里会开多家品牌闸租户，只按标题计数会把别家算进来。
func brandGateNotif(t *testing.T, f *renewFixture, tid int64, title string) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM notifications WHERE ref_type='tenant' AND ref_id=? AND title=?",
		tid, title).Scan(&n); err != nil {
		t.Fatalf("统计站内信失败: %v", err)
	}
	return n
}

// brandGateNotifPrefix 按标题前缀统计（标题含动态天数时用前缀，避免把配置改动的用例钉死文案）。
func brandGateNotifPrefix(t *testing.T, f *renewFixture, tid int64, prefix string) int {
	t.Helper()
	var n int
	if err := f.srv.Store.DB().QueryRow(
		"SELECT COUNT(1) FROM notifications WHERE ref_type='tenant' AND ref_id=? AND title LIKE ?",
		tid, prefix+"%").Scan(&n); err != nil {
		t.Fatalf("统计站内信失败: %v", err)
	}
	return n
}

// brandGatePerms 读权限快照（不可读即判失败）。
func brandGatePerms(t *testing.T, f *renewFixture, tid int64) *tenant.Perms {
	t.Helper()
	p, err := f.srv.Store.GetTenantPerms(tid)
	if err != nil {
		t.Fatalf("读取权限失败: %v", err)
	}
	return p
}

// A：出栈三态——在效展示、宽限期内展示、宽限结束隐藏。
func TestBrandGateDisplayThreeStates(t *testing.T) {
	f := newRenewFixture(t)

	// ① 付费在效：匿名访客看到完整品牌
	live := brandGateTenant(t, f, "bg_live", "renew_month", time.Now().AddDate(0, 0, 10))
	if m := brandGateGet(t, f, live, ""); !brandGateVisualsShown(m) {
		t.Fatalf("付费在效应向访客展示品牌: %v", m)
	} else if m["brand_paid"] != true {
		t.Fatalf("付费在效应报 brand_paid=true: %v", m)
	}

	// ② 已到期但处于 30 天宽限期内：仍展示（客户续费前不丢门面）
	graced := brandGateTenant(t, f, "bg_grace", "renew_month", time.Now().AddDate(0, 0, -5))
	if err := f.srv.Store.SetBrandGrace(graced, time.Now().AddDate(0, 0, 20)); err != nil {
		t.Fatalf("预置品牌宽限期失败: %v", err)
	}
	if m := brandGateGet(t, f, graced, ""); !brandGateVisualsShown(m) {
		t.Fatalf("宽限期内应继续向访客展示品牌: %v", m)
	}

	// ③ 宽限期已过：八项视觉字段清空、brand_paid=false，但企业身份字段照旧（排障要认归属）
	dead := brandGateTenant(t, f, "bg_dead", "renew_month", time.Now().AddDate(0, 0, -40))
	if err := f.srv.Store.SetBrandGrace(dead, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("预置已过宽限期失败: %v", err)
	}
	m := brandGateGet(t, f, dead, "")
	if brandGateVisualsShown(m) {
		t.Fatalf("宽限期结束不得再向访客展示品牌: %v", m)
	}
	for _, k := range []string{
		"brand_name", "brand_names", "brand_name_en", "brand_logo",
		"brand_home_bg", "brand_home_bg_style", "brand_login_card_pos", "brand_login_layout",
	} {
		if v, _ := m[k].(string); v != "" {
			t.Fatalf("回收后视觉字段 %s 应为空，实际 %q", k, v)
		}
	}
	if m["brand_paid"] != false {
		t.Fatalf("回收后应钉 brand_paid=false，实际 %v", m["brand_paid"])
	}
	if v, _ := m["code"].(string); v != "bg_dead" {
		t.Fatalf("归属字段 code 不该被回收（注册页与排障依赖它），实际 %q", v)
	}
	// 回收只改出栈视图，库里配置必须原样留着（续费当天自动恢复，不需要重传图）
	if got, _ := f.srv.Ten.GetByID(dead); got == nil || got.BrandName != "极光汽车" {
		t.Fatalf("回收不得删库里的品牌配置: %+v", got)
	}
}

// B：视角豁免——被闸住的租户，其管理员与平台超管仍看真值。
// WHY：管理台用同一个接口回显现值；若匿名口径泄漏到管理台，客户保存一次就把真配置抹成空。
func TestBrandGateViewerExemption(t *testing.T) {
	f := newRenewFixture(t)
	tid := brandGateTenant(t, f, "bg_exempt", "renew_month", time.Now().AddDate(0, 0, -40))

	if m := brandGateGet(t, f, tid, ""); brandGateVisualsShown(m) {
		t.Fatalf("夹具前提：匿名视角应已被回收: %v", m)
	}
	if m := brandGateGet(t, f, tid, brandGateAdminToken(t, f, tid)); !brandGateVisualsShown(m) {
		t.Fatalf("该租户管理员必须看到真值（否则后台一保存就覆盖掉配置）: %v", m)
	}
	if m := brandGateGet(t, f, tid, brandGateSuperToken(t, f)); !brandGateVisualsShown(m) {
		t.Fatalf("平台超管预览必须看到真值: %v", m)
	}
	// 反向：别家租户的管理员不算豁免（跨租户不能借豁免视角看别人品牌）
	other := brandGateTenant(t, f, "bg_other", "renew_month", time.Now().AddDate(0, 0, 10))
	if m := brandGateGet(t, f, tid, brandGateAdminToken(t, f, other)); brandGateVisualsShown(m) {
		t.Fatalf("他租户管理员不该获得豁免视角: %v", m)
	}
}

// C：到期当轮由订阅扫描起算宽限——落截止时刻 + 一条进入宽限期站内信，重复扫描不重发。
func TestBrandGraceStartsAtExpiryScan(t *testing.T) {
	f := newRenewFixture(t)
	now := time.Now()
	tid := brandGateTenant(t, f, "bg_start", "renew_month", now.Add(-2*time.Hour))

	f.srv.runSubscriptionScan()

	p := brandGatePerms(t, f, tid)
	if p.PackageCode != "" {
		t.Fatalf("夹具前提：本轮订阅身份应已摘除，实际 %q", p.PackageCode)
	}
	if p.BrandGraceExpiresAt == "" {
		t.Fatal("到期摘除应同时起算品牌展示宽限期（brand_grace_expires_at 为空）")
	}
	end, err := time.Parse(time.RFC3339, p.BrandGraceExpiresAt)
	if err != nil {
		t.Fatalf("brand_grace_expires_at 非 RFC3339: %q", p.BrandGraceExpiresAt)
	}
	want := now.AddDate(0, 0, defaultBrandGraceDays)
	if d := end.Sub(want); d > time.Minute || d < -time.Minute {
		t.Fatalf("默认宽限应为到期时刻+%d 天：want %v got %v", defaultBrandGraceDays, want, end)
	}
	if !p.BrandGraceNoticeStart {
		t.Fatal("进入宽限期的去重标记应已置位")
	}
	// 匿名视角宽限期内仍展示（展示闸与扫描侧同一份状态）
	if m := brandGateGet(t, f, tid, ""); !brandGateVisualsShown(m) {
		t.Fatalf("刚进宽限期的租户应向访客继续展示品牌: %v", m)
	}
	// 再跑一轮（同日重复扫描）：不得重发通知，也不得把宽限期重新起算
	f.srv.runSubscriptionScan()
	if n := brandGateNotifPrefix(t, f, tid, "品牌定制进入"); n != 1 {
		t.Fatalf("进入宽限期的站内信每轮到期只发一次，实际 %d", n)
	}
	if again := brandGatePerms(t, f, tid).BrandGraceExpiresAt; again != p.BrandGraceExpiresAt {
		t.Fatalf("宽限期窗口不得逐轮重新起算: first=%q again=%q", p.BrandGraceExpiresAt, again)
	}
}

// D：四种不该起宽限的情形——企业根租户／超管授权／没配品牌／免费包到期，一律零键零通知。
func TestBrandGraceNotStartedWhenNotApplicable(t *testing.T) {
	f := newRenewFixture(t)
	now := time.Now()

	// ① 企业根租户（is_personal=false）：品牌本就不依赖套餐，摘除订阅也不该给它起宽限
	root, err := f.srv.Ten.Create("bg_root", "企业根租户", "", func() string {
		b, _ := json.Marshal(tenant.Perms{PackageCode: "renew_month", PackageExpires: now.Add(-time.Hour).Format(time.RFC3339)})
		return string(b)
	}())
	if err != nil {
		t.Fatalf("创建企业根租户失败: %v", err)
	}
	if err := f.srv.Ten.SetBranding(root.ID, "根品牌", "/brand/logo-root.png", "bg_root", ""); err != nil {
		t.Fatalf("预置根品牌失败: %v", err)
	}
	if err := f.srv.Ten.SetBrandHomeBg(root.ID, "/brand/bg-root.png", "cover"); err != nil {
		t.Fatalf("预置根品牌背景失败: %v", err)
	}

	// ② 超管显式授权的品牌租户
	granted := brandGateTenant(t, f, "bg_granted", "renew_month", now.Add(-time.Hour))
	if err := f.srv.Store.SetConfig("brand_grants", `{"`+fmt.Sprint(granted)+`":true}`); err != nil {
		t.Fatalf("写超管授权失败: %v", err)
	}

	// ③ 没配过任何品牌视觉的租户
	plain := brandGateTenant(t, f, "bg_plain", "renew_month", now.Add(-time.Hour))
	if err := f.srv.Ten.SetBranding(plain, "", "", "", ""); err != nil {
		t.Fatalf("清空品牌失败: %v", err)
	}
	if err := f.srv.Ten.SetBrandHomeBg(plain, "", ""); err != nil {
		t.Fatalf("清空品牌背景失败: %v", err)
	}

	// ④ 免费包到期（谈不上"权益回收"）
	freePkg, err := f.srv.Store.CreatePackage(&store.Package{
		Code: "bg_free_pkg", Name: "免费包", PType: store.PackageFree,
		Sentences: 100, PriceMoney: 0, DurationDays: 30, Enabled: 1,
	})
	if err != nil {
		t.Fatalf("创建免费包失败: %v", err)
	}
	_ = freePkg
	freet := brandGateTenant(t, f, "bg_free", "bg_free_pkg", now.Add(-time.Hour))

	f.srv.runSubscriptionScan()

	for _, c := range []struct {
		name string
		tid  int64
	}{
		{"企业根租户", root.ID}, {"超管授权", granted}, {"未配品牌", plain}, {"免费包到期", freet},
	} {
		if p := brandGatePerms(t, f, c.tid); p.BrandGraceExpiresAt != "" || p.BrandGraceNoticeStart {
			t.Fatalf("%s 不该进入品牌宽限期，实际 %+v", c.name, p)
		}
		if n := brandGateNotifPrefix(t, f, c.tid, "品牌定制"); n != 0 {
			t.Fatalf("%s 不该收到品牌宽限站内信，实际 %d 条", c.name, n)
		}
	}
	// 企业根租户与超管授权租户：订阅摘除后品牌照旧展示（展示闸与保存闸同源）
	for _, c := range []struct {
		name string
		tid  int64
	}{
		{"企业根租户", root.ID}, {"超管授权", granted},
	} {
		if m := brandGateGet(t, f, c.tid, ""); !brandGateVisualsShown(m) {
			t.Fatalf("%s 的品牌不应因订阅到期被回收: %v", c.name, m)
		}
	}
}

// E：宽限结束——日扫发一条回收通知并清键，重跑不重发，展示侧同步转隐藏。
func TestBrandGraceEndReclaimsOnce(t *testing.T) {
	f := newRenewFixture(t)
	tid := brandGateTenant(t, f, "bg_end", "renew_month", time.Now().AddDate(0, 0, -5))
	if err := f.srv.Store.SetBrandGrace(tid, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("预置已过宽限期失败: %v", err)
	}
	if err := f.srv.Store.MarkBrandGraceNotice(tid, store.BrandGraceNoticeStart); err != nil {
		t.Fatalf("预置进入宽限已发标记失败: %v", err)
	}
	f.srv.runBrandGraceScan()

	if p := brandGatePerms(t, f, tid); p.BrandGraceExpiresAt != "" || p.BrandGraceNoticeEnd || p.BrandGraceNoticeStart {
		t.Fatalf("回收后应清干净宽限期三键，实际 %+v", p)
	}
	if n := brandGateNotif(t, f, tid, "品牌定制已停止展示"); n != 1 {
		t.Fatalf("宽限结束应发一条回收站内信，实际 %d", n)
	}
	if m := brandGateGet(t, f, tid, ""); brandGateVisualsShown(m) {
		t.Fatalf("回收后匿名视角不该再有品牌: %v", m)
	}
	// 再跑一轮：已清键即终态，不得重复轰炸
	f.srv.runBrandGraceScan()
	if n := brandGateNotif(t, f, tid, "品牌定制已停止展示"); n != 1 {
		t.Fatalf("回收通知每轮只发一次，实际 %d", n)
	}
}

// F：续费到账后日扫只清键、不发任何通知（客户自己看得到品牌还在）。
func TestBrandGraceClearedOnRenewal(t *testing.T) {
	f := newRenewFixture(t)
	tid := brandGateTenant(t, f, "bg_renew", "renew_month", time.Now().AddDate(0, 0, -5))
	if err := f.srv.Store.SetBrandGrace(tid, time.Now().AddDate(0, 0, 10)); err != nil {
		t.Fatalf("预置宽限期失败: %v", err)
	}
	// 订阅身份回来（等价 MarkOrderPaid 后的权限快照）
	p := brandGatePerms(t, f, tid)
	p.PackageCode = "renew_month"
	p.PackageExpires = time.Now().AddDate(0, 0, 30).Format(time.RFC3339)
	if err := f.srv.Store.SaveTenantPerms(tid, p); err != nil {
		t.Fatalf("预置续费失败: %v", err)
	}
	f.srv.runBrandGraceScan()
	after := brandGatePerms(t, f, tid)
	if after.BrandGraceExpiresAt != "" {
		t.Fatalf("已续费的租户应清掉遗留宽限期键，实际 %q", after.BrandGraceExpiresAt)
	}
	if after.PackageCode != "renew_month" {
		t.Fatalf("清宽限键不得碰订阅身份，实际 %q", after.PackageCode)
	}
	if n := brandGateNotifPrefix(t, f, tid, "品牌定制"); n != 0 {
		t.Fatalf("续费清理不该发通知，实际 %d 条", n)
	}
}

// G：宽限天数的配置优先序与防呆（env BRAND_GRACE_DAYS > 库配置 > 默认 30；0＝当天回收）。
func TestBrandGraceDaysConfig(t *testing.T) {
	f := newRenewFixture(t)
	// 默认 30
	if d := f.srv.brandGraceDays(); d != defaultBrandGraceDays {
		t.Fatalf("默认宽限天数应为 %d，实际 %d", defaultBrandGraceDays, d)
	}
	// 库配置生效
	if err := f.srv.Store.SetConfig(cfgBrandGraceDays, "7"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	if d := f.srv.brandGraceDays(); d != 7 {
		t.Fatalf("库配置 7 天应生效，实际 %d", d)
	}
	// 环境变量压过库配置
	graceSetenv(t, "BRAND_GRACE_DAYS", "15")
	if d := f.srv.brandGraceDays(); d != 15 {
		t.Fatalf("环境变量应压过库配置，实际 %d", d)
	}
	// 超上限（91 起回落默认）与非数字
	graceSetenv(t, "BRAND_GRACE_DAYS", "91")
	if d := f.srv.brandGraceDays(); d != defaultBrandGraceDays {
		t.Fatalf("91 天超上限应回落默认 %d，实际 %d", defaultBrandGraceDays, d)
	}
	graceSetenv(t, "BRAND_GRACE_DAYS", "abc")
	if d := f.srv.brandGraceDays(); d != 7 {
		t.Fatalf("env 非数字应视为未配置并落回库配置 7，实际 %d", d)
	}
	// 库里配 0＝关闭宽限：到期当轮直接发「已停止展示」，不落截止时刻
	if err := f.srv.Store.SetConfig(cfgBrandGraceDays, "0"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	now := time.Now()
	tid := brandGateTenant(t, f, "bg_zero", "renew_month", now.Add(-time.Hour))
	f.srv.runSubscriptionScan()
	if p := brandGatePerms(t, f, tid); p.BrandGraceExpiresAt != "" {
		t.Fatalf("宽限天数 0 不该落截止时刻，实际 %q", p.BrandGraceExpiresAt)
	}
	if n := brandGateNotif(t, f, tid, "品牌定制已停止展示"); n != 1 {
		t.Fatalf("关闭宽限仍须告知客户发生了什么，实际收到 %d 条回收通知", n)
	}
	if n := brandGateNotifPrefix(t, f, tid, "品牌定制进入"); n != 0 {
		t.Fatalf("没有宽限期就不该发「进入宽限期」，实际 %d 条", n)
	}
	// 展示侧同步：0 天宽限的租户到期后即被闸
	if m := brandGateGet(t, f, tid, ""); brandGateVisualsShown(m) {
		t.Fatalf("宽限 0 天的租户到期后不该继续展示品牌: %v", m)
	}
}

// H：落库层语义——起算时复位两条去重标记；未知档位显式报错。
func TestBrandGraceStoreLayer(t *testing.T) {
	f := newRenewFixture(t)
	tid := brandGateTenant(t, f, "bg_store", "renew_month", time.Now().AddDate(0, 0, -3))
	// 先造一个"上一期已发过两条通知"的终态（模拟二次到期）
	if err := f.srv.Store.SetBrandGrace(tid, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("预置上一期宽限失败: %v", err)
	}
	if err := f.srv.Store.MarkBrandGraceNotice(tid, store.BrandGraceNoticeStart); err != nil {
		t.Fatalf("置位 start 标记失败: %v", err)
	}
	if err := f.srv.Store.MarkBrandGraceNotice(tid, store.BrandGraceNoticeEnd); err != nil {
		t.Fatalf("置位 end 标记失败: %v", err)
	}
	if err := f.srv.Store.SetBrandGrace(tid, time.Now().AddDate(0, 0, 30)); err != nil {
		t.Fatalf("二次起算失败: %v", err)
	}
	p := brandGatePerms(t, f, tid)
	if p.BrandGraceNoticeStart || p.BrandGraceNoticeEnd {
		t.Fatalf("新一期起算必须复位两条去重标记，否则二期永远收不到提醒: %+v", p)
	}
	// 起算不得覆盖 permissions 里的其他键（单字段原子写，回归 B1 整改口径）
	if p.PackageCode != "renew_month" || p.SentenceBalance != 100 {
		t.Fatalf("宽限期写入覆盖了其他权限键: %+v", p)
	}
	// 未知档位：既不当"已发"也不当"未发"，直接报错
	if err := f.srv.Store.MarkBrandGraceNotice(tid, "middle"); err == nil {
		t.Fatal("未知去重档位应报错，实际静默通过")
	}
	// 清键三键一并复位
	if err := f.srv.Store.ClearBrandGrace(tid); err != nil {
		t.Fatalf("清宽限期失败: %v", err)
	}
	c := brandGatePerms(t, f, tid)
	if c.BrandGraceExpiresAt != "" || c.BrandGraceNoticeStart || c.BrandGraceNoticeEnd {
		t.Fatalf("ClearBrandGrace 应三键归零，实际 %+v", c)
	}
}

// I：brandGraceDeadline 纯函数口径——脏值一律判无效（宁可不展示，不给到期租户白送品牌）。
func TestBrandGraceDeadlineParsesConservatively(t *testing.T) {
	now := time.Now()
	for _, c := range []struct {
		name  string
		perms *tenant.Perms
	}{
		{"nil 快照", nil},
		{"空键", &tenant.Perms{}},
		{"不可解析", &tenant.Perms{BrandGraceExpiresAt: "2026-13-45"}},
		{"非 RFC3339", &tenant.Perms{BrandGraceExpiresAt: "2026-09-27"}},
		{"恰好等于当前", &tenant.Perms{BrandGraceExpiresAt: now.Format(time.RFC3339)}},
		{"已过", &tenant.Perms{BrandGraceExpiresAt: now.Add(-time.Hour).Format(time.RFC3339)}},
	} {
		if _, ok := brandGraceDeadline(c.perms, now); ok {
			t.Fatalf("%s：应判不在宽限期", c.name)
		}
	}
	future := &tenant.Perms{BrandGraceExpiresAt: now.Add(24 * time.Hour).Format(time.RFC3339)}
	got, ok := brandGraceDeadline(future, now)
	if !ok || got.After(now.Add(25*time.Hour)) {
		t.Fatalf("未来截止时刻应判在宽限期内，实际 ok=%v got=%v", ok, got)
	}
}

// J：次序锁——宽限键落库失败时不发通知（否则客户收到"还有 30 天"而系统里根本没有宽限）。
func TestBrandGraceNotNotifiedWhenPersistFails(t *testing.T) {
	f := newRenewFixture(t)
	tid := brandGateTenant(t, f, "bg_fail", "renew_month", time.Now().Add(-time.Hour))
	if _, err := f.srv.Store.DB().Exec(`CREATE TRIGGER brand_grace_fail BEFORE UPDATE ON tenants
		  BEGIN SELECT RAISE(ABORT, '模拟 tenants 写入故障'); END`); err != nil {
		t.Fatalf("注入故障触发器失败: %v", err)
	}
	t.Cleanup(func() { _, _ = f.srv.Store.DB().Exec("DROP TRIGGER IF EXISTS brand_grace_fail") })

	f.srv.startBrandGraceAtExpiry(mustTenant(t, f, tid), "renew_month", time.Now())
	if n := brandGateNotifPrefix(t, f, tid, "品牌定制"); n != 0 {
		t.Fatalf("落库失败的一轮不得发通知，实际 %d 条", n)
	}
	// 故障撤销后重跑应能正常起算（幂等重试，不需要人工介入）
	if _, err := f.srv.Store.DB().Exec("DROP TRIGGER brand_grace_fail"); err != nil {
		t.Fatalf("撤除触发器失败: %v", err)
	}
	f.srv.startBrandGraceAtExpiry(mustTenant(t, f, tid), "renew_month", time.Now())
	if p := brandGatePerms(t, f, tid); p.BrandGraceExpiresAt == "" {
		t.Fatal("故障恢复后应可正常起算宽限期")
	}
	if n := brandGateNotifPrefix(t, f, tid, "品牌定制进入"); n != 1 {
		t.Fatalf("恢复后应补发一条进入宽限期通知，实际 %d", n)
	}
}

// mustTenant 读租户实体（快照不可缺，读不到即判失败）。
func mustTenant(t *testing.T, f *renewFixture, tid int64) *tenant.Tenant {
	t.Helper()
	tt, err := f.srv.Ten.GetByID(tid)
	if err != nil || tt == nil {
		t.Fatalf("读取租户 %d 失败: %v", tid, err)
	}
	return tt
}
