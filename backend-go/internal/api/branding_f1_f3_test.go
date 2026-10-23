// ============ branding_f1_f3_test.go · 职责说明 ============
// 2026-10-23《后台报障修复方案》F1/F3 的机械锁：
//
//	F1-a/b  超管显式 tenant_id=0 必须命中平台根，且**短路 Host 解析**——超管从租户品牌域
//	        进后台时不能被那个租户的品牌带偏；非超管点名 0 仍不生效（回落 Host）。
//	F1-c    平台品牌分支必须读写 brand_name_en / brand_names（旧形态整段丢弃）。
//	F3-a    注册/建租户的种入链路要写 tenants.brand_name（旧形态该列恒空，中文名框灰显占位符）。
//	F3-b    保存合并语义：库内为底、表单非空覆盖；显式 brand_names 为权威全集（可清空）。
//	        seed 只许动三列名称，不许碰 logo/domain/links（SetBranding 整行覆写的坑）。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'TestMergeBrandNameMap|TestPlatformBranding|TestSeedTenantBrandTerms'
// ============================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMergeBrandNameMap F3-b 的纯函数判据：显式全集/库内为底/表单覆盖/空值剔除四条。
func TestMergeBrandNameMap(t *testing.T) {
	cases := []struct {
		name     string
		base     string
		explicit string
		zh, en   string
		want     map[string]string
	}{
		{"未提供名字时整份保留", `{"zh":"比亚迪","en":"BYD"}`, "", "", "", map[string]string{"zh": "比亚迪", "en": "BYD"}},
		{"表单覆盖中文", `{"zh":"比亚迪","en":"BYD"}`, "", "新中文", "", map[string]string{"zh": "新中文", "en": "BYD"}},
		{"表单覆盖英文", `{"zh":"比亚迪","en":"BYD"}`, "", "", "NewEN", map[string]string{"zh": "比亚迪", "en": "NewEN"}},
		{"显式全集是权威（可清空）", `{"zh":"比亚迪","en":"BYD"}`, `{"en":"OnlyEN"}`, "", "", map[string]string{"en": "OnlyEN"}},
		{"显式空对象即清空", `{"zh":"比亚迪","en":"BYD"}`, `{}`, "", "", map[string]string{}},
		{"空串不出库", `{}`, `{"zh":"","en":"X","ja":" "}`, "", "", map[string]string{"en": "X"}},
		{"非法库内 JSON 按空底", `not-json`, "", "甲", "Yi", map[string]string{"zh": "甲", "en": "Yi"}},
	}
	for _, c := range cases {
		got, err := mergeBrandNameMap(c.base, c.explicit, c.zh, c.en)
		if err != nil {
			t.Fatalf("%s：不应报错，got %v", c.name, err)
		}
		if len(got) != len(c.want) {
			t.Fatalf("%s：got=%v want=%v", c.name, got, c.want)
		}
		for k, v := range c.want {
			if got[k] != v {
				t.Fatalf("%s：键 %s got=%q want=%q（全量 got=%v）", c.name, k, got[k], v, got)
			}
		}
	}
	// 反证：显式 JSON 非法时必须报错，不许静默当空集把库洗掉
	if _, err := mergeBrandNameMap(`{"zh":"甲"}`, `{bad`, "", ""); err == nil {
		t.Fatal("显式 brand_names 非法 JSON 必须报错（静默当空集会整列洗掉品牌名）")
	}
}

