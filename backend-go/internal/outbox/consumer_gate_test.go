// ============ consumer_gate_test.go · 职责说明 ============
// E3（§4.4.3）：幂等——同一 event_id 投两次，副作用只发生一次；反证 M0294「去掉唯一键 ⇒ 红」。
// 真跑 consumer.Process（真事务、真 INSERT OR IGNORE / PG ON CONFLICT DO NOTHING、RowsAffected 判据），
// 用「读模型行数 / 通知次数 / Process 返回的 processed 布尔」三把尺子同时钉「恰好一次」。
// ================================================
package outbox

import (
	"context"
	"testing"

	"translator/internal/db"
	"translator/internal/mq"
)

func TestConsumerExactlyOnceSideEffect(t *testing.T) {
	conn := newTestDB(t)
	d := db.DialectSQLite
	ctx := context.Background()

	notifyCalls := 0
	c := NewConsumer(conn, d, func(_ context.Context, _ mq.Envelope) { notifyCalls++ })
	env := testEnv(t, TopicLeadCaptured, "ev-idem", 7, "tr-idem")

	// 第一次投递：抢账首胜，真正副作用发生。
	p1, err := c.Process(ctx, env)
	if err != nil {
		t.Fatalf("E3 首次消费失败: %v", err)
	}
	if !p1 {
		t.Fatal("E3 首次投递必须真正处理（processed=true）")
	}

	// 第二次投递：同一 (topic,event_id) ⇒ INSERT 0 行 ⇒ 跳过，不报错（跳过不是失败）。
	p2, err := c.Process(ctx, env)
	if err != nil {
		t.Fatalf("E3 重复投递不许报错（幂等跳过不是失败），实得 %v", err)
	}
	if p2 {
		t.Fatal("E3 重复投递必须跳过（processed=false，0 行即跳过），不许再执行副作用")
	}

	// 三把尺子同判「恰好一次副作用」。
	if got := countRows(t, conn, "event_inbox"); got != 1 {
		t.Fatalf("E3 幂等账本应 1 行，实得 %d", got)
	}
	if got := countRows(t, conn, "lead_captured_inbox"); got != 1 {
		t.Fatalf("E3 重复投递恰好一次副作用：读模型应 1 行，实得 %d（M0294 去掉 UNIQUE 会让这里变 2 ⇒ 红）", got)
	}
	if notifyCalls != 1 {
		t.Fatalf("E3 发通知副作用应恰好 1 次，实得 %d", notifyCalls)
	}

	// 再投第三次：幂等必须稳定，不因次数增长而漏判。
	if _, err := c.Process(ctx, env); err != nil {
		t.Fatalf("E3 第三次投递失败: %v", err)
	}
	if got := countRows(t, conn, "lead_captured_inbox"); got != 1 {
		t.Fatalf("E3 三次投递后读模型仍应恰好 1 行，实得 %d", got)
	}
}
