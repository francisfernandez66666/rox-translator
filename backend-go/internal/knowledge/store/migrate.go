// ============ migrate.go · 职责说明 ============
// 四期 W3-2：KnowledgeMigrate() 在**独立 DSN** 上建四张知识表（§4.3.2 / §4.3.3 第 4 条）。
//
//   - 四表逐列与主库现形态等值，且直接带 lang/priority/target_langs（原本是后补列）。
//   - CREATE TABLE 用 SQLite 方言（INTEGER PRIMARY KEY AUTOINCREMENT），由 db.ExecDDL
//     经 db.ToDialect 在 PG 下译成 BIGSERIAL PRIMARY KEY——**禁止**直写 SERIAL/BIGSERIAL。
//   - 保留 db.EnsureColumns 供「搬过来的旧知识库」幂等补 lang/priority/target_langs。
//   - 唯一迁移入口＝ db.ExecDDL / db.EnsureColumns；失败即返回 error（不静默降级）。
// ================================================
package store

import (
	"context"
	"database/sql"
	"fmt"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
	"translator/internal/observability"
)

// 四条 CREATE TABLE（列顺序与 contract.TableColumns 逐列一致；后补列直接写进建表）。
const (
	createProducts = `CREATE TABLE IF NOT EXISTS products (
		id TEXT PRIMARY KEY,
		tenant_id BIGINT NOT NULL,
		user_id INTEGER NOT NULL DEFAULT 0,
		code TEXT NOT NULL DEFAULT '',
		name TEXT NOT NULL,
		tagline TEXT NOT NULL DEFAULT '',
		industry_code TEXT NOT NULL DEFAULT '',
		audience TEXT NOT NULL DEFAULT '',
		use_case TEXT NOT NULL DEFAULT '',
		style_notes TEXT NOT NULL DEFAULT '',
		banned_notes TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'draft',
		slug TEXT NOT NULL,
		tenant_code TEXT NOT NULL,
		page_code TEXT NOT NULL,
		seo_indexable INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		target_langs TEXT NOT NULL DEFAULT ''
	)`
	createProductFacts = `CREATE TABLE IF NOT EXISTS product_facts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id BIGINT NOT NULL,
		product_id TEXT NOT NULL,
		kind TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		status TEXT NOT NULL DEFAULT 'unverified',
		visibility TEXT NOT NULL DEFAULT 'gated',
		source TEXT NOT NULL DEFAULT 'manual',
		locked INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL,
		lang TEXT NOT NULL DEFAULT 'zh',
		priority INTEGER NOT NULL DEFAULT 0
	)`
	createProductAssets = `CREATE TABLE IF NOT EXISTS product_assets (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id BIGINT NOT NULL,
		product_id TEXT NOT NULL,
		asset_type TEXT NOT NULL,
		lang TEXT NOT NULL,
		body TEXT NOT NULL,
		artifact_ref TEXT NOT NULL DEFAULT '',
		review_status TEXT NOT NULL DEFAULT 'unreviewed',
		validity TEXT NOT NULL DEFAULT 'valid',
		visibility TEXT NOT NULL DEFAULT 'gated',
		fact_refs TEXT NOT NULL DEFAULT '[]',
		source_kind TEXT NOT NULL DEFAULT 'model',
		version INTEGER NOT NULL DEFAULT 1,
		created_at TEXT NOT NULL,
		updated_at TEXT NOT NULL
	)`
	createProductFactI18n = `CREATE TABLE IF NOT EXISTS product_fact_i18n (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id BIGINT NOT NULL,
		fact_id BIGINT NOT NULL,
		lang TEXT NOT NULL,
		value TEXT NOT NULL,
		source_hash TEXT NOT NULL DEFAULT '',
		translated_at TEXT NOT NULL DEFAULT '',
		engine TEXT NOT NULL DEFAULT ''
	)`
)

// 七条产品/事实/物料索引 + 两条译文索引（与主库 store.go:754-760、product_fact_i18n.go:82-83 逐条等值）。
var knowledgeIndexes = []string{
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_product_tenant_slug ON products(tenant_id, slug)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_product_codes ON products(tenant_code, page_code)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_product_page ON products(page_code)`,
	`CREATE INDEX IF NOT EXISTS idx_product_tenant ON products(tenant_id, status, id)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_fact ON product_facts(product_id, kind, key)`,
	`CREATE INDEX IF NOT EXISTS idx_fact_tenant_updated ON product_facts(tenant_id, updated_at)`,
	`CREATE INDEX IF NOT EXISTS idx_asset_product_lang ON product_assets(product_id, lang, validity)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS ux_fact_i18n ON product_fact_i18n(tenant_id, fact_id, lang)`,
	`CREATE INDEX IF NOT EXISTS idx_fact_i18n_tenant_lang ON product_fact_i18n(tenant_id, lang)`,
}

// KnowledgeMigrate 在独立知识库上建四表 + 索引，并对旧库幂等补后补列。
// 参数：conn=知识库连接；d=知识库方言；返回迁移错误。
func KnowledgeMigrate(conn *sql.DB, d db.Dialect) error {
	if conn == nil {
		return fmt.Errorf("knowledge: KnowledgeMigrate 收到 nil 连接")
	}
	stmts := make([]string, 0, 4+len(knowledgeIndexes))
	stmts = append(stmts, createProducts, createProductFacts, createProductAssets, createProductFactI18n)
	stmts = append(stmts, knowledgeIndexes...)
	for _, s := range stmts {
		if err := db.ExecDDL(conn, d, s); err != nil {
			return fmt.Errorf("knowledge: 建表/索引失败: %w\nSQL: %s", err, s)
		}
	}
	// 旧库幂等补列：新建表已带这些列 ⇒ EnsureColumns 为 no-op；搬过来的旧知识库可能缺。
	if err := db.EnsureColumns(conn, d, contract.TableProductFacts, map[string]string{
		"lang":     "TEXT NOT NULL DEFAULT 'zh'",
		"priority": "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		return fmt.Errorf("knowledge: product_facts 补 lang/priority 列失败: %w", err)
	}
	if err := db.EnsureColumns(conn, d, contract.TableProducts, map[string]string{
		"target_langs": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("knowledge: products 补 target_langs 列失败: %w", err)
	}
	observability.Info(context.Background(), "知识库四表迁移完成",
		"tables", len(contract.KnowledgeTableNames), "statements", len(stmts))
	return nil
}
