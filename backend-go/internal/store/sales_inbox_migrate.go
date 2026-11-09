// ============ sales_inbox_migrate.go · 职责说明 ============
// 二期 mq 核心层的独立迁移入口：幂等建 inbox_events 表与租户维度唯一索引。
// ★ 刻意不挂进 store.go/migrate()/New()——由主代理串行挂接，本文件可单独调用、单独验证。
// 唯一键落在 (tenant_id, event_id)：跨租户同 event_id 互不干扰，
// 这是「消息重复投递只处理一次」的唯一事实源，不许在应用侧再造第二把尺子。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// SalesInboxMigrate 幂等建收件箱表与索引；失败即中断并带 SQL 返回。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) SalesInboxMigrate() error {
	d := db.CurrentDialect()
	// stmts 是本文件唯一的 DDL 真源：建表与索引逐条走 db.ExecDDL，PG 下由 ToDialect 自动翻译。
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS inbox_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			event_id TEXT NOT NULL,
			one_id TEXT NOT NULL DEFAULT '',
			topic TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT '',
			processed_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_inbox_tenant_event ON inbox_events(tenant_id, event_id)`,
		`CREATE INDEX IF NOT EXISTS idx_inbox_tenant_status ON inbox_events(tenant_id, status, id)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("收件箱域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	observability.Info(context.Background(), "收件箱域迁移完成", "statements", len(stmts))
	return nil
}
