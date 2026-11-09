// ============ sales_config_migrate.go · 职责说明 ============
// 二期「前端可配」配置表的独立迁移入口：幂等建 sales_config（一租户一行）。
// 挂接由主代理串行做；失败即拒启，不许把租户配置退化成全局散键。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// SalesConfigMigrate 幂等建租户级销售配置表；失败即中断并带 SQL 返回。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) SalesConfigMigrate() error {
	d := db.CurrentDialect()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sales_config (
			tenant_id BIGINT PRIMARY KEY,
			config_json TEXT NOT NULL DEFAULT '{}',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("销售配置域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	observability.Info(context.Background(), "销售配置域迁移完成", "statements", len(stmts))
	return nil
}
