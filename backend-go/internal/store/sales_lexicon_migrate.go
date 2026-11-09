// ============ sales_lexicon_migrate.go · 职责说明 ============
// 二期 §5 词表配置层的独立迁移入口：幂等建 sales_lexicon 表与四档唯一键。
// ★ 刻意不挂进 store.go/migrate()/New()——由主代理串行挂接，本文件可单独调用、单独验证。
// 唯一键 (tenant_id, scene, lang, kind) 是「一档一份配置」的唯一事实源，
// 平台默认档用 tenant_id=0，与租户档共用同一张表，不另起第二张词表。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// SalesLexiconMigrate 幂等建词表与索引；失败即中断并带 SQL 返回。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) SalesLexiconMigrate() error {
	d := db.CurrentDialect()
	// stmts 是本文件唯一的 DDL 真源：建表与索引逐条走 db.ExecDDL，PG 下由 ToDialect 自动翻译。
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sales_lexicon (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			scene TEXT NOT NULL DEFAULT '',
			lang TEXT NOT NULL DEFAULT '',
			kind TEXT NOT NULL DEFAULT '',
			words TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_sales_lexicon_dims ON sales_lexicon(tenant_id, scene, lang, kind)`,
		`CREATE INDEX IF NOT EXISTS idx_sales_lexicon_tenant_scene ON sales_lexicon(tenant_id, scene, lang)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("词表域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	observability.Info(context.Background(), "词表域迁移完成", "statements", len(stmts))
	return nil
}
