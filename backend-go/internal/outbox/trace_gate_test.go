// ============ trace_gate_test.go · 职责说明 ============
// E5（§4.4.3）：trace 贯穿——outbox → relay → consumer 同一个 trace_id；反证 M0296「丢掉 trace 字段 ⇒ 红」。
// 真链路：WritePoint 落 event_outbox.trace_id + 完整信封 payload → relay 经 UnmarshalEnvelope 还原投递
// → consumer 写 event_inbox.trace_id。四处逐字等值，任一环丢 trace 即红。
// ================================================
package outbox

import (
	"context"
	"testing"

	"translator/internal/db"
	"translator/internal/mq"
)

func TestTraceIDFlowsOutboxToInbox(t *testing.T) {
	conn := newTestDB(t)
	d := db.DialectSQLite
	ctx := context.Background()
	fq := newFakeQueue()

	const trace = "trace-w4-e5-xyz"
	env := testEnv(t, TopicLeadCaptured, "ev-trace", 7, trace)
	ev := testStoredEvent(t, env)

	// ① 写点：event_outbox.trace_id 必须带原值（信封头也带）。
	if err := WritePoint(ctx, conn, d, bizInsert("lead-trace"), ev); err != nil {
		t.Fatalf("E5 前置写点失败: %v", err)
	}
	outTrace, ok := scalarString(t, conn, "SELECT trace_id FROM event_outbox WHERE event_id=?", "ev-trace")
	if !ok || outTrace != trace {
		t.Fatalf("E5 outbox.trace_id 应为 %q，实得 %q（存在=%v）", trace, outTrace, ok)
	}

	// ② relay 投递 → consumer 消费（真链路，Deliverer 直接接 consumer.Handle）。
	c := NewConsumer(conn, d, nil)
	relay := NewRelay(conn, d, fq, c.Handle, true)
	for i := 0; i < 3; i++ {
		if err := relay.RelayOnce(ctx); err != nil {
			t.Fatalf("E5 relay 第 %d 轮投递失败: %v", i, err)
		}
	}

	// ③ consumer 写进 event_inbox.trace_id 的必须还是同一个 trace_id（M0296 靶）。
	inTrace, ok := scalarString(t, conn, "SELECT trace_id FROM event_inbox WHERE event_id=?", "ev-trace")
	if !ok {
		t.Fatal("E5 事件未被消费（event_inbox 无行），链路没跑通")
	}
	if inTrace != trace {
		t.Fatalf("E5 trace 必须贯穿 outbox→consumer，实得 event_inbox.trace_id=%q（M0296 丢掉 trace 字段会让这里红）", inTrace)
	}
	if inTrace != outTrace {
		t.Fatalf("E5 outbox.trace_id=%q 与 inbox.trace_id=%q 必须相等", outTrace, inTrace)
	}

	// ④ 载荷信封里的 trace_id 也没被 relay 抹掉（UnmarshalEnvelope 还原后逐字等值）。
	payloadStr, ok := scalarString(t, conn, "SELECT payload FROM event_inbox WHERE event_id=?", "ev-trace")
	if !ok {
		t.Fatal("E5 缺 event_inbox.payload")
	}
	decoded, err := mq.UnmarshalEnvelope([]byte(payloadStr))
	if err != nil {
		t.Fatalf("E5 payload 不是可还原信封: %v", err)
	}
	if decoded.Header.TraceID != trace {
		t.Fatalf("E5 载荷信封 trace_id 也必须贯穿，实得 %q", decoded.Header.TraceID)
	}
}
