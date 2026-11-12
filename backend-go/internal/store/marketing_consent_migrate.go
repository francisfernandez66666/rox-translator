// ============ marketing_consent_migrate.go · 职责说明 ============
// 三期 W5：外联合规四表的独立迁移入口（幂等、失败即拒启，由主 agent 挂 store.go 域链）。
//
// 四张表（施工图 §3 W5 / A4 / A2）：
//   - marketing_consents：opt-in/opt-out 与留痕（唯一键 tenant_id+customer_email，后写覆盖前态）；
//   - suppression_list：抑制名单（email/domain 两类匹配值，唯一键 tenant_id+match_value）；
//   - outbound_sequences：邮件序列节点（node_key 唯一，delay_hours 相对上一节点的时延）；
//   - outbound_sends：发送占位与结果。★ 唯一键 ux_outbound_once(tenant_id, customer_email, node_key)
//     是「多实例重复发」的结构性防线（A4/M0246）：先 INSERT 占位成功者才是发送赢家，
//     并发败方经 db.IsUniqueViolation 双方言判定折成 false,nil（幂等语义不是错误）。
//
// 双方言：db.ExecDDL（PG 由 ToDialect 自动翻译）；只减不增纪律下新域新文件。
// ================================================
package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// MarketingConsentMigrate 幂等建外联合规四表与索引；失败即中断并带 SQL 返回。
// 参数：无（方法接收者持有连接）；返回：迁移错误。
func (s *Store) MarketingConsentMigrate() error {
	d := db.CurrentDialect()
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS marketing_consents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			customer_email TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'opted_in',
			source TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_consent_tenant_email ON marketing_consents(tenant_id, customer_email)`,
		`CREATE TABLE IF NOT EXISTS suppression_list (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			match_value TEXT NOT NULL,
			kind TEXT NOT NULL DEFAULT 'email',
			reason TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_suppression_tenant_value ON suppression_list(tenant_id, match_value)`,
		`CREATE TABLE IF NOT EXISTS outbound_sequences (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			node_key TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			delay_hours INTEGER NOT NULL DEFAULT 0,
			subject TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_seq_tenant_node ON outbound_sequences(tenant_id, node_key)`,
		`CREATE TABLE IF NOT EXISTS outbound_sends (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id BIGINT NOT NULL,
			customer_email TEXT NOT NULL,
			node_key TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'claimed',
			reason TEXT NOT NULL DEFAULT '',
			claimed_at TEXT NOT NULL DEFAULT '',
			sent_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		// ★ A4/M0246：同一 (租户, 客户, 节点) 全历史只许一条——重发就是第二把尺子。
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_outbound_once ON outbound_sends(tenant_id, customer_email, node_key)`,
		`CREATE INDEX IF NOT EXISTS idx_sends_tenant_day ON outbound_sends(tenant_id, customer_email, claimed_at)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(s.db, d, stmt); err != nil {
			return fmt.Errorf("外联合规域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	observability.Info(context.Background(), "外联合规域迁移完成", "statements", len(stmts))
	return nil
}
