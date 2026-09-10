// ============ 本文件职责中文说明 ============
// 品牌名统一翻译（2026-09-10 需求回归）引擎方法单元测试：
//   - fetchBrandTerms：一次查询 KB module=brand+layer=1 术语，组装 map[lang]map[src]target
//   - normalizeFileBrandTerms：文件路径译后剥离「ROX vehicles/motor」等自创后缀
//   - normalizeBrandTerms：对话路径译后归一化（覆盖后缀在前/环绕等形态）
// 使用内存 SQLite 构建 Store 并插入品牌术语数据，不依赖业务库。
// ========================================
package engine

import (
	"context"
	"database/sql"
	"testing"

	"translator/internal/config"
	"translator/internal/tenant"
)

// insertBrandTerm 测试辅助：向内存 Store 插入一条品牌术语（module=brand, layer=1）。
func insertBrandTerm(t *testing.T, db *sql.DB, pkgID int64, src, lang, target string) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO kb_entries
		(tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at)
		VALUES (1,?,1,'zh',?,?,?, 'brand', datetime('now'), datetime('now'))`,
		pkgID, src, lang, target); err != nil {
		t.Fatalf("插入品牌术语 %s→%s 失败: %v", src, lang, err)
	}
}

// createBrandPackage 测试辅助：建一个租户级 source 品牌包，返回包 ID。
func createBrandPackage(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at)
		VALUES (1,0,'brand_test','品牌测试包','tenant','source',0,0,datetime('now'),datetime('now'))`)
	if err != nil {
		t.Fatalf("插入品牌包失败: %v", err)
	}
	pkgID, err := res.LastInsertId()
	if err != nil || pkgID <= 0 {
		t.Fatalf("获取品牌包 ID 失败: %v", err)
	}
	return pkgID
}

// TestFetchBrandTerms 验证 fetchBrandTerms 组装结果：
// 命中 brand 术语按语言聚合 src→target；非 brand/非 layer1 被过滤。
func TestFetchBrandTerms(t *testing.T) {
	st := newTestStore(t)
	db := st.DB()
	// 建租户级品牌包
	res, err := db.Exec(`INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at)
		VALUES (1,0,'brand_test','品牌测试包','tenant','source',0,0,datetime('now'),datetime('now'))`)
	if err != nil {
		t.Fatalf("插入品牌包失败: %v", err)
	}
	pkgID, err := res.LastInsertId()
	if err != nil || pkgID <= 0 {
		t.Fatalf("获取品牌包 ID 失败: %v", err)
	}
	insertBrandTerm(t, db, pkgID, "极石汽车", "ru", "ROX")
	insertBrandTerm(t, db, pkgID, "极石汽车", "ar", "ROX")
	insertBrandTerm(t, db, pkgID, "极石汽车", "en", "ROX")
	insertBrandTerm(t, db, pkgID, "极石", "ru", "ROX")
	insertBrandTerm(t, db, pkgID, "极石", "en", "ROX")
	insertBrandTerm(t, db, pkgID, "极石", "ar", "ROX")
	// 干扰项：layer=2（不应进入品牌映射）
	if _, err := db.Exec(`INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module) VALUES (1,?,2,'zh','极石汽车','ru','РОКС','manual')`, pkgID); err != nil {
		t.Fatalf("插入干扰术语失败: %v", err)
	}

	e := &Engine{St: st, Cfg: config.Default()}
	ctx := tenant.WithTenant(context.Background(), 1)

	bm := e.fetchBrandTerms(ctx, "极石汽车驰骋全球山海。")
	if len(bm) != 3 {
		t.Fatalf("期望命中 3 语言，实际 %d", len(bm))
	}
	for _, lc := range []string{"en", "ru", "ar"} {
		m, ok := bm[lc]
		if !ok {
			t.Fatalf("语言 %s 缺失", lc)
		}
		if m["极石汽车"] != "ROX" || m["极石"] != "ROX" {
			t.Fatalf("语言 %s 术语映射异常: %v", lc, m)
		}
	}
}

