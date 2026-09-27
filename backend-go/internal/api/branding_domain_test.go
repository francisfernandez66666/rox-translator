// ============ branding_domain_test.go · 职责说明 ============
// F-76 品牌域前缀入库口径（★ 2026-09-27 〇-X 用户批准「校验只允许小写字母」，
// 字符集按同日确认的「小写字母＋数字、必须字母开头」执行）的测试：
//
//	A) 纯函数归一与拒绝表：大小写、全角空格、完整子域、带协议/路径/端口的 URL、
//	   基础域本身、主站前缀、外域整域名、连字符/下划线/数字开头/点号、保留名、清空；
//	B) 保存链路（真实 POST /api/tenant/branding）：合法值归一小写入库，非法值 400 且库里不动；
//	C) 占用校验：撞别家品牌域 409、撞别家企业编码 409（编码本身就是可访问子域），
//	   自己改自己不冲突；
//	D) 存量原值豁免：库里历史值（如 rox-test）整表回提时原样放行，一旦改动即走新规则；
//	E) 读写同源：保存后的前缀必须能被 `<前缀>.<基础域>` 的 Host 解析回该租户
//	   （F-76 的根因就是两侧不认同一个字符串）。
//
// ★ 单测自钉 sqlite 方言由 newRenewFixture 承担（AGENTS.md §一·4）。
// ==========================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/store"
)

// TestBrandDomainNormalize 归一与拒绝表（纯函数，不碰库）。
func TestBrandDomainNormalize(t *testing.T) {
	const base = "lexicorn.cn"
	const primary = "langcross.lexicorn.cn"
	for _, c := range []struct {
		name      string
		raw       string
		want      string // 期望前缀（wantErr 为真时忽略）
		wantErr   bool
		errSubstr string // 错误信息必须点到的关键词（防止退化成一句"格式错误"）
	}{
		{"裸前缀", "rox", "rox", false, ""},
		{"大写归一", "ROX", "rox", false, ""},
		{"混合大小写", "AutoBot1", "autobot1", false, ""},
		{"首尾空格", "  rox  ", "rox", false, ""},
		{"全角空格", "　rox　", "rox", false, ""},
		{"带基础域后缀", "rox.lexicorn.cn", "rox", false, ""},
		{"大写整子域", "ROX.Lexicorn.CN", "rox", false, ""},
		{"带协议与路径", "https://rox.lexicorn.cn/login", "rox", false, ""},
		{"带端口与斜杠", "rox.lexicorn.cn:8443/", "rox", false, ""},
		{"仅协议前缀", "http://rox", "rox", false, ""},
		{"字母加数字", "corp2026", "corp2026", false, ""},
		{"清空＝解绑", "", "", false, ""},
		// ---- 以下必须被拒绝（拒绝而不是静默入库是本条缺陷的核心）----
		{"基础域本身", "lexicorn.cn", "", true, "子域前缀"},
		{"主站完整主机名", "langcross.lexicorn.cn", "", true, "主站"},
		{"主站前缀裸写", "langcross", "", true, "主站"},
		{"外域整域名", "translate.example.com", "", true, "自定义完整域名"},
		{"连字符", "rox-test", "", true, "小写字母"},
		{"下划线", "rox_test", "", true, "小写字母"},
		{"数字开头", "1rox", "", true, "小写字母"},
		{"纯数字", "12345", "", true, "小写字母"},
		{"前导点", ".rox", "", true, ""},
		{"点号中段", "ro.x", "", true, ""},
		{"保留名 www", "www", "", true, "保留"},
		{"保留名 api", "API", "", true, "保留"},
		{"保留名 admin", "admin", "", true, "保留"},
		{"保留名 demo", "demo", "", true, "保留"},
		{"保留名 assets", "assets", "", true, "保留"},
	} {
		got, msg := normalizeBrandDomainPrefix(c.raw, base, primary)
		if c.wantErr {
			if msg == "" {
				t.Fatalf("%s：入参 %q 应被拒绝，实际放行成 %q", c.name, c.raw, got)
			}
			if c.errSubstr != "" && !strings.Contains(msg, c.errSubstr) {
				t.Fatalf("%s：错误信息应包含 %q（要能告诉客户怎么改），实际 %q", c.name, c.errSubstr, msg)
			}
			if got != "" {
				t.Fatalf("%s：被拒时不得返回值，实际 %q", c.name, got)
			}
			continue
		}
		if msg != "" {
			t.Fatalf("%s：入参 %q 应通过，实际报错 %q", c.name, c.raw, msg)
		}
		if got != c.want {
			t.Fatalf("%s：入参 %q 归一结果 want %q got %q", c.name, c.raw, c.want, got)
		}
	}
	// 基础域未配置（品牌子域解析关闭）时：裸前缀仍可存（不改库语义），整域名仍被拒
	if got, msg := normalizeBrandDomainPrefix("ROX", "", ""); msg != "" || got != "rox" {
		t.Fatalf("未配置基础域时裸前缀应可归一，实际 %q %q", got, msg)
	}
	if _, msg := normalizeBrandDomainPrefix("rox.other.com", "", ""); msg == "" {
		t.Fatal("未配置基础域时点号整域名仍应被拒（字符集口径统一）")
	}
}

