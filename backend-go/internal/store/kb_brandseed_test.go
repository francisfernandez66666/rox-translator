// ============ 本文件职责中文说明 ============
// 品牌固定用法种入企业包 L1 术语的单元测试：
// SeedBrandTerms 应把品牌中文名→各目标语名称写入企业包，且遵循语言兜底规则
// （无特定语言名时：zh_hant 沿用中文名，其余语言用英文名兜底；无可用译文则跳过）。
// =============================================
package store

import "testing"

// tenantPkgID 返回租户企业包（code='tenant'）的 ID。
func tenantPkgID(t *testing.T, s *Store) int64 {
	t.Helper()
	p, err := s.queryKBPackage("SELECT " + kbPkgCols + " FROM kb_packages WHERE tenant_id=? AND code='tenant'", 1)
	if err != nil {
		t.Fatalf("查询企业包失败: %v", err)
	}
	return p.ID
}

// TestSeedBrandTermsEnFallback 验证：中文名+英文名 → 除 zh 外均种入（zh_hant 沿用中文名，其余英文名兜底）。
func TestSeedBrandTermsEnFallback(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	if err := s.SeedBrandTerms(1, map[string]string{"zh": "极石", "en": "ROX"}); err != nil {
		t.Fatalf("SeedBrandTerms 失败: %v", err)
	}
	entries, _, err := s.ListEntriesPage(1, tenantPkgID(t, s), LayerTerm, "", "", 1, 2000)
	if err != nil {
		t.Fatalf("ListEntriesPage 失败: %v", err)
	}
	expect := map[string]string{"en": "ROX", "ru": "ROX", "zh_hant": "极石", "de": "ROX"}
	got := map[string]string{}
	for _, e := range entries {
		if e.SourceText != "极石" || e.SourceLang != "zh" {
			t.Fatalf("意外的来源: %+v", e)
		}
		got[e.TargetLang] = e.TargetText
	}
	for lang, want := range expect {
		if got[lang] != want {
			t.Fatalf("语言 %s 译文=%q, 期望 %q（全部=%v）", lang, got[lang], want, got)
		}
	}
	// 排除 zh：不应种入源语言自身
	if got["zh"] != "" {
		t.Fatalf("不应种入 zh 语言自身")
	}
}

// TestSeedBrandTermsZhOnly 验证：仅有中文名无英文名 → 只种入 zh_hant（zh→zh_hant 品牌名不变），其余跳过。
func TestSeedBrandTermsZhOnly(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	if err := s.SeedBrandTerms(1, map[string]string{"zh": "极石"}); err != nil {
		t.Fatalf("SeedBrandTerms 失败: %v", err)
	}
	entries, _, err := s.ListEntriesPage(1, tenantPkgID(t, s), LayerTerm, "", "", 1, 2000)
	if err != nil {
		t.Fatalf("ListEntriesPage 失败: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("应仅种入 zh_hant 一条, 实际 %d 条: %+v", len(entries), entries)
	}
	if entries[0].TargetLang != "zh_hant" || entries[0].TargetText != "极石" {
		t.Fatalf("zh_hant 应沿用中文名, got %+v", entries[0])
	}
}

// TestSeedBrandTermsNoZh 验证：无中文名 → 不种入任何术语。
func TestSeedBrandTermsNoZh(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	if err := s.SeedBrandTerms(1, map[string]string{"en": "ROX"}); err != nil {
		t.Fatalf("SeedBrandTerms 失败: %v", err)
	}
	entries, _, err := s.ListEntriesPage(1, tenantPkgID(t, s), LayerTerm, "", "", 1, 2000)
	if err != nil {
		t.Fatalf("ListEntriesPage 失败: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("无中文名时不应种入, 实际 %d 条", len(entries))
	}
}

// TestSeedBrandTermsLangSpecific 验证：显式提供的语言名优先于英文名兜底。
func TestSeedBrandTermsLangSpecific(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	if err := s.SeedBrandTerms(1, map[string]string{"zh": "极石", "en": "ROX", "ru": "РОКС"}); err != nil {
		t.Fatalf("SeedBrandTerms 失败: %v", err)
	}
	entries, _, err := s.ListEntriesPage(1, tenantPkgID(t, s), LayerTerm, "", "", 1, 2000)
	if err != nil {
		t.Fatalf("ListEntriesPage 失败: %v", err)
	}
	got := map[string]string{}
	for _, e := range entries {
		got[e.TargetLang] = e.TargetText
	}
	if got["ru"] != "РОКС" {
		t.Fatalf("ru 应使用显式名 РОКС, got=%q", got["ru"])
	}
	if got["de"] != "ROX" {
		t.Fatalf("de 无显式名应回退英文名 ROX, got=%q", got["de"])
	}
}