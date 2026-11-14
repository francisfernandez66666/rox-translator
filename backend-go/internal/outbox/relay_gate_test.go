// ============ relay_gate_test.go · 职责说明 ============
// E2（§4.4.3）：至少一次——投递失败重试，耗尽进死信；反证 M0293「失败即 MarkDone ⇒ 红」。
// 真跑 relay 多轮 + fake queue（复刻 DirectQueue 的 Reserve/MarkFailed CASE WHEN 语义），
// 断言 job 经 MaxAttempts 次 Reserve 后状态为 dead，且 relay 全程不碰消费侧三张表（只做投递）。
// ================================================
package outbox

import (
	"context"
	"errors"
	"testing"

	"translator/internal/db"
	"translator/internal/mq"
)

func TestRelayRetriesThenDeadLetters(t *testing.T) {
	conn := newTestDB(t)
	d := db.DialectSQLite
	ctx := context.Background()
	fq := newFakeQueue()

	// 投递目标恒失败：每一轮都必须交回 queue 判重试/死信。
	failDeliver := func(_ context.Context, _ mq.Envelope) error {
		return errors.New("投递目标不可用（故意失败）")
	}
	relay := NewRelay(conn, d, fq, failDeliver, true)

	ev := testStoredEvent(t, testEnv(t, TopicLeadCaptured, "ev-retry", 7, "tr-retry"))
	if err := WritePoint(ctx, conn, d, bizInsert("lead-retry"), ev); err != nil {
		t.Fatalf("E2 前置写点失败: %v", err)
	}

	// 反复拨 RelayOnce（模拟 watchdog 的 5s 槽）直到重试耗尽。
	for i := 0; i < DeliveryMaxAttempts+3; i++ {
		_ = relay.RelayOnce(ctx) // 单轮失败会上抛，交给下一轮重试；不静默吞由调用方决定
	}

	jobs := fq.snapshot()
	if len(jobs) != 1 {
		t.Fatalf("E2 应恰好搬运出 1 条投递任务，实得 %d（重复搬运会让幂等链失真）", len(jobs))
	}
	j := jobs[0]
	if j.status != "dead" {
		t.Fatalf("E2 重试耗尽必须进死信，实得 status=%s（M0293 把 MarkFailed 换成 MarkDone 会让这里红）", j.status)
	}
	if j.attempts != DeliveryMaxAttempts {
		t.Fatalf("E2 必须真重试到 attempts=%d 才判死信，实得 %d（一次判死＝没复用 queue 的重试语义）",
			DeliveryMaxAttempts, j.attempts)
	}

	// relay 只做投递、不做业务：消费侧三张表必须一行未动。
	if got := countRows(t, conn, "event_inbox"); got != 0 {
		t.Fatalf("E2 relay 不许写 event_inbox（那是 consumer 的职责），实得 %d", got)
	}
	if got := countRows(t, conn, "lead_captured_inbox"); got != 0 {
		t.Fatalf("E2 relay 不许写读模型，实得 %d", got)
	}
	// 积压账本行不丢：搬运只是把 status 从 pending 改 relayed，不删行。
	if got := countRows(t, conn, "event_outbox"); got != 1 {
		t.Fatalf("E2 event_outbox 行不许丢，实得 %d", got)
	}
}
