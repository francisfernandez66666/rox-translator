// ============ product_tables_helper_api_test.go · 测试专属助手 ============
// ★ 仅测试用：在测试 Store（主库回滚路径 / kc=nil）重建产品四表。
//
// KN11 终态主库零产品表（生产 New() 启动链已摘除四表 DDL，见 store.go 注释），
// 但本包多个 API gate 测的是「API 层行为」（鉴权 / 状态 / 跨租户 / SEO / 留资归属），
// 它们经 store 方法走主库回滚分支，需要表在位。故本助手在测试 Store 上独立建表，
// 与生产线解耦——不影响 KN11「主库零产品表」判据（no_knowledge_ddl_gate_test.go 测的是
// 生产 migrate，不是测试库）。
//
// 列结构严格对齐 store 包 scanProduct/scanFact/scanAsset 的列序与 productCols/factCols/
// assetCols（factCols 为 12 列，不含 lang/priority：主库读口不读这两列，知识库才读）。
// 与 store/product_tables_helper_test.go 等价，但 api 包无法复用未导出助手，故在此重建。
// ========================================================
package api

import (
	"testing"

	"translator/internal/db"
	"translator/internal/store"
)

// migrateProductTablesForTest 在测试 Store 重建产品四表。参数：t=测试句柄，s=测试 Store。
func migrateProductTablesForTest(t *testing.T, s *store.Store) {
	t.Helper()
	d := db.CurrentDialect()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS products (
			id TEXT PRIMARY KEY,
			tenant_id BIGINT NOT NULL,
			user_id BIGINT NOT NULL DEFAULT 0,
			code TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			tagline TEXT NOT NULL DEFAULT '',
			industry_code TEXT NOT NULL DEFAULT '',
			audience TEXT NOT NULL DEFAULT '',
			use_case TEXT NOT NULL DEFAULT '',
			style_notes TEXT NOT NULL DEFAULT '',
			banned_notes TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'draft',
			slug TEXT NOT NULL DEFAULT '',
			tenant_code TEXT NOT NULL DEFAULT '',
			page_code TEXT NOT NULL DEFAULT '',
			seo_indexable INTEGER NOT NULL DEFAULT 1,
			target_langs TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			UNIQUE(tenant_id, slug)
		)`,
		`CREATE TABLE IF NOT EXISTS product_facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			product_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			key TEXT NOT NULL,
			value TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'unverified',
			visibility TEXT NOT NULL DEFAULT 'gated',
			source TEXT NOT NULL DEFAULT 'manual',
			locked INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			lang TEXT NOT NULL DEFAULT 'zh',
			priority INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS product_assets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			product_id TEXT NOT NULL,
			asset_type TEXT NOT NULL,
			lang TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			artifact_ref TEXT NOT NULL DEFAULT '',
			review_status TEXT NOT NULL DEFAULT 'unreviewed',
			validity TEXT NOT NULL DEFAULT 'valid',
			visibility TEXT NOT NULL DEFAULT 'gated',
			fact_refs TEXT NOT NULL DEFAULT '[]',
			source_kind TEXT NOT NULL DEFAULT '',
			version INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS product_fact_i18n (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			fact_id BIGINT NOT NULL,
			lang TEXT NOT NULL,
			value TEXT NOT NULL DEFAULT '',
			source_hash TEXT NOT NULL DEFAULT '',
			translated_at TEXT NOT NULL DEFAULT '',
			engine TEXT NOT NULL DEFAULT ''
		)`,
		// ★ 与主库 ProductFactI18nMigrate 同形态：译文唯一键落在租户维度，且 i18n 写口
		//   ON CONFLICT(tenant_id, fact_id, lang) 依赖该唯一约束，缺它即报
		//   "ON CONFLICT clause does not match any PRIMARY KEY or UNIQUE constraint"。
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_fact_i18n ON product_fact_i18n(tenant_id, fact_id, lang)`,
		`CREATE INDEX IF NOT EXISTS idx_fact_i18n_tenant_lang ON product_fact_i18n(tenant_id, lang)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.DB(), d, stmt); err != nil {
			t.Fatalf("测试库建产品表失败: %v\nSQL: %s", err, stmt)
		}
	}
}