// brandDomainCfg 给夹具库钉上品牌域配置（基础域与主站主机名），保存与解析两侧共用同一份。
func brandDomainCfg(t *testing.T, f *renewFixture) {
	t.Helper()
	if err := f.srv.Store.SetConfig("base_domain", "lexicorn.cn"); err != nil {
		t.Fatalf("写 base_domain 失败: %v", err)
	}
	if err := f.srv.Store.SetConfig("primary_host", "langcross.lexicorn.cn"); err != nil {
		t.Fatalf("写 primary_host 失败: %v", err)
	}
}

// brandDomainPost 以某租户管理员身份提交品牌表单（整表回提：只给 domain 与品牌名）。
// 返回状态码与解析后的响应体。
func brandDomainPost(t *testing.T, f *renewFixture, tid int64, token string, body map[string]any) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/tenant/branding", brandDomainBody(t, body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	brandGateMux(f.srv).ServeHTTP(rec, r)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析品牌保存响应失败: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, m
}

// brandDomainBody 把 map 编成请求体。
func brandDomainBody(t *testing.T, m map[string]any) *strings.Reader {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("编码请求体失败: %v", err)
	}
	return strings.NewReader(string(b))
}

// B~E：保存链路的归一、拒绝、占用与读写同源。
func TestBrandDomainSaveFlow(t *testing.T) {
	f := newRenewFixture(t)
	brandDomainCfg(t, f)
	now := time.Now()

	tid := brandGateTenant(t, f, "bgdoma", "renew_month", now.AddDate(0, 0, 20))
	tok := brandGateAdminToken(t, f, tid)
	other := brandGateTenant(t, f, "bgdomb", "renew_month", now.AddDate(0, 0, 20))
	otherTok := brandGateAdminToken(t, f, other)

	// B① 合法值（带大写与整子域写法）→ 200 且入库为纯前缀
	if code, m := brandDomainPost(t, f, tid, tok, map[string]any{
		"brand_name": "极光汽车", "domain": "AutoBot.lexicorn.cn", "brand_logo": "/brand/logo-a.png",
	}); code != http.StatusOK || m["success"] != true {
		t.Fatalf("合法品牌域应保存成功，实际 %d %v", code, m)
	}
	if got := brandDomainOf(t, f, tid); got != "autobot" {
		t.Fatalf("入库应归一为纯前缀 autobot，实际 %q", got)
	}
	// E 读写同源：该前缀拼成的 Host 必须解析回这家租户（F-76 根因即两侧字符串不一致）
	if m := brandGateGetByHost(t, f, "autobot.lexicorn.cn"); int64(m["tenant_id"].(float64)) != tid {
		t.Fatalf("品牌域 Host 应解析到租户 %d，实际 %v", tid, m["tenant_id"])
	}
	if m := brandGateGetByHost(t, f, "autobot.lexicorn.cn"); m["brand_name"] != "极光汽车" {
		t.Fatalf("Host 解析到的租户品牌应出栈，实际 %v", m)
	}

	// B② 非法值 → 400 且库里保持原值（拒绝必须落库前，不能半写）
	if code, m := brandDomainPost(t, f, tid, tok, map[string]any{
		"brand_name": "改名", "domain": "auto-bot", "brand_logo": "/brand/logo-a.png",
	}); code != http.StatusBadRequest {
		t.Fatalf("连字符品牌域应 400，实际 %d %v", code, m)
	} else if msg, _ := m["message"].(string); msg == "" {
		t.Fatal("拒绝必须带可展示的中文原因")
	}
	if got := brandDomainOf(t, f, tid); got != "autobot" {
		t.Fatalf("被拒的一轮不得改动库里值，实际 %q", got)
	}
	if got := brandNameOf(t, f, tid); got != "极光汽车" {
		t.Fatalf("同请求里的品牌名也不该写进去（校验在写侧之前），实际 %q", got)
	}

	// C① 撞别家品牌域 → 409（大写写法同样要拦住：归一后才比占用）
	if code, m := brandDomainPost(t, f, other, otherTok, map[string]any{
		"brand_name": "另一家", "domain": "AUTOBOT", "brand_logo": "/brand/logo-b.png",
	}); code != http.StatusConflict {
		t.Fatalf("撞别家品牌域应 409，实际 %d %v", code, m)
	}
	// C② 撞别家企业编码 → 409（编码本身就是能打开的子域，见 resolveDedicatedTenant 兜底）
	if code, m := brandDomainPost(t, f, other, otherTok, map[string]any{
		"brand_name": "另一家", "domain": "bgdoma", "brand_logo": "/brand/logo-b.png",
	}); code != http.StatusConflict {
		t.Fatalf("撞别家企业编码应 409，实际 %d %v", code, m)
	}
	// C③ 自己改自己（同一前缀换个大小写）不该被判冲突
	if code, m := brandDomainPost(t, f, tid, tok, map[string]any{
		"brand_name": "极光汽车", "domain": "AutoBot", "brand_logo": "/brand/logo-a.png",
	}); code != http.StatusOK || m["success"] != true {
		t.Fatalf("重复保存自己的品牌域不该被判占用，实际 %d %v", code, m)
	}
	// 他租户可以正常绑定自己的前缀（占用校验只拦撞车）
	if code, m := brandDomainPost(t, f, other, otherTok, map[string]any{
		"brand_name": "另一家", "domain": "vector", "brand_logo": "/brand/logo-b.png",
	}); code != http.StatusOK || m["success"] != true {
		t.Fatalf("不冲突的品牌域应可保存，实际 %d %v", code, m)
	}
	if int64(brandGateGetByHost(t, f, "vector.lexicorn.cn")["tenant_id"].(float64)) != other {
		t.Fatal("第二家租户的品牌域 Host 解析不符")
	}

	// 解绑：domain 清空 → 库里为空（"取消品牌域"这条正常路径不能被新校验堵死）
	if code, m := brandDomainPost(t, f, other, otherTok, map[string]any{
		"brand_name": "另一家", "domain": "", "brand_logo": "/brand/logo-b.png",
	}); code != http.StatusOK || m["success"] != true {
		t.Fatalf("清空品牌域应成功，实际 %d %v", code, m)
	}
	if got := brandDomainOf(t, f, other); got != "" {
		t.Fatalf("清空后库里应为空，实际 %q", got)
	}
	// 未登录/权限不足依旧由既有闸拦住（付费闸）：免费身份租户保存品牌应 403
	free := brandGateFreeTenant(t, f)
	if code, m := brandDomainPost(t, f, free, brandGateAdminToken(t, f, free), map[string]any{
		"brand_name": "免费家", "domain": "freeco",
	}); code != http.StatusForbidden {
		t.Fatalf("未解锁品牌的租户保存应 403，实际 %d %v", code, m)
	}
}

