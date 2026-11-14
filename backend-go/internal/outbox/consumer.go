// ============ consumer.go · 职责说明 ============
// W4 第一个消费者：INSERT event_inbox(topic,event_id) ON CONFLICT DO NOTHING，
// 0 行即跳过（幂等）；首次抢账成功才做「真正副作用」＝写 inbox 读模型 / 发通知。
//
// ★ 硬口径 2「消费幂等键唯一 (topic,event_id)；ON CONFLICT DO NOTHING 后 0 行即跳过」：
//
//	抢账与副作用在**同一事务**，崩溃不会留下「已处理却没副作用」的半行。
//	SQLite 唯一真源写 INSERT OR IGNORE，PG 下由 db.ToDialect 自动翻成 ON CONFLICT DO NOTHING
//	（AGENTS §一·双方言，不许在业务里硬写方言分支）。RowsAffected()==0 是双方言统一的跳过判据。
//
// ★ relay 只把信封投到这里；本文件才是「业务副作用」的唯一落点（relay 不碰 event_inbox）。
// ================================================
package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"translator/internal/db"
	"translator/internal/mq"
	"translator/internal/observability"
)

// Notifier 是「发通知」副作用的可注入挂钩（测试计数；生产可接 notify/站内信）。
// 它在抢账事务 Commit 之后调用，故与读模型落库是「至少一次通知」语义，重复投递不会触发。
type Notifier func(ctx context.Context, env mq.Envelope)

// Consumer lead.captured 消费者。conn=库连接；d=方言；notify=通知回调（可 nil）。
type Consumer struct {
	conn   *sql.DB
	d      db.Dialect
	notify Notifier
}

// NewConsumer 构造消费者。参数：conn=连接；d=方言；notify=通知回调（可 nil）；返回：消费者。
func NewConsumer(conn *sql.DB, d db.Dialect, notify Notifier) *Consumer {
	return &Consumer{conn: conn, d: d, notify: notify}
}

// Process 幂等消费一条事件，返回 (本次是否真正处理, error)。
// 重复投递 ⇒ (false, nil)：0 行即跳过，绝不执行副作用（E3 的「恰好一次」由此而来）。
// 参数：ctx=上下文；env=信封；返回：(是否首胜, 错误)。
func (c *Consumer) Process(ctx context.Context, env mq.Envelope) (bool, error) {
	raw, merr := env.Marshal()
	if merr != nil {
		return false, fmt.Errorf("消费前信封序列化失败: %w", merr)
	}
	tx, err := c.conn.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("消费抢账开启事务失败: %w", err)
	}
	defer tx.Rollback()
	nowStr := time.Now().UTC().Format(time.RFC3339)
	// ★ E3 / M0294 靶：唯一键 ux_event_inbox_topic_event 去掉 UNIQUE ⇒ 重复投递插出两行 ⇒ 副作用两次 ⇒ 红。
	res, err := db.Exec(tx, c.d,
		"INSERT OR IGNORE INTO event_inbox(topic,event_id,tenant_id,trace_id,payload,processed_at) VALUES(?,?,?,?,?,?)",
		env.Topic, env.Header.EventID, env.Header.TenantID, env.Header.TraceID, string(raw), nowStr)
	if err != nil {
		return false, fmt.Errorf("写 event_inbox 幂等抢账失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取抢账影响行数失败: %w", err)
	}
	if n == 0 {
		observability.Info(ctx, "事件重复投递，幂等跳过（0 行）", "event_id", env.Header.EventID, "topic", env.Topic)
		return false, nil // 已被处理过：跳过，不是错误
	}
	// 真正副作用（与抢账同一事务）：写 inbox 读模型。
	if _, err := db.Exec(tx, c.d,
		"INSERT INTO lead_captured_inbox(topic,event_id,tenant_id,created_at) VALUES(?,?,?,?)",
		env.Topic, env.Header.EventID, env.Header.TenantID, nowStr); err != nil {
		return false, fmt.Errorf("写 inbox 读模型失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("消费提交失败: %w", err)
	}
	// 发通知（Commit 之后；重复投递走到上面 n==0 就返回，不会到这里）。
	if c.notify != nil {
		c.notify(ctx, env)
	}
	observability.Info(ctx, "事件首次消费完成（写读模型）", "event_id", env.Header.EventID, "topic", env.Topic, "tenant_id", env.Header.TenantID)
	return true, nil
}

// Handle 满足 Deliverer / mq.EventHandler 签名（relay 与 mq.Bus 都按此调）。
// 重复投递返回 nil——「跳过」不是失败，不能让 relay 误当失败去 MarkFailed 重试/进死信。
func (c *Consumer) Handle(ctx context.Context, env mq.Envelope) error {
	_, err := c.Process(ctx, env)
	return err
}
