// ============ product_tables_helper_test.go · 测试专属助手 ============
// ★ 仅测试用：在测试库（主库回滚路径）重建产品四表。
//
// KN11 终态主库零产品表（生产 New() 启动链已摘除四表 DDL，见 store.go 注释），
// 但大量产品行为单测测的就是「主库路径／回滚态」，需要表在位。故本助手在测试 Store
// 上独立建表，与生产线解耦——绝不进 New() 启动链，也不会让 KN11 的「主库零产品表」
// 判据（no_knowledge_ddl_gate_test.go）受影响（那条测的是生产 migrate，不是测试库）。
//
// 列结构对齐 store 包 scanProduct/scanFact/scanAsset 的列序与 productCols/factCols/
// assetCols；lang/priority 两列故意不建，交由 ProductFactI18nMigrate 的 EnsureColumns 补，
// 顺带覆盖「存量库补列」路径。
// ========================================
package store

import (
	"testing"

	"translator/internal/db"
)

// migrateProductTablesTest 在测试库重建产品四表（products / product_facts /
// product_assets / product_fact_i18n）。参数：t=测试句柄，s=测试 Store；失败直接 t.Fatalf。
func migrateProductTablesTest(t *testing.T, s *Store) {
	t.Helper()
	// ★ 调用方已用文件库（多连接共享同一文件，无 :memory: 分裂问题），故无需
	//   SetMaxOpenConns(1)；且该调用会令 New() 迁移里的嵌套查询死锁，故不设。
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
			updated_at TEXT NOT NULL DEFAULT ''
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
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			t.Fatalf("测试库建产品表失败: %v\nSQL: %s", err, stmt)
		}
	}
}
