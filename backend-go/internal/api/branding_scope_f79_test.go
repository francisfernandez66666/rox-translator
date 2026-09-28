// ============ branding_scope_f79_test.go · 职责说明 ============
// F-79（2026-09-28 登记 → 同日用户拍板「把 F-79 的修法也加上，统一发布」）的自动化锁。
//
// 缺陷本体：品牌解析里「显式 `?tenant_id=`」这条支路过去**不做身份判定**，未登录访客在主站打
// `/api/tenant/branding?tenant_id=1`（或直接打开 `/?tenant_id=1`）就能把别家租户的品牌名/Logo/背景图
// 整份拿走——线上当时实测「首屏注入命中」，即注入面同样漏。判据见《缺陷核实与修复文档》§21.11。
//
// 本锁覆盖七条，每条都配了反证方向（防"结构性必红/必绿"）：
//
//	A) 匿名在主站点名别家 → 参数被忽略，回落平台品牌（哨兵名与哨兵 Logo 都在，租户的都不在）；
//	B) 同一请求带超管令牌 → 必须命中被点名的租户（A 的正面对照：证明 A 红不是"这家租户读不到"，
//	   也证明后台跨租户配置这条正路没被误杀）；
//	C) 该租户自己的管理员点名自己 → 仍拿真值（F-75 的读写同源红线：管理台把隐藏后的空字段当现值
//	   载回，客户点一次保存就抹掉真配置）；
//	D) 别家租户的管理员点名第三方 → 忽略（"能点名"不等于"能看别家"，收紧到自家）；
//	E) 按访问域名解析那条路**一字未动**（Host=该租户品牌域、不带参数 → 真值）；
//	F) 脏参数（"abc"/"0"/"-7"/空串）→ 200 且不 panic、一律回落平台，绝不半路把公开首屏打成错误页；
//	G) 与 F-75 展示闸的叠加口径：被闸住的租户，其**普通成员**用显式参数仍然拿不到视觉字段——
//	   证明"放宽第②条（本租户成员可点名自己）"没有在付费闸上捅开口子。
//
// ★ 断言落在 ID>1 的租户上（夹具自身租户是 1，平台宿主语义特殊）。
// ★ 方言自钉由 newRenewFixture 承担（AGENTS §一·4：内存 SQLite 必须显式钉住，否则 PG 模式下假红）。
// ★ 注入面判据用 ASCII 的 Logo 路径而不是中文品牌名——序列化对 HTML 转义的处理会变，别把文案钉进锁里。
// ============================================================
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// f79Get 以指定 Host + 原始查询串 + 令牌读品牌接口，返回解析后的出栈 map。
// 参数 query 传的是「?」后面那一段（含空串），因此脏值与"不带参数"两种形态都能覆盖。
func f79Get(t *testing.T, f *renewFixture, host, query, token string) map[string]any {
	t.Helper()
	u := "/api/tenant/branding"
	if query != "" {
		u += "?" + query
	}
	r := httptest.NewRequest(http.MethodGet, u, nil)
	if host != "" {
		r.Host = host
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	brandGateMux(f.srv).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("品牌接口应 200（忽略参数是降级，不是报错），实际 %d body=%s", rec.Code, rec.Body.String())
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

// f79Tenant 造一家「付费在效 + 配了品牌 + 有自己的品牌域前缀 + 一名管理员」的租户，返回其 id。
// 前缀同时写进 tenants.domain（E 段要靠 Host 命中它）；品牌视觉三件套用可区分的 ASCII 路径。
func f79Tenant(t *testing.T, f *renewFixture, prefix, brandName string) int64 {
	t.Helper()
	tid := brandGateTenant(t, f, prefix, "renew_month", time.Now().AddDate(0, 0, 30))
	// brandGateTenant 把 domain 写成了 code 同值，这里换成能拼成子域的前缀（F-76 之后写侧也要求裸小写）
	if err := f.srv.Ten.SetBranding(tid, brandName, "/brand/logo-"+prefix+".png", prefix, ""); err != nil {
		t.Fatalf("预置品牌域失败: %v", err)
	}
	if err := f.srv.Ten.SetBrandHomeBg(tid, "/brand/bg-"+prefix+".png", "cover"); err != nil {
		t.Fatalf("预置品牌背景失败: %v", err)
	}
	return tid
}

// f79UserToken 在指定租户下建一名给定角色的用户并签发 JWT（视角对照用；username 须全局唯一）。
func f79UserToken(t *testing.T, f *renewFixture, tid int64, username, role string) string {
	t.Helper()
	if _, err := f.srv.Store.CreateUser(tid, username, "hash-f79", "F79 视角用户", role, 1, 0); err != nil {
		t.Fatalf("创建用户 %s 失败: %v", username, err)
	}
	u, err := f.srv.Store.GetUserByUsername(tid, username)
	if err != nil {
		t.Fatalf("读取用户 %s 失败: %v", username, err)
	}
	tk, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	return tk
}

// f79Fixture 起夹具并铺两件事：平台品牌哨兵（回落形态的判据）、品牌基础域（Host 解析的前提）。
// 返回（ victim 被点名租户, other 别家租户, 超管令牌 ）。
func f79Fixture(t *testing.T) (*renewFixture, int64, int64, string) {
	t.Helper()
	f := newRenewFixture(t)
	if err := f.srv.Store.SetConfig("base_domain", brandGateBaseDomain); err != nil {
		t.Fatalf("配置品牌基础域失败: %v", err)
	}
	if err := f.srv.setPlatformBranding(map[string]string{
		"brand_name": "平台哨兵", "brand_logo": "/brand/logo-platform.png", "brand_home_bg": "/brand/bg-platform.png",
	}); err != nil {
		t.Fatalf("预置平台品牌失败: %v", err)
	}
	victim := f79Tenant(t, f, "vic", "极光汽车")
	other := f79Tenant(t, f, "oth", "别家品牌")
	if victim <= 1 || other <= 1 || victim == other {
		t.Fatalf("夹具租户 id 异常: victim=%d other=%d", victim, other)
	}
	return f, victim, other, brandGateSuperToken(t, f)
}

// f79Num 把 JSON 里的 tenant_id 读成 int64（序列化后是 float64，直接断言等于 int 会永远不等）。
func f79Num(t *testing.T, m map[string]any, key string) int64 {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("出栈缺数字字段 %s: %v", key, m[key])
	}
	return int64(v)
}

// A/B/E：匿名点名被忽略、超管点名生效、按域解析照旧——三条必须在同一个夹具里一起看，
// 否则 A 段可能只是"这家租户根本读不出来"的假绿。
func TestBrandingExplicitTenantIDScope(t *testing.T) {
	f, victim, other, superTK := f79Fixture(t)

	// A) 匿名在主站点名 victim：回落平台哨兵，victim 的三件视觉一件都不许出现
	m := f79Get(t, f, brandGateBaseDomain, fmt.Sprintf("tenant_id=%d", victim), "")
	if got := f79Num(t, m, "tenant_id"); got != 0 {
		t.Fatalf("F-79 复发：匿名跨域点名仍被采纳，tenant_id=%d（应为 0＝回落平台）", got)
	}
	if s, _ := m["brand_name"].(string); s != "平台哨兵" {
		t.Fatalf("匿名点名应回落平台品牌，实际 brand_name=%q", s)
	}
	if s, _ := m["brand_logo"].(string); s != "/brand/logo-platform.png" {
		t.Fatalf("匿名点名的 Logo 必须是平台的，实际 %q", s)
	}

	// B) 反证：同一条请求带超管令牌必须命中 victim——
	//    ① 证明 A 段红不是"victim 读不出来"；② 证明后台跨租户配置这条正路没被误杀。
	ms := f79Get(t, f, brandGateBaseDomain, fmt.Sprintf("tenant_id=%d", victim), superTK)
	if got := f79Num(t, ms, "tenant_id"); got != victim {
		t.Fatalf("超管点名必须命中指定租户（正路被误杀＝白修），实际 tenant_id=%d want=%d", got, victim)
	}
	if !brandGateVisualsShown(ms) {
		t.Fatalf("超管视角应拿得到 victim 的真品牌视觉: %v", ms)
	}

	// C) 该租户管理员点名自己仍拿真值（读写同源：管理台回显被闸成空＝客户一次保存抹掉真配置）
	vicAdmin := brandGateAdminToken(t, f, victim)
	mc := f79Get(t, f, brandGateBaseDomain, fmt.Sprintf("tenant_id=%d", victim), vicAdmin)
	if got := f79Num(t, mc, "tenant_id"); got != victim {
		t.Fatalf("本租户管理员应能点名自己家，实际 tenant_id=%d", got)
	}
	if !brandGateVisualsShown(mc) {
		t.Fatalf("本租户管理员必须看到自家真值: %v", mc)
	}

	// D) 别家管理员点名 victim：忽略（"成员可点名"只到自己家为止）
	othAdmin := brandGateAdminToken(t, f, other)
	md := f79Get(t, f, brandGateBaseDomain, fmt.Sprintf("tenant_id=%d", victim), othAdmin)
	if got := f79Num(t, md, "tenant_id"); got != 0 {
		t.Fatalf("别家管理员不得点名 victim，实际 tenant_id=%d", got)
	}

	// E) 按访问域名解析这条路一字未动：Host 就是 victim 的品牌域、不带参数
	me := f79Get(t, f, "vic."+brandGateBaseDomain, "", "")
	if got := f79Num(t, me, "tenant_id"); got != victim {
		t.Fatalf("品牌域按 Host 解析被改坏了，实际 tenant_id=%d want=%d", got, victim)
	}
	if s, _ := me["brand_logo"].(string); s != "/brand/logo-vic.png" {
		t.Fatalf("品牌域访客应拿到该域对应的 Logo，实际 %q", s)
	}
	// 主站域名上「不带参数」也必须是平台品牌（F-74/根域名不跟随登录用户的既有口径）
	mm := f79Get(t, f, brandGateBaseDomain, "", vicAdmin)
	if got := f79Num(t, mm, "tenant_id"); got != 0 {
		t.Fatalf("主站域名不带参数应回落平台，实际 tenant_id=%d", got)
	}
}

// F：脏参数一律回落平台，且接口仍 200——公开首屏不许因为一个奇怪的查询串变成错误页。
func TestBrandingExplicitTenantIDDirtyValues(t *testing.T) {
	f, victim, _, superTK := f79Fixture(t)
	for _, q := range []string{"tenant_id=abc", "tenant_id=0", "tenant_id=-7", "tenant_id=", "tenant_id=1e999", "tenant_id=999999999"} {
		m := f79Get(t, f, brandGateBaseDomain, q, "")
		if got := f79Num(t, m, "tenant_id"); got != 0 {
			t.Fatalf("脏参数 %q 被采纳了（tenant_id=%d），期望忽略并回落平台", q, got)
		}
		if s, _ := m["brand_name"].(string); s != "平台哨兵" {
			t.Fatalf("脏参数 %q 没回落平台品牌，实际 %q", q, s)
		}
	}
	// 反向对照：同一家 victim 在超管手里确实读得出来，说明上面六条不是"永远读不到"的假绿
	ms := f79Get(t, f, brandGateBaseDomain, fmt.Sprintf("tenant_id=%d", victim), superTK)
	if got := f79Num(t, ms, "tenant_id"); got != victim {
		t.Fatalf("反证失败：超管也读不到 victim（want=%d got=%d），前面六条脏值断言是假绿", victim, got)
	}
}

// G：与 F-75 付费闸叠加——被闸住的租户，其普通成员用显式参数仍然拿不到视觉字段。
// 这条锁的是"放宽本租户成员可点名自己"这个决定：如果它把付费闸捅开一个口子，这里就红。
func TestBrandingExplicitIDDoesNotPunchPaidGate(t *testing.T) {
	f := newRenewFixture(t)
	if err := f.srv.Store.SetConfig("base_domain", brandGateBaseDomain); err != nil {
		t.Fatalf("配置品牌基础域失败: %v", err)
	}
	// 到期且已过宽限的租户（匿名与该租户普通成员都应看不到视觉，只有超管/租户管理员豁免）
	dead := brandGateTenant(t, f, "f79dead", "renew_month", time.Now().AddDate(0, 0, -40))
	if err := f.srv.Store.SetBrandGrace(dead, time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("预置已过宽限期失败: %v", err)
	}
	memberTK := f79UserToken(t, f, dead, fmt.Sprintf("f79_member_%d", dead), store.RoleUser)
	m := f79Get(t, f, brandGateBaseDomain, fmt.Sprintf("tenant_id=%d", dead), memberTK)
	if brandGateVisualsShown(m) {
		t.Fatalf("普通成员不得绕过 F-75 展示闸看到视觉字段: %v", m)
	}
	// 但归属字段照旧（这是身份而非权益，注册页与排障依赖它），且参数确实被采纳了（tenant_id 命中）
	if got := f79Num(t, m, "tenant_id"); got != dead {
		t.Fatalf("本租户成员点名自己应命中（否则 D/G 两条无从区分），实际 tenant_id=%d", got)
	}
	if s, _ := m["code"].(string); s != "f79dead" {
		t.Fatalf("归属字段 code 不该被回收，实际 %q", s)
	}
	// 超管同一条请求仍拿真值——证明隐藏来自闸而不是来自"读不到"
	if ms := f79Get(t, f, brandGateBaseDomain, fmt.Sprintf("tenant_id=%d", dead), brandGateSuperToken(t, f)); !brandGateVisualsShown(ms) {
		t.Fatalf("反证失败：超管也看不到该租户品牌，G 段是假绿: %v", ms)
	}
}

// 注入面：`/?tenant_id=` 的首屏注入是 F-79 的案发现场（线上实测注入命中）。
// 同一条判据在接口面（上一条）与 HTML 面各钉一次，防止有人只堵了一半。
func TestBrandingIndexInjectionIgnoresForeignTenantID(t *testing.T) {
	f, victim, _, superTK := f79Fixture(t)
	f.srv.Dist = brandDist(t)

	render := func(query, token string) string {
		r := httptest.NewRequest(http.MethodGet, "/?"+query, nil)
		r.Host = brandGateBaseDomain
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		f.srv.serveIndexHTML(rec, r, filepath.Join(f.srv.Dist, "index.html"))
		return rec.Body.String()
	}

	html := render(fmt.Sprintf("tenant_id=%d", victim), "")
	if strings.Contains(html, "/brand/logo-vic.png") || strings.Contains(html, "bg-vic") {
		t.Fatal("F-79 复发：匿名访客的首屏注入里带着别家租户的品牌件")
	}
	if !strings.Contains(html, "/brand/logo-platform.png") {
		t.Fatalf("匿名点名回落平台后，注入里必须有平台品牌件（否则注入整块没了）: 片段=%s", cutForErr(html))
	}
	// 反证：同一条 URL 在超管手里确实会注入 victim 的品牌——证明上面不是"注入根本没跑"的假绿
	if got := render(fmt.Sprintf("tenant_id=%d", victim), superTK); !strings.Contains(got, "/brand/logo-vic.png") {
		t.Fatalf("超管预览的首屏注入丢了指定租户品牌（正路被误杀）: 片段=%s", cutForErr(got))
	}
}

// cutForErr 截取响应前 400 字节供失败信息定位（避免整页 HTML 灌进测试输出）。
func cutForErr(s string) string {
	if len(s) > 400 {
		return s[:400]
	}
	return s
}
