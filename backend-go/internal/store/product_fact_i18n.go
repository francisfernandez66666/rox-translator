// ============ product_fact_i18n.go · 职责说明 ============
// 三期 W4：主库产品事实的多语种译文表 + product_facts 补列（§0.4 第 3/4/5 条）。
//
//   - ProductFactI18nMigrate：幂等补列 + 建表，唯一迁移入口＝db.EnsureColumns / db.ExecDDL，
//     双方言兼容（SQLite/PG）；刻意不挂进 store.go 的 New()——由主 agent 统一串行挂域链
//     （同 sales_attribution_migrate.go「单独调用、单独验证」的形态）。
//   - product_facts 补 lang（原文语种，存量回填 'zh'）与 priority（准入排序／截断依据，缺省 0）。
//   - product_fact_i18n 一行＝(tenant_id, fact_id, lang) 一条已落库译文，带 source_hash 绑定原文；
//     原文变更 ⇒ hash 变 ⇒ 译文失效，读口按 hash 失配判「无判据」（§0.4 第 5 条 fail-closed）。
//   - factSourceHash 是原文指纹的单一真值：写时预生成（product_fact_i18n_api.go）与
//     读口准入（sales_facts_read.go）共用这一把尺，绝不各写一份。
//
// ★ 热路径零 LLM：本文件只有 DDL/CRUD；译文预生成走写时口，绝不进对话热路径。
// 所有 SQL 一律走 db.Exec/Query/QueryRow ＋ db.CurrentDialect()（db/guard_test.go 裸 SQL 闸）。
// ================================================
package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
)

// FactI18nLangs 12 语种内部码单一真值（与前端 src/i18n/index.ts、seoNormalizeLang 同集合）。
// ★ 预生成、读口、回避句按同一张表对齐；A13c 的译文语种集合同源，不许另写一份 []string。
var FactI18nLangs = []string{
	"zh", "zh_hant", "en", "ru", "fr", "ar", "es", "pt", "de", "ja", "ko", "th",
}

// ProductFactI18nRow 一行已落库译文。
type ProductFactI18nRow struct {
	TenantID     int64
	FactID       int64
	Lang         string
	Value        string
	SourceHash   string
	TranslatedAt string
	Engine       string
}

// factI18nEngineManual 运营手工档标记：这类行永不被后台预生成覆盖（A13c M0240）。
const factI18nEngineManual = "manual"

// factSourceHash 原文指纹（sha256 hex）。★ 写时预生成与读口准入共用这一把尺子：
// 原文一改 hash 就变，旧译文行自动失效，绝不靠「记得删缓存」。
// 参数：zhText=中文原文；返回 64 位 hex 指纹。
func factSourceHash(zhText string) string {
	sum := sha256.Sum256([]byte(zhText))
	return hex.EncodeToString(sum[:])
}

// ProductFactI18nMigrate 幂等补列 + 建 product_fact_i18n 表及其唯一键。
// 调用位置：由主 agent 挂进 store.go New() 的域补丁链（migrate() 之后、读路径之前）。
// 参数：无；返回迁移错误（失败直接中断启动，不静默降级）。
func (s *Store) ProductFactI18nMigrate() error {
	d := db.CurrentDialect()
	// ① 存量 product_facts 补 lang（原文语种）与 priority（准入排序／超限截断依据）。
	//    ★ 只补不删、幂等；EnsureColumns 对不存在的表会报错，故必须排在 migrate() 之后。
	if err := db.EnsureColumns(s.db, d, "product_facts", map[string]string{
		"lang":     "TEXT NOT NULL DEFAULT 'zh'",
		"priority": "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		return fmt.Errorf("product_facts 补 lang/priority 列失败: %w", err)
	}
	// ② 新表：唯一键落在租户维度 (tenant_id, fact_id, lang)，杜绝跨租户串译文。
	stmts := []string{
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
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_fact_i18n ON product_fact_i18n(tenant_id, fact_id, lang)`,
		`CREATE INDEX IF NOT EXISTS idx_fact_i18n_tenant_lang ON product_fact_i18n(tenant_id, lang)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("product_fact_i18n 建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	observability.Info(context.Background(), "产品事实译文域迁移完成", "statements", len(stmts))
	return nil
}

// UpsertFactI18n 幂等写一行译文（唯一键 tenant_id+fact_id+lang）。
// ★ 写口只认「当前原文 hash」：调用方（PreGenerateFactI18n）负责判幂等／护运营手工档，
//
//	本函数不做二次判据，避免两处各写一把尺。
//
// 参数：row=译文行；返回错误。
func (s *Store) UpsertFactI18n(row ProductFactI18nRow) error {
	if row.Engine == "" {
		row.Engine = "single_lang"
	}
	if row.TranslatedAt == "" {
		row.TranslatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO product_fact_i18n(tenant_id, fact_id, lang, value, source_hash, translated_at, engine)
		 VALUES (?,?,?,?,?,?,?)
		 ON CONFLICT(tenant_id, fact_id, lang) DO UPDATE SET
		   value=excluded.value, source_hash=excluded.source_hash,
		   translated_at=excluded.translated_at, engine=excluded.engine`,
		row.TenantID, row.FactID, row.Lang, row.Value, row.SourceHash, row.TranslatedAt, row.Engine)
	if err != nil {
		return fmt.Errorf("产品事实译文落库失败: %w", err)
	}
	return nil
}

// factI18nRow 按 (tenant_id, fact_id, lang) 取整行译文；未命中返回 ok=false。
// 参数：tid=租户；factID=事实主键；lang=语种码；返回译文行、是否命中。
func (s *Store) factI18nRow(tid, factID int64, lang string) (ProductFactI18nRow, bool) {
	var r ProductFactI18nRow
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT tenant_id, fact_id, lang, value, source_hash, translated_at, engine
		 FROM product_fact_i18n WHERE tenant_id=? AND fact_id=? AND lang=?`,
		tid, factID, lang).Scan(&r.TenantID, &r.FactID, &r.Lang, &r.Value,
		&r.SourceHash, &r.TranslatedAt, &r.Engine)
	if err != nil {
		return ProductFactI18nRow{}, false
	}
	return r, true
}