// D：存量原值豁免——历史不合规值整表回提时放行，一旦改动即走新规则。
// WHY：后台是整表回提（改 Logo 也会把 domain 原值带回来），不豁免会让老租户连"换张图"都保存不了。
func TestBrandDomainLegacyValueExemption(t *testing.T) {
	f := newRenewFixture(t)
	brandDomainCfg(t, f)
	tid := brandGateTenant(t, f, "bgleg", "renew_month", time.Now().AddDate(0, 2, 0))
	tok := brandGateAdminToken(t, f, tid)
	// 直接落一个不合规的历史值（模拟 F-76 之前的入库结果，等价演示站的 rox-test）
	if err := f.srv.Ten.SetBranding(tid, "存量品牌", "/brand/logo-legacy.png", "legacy-test", ""); err != nil {
		t.Fatalf("预置存量品牌域失败: %v", err)
	}
	// ① 整表回提且 domain 未变 → 放行，库里原样保留（读侧照旧生效，不把在跑的站点打停）
	if code, m := brandDomainPost(t, f, tid, tok, map[string]any{
		"brand_name": "存量品牌改个字", "domain": "legacy-test", "brand_logo": "/brand/logo-legacy.png",
	}); code != http.StatusOK || m["success"] != true {
		t.Fatalf("存量原值整表回提应放行，实际 %d %v", code, m)
	}
	if got := brandDomainOf(t, f, tid); got != "legacy-test" {
		t.Fatalf("存量原值不得被静默清洗，实际 %q", got)
	}
	if got := brandNameOf(t, f, tid); got != "存量品牌改个字" {
		t.Fatalf("豁免轮次其他字段应正常保存，实际 %q", got)
	}
	// ② 一旦改动（哪怕改成合法值）就走新规则；改成另一个不合规值即被拒
	if code, m := brandDomainPost(t, f, tid, tok, map[string]any{
		"brand_name": "存量品牌", "domain": "legacy2", "brand_logo": "/brand/logo-legacy.png",
	}); code != http.StatusOK || brandDomainOf(t, f, tid) != "legacy2" {
		t.Fatalf("改成合规值应成功，实际 %d %v / 库里 %q", code, m, brandDomainOf(t, f, tid))
	}
	if code, m := brandDomainPost(t, f, tid, tok, map[string]any{
		"brand_name": "存量品牌", "domain": "legacy-3", "brand_logo": "/brand/logo-legacy.png",
	}); code != http.StatusBadRequest {
		t.Fatalf("存量豁免不得覆盖真实改动的新校验，实际 %d %v", code, m)
	}
	// ③ 历史值仍然占着名额：别家撞它应 409（用合规写法的存量值验，
	//    不合规的历史值会先被字符集判 400——那是另一条更靠前的闸，见上表）
	other := brandGateTenant(t, f, "bgleg2", "renew_month", time.Now().AddDate(0, 2, 0))
	if err := f.srv.Ten.SetBranding(other, "别家存量", "/brand/logo-legacy.png", "oldco", ""); err != nil {
		t.Fatalf("预置别家存量品牌域失败: %v", err)
	}
	if code, m := brandDomainPost(t, f, tid, tok, map[string]any{
		"brand_name": "抢注", "domain": "oldco", "brand_logo": "/brand/logo-legacy.png",
	}); code != http.StatusConflict {
		t.Fatalf("历史值同样占着名额，撞它应 409，实际 %d %v", code, m)
	}
}

