// ============ sales_attribution_migrate.go · 职责说明 ============
// 二期六包平移第三刀（attribution）的独立迁移入口：幂等建 reply_attributions 与 pack_stats
// 两张表及其索引。★ 刻意不挂进 store.go/migrate()/New()——由主代理串行挂接，
// 本文件保持可被单独调用、单独验证。
// 列形：时间列 TEXT、租户列 BIGINT NOT NULL、布尔标记用 INTEGER 0/1、eval_score 默认 -1；
// 索引名全部显式，唯一键 (tenant_id, message_id) 与 (tenant_id, package_id, version, draft_id)
// 都落在租户维度上，杜绝跨租户污染。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// SalesAttributionMigrate 幂等建归因域两张表与索引；逐条执行，失败即中断并带 SQL 返回。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) SalesAttributionMigrate() error {
	d := db.CurrentDialect()
	// stmts 是本文件唯一的 DDL 真源：建表与索引逐条走 db.ExecDDL，PG 下由 ToDialect 自动翻译。
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS reply_attributions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			message_id BIGINT NOT NULL,
			conversation_id BIGINT NOT NULL DEFAULT 0,
			customer_id BIGINT NOT NULL DEFAULT 0,
			package_id TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL DEFAULT '',
			draft_id TEXT NOT NULL DEFAULT '',
			anchor_type INTEGER NOT NULL DEFAULT 0,
			route_result TEXT NOT NULL DEFAULT '',
			provider TEXT NOT NULL DEFAULT '',
			model TEXT NOT NULL DEFAULT '',
			intent_before REAL NOT NULL DEFAULT 0,
			intent_after REAL NOT NULL DEFAULT 0,
			hooked INTEGER NOT NULL DEFAULT 0,
			lead_captured INTEGER NOT NULL DEFAULT 0,
			pending_human INTEGER NOT NULL DEFAULT 0,
			eval_score INTEGER NOT NULL DEFAULT -1,
			eval_checked_at TEXT NOT NULL DEFAULT '',
			outcome_stage TEXT NOT NULL DEFAULT '',
			outcome_checked_at TEXT NOT NULL DEFAULT '',
			arrived_at TEXT NOT NULL DEFAULT '',
			dealt_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_reply_attr_tenant_message ON reply_attributions(tenant_id, message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reply_attr_tenant_conv ON reply_attributions(tenant_id, conversation_id, message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_reply_attr_tenant_cust ON reply_attributions(tenant_id, customer_id, id)`,
		`CREATE TABLE IF NOT EXISTS pack_stats (
			tenant_id BIGINT NOT NULL,
			package_id TEXT NOT NULL DEFAULT '',
			version TEXT NOT NULL DEFAULT '',
			draft_id TEXT NOT NULL DEFAULT '',
			sample_count BIGINT NOT NULL DEFAULT 0,
			hook_rate REAL NOT NULL DEFAULT 0,
			lead_rate REAL NOT NULL DEFAULT 0,
			pending_human_rate REAL NOT NULL DEFAULT 0,
			arrive_rate REAL NOT NULL DEFAULT 0,
			deal_rate REAL NOT NULL DEFAULT 0,
			avg_intent_delta REAL NOT NULL DEFAULT 0,
			avg_eval_score REAL NOT NULL DEFAULT 0,
			computed_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_pack_stats_tenant_dims ON pack_stats(tenant_id, package_id, version, draft_id)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("归因域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	observability.Info(context.Background(), "归因域迁移完成", "statements", len(stmts))
	return nil
}
