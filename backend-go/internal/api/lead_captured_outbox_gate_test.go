// ============ lead_captured_outbox_gate_test.go · 职责说明 ============
// 四期 W4 收口（2026-11-15）：**产品写点**真的写了 event_outbox，且与业务行同事务。
//
// 旧形态：E1–E6 全绿（框架台架正确），但 WriteLeadCaptured / EmitLeadCapturedInTx
// 零生产调用、bridge 直写 inbox_events ⇒ outbox 恒空。审计 §3.4 判「产品腿不存在」。
//
// 三条腿：
//
//	① 行为：一次 bridge ⇒ inbox_events 行 + event_outbox 行同时存在，且 tenant_id /
//	   event_id / topic 三者逐字对齐（不是「各写一行就算」）；
//	② 幂等：同一 eventID 重放 ⇒ 两表都不许多一行（sentinel 跳过生效）；
//	③ 反证（进程内，不依赖变异体）：business 失败 ⇒ 事务整体回滚 ⇒ outbox 必须 0 行。
//	   把 InsertOutboxEvent 挪到事务外（用 conn 而非 tx）就会在此红。
//	④ 源码腿：bridge 里必须真的调用 WriteLeadCaptured（防「接好了又被人摘掉」）。
//
// 夹具复用 sales_inbox_lead_gate_test.go 的 leadInboxHarness（store.New 已跑
// OutboxMigrate，event_outbox 表在位）；方言自钉 SQLite。
// ================================================
package api

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"

	"translator/internal/db"
	"translator/internal/outbox"
	"translator/internal/store"
)

// countOutbox 按 event_id 数 event_outbox 行（真读库，不是桩）。
func countOutbox(t *testing.T, s *Server, eventID string) int {
	t.Helper()
	var n int
	if err := s.Store.DB().QueryRow(
		"SELECT COUNT(*) FROM event_outbox WHERE event_id=?", eventID).Scan(&n); err != nil {
		t.Fatalf("读 event_outbox 失败: %v", err)
	}
	return n
}

// countInbox 按 event_id 数 inbox_events 行。
func countInbox(t *testing.T, s *Server, eventID string) int {
	t.Helper()
	var n int
	if err := s.Store.DB().QueryRow(
		"SELECT COUNT(*) FROM inbox_events WHERE event_id=?", eventID).Scan(&n); err != nil {
		t.Fatalf("读 inbox_events 失败: %v", err)
	}
	return n
}

func TestLeadCapturedWritesInboxAndOutboxSameTx(t *testing.T) {
	s := leadInboxHarness(t)
	const sellerTid = int64(7)
	// ★ 方案 §4.2：判据要问「业务行 + inbox_events 行 + event_outbox 行」三件同时存在。
	//   先真落一行 feedbacks（走产品侧 CreateFeedback，不是桩），再用它的 id 当幂等键，
	//   否则两表断言会对着「一条根本没落库的线索」绿着说谎。
	fb := &store.Feedback{
		TenantID: sellerTid, UserID: 0, TargetType: "lead",
		Content: "W4 双写夹具", TargetLangs: "en",
	}
	if err := s.Store.CreateFeedback(fb); err != nil {
		t.Fatalf("落业务行 feedbacks 失败: %v", err)
	}
	var businessRows int
	if err := s.Store.DB().QueryRow("SELECT COUNT(*) FROM feedbacks WHERE id=?", fb.ID).Scan(&businessRows); err != nil {
		t.Fatalf("读业务行失败: %v", err)
	}
	if businessRows != 1 {
		t.Fatalf("业务行 feedbacks 应恰 1 行，实得 %d（夹具自伤）", businessRows)
	}
	oneID := strconv.FormatInt(fb.ID, 10)
	eventID := LeadCapturedInboxEventID("lead-product:acme:9f3a", fb.ID)

	if err := s.bridgeLeadCapturedToInbox(context.Background(), sellerTid, eventID, oneID); err != nil {
		t.Fatalf("W4 双写失败: %v", err)
	}
	if got := countInbox(t, s, eventID); got != 1 {
		t.Fatalf("inbox_events 应有 1 行，实得 %d（原 W2 能力不能被新事件行打掉）", got)
	}
	if got := countOutbox(t, s, eventID); got != 1 {
		t.Fatalf("event_outbox 应有 1 行，实得 %d（产品写点仍然零事件 ⇒ W4 没落地）", got)
	}

	var topic, status string
	var tid int64
	if err := s.Store.DB().QueryRow(
		"SELECT topic,status,tenant_id FROM event_outbox WHERE event_id=?", eventID).
		Scan(&topic, &status, &tid); err != nil {
		t.Fatalf("读 event_outbox 字段失败: %v", err)
	}
	if topic != outbox.TopicLeadCaptured || status != outbox.StatusPending || tid != sellerTid {
		t.Fatalf("outbox 行字段不对: topic=%q status=%q tenant_id=%d", topic, status, tid)
	}

	// ② 幂等重放：两表都不许多一行。
	if err := s.bridgeLeadCapturedToInbox(context.Background(), sellerTid, eventID, oneID); err != nil {
		t.Fatalf("幂等重放返回错误: %v", err)
	}
	if got := countInbox(t, s, eventID); got != 1 {
		t.Fatalf("幂等重放后 inbox_events 变成 %d 行", got)
	}
	if got := countOutbox(t, s, eventID); got != 1 {
		t.Fatalf("幂等重放后 event_outbox 变成 %d 行（重复投递会撞唯一键并把事务打回）", got)
	}
}

// TestLeadCapturedOutboxRollsBackWithBusiness 同事务反证：
// business 失败 ⇒ outbox 必须一行不留（挪到事务外就会红）。
func TestLeadCapturedOutboxRollsBackWithBusiness(t *testing.T) {
	s := leadInboxHarness(t)
	const boomID = "ev-tx-rollback-probe"
	err := WriteLeadCaptured(context.Background(), s.Store.DB(), db.CurrentDialect(),
		func(*sql.Tx) error { return errors.New("业务写故意失败") },
		LeadCapturedParams{TenantID: 7, OneID: "42", EventID: boomID, TraceID: "tr-boom"})
	if err == nil {
		t.Fatal("业务写失败却返回 nil（事务没回滚）")
	}
	if got := countOutbox(t, s, boomID); got != 0 {
		t.Fatalf("反证失效：业务写回滚后 event_outbox 仍有 %d 行 ⇒ 事件写不在同一事务", got)
	}
}

// TestLeadCapturedBridgeCallsWriteLeadCaptured 源码腿：生产写点不许被摘掉。
func TestLeadCapturedBridgeCallsWriteLeadCaptured(t *testing.T) {
	b, err := os.ReadFile("sales_lead_inbox_bridge.go")
	if err != nil {
		t.Fatalf("读 bridge 源码失败: %v", err)
	}
	if !strings.Contains(string(b), "WriteLeadCaptured(ctx, s.Store.DB()") {
		t.Fatal("bridge 不再调用 WriteLeadCaptured ⇒ 产品写点被架空，event_outbox 会重新恒空")
	}
	if !strings.Contains(string(b), "TryProcessInboxEventTx(tx,") {
		t.Fatal("bridge 的 inbox 写不在同一个 tx 内 ⇒ 双写不是原子")
	}
}