// brandDomainOf 读库里该租户的 domain 列。
func brandDomainOf(t *testing.T, f *renewFixture, tid int64) string {
	t.Helper()
	tt, err := f.srv.Ten.GetByID(tid)
	if err != nil || tt == nil {
		t.Fatalf("读取租户 %d 失败: %v", tid, err)
	}
	return tt.Domain
}

// brandNameOf 读库里该租户的 brand_name 列。
func brandNameOf(t *testing.T, f *renewFixture, tid int64) string {
	t.Helper()
	tt, err := f.srv.Ten.GetByID(tid)
	if err != nil || tt == nil {
		t.Fatalf("读取租户 %d 失败: %v", tid, err)
	}
	return tt.BrandName
}

// brandGateGetByHost 以品牌子域 Host 匿名读品牌接口（验读侧 `WHERE domain=?` 真能命中）。
func brandGateGetByHost(t *testing.T, f *renewFixture, host string) map[string]any {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, "/api/tenant/branding", nil)
	r.Host = host
	rec := httptest.NewRecorder()
	brandGateMux(f.srv).ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("Host=%s 品牌接口应 200，实际 %d", host, rec.Code)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析品牌响应失败: %v (%s)", err, rec.Body.String())
	}
	return m
}

// brandGateFreeTenant 造一家「个人身份 + 无任何付费身份」的租户及其管理员 Token（保存侧 403 断言用）。
func brandGateFreeTenant(t *testing.T, f *renewFixture) int64 {
	t.Helper()
	co, err := f.srv.Ten.Create("bgfree", "免费身份租户", "", "{}")
	if err != nil {
		t.Fatalf("创建免费身份租户失败: %v", err)
	}
	if err := f.srv.Ten.SetPersonal(co.ID, true); err != nil {
		t.Fatalf("置个人租户失败: %v", err)
	}
	if _, err := f.srv.Store.CreateUser(co.ID, "bgfree_admin", "hash-free", "免费管理员",
		store.RoleTenantAdmin, 1, 0); err != nil {
		t.Fatalf("创建管理员失败: %v", err)
	}
	return co.ID
}
