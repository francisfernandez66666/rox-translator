// ============ migrate.go · 职责说明 ============
// W4 outbox 域建表（DDL 唯一入口：db.ExecDDL / db.EnsureColumns，双方言 SQLite+PG）。
//
// 三张表：
//   - event_outbox：业务事务内写入的持久积压账本（关 relay 只积压不丢，E6）。
//   - event_inbox ：消费侧幂等账本，唯一键 (topic,event_id)，ON CONFLICT DO NOTHING 后 0 行即跳过（E3）。
//   - lead_captured_inbox：consumer 的「真正副作用」读模型行（写 inbox 读模型 / 发通知，E3 数它）。
//
// ★ 唯一键一律落在 (topic, event_id)：跨 topic 同 event_id 互不干扰；这是「重复投递只处理一次」
//
//	的唯一事实源，不许在应用侧再造第二把尺子（施工 §4.4.2 硬口径 2）。
//
// ================================================
package outbox

import (
	"context"
	"database/sql"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// Migrate 幂等建三张表与两把唯一键；失败即带 SQL 上抛，绝不静默降级（缺表会让读路径运行期才炸）。
// 所有 DDL 以 SQLite 方言为唯一真源，PG 下由 db.ExecDDL / db.EnsureColumns 内部翻译。
// 参数：conn=平台库连接；d=方言；返回：迁移错误。
func Migrate(conn *sql.DB, d db.Dialect) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS event_outbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			topic TEXT NOT NULL,
			event_id TEXT NOT NULL,
			tenant_id BIGINT NOT NULL,
			one_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			payload TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'pending',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_event_outbox_topic_event ON event_outbox(topic, event_id)`,
		`CREATE INDEX IF NOT EXISTS idx_event_outbox_pending ON event_outbox(status, id)`,
		`CREATE TABLE IF NOT EXISTS event_inbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			topic TEXT NOT NULL,
			event_id TEXT NOT NULL,
			tenant_id BIGINT NOT NULL,
			trace_id TEXT NOT NULL DEFAULT '',
			payload TEXT NOT NULL DEFAULT '',
			processed_at TEXT NOT NULL DEFAULT ''
		)`,
		// ★ E3/M0294 靶：去掉 UNIQUE 会让重复投递插出两行、副作用发生两次 ⇒ 对应断言红。
		`CREATE UNIQUE INDEX IF NOT EXISTS ux_event_inbox_topic_event ON event_inbox(topic, event_id)`,
		`CREATE TABLE IF NOT EXISTS lead_captured_inbox (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			topic TEXT NOT NULL,
			event_id TEXT NOT NULL,
			tenant_id BIGINT NOT NULL,
			created_at TEXT NOT NULL DEFAULT ''
		)`,
	}
	for _, stmt := range stmts {
		if err := db.ExecDDL(conn, d, stmt); err != nil {
			return fmt.Errorf("outbox 域建表失败: %w\nSQL: %s", err, stmt)
		}
	}
	// 幂等补列兜底（AGENTS §一·1「新增列走 db.EnsureColumns」）：老库若已有 event_outbox 但缺
	// 信封必带字段列，这里补齐；新库列齐全 ⇒ 两方言均为 no-op。DDL 仍只经本包唯一入口。
	if err := db.EnsureColumns(conn, d, "event_outbox", map[string]string{
		"tenant_id": "BIGINT NOT NULL DEFAULT 0",
		"trace_id":  "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		return fmt.Errorf("outbox 域补列失败: %w", err)
	}
	observability.Info(context.Background(), "outbox 域迁移完成", "statements", len(stmts))
	return nil
}
