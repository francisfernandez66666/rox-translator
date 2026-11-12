// ============ sales_crm_migrate.go · 职责说明 ============
// 三期 W6：CRM 域五表的独立迁移入口（幂等、失败即拒启，由主 agent 挂 store.go 域链）。
//
// 五张表（施工图 §3 W6 / A20–A25）：
//   - handoff_sessions：assist 会话 → 主库 conversation_id 的**会话映射**，
//     并承载 pending_human / status（open|resolved）。★ v1.4 裁定：pending_human 的
//     W6 回填以「会话映射」为锚——assist 的 session_id 是 TEXT，主库归因行的
//     conversation_id 是 int64，映射行 id 即 conversation_id 维度，按会话精确回填，
//     不再按「最近一条」找（那会标错行）。
//   - sales_customers / sales_leads / sales_deals：客户-线索-商机三件套；
//     线索五阶段 new/engaged/quoted/won/lost（漏斗扩段的数据源）。
//   - sales_flow_instances：成交流程实例（推进状态 + 心跳；SweepOnce 的巡检对象）。
//
// 双方言：db.ExecDDL（PG 由 ToDialect 自动翻译）；金额一律「分」（TotalCents/DealCents）。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// SalesCRMMigrate 幂等建 CRM 域五表与索引；失败即中断并带 SQL 返回。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) SalesCRMMigrate() error {
	d := db.CurrentDialect()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS handoff_sessions (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			session_id TEXT NOT NULL,
			message_id INTEGER NOT NULL DEFAULT 0,
			pending_human INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT 'open',
			created_at TEXT NOT NULL DEFAULT '',
			resolved_at TEXT NOT NULL DEFAULT ''
		)`,
		// 会话映射唯一锚：同一 assist 会话在主库只有一行（幂等建行的基础）。
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_handoff_tenant_session ON handoff_sessions(tenant_id, session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_handoff_pending ON handoff_sessions(tenant_id, status, pending_human, id)`,
		`CREATE TABLE IF NOT EXISTS sales_customers (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			email TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_crm_customer_email ON sales_customers(tenant_id, email)`,
		`CREATE TABLE IF NOT EXISTS sales_leads (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			customer_id INTEGER NOT NULL DEFAULT 0,
			email TEXT NOT NULL DEFAULT '',
			stage TEXT NOT NULL DEFAULT 'new',
			score INTEGER NOT NULL DEFAULT 0,
			pending_human INTEGER NOT NULL DEFAULT 0,
			owner_user_id INTEGER NOT NULL DEFAULT 0,
			updated_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_leads_tenant_stage ON sales_leads(tenant_id, stage, id)`,
		`CREATE TABLE IF NOT EXISTS sales_deals (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			lead_id INTEGER NOT NULL DEFAULT 0,
			total_cents INTEGER NOT NULL DEFAULT 0,
			currency TEXT NOT NULL DEFAULT 'CNY',
			status TEXT NOT NULL DEFAULT 'open',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_deals_tenant_lead ON sales_deals(tenant_id, lead_id)`,
		`CREATE TABLE IF NOT EXISTS sales_flow_instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			one_id TEXT NOT NULL DEFAULT '',
			flow_key TEXT NOT NULL DEFAULT '',
			current_node TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'running',
			version INTEGER NOT NULL DEFAULT 0,
			heartbeat_ts TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_flowinst_tenant_one ON sales_flow_instances(tenant_id, one_id, status)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("CRM 域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	observability.Info(context.Background(), "CRM 域迁移完成", "statements", len(stmts))
	return nil
}