// TestPlatformBrandingEnglishNameRoundTrip F1-c + F1-a：平台分支读写英文名/多语名，
// 且显式 0 在**租户品牌域**上也必须命中平台根（短路 Host 解析）。
func TestPlatformBrandingEnglishNameRoundTrip(t *testing.T) {
	f := newRenewFixture(t)
	if err := f.srv.Store.SetConfig("base_domain", brandGateBaseDomain); err != nil {
		t.Fatalf("配置品牌基础域失败: %v", err)
	}
	if err := f.srv.setPlatformBranding(map[string]string{"brand_name": "平台旧名"}); err != nil {
		t.Fatalf("预置平台品牌失败: %v", err)
	}
	victim := brandGateTenant(t, f, "vic", "renew_month", time.Now().AddDate(0, 0, 30))
	superTK := brandGateSuperToken(t, f)

	names := `{"zh":"平台新名","en":"Platform EN","ja":"プラット"}`
	body, _ := json.Marshal(map[string]interface{}{
		"id": 0, "brand_name": "平台新名", "brand_name_en": "Platform EN", "brand_names": names,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/tenant/branding", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+superTK)
	rec := httptest.NewRecorder()
	f.srv.handleTenantBrandingSet(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("超管保存平台品牌应 200，实际 %d body=%s", rec.Code, rec.Body.String())
	}

	// 在 victim 的品牌域上显式点名 0：必须是平台品牌（旧形态会被 Host 解析改成 victim）
	m := f79Get(t, f, "vic."+brandGateBaseDomain, "tenant_id=0", superTK)
	if got := f79Num(t, m, "tenant_id"); got != 0 {
		t.Fatalf("超管显式 tenant_id=0 应命中平台根，实际 %d（Host 解析没被短路）", got)
	}
	if s, _ := m["brand_name"].(string); s != "平台新名" {
		t.Fatalf("平台 brand_name 回读不符：%q", s)
	}
	if s, _ := m["brand_name_en"].(string); s != "Platform EN" {
		t.Fatalf("F1-c 未生效：平台 brand_name_en 回读=%q want=Platform EN", s)
	}
	if s, _ := m["brand_names"].(string); !strings.Contains(s, "プラット") || !strings.Contains(s, "平台新名") {
		t.Fatalf("F1-c 未生效：平台 brand_names 回读=%q", s)
	}

	// 非超管点名 0 仍不生效：Host 是 victim 域 ⇒ 回 victim（证明 0 只对超管放行）
	vicAdmin := brandGateAdminToken(t, f, victim)
	md := f79Get(t, f, "vic."+brandGateBaseDomain, "tenant_id=0", vicAdmin)
	if got := f79Num(t, md, "tenant_id"); got != victim {
		t.Fatalf("非超管点名 0 不得命中平台根，应回落 Host 解析的 victim=%d，实际 %d", victim, got)
	}

	// 反向对照：超管不带参数在 victim 域上仍是 victim（Host 解析这条正路没被短路掉）
	me := f79Get(t, f, "vic."+brandGateBaseDomain, "", superTK)
	if got := f79Num(t, me, "tenant_id"); got != victim {
		t.Fatalf("不带参数时 Host 解析必须照旧命中 victim=%d，实际 %d", victim, got)
	}
}

// TestRegisterWritesBrandNameColumn F3-a 的**真实注册链路**判据（不是直调 seed）：
// 企业自助注册带品牌名 ⇒ tenants.brand_name 必须等于品牌中文名、三列一致。
// 旧形态只写 brand_names/brand_name_en，注册后的品牌页中文名框恒空并灰显占位符。
func TestRegisterWritesBrandNameColumn(t *testing.T) {
	f := f77Fixture(t)
	code, m := f77Register(t, f.srv, "langcross.lexicorn.cn", map[string]any{
		"username": "f3_reg_user", "password": "pw123456", "code": "f3regco", "name": "F3 注册企业",
		"email": "f3reg@example.com", "agreed": true, "type": "enterprise", "role_choice": "admin",
		"brand_name": "比亚迪", "brand_name_en": "BYD",
	})
	if code != http.StatusOK {
		t.Fatalf("企业注册应 200，实际 %d body=%v", code, m)
	}
	tn, _ := m["tenant"].(map[string]any)
	tid := int64(0)
	if v, ok := tn["id"].(float64); ok {
		tid = int64(v)
	}
	if tid <= 0 {
		t.Fatalf("注册响应缺 tenant.id：%v", m)
	}
	got, err := f.srv.Ten.GetByID(tid)
	if err != nil {
		t.Fatalf("回读新租户失败: %v", err)
	}
	if got.BrandName != "比亚迪" {
		t.Fatalf("F3-a 未生效：注册后 brand_name=%q want=比亚迪", got.BrandName)
	}
	if got.BrandNameEn != "BYD" || !strings.Contains(got.BrandNames, "比亚迪") {
		t.Fatalf("注册后三列不一致：brand_name_en=%q brand_names=%q", got.BrandNameEn, got.BrandNames)
	}
}

// TestSeedTenantBrandTermsThreeColumns F3-a/F3-b 的 DB 级判据：
// 种入写 brand_name 且只写三列；保存时空中文名保留库内 zh；显式全集可清空。
func TestSeedTenantBrandTermsThreeColumns(t *testing.T) {
	f := newRenewFixture(t)
	co, err := f.srv.Ten.Create("f3_co", "F3 公司", "", "{}")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	// 模拟注册链路：只种品牌名，brand_name 列必须同步写上（旧形态恒空）
	f.srv.seedTenantBrandTerms(co.ID, "比亚迪", "BYD", "")
	got, err := f.srv.Ten.GetByID(co.ID)
	if err != nil {
		t.Fatalf("回读租户失败: %v", err)
	}
	if got.BrandName != "比亚迪" {
		t.Fatalf("F3-a 未生效：注册后 brand_name=%q want=比亚迪", got.BrandName)
	}
	if !strings.Contains(got.BrandNames, "比亚迪") || got.BrandNameEn != "BYD" {
		t.Fatalf("三列不一致：brand_names=%q brand_name_en=%q", got.BrandNames, got.BrandNameEn)
	}

	// 预置视觉与链接：后续种入只许动名称三列
	if err := f.srv.Ten.SetBranding(co.ID, got.BrandName, "/brand/logo-f3.png", "f3co", `[{"label":"帮助","url":"/help"}]`); err != nil {
		t.Fatalf("预置视觉失败: %v", err)
	}
	// 保存时空中文名/英文名（旧 BrandP 载荷形态）⇒ zh 必须保留
	f.srv.seedTenantBrandTerms(co.ID, "", "", "")
	got, _ = f.srv.Ten.GetByID(co.ID)
	if got.BrandName != "比亚迪" || !strings.Contains(got.BrandNames, "比亚迪") {
		t.Fatalf("F3-b 未生效：空表单保存把中文名洗掉了 brand_name=%q brand_names=%q", got.BrandName, got.BrandNames)
	}
	if got.BrandLogo != "/brand/logo-f3.png" || got.Domain != "f3co" || !strings.Contains(got.BrandLinks, "/help") {
		t.Fatalf("种入链路动了视觉/域名/链接：logo=%q domain=%q links=%q", got.BrandLogo, got.Domain, got.BrandLinks)
	}

	// 显式全集是权威：只给 en ⇒ zh 被删除（用户能真正清空）
	f.srv.seedTenantBrandTerms(co.ID, "", "", `{"en":"OnlyEN"}`)
	got, _ = f.srv.Ten.GetByID(co.ID)
	if got.BrandName != "" || strings.Contains(got.BrandNames, "比亚迪") {
		t.Fatalf("显式全集未生效：brand_name=%q brand_names=%q（应为空/仅 en）", got.BrandName, got.BrandNames)
	}
	if !strings.Contains(got.BrandNames, "OnlyEN") {
		t.Fatalf("显式全集丢了 en：%q", got.BrandNames)
	}
}