// TestNormalizeBrandTerms 对话路径归一化：源文命中品牌术语时，
// 剥离「ROX vehicles/Автомобили ROX/سيارات ROX」等后缀，统一为纯 ROX。
func TestNormalizeBrandTerms(t *testing.T) {
	st := newTestStore(t)
	db := st.DB()
	res, _ := db.Exec(`INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at)
		VALUES (1,0,'brand_test','品牌测试包','tenant','source',0,0,datetime('now'),datetime('now'))`)
	pkgID, _ := res.LastInsertId()
	insertBrandTerm(t, db, pkgID, "极石汽车", "en", "ROX")
	insertBrandTerm(t, db, pkgID, "极石汽车", "ru", "ROX")
	insertBrandTerm(t, db, pkgID, "极石汽车", "ar", "ROX")

	e := &Engine{St: st, Cfg: config.Default()}
	ctx := tenant.WithTenant(context.Background(), 1)

	tr := map[string]string{
		"en": "ROX vehicles are expanding fast",
		"ru": "Автомобили ROX мчатся по морям",
		"ar": "تجوب سيارات ROX جبال العالم",
		"de": "Marken seil 极石汽车 ist stark", // 无 brand 术语语言，应原封不动
	}
	hit := e.normalizeBrandTerms(ctx, "极石汽车驰骋全球山海。", tr, "zh")
	if hit != 3 {
		t.Fatalf("期望 3 语言被归一化，实际 %d", hit)
	}
	cases := map[string]string{
		"en": "ROX",
		"ru": "ROX",
		"ar": "ROX",
		"de": "Marken seil 极石汽车 ist stark", // 语义保证：de 无术语，译文不被触碰
	}
	for lc, must := range cases {
		if lc == "de" {
			if tr[lc] != must {
				t.Fatalf("de 应原封不动，实际 %q", tr[lc])
			}
			continue
		}
		if !containsStr(tr[lc], must) {
			t.Fatalf("语言 %s 归一化后应含 %s，实际 %q", lc, must, tr[lc])
		}
	}
}

// TestNormalizeFileBrandTerms 文件路径归一化（译后兜底）：
// 命中品牌语言的段落译文剥离后缀；未命中语言不动；返回修正语言数。
func TestNormalizeFileBrandTerms(t *testing.T) {
	st := newTestStore(t)
	db := st.DB()
	res, _ := db.Exec(`INSERT INTO kb_packages (tenant_id, parent_id, code, name, pack_type, role, org_id, sort_order, created_at, updated_at)
		VALUES (1,0,'brand_test','品牌测试包','tenant','source',0,0,datetime('now'),datetime('now'))`)
	pkgID, _ := res.LastInsertId()
	insertBrandTerm(t, db, pkgID, "极石汽车", "en", "ROX")
	insertBrandTerm(t, db, pkgID, "极石汽车", "ru", "ROX")

	e := &Engine{St: st, Cfg: config.Default()}
	ctx := tenant.WithTenant(context.Background(), 1)

	langTr := map[string]map[string]string{
		"en": {"极石汽车驰骋全球山海。": "ROX motor races across the world"},
		"ru": {"极石汽车驰骋全球山海。": "Автомобили ROX мчатся"},
		"ja": {"极石汽车驰骋全球山海。": "ロックスは世界を駆ける"}, // ja 无 brand 术语，不动
	}
	hit := e.normalizeFileBrandTerms(ctx, []string{"极石汽车驰骋全球山海。"}, langTr)
	if hit != 2 {
		t.Fatalf("期望 2 语言被修正，实际 %d", hit)
	}
	if langTr["en"]["极石汽车驰骋全球山海。"] != "ROX races across the world" {
		t.Fatalf("en 应为纯 ROX，实际 %q", langTr["en"]["极石汽车驰骋全球山海。"])
	}
	if langTr["ru"]["极石汽车驰骋全球山海。"] != "ROX мчатся" {
		t.Fatalf("ru 应剥离前缀后缀，实际 %q", langTr["ru"]["极石汽车驰骋全球山海。"])
	}
	if langTr["ja"]["极石汽车驰骋全球山海。"] != "ロックスは世界を駆ける" {
		t.Fatalf("ja 应保持原样，实际 %q", langTr["ja"]["极石汽车驰骋全球山海。"])
	}
	// 无品牌术语命中时数量为 0（de 场景）
	langTr2 := map[string]map[string]string{"de": {"极石汽车": "123"}}
	if h2 := e.normalizeFileBrandTerms(ctx, []string{"今天天气不错。"}, langTr2); h2 != 0 {
		t.Fatalf("无命中语应返回 0，实际 %d", h2)
	}
}

// containsStr 判断字符串是否包含子串（测试断言辅助）。
func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}