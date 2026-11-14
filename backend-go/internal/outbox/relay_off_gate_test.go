// ============ relay_off_gate_test.go · 职责说明 ============
// E6（§4.4.3）：关闭 relay ⇒ 只积压、不丢、不重复；反证 M0297「关闭时删行 ⇒ 红」。
// 先造两条 pending 积压，关闭态连拨三轮，逐表断言「行还在 / status 仍 pending / 零消费 / 零搬运 / 零投递」；
// 再重开 relay 走真链路，断言两条积压各消费一次（幂等不重复）。
// ================================================
package outbox

import (
	"context"
	"testing"

	"translator/internal/db"
	"translator/internal/mq"
)

func TestRelayOffKeepsRowsAndSkipsConsume(t *testing.T) {
	conn := newTestDB(t)
	d := db.DialectSQLite
	ctx := context.Background()
	fq := newFakeQueue()

	deliverCalls := 0
	countingDeliver := func(_ context.Context, _ mq.Envelope) error {
		deliverCalls++
		return nil
	}

	// 两条积压（业务行 + 事件行同事务写入，尚未 relay）。
	for _, eid := range []string{"ev-off-1", "ev-off-2"} {
		ev := testStoredEvent(t, testEnv(t, TopicLeadCaptured, eid, 7, "tr-off"))
		if err := WritePoint(ctx, conn, d, bizInsert("biz-"+eid), ev); err != nil {
			t.Fatalf("E6 前置写点失败: %v", err)
		}
	}
	if got := countRows(t, conn, "event_outbox"); got != 2 {
		t.Fatalf("E6 夹具应有 2 条积压，实得 %d", got)
	}

	// 关闭态 relay 连拨三轮。
	relay := NewRelay(conn, d, fq, countingDeliver, false)
	if relay.Enabled() {
		t.Fatal("E6 夹具 relay 初始必须为关闭态")
	}
	for i := 0; i < 3; i++ {
		if err := relay.RelayOnce(ctx); err != nil {
			t.Fatalf("E6 关闭态第 %d 轮不许报错: %v", i, err)
		}
	}

	// 行不丢（M0297 靶）：仍在、且 status 仍是 pending（连标记都不许改）。
	if got := countRows(t, conn, "event_outbox"); got != 2 {
		t.Fatalf("E6 关闭 relay 后 outbox 行不许丢，实得 %d（M0297 关闭时删行会让这里红）", got)
	}
	status, ok := scalarString(t, conn, "SELECT status FROM event_outbox WHERE event_id=?", "ev-off-1")
	if !ok || status != StatusPending {
		t.Fatalf("E6 关闭态不得改 status（仍应为 pending），实得 %q", status)
	}
	// 不消费、不搬运、不投递。
	if got := countRows(t, conn, "event_inbox"); got != 0 {
		t.Fatalf("E6 关闭态不许消费，event_inbox 实得 %d", got)
	}
	if got := countRows(t, conn, "lead_captured_inbox"); got != 0 {
		t.Fatalf("E6 关闭态不许写读模型，实得 %d", got)
	}
	if got := len(fq.snapshot()); got != 0 {
		t.Fatalf("E6 关闭态不许搬运进 queue 账本，实得 %d 条 job", got)
	}
	if deliverCalls != 0 {
		t.Fatalf("E6 关闭态不许投递，实得 %d 次", deliverCalls)
	}

	// 重开：走真链路（Deliverer=consumer.Handle），两条积压各消费一次、不重复。
	c := NewConsumer(conn, d, nil)
	relayOn := NewRelay(conn, d, fq, c.Handle, true)
	for i := 0; i < 4; i++ {
		if err := relayOn.RelayOnce(ctx); err != nil {
			t.Fatalf("E6 重开后第 %d 轮投递失败: %v", i, err)
		}
	}
	if got := countRows(t, conn, "event_inbox"); got != 2 {
		t.Fatalf("E6 重开后两条积压应各消费一次（不重复），实得 %d", got)
	}
	if got := countRows(t, conn, "lead_captured_inbox"); got != 2 {
		t.Fatalf("E6 重开后读模型副作用应恰好 2 行，实得 %d", got)
	}
}
