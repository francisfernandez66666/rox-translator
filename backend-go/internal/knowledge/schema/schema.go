// ============ schema.go · 职责说明 ============
// 四期 W3 / KN11 收口（2026-11-15）：产品四表 DDL 的**唯一事实源**。
//
// ★ 为什么要有这个包：四期把四条 CREATE TABLE 从主库 store 摘走、只留在
// internal/knowledge/store/migrate.go。而 KNOWLEDGE_SERVICE_READ 默认 off，
// 32 处 kcCutover() 在 off 档回落主库 SQL ⇒ 新库/新部署的主库里没有这些表，
// 产品域第一条查询就是 no such table: products，而启动链不报错。
// 「DDL 归属」必须跟着切读开关走，但**文本只许有一份**（两份迟早打架）。
//
// 三处薄委托本包：
//   - internal/knowledge/store/migrate.go：知识服务独立库建表（切读态的正主）；
//   - internal/store/product_fallback.go：off 档在主库建表（影子期/回滚档的正主）；
//   - cmd/knowledge-migrate：回滚/收尾时的显式 drop（不进任何启动链）。
//
// ★ 方言：四条 CREATE 一律写 SQLite 方言（INTEGER PRIMARY KEY AUTOINCREMENT），
// 由 db.ExecDDL 经 db.ToDialect 在 PG 下译成 BIGSERIAL PRIMARY KEY。
// 禁止直写 SERIAL/BIGSERIAL —— 唯一迁移入口＝ db.ExecDDL / db.EnsureColumns（AGENTS §一·1 第 4 条）。
// ================================================
package schema

import (
	"context"
	"database/sql"
	"fmt"

	"translator/internal/db"
	"translator/internal/knowledge/contract"
	"translator/internal/observability"
)

// TableDDL 四条 CREATE TABLE（列顺序与 contract.TableColumns 逐列一致；后补列直接写进建表）。
// ★ 文本从 internal/knowledge/store/migrate.go 逐字搬来，改动必须两边同源（本包已是唯一真源）。
var TableDDL = []string{
	`CREATE TABLE IF NOT EXISTS products (
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
	)`,
	`CREATE TABLE IF NOT EXISTS product_facts (
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
	)`,
	`CREATE TABLE IF NOT EXISTS product_assets (
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
	)`,
	`CREATE TABLE IF NOT EXISTS product_fact_i18n (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id BIGINT NOT NULL,
		fact_id BIGINT NOT NULL,
		lang TEXT NOT NULL,
		value TEXT NOT NULL,
		source_hash TEXT NOT NULL DEFAULT '',
		translated_at TEXT NOT NULL DEFAULT '',
		engine TEXT NOT NULL DEFAULT ''
	)`,
}

