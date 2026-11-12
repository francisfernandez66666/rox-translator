// ============ sales_crm_gate_test.go · 职责说明 ============
// 三期 W6 CRM 域的落库锁（真 SQLite 文件库）：
//   A24/M0263：pending_human 按会话精确回填——只动 conversation_id 匹配且 pending_human=0 的
//     归因行（重复回填不翻已置位标记、不误伤他人会话的行）；resolve 只动会话行。
//   A20：线索阶段单向推进（回退事件被忽略）；漏斗五段计数互不污染。
//   续跑闭环：TouchFlowInstanceByOneID 对无 running 行 0 行即正常。
// ================================================
package store

import (
	"path/filepath"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

// newCRMTestStore 文件库建 Store（CRM 域表随 New() 域链幂等建立）。
func newCRMTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "crm.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := New(conn)
	if err != nil {
		t.Fatalf("Store 迁移失败: %v", err)
	}
	return st
}

// attributionByMessage 用 ListReplyAttributions 定位指定 message_id 的行（分页读口当单行读用）。
func attributionByMessage(t *testing.T, st *Store, tid, msgID int64) *ReplyAttributionRow {
	t.Helper()
	rows, err := st.ListReplyAttributions(tid, 500, 0)
	if err != nil {
		t.Fatalf("ListReplyAttributions: %v", err)
	}
	for i := range rows {
		if rows[i].MessageID == msgID {
			return &rows[i]
		}
	}
	return nil
}

// TestHandoffPendingBackfillBySession 按会话回填：精确、幂等、不误伤他人会话。
func TestHandoffPendingBackfillBySession(t *testing.T) {
	st := newCRMTestStore(t)
	const tid = int64(9)
	// 两条归因行：A 挂会话 sess-A，B 挂会话 sess-B（互为「不误伤」对照）。
	if err := st.RecordReply(ReplyAttributionRow{TenantID: tid, MessageID: 101}); err != nil {
		t.Fatalf("RecordReply A: %v", err)
	}
	if err := st.RecordReply(ReplyAttributionRow{TenantID: tid, MessageID: 202}); err != nil {
		t.Fatalf("RecordReply B: %v", err)
	}
	convA, err := st.EnsureHandoffSession(tid, "sess-A", 101)
	if err != nil || convA <= 0 {
		t.Fatalf("EnsureHandoffSession A: id=%d err=%v", convA, err)
	}
	// 幂等：同会话重复建映射返回同一行。
	if convA2, _ := st.EnsureHandoffSession(tid, "sess-A", 101); convA2 != convA {
		t.Fatalf("会话映射不幂等：%d vs %d", convA, convA2)
	}
	// 把 A 行挂到 convA（真实链路由 writeBackAttribution 组 row 时写入 ConversationID）。
	if _, err := st.db.Exec(
		`UPDATE reply_attributions SET conversation_id=? WHERE tenant_id=? AND message_id=?`,
		convA, tid, 101); err != nil {
		t.Fatalf("挂会话列: %v", err)
	}
	// 回填：只有 A 行被置位。
	changed, err := st.MarkPendingHumanBySession(tid, "sess-A")
	if err != nil || !changed {
		t.Fatalf("MarkPendingHumanBySession: changed=%v err=%v", changed, err)
	}
	if rowA := attributionByMessage(t, st, tid, 101); rowA == nil || rowA.PendingHuman != 1 {
		t.Fatalf("A 行未被置位: %+v", rowA)
	}
	if rowB := attributionByMessage(t, st, tid, 202); rowB == nil || rowB.PendingHuman != 0 {
		t.Fatalf("B 行被误伤（按会话精确回填失守）: %+v", rowB)
	}
	// 重复回填：A 行已是 1，changed=false（不重复写标记——幂等判据）。
	if changed2, _ := st.MarkPendingHumanBySession(tid, "sess-A"); changed2 {
		t.Fatal("重复回填又改了行：不满足「只补映射不重复写标记」")
	}
	// resolve：会话行清 pending、归因行不动（管理面只消不造）。
	if err := st.ResolveHandoffSession(tid, "sess-A"); err != nil {
		t.Fatalf("ResolveHandoffSession: %v", err)
	}
	sessions, _ := st.ListHandoffSessions(tid, true, 10)
	if len(sessions) != 0 {
		t.Fatalf("resolve 后仍列在待人工里: %+v", sessions)
	}
	if rowA2 := attributionByMessage(t, st, tid, 101); rowA2 == nil || rowA2.PendingHuman != 1 {
		t.Fatal("resolve 不该回翻归因行的 pending_human")
	}
}

// TestLeadStageForwardOnly 线索阶段单向推进 + 回退忽略 + 漏斗计数。
func TestLeadStageForwardOnly(t *testing.T) {
	st := newCRMTestStore(t)
	const tid = int64(9)
	if err := st.UpsertLeadFromEmail(tid, "l@x.com", LeadStageNew, 10); err != nil {
		t.Fatalf("建线索: %v", err)
	}
	if err := st.UpsertLeadFromEmail(tid, "l@x.com", LeadStageEngaged, 40); err != nil {
		t.Fatalf("推进 engaged: %v", err)
	}
	// 回退事件（new）被忽略：只刷分不降档。
	if err := st.UpsertLeadFromEmail(tid, "l@x.com", LeadStageNew, 5); err != nil {
		t.Fatalf("回退事件: %v", err)
	}
	rows, _ := st.ListLeads(tid, "", false, 10)
	if len(rows) != 1 || rows[0].Stage != LeadStageEngaged || rows[0].Score != 40 {
		t.Fatalf("阶段/分数不符（期望 engaged/40）: %+v", rows)
	}
	// 终态推进 won；漏斗五段计数不互相污染。
	if err := st.UpsertLeadFromEmail(tid, "l@x.com", LeadStageWon, 60); err != nil {
		t.Fatalf("推进 won: %v", err)
	}
	if err := st.UpsertLeadFromEmail(tid, "m@x.com", LeadStageLost, 1); err != nil {
		t.Fatalf("另一客户 lost: %v", err)
	}
	counts, err := st.LeadFunnelCounts(tid)
	if err != nil {
		t.Fatalf("LeadFunnelCounts: %v", err)
	}
	if counts[LeadStageWon] != 1 || counts[LeadStageLost] != 1 || counts[LeadStageNew] != 0 {
		t.Fatalf("漏斗计数被污染: %+v", counts)
	}
	if counts["pending_human"] != 0 {
		t.Fatalf("待人工计数档缺失或被污染: %+v", counts)
	}
}

// TestTouchFlowInstanceHeartbeat 续跑心跳：空 one_id 零动作；无 running 行 0 行即正常。
func TestTouchFlowInstanceHeartbeat(t *testing.T) {
	st := newCRMTestStore(t)
	const tid = int64(9)
	if n, err := st.TouchFlowInstanceByOneID(tid, ""); err != nil || n != 0 {
		t.Fatalf("空 one_id 应零动作: n=%d err=%v", n, err)
	}
	if _, err := st.TouchFlowInstanceByOneID(tid, "one-x"); err != nil {
		t.Fatalf("TouchFlowInstanceByOneID: %v", err)
	}
}