// IndexDDL 七条产品/事实/物料索引 + 两条译文索引
// （与主库 store.go、product_fact_i18n.go 的既有索引逐条等值）。
var IndexDDL = []string{
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

// TableNames 四张产品表的清单（闸门 / 健康面 / 回滚共用的单一真值，方案 §1.2）。
// ★ 不复制 contract.KnowledgeTableNames：contract 那份是「知识域表名」的契约口径，
//
//	本包这份是「DDL 归属」口径，两者刻意各自具名，但内容必须逐项等值（由 schema_test 兜）。
var TableNames = []string{
	contract.TableProducts,
	contract.TableProductFacts,
	contract.TableProductAssets,
	contract.TableProductFactI18n,
}

// DropOrder 回滚/收尾时的 DROP 顺序：子表在前、父表在后。
// SQLite 默认不开外键、顺序错了不会炸，但 PG 上会炸 —— 这条顺序是为了 PG 的正确性。
var DropOrder = []string{
	contract.TableProductFactI18n,
	contract.TableProductAssets,
	contract.TableProductFacts,
	contract.TableProducts,
}

// EnsureProductTables 在给定连接上建四表 + 九索引（幂等）。
// 唯一实现：知识服务独立库与主库 off 档回落**共用这一份**。
// 参数：conn=目标库连接；d=该库方言；返回迁移错误（不静默降级）。
func EnsureProductTables(conn *sql.DB, d db.Dialect) error {
	if conn == nil {
		return fmt.Errorf("knowledge/schema: EnsureProductTables 收到 nil 连接")
	}
	stmts := make([]string, 0, len(TableDDL)+len(IndexDDL))
	stmts = append(stmts, TableDDL...)
	stmts = append(stmts, IndexDDL...)
	for _, s := range stmts {
		if err := db.ExecDDL(conn, d, s); err != nil {
			return fmt.Errorf("knowledge/schema: 建表/索引失败: %w\nSQL: %s", err, s)
		}
	}
	return nil
}

// EnsureLegacyColumns 对「搬过来的旧知识库」幂等补后补列。
// 新建表已带这些列 ⇒ 本函数为 no-op；旧库可能缺 lang/priority/target_langs。
// 参数：conn=目标库连接；d=该库方言；返回错误。
func EnsureLegacyColumns(conn *sql.DB, d db.Dialect) error {
	if conn == nil {
		return fmt.Errorf("knowledge/schema: EnsureLegacyColumns 收到 nil 连接")
	}
	if err := db.EnsureColumns(conn, d, contract.TableProductFacts, map[string]string{
		"lang":     "TEXT NOT NULL DEFAULT 'zh'",
		"priority": "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		return fmt.Errorf("knowledge/schema: product_facts 补 lang/priority 列失败: %w", err)
	}
	if err := db.EnsureColumns(conn, d, contract.TableProducts, map[string]string{
		"target_langs": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("knowledge/schema: products 补 target_langs 列失败: %w", err)
	}
	return nil
}

// Migrate 建表 + 补旧列 + 出读数行（知识服务启动链用这一个入口）。
func Migrate(conn *sql.DB, d db.Dialect) error {
	if err := EnsureProductTables(conn, d); err != nil {
		return err
	}
	if err := EnsureLegacyColumns(conn, d); err != nil {
		return err
	}
	observability.Info(context.Background(), "知识库四表迁移完成",
		"tables", len(contract.KnowledgeTableNames),
		"statements", len(TableDDL)+len(IndexDDL))
	return nil
}

// validateDropOrder 语义校验 drop 顺序：四表齐全、父表 products 必须在三张子表之后、
// product_fact_i18n 必须在 product_facts 之后。
// ★ 刻意不写「与 DropOrder 逐项等值」：那样是把真值拿自己比自己（KN1 那一族空转锁）。
// 这里问的是「有没有哪张子表被排到父表后面」，所以将来有人改错顺序会当场报错，
// 而合法的并列子表之间换序仍然放行。参数：order=待校验顺序；返回：错误。
func validateDropOrder(order []string) error {
	if len(order) != len(TableNames) {
		return fmt.Errorf("drop 顺序表数 %d，期望 %d（少一张会在主库留孤儿产品表）",
			len(order), len(TableNames))
	}
	pos := map[string]int{}
	for i, t := range order {
		if _, dup := pos[t]; dup {
			return fmt.Errorf("drop 顺序里 %s 出现两次", t)
		}
		pos[t] = i
	}
	for _, t := range TableNames {
		if _, ok := pos[t]; !ok {
			return fmt.Errorf("drop 顺序缺表 %s", t)
		}
	}
	parent := pos[contract.TableProducts]
	for _, child := range []string{
		contract.TableProductFacts, contract.TableProductAssets, contract.TableProductFactI18n,
	} {
		if pos[child] >= parent {
			return fmt.Errorf("drop 顺序违例：子表 %s 排在父表 %s 之后（PG 外键会拒绝删除）",
				child, contract.TableProducts)
		}
	}
	if pos[contract.TableProductFactI18n] >= pos[contract.TableProductFacts] {
		return fmt.Errorf("drop 顺序违例：%s 排在 %s 之后",
			contract.TableProductFactI18n, contract.TableProductFacts)
	}
	return nil
}

// DropProductTablesForRollback 显式删四表（回滚 / 切读收尾）。
//
// ★ 不进任何启动链：只由 cmd/knowledge-migrate 的 `--drop-main-product-tables --yes` 调用。
// 没有 --yes 的调用方必须在**调用本函数之前**拒绝执行（本包不做确认，确认是 CLI 的职责）。
// 参数：conn=目标库连接；d=该库方言；返回错误。
func DropProductTablesForRollback(conn *sql.DB, d db.Dialect) error {
	if conn == nil {
		return fmt.Errorf("knowledge/schema: DropProductTablesForRollback 收到 nil 连接")
	}
	if err := validateDropOrder(DropOrder); err != nil {
		return fmt.Errorf("knowledge/schema: %w", err)
	}
	for _, t := range DropOrder {
		if err := db.ExecDDL(conn, d, "DROP TABLE IF EXISTS "+t); err != nil {
			return fmt.Errorf("knowledge/schema: drop %s 失败: %w", t, err)
		}
	}
	observability.Info(context.Background(), "产品四表已删除（回滚/收尾）", "tables", len(DropOrder))
	return nil
}
