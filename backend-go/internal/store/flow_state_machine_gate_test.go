// ============ flow_state_machine_gate_test.go · 数据层行为锁 ============
// 二期 S2 六包平移第二刀 state_machine 的落库口判据：
//   - 乐观锁：Advance 用版本号推进，版本不符命中 0 行（F2 的 DB 侧根基）。
//   - 租户隔离：同 flow_instance_id 不同租户各成一行，互不串。
//   - 幂等 Init：重复 init 不覆盖已有节点、不新增行。
//   - 终态收口：流程已 completed 但状态机漏标的行可补标。
//   - 复合主键 upsert：同 id 不同租户各成一行，冲突目标必须是 (id, tenant_id)。
//
// 配变异体 M0184（摘掉 Get 的 tenant_id 条件 ⇒ 租户隔离红）、
// M0185（摘掉 UPDATE 的 version 条件 ⇒ 乐观锁失效、本锁必红）、
// M0186（冲突目标写回单列 id ⇒ 复合主键 upsert 必红）。
// ================================================
package store

import (
	"testing"

	"translator/internal/config"
	"translator/internal/db"
)

func newFSMTestStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	return newTestStore(t)
}

// TestFlowStateMachineAdvanceVersionConflict 乐观锁：版本不符必须 0 行。
func TestFlowStateMachineAdvanceVersionConflict(t *testing.T) {
	s := newFSMTestStore(t)
	if _, err := s.InitFlowStateMachine(1, "one-1", 1001, "start"); err != nil {
		t.Fatalf("init: %v", err)
	}
	// 首推进（expectedVersion=1）应成功。
	if n, err := s.AdvanceFlowStateMachine(1, 1001, "mid", "{}", 1); err != nil || n != 1 {
		t.Fatalf("首推进应成功 n=%d err=%v", n, err)
	}
	// 用旧版本号再推：乐观锁冲突，必须 0 行。
	n, err := s.AdvanceFlowStateMachine(1, 1001, "end", "{}", 1)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if n != 0 {
		t.Fatalf("版本冲突必须命中 0 行（乐观锁根基），实得 %d", n)
	}
	// 用当前版本（2）推进：成功。
	if n, err := s.AdvanceFlowStateMachine(1, 1001, "end", "{}", 2); err != nil || n != 1 {
		t.Fatalf("当前版本推进应成功 n=%d err=%v", n, err)
	}
}

// TestFlowStateMachineTenantIsolation 同 flow_instance_id 跨租户必须各成一行。
func TestFlowStateMachineTenantIsolation(t *testing.T) {
	s := newFSMTestStore(t)
	if _, err := s.InitFlowStateMachine(1, "o1", 2001, "start"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InitFlowStateMachine(2, "o2", 2001, "start"); err != nil {
		t.Fatal(err)
	}
	got1, err := s.GetFlowStateMachine(1, 2001)
	if err != nil || got1 == nil {
		t.Fatalf("租户1 应读到: got=%v err=%v", got1, err)
	}
	got2, err := s.GetFlowStateMachine(2, 2001)
	if err != nil || got2 == nil {
		t.Fatalf("租户2 应读到: got=%v err=%v", got2, err)
	}
	if got1.TenantID != 1 || got2.TenantID != 2 {
		t.Fatalf("租户串了: %+v %+v", got1, got2)
	}
	if got1.OneID != "o1" || got2.OneID != "o2" {
		t.Fatalf("one_id 串了: %+v %+v", got1, got2)
	}
}

// TestFlowStateMachineIdempotentInit 重复 init 不覆盖节点、不新增行。
func TestFlowStateMachineIdempotentInit(t *testing.T) {
	s := newFSMTestStore(t)
	a, err := s.InitFlowStateMachine(1, "o", 3001, "start")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.InitFlowStateMachine(1, "o", 3001, "other")
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("幂等应返回同一行: %s vs %s", a.ID, b.ID)
	}
	if b.CurrentNode != "start" {
		t.Fatalf("重复 init 不应覆盖节点，实得 %s", b.CurrentNode)
	}
}

// TestFlowStateMachineTerminalReconcile 终态收口：流程已 completed 但状态机漏标 → 补标。
func TestFlowStateMachineTerminalReconcile(t *testing.T) {
	s := newFSMTestStore(t)
	if _, err := s.InitFlowStateMachine(1, "o", 4001, "end"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO flow_instances (id, tenant_id, status) VALUES (?,?,?)`, 4001, 1, SMStatusCompleted); err != nil {
		t.Fatalf("插 flow_instances 失败: %v", err)
	}
	done, err := s.CompletedFlowInstanceIDs([]int64{4001})
	if err != nil || done[4001] != 1 {
		t.Fatalf("应查到租户1的 completed 实例: %v err=%v", done, err)
	}
	if err := s.ReconcileTerminalFlowStateMachine("fsm_1_4001"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetFlowStateMachine(1, 4001)
	if err != nil || got == nil {
		t.Fatalf("读回失败: %v", err)
	}
	if got.Status != SMStatusCompleted {
		t.Fatalf("收口后应为 completed，实得 %s", got.Status)
	}
}

// TestFlowInstanceUpsertCompositeKey 复合主键 upsert：同 id 不同租户各成一行；
// 同 (id, tenant_id) 重写只更新本租户那行的 status。冲突目标写回单列 id 时
// SQLite/PG 都会报「ON CONFLICT 不匹配任何主键/唯一约束」，本用例即红（M0186 靶）。
func TestFlowInstanceUpsertCompositeKey(t *testing.T) {
	s := newFSMTestStore(t)
	if err := s.UpsertFlowInstance(7001, 1, SMStatusRunning); err != nil {
		t.Fatalf("租户1写入失败: %v", err)
	}
	if err := s.UpsertFlowInstance(7001, 2, SMStatusRunning); err != nil {
		t.Fatalf("同 id 不同租户应各成一行，实得错误: %v", err)
	}
	// 同键重写：只动租户1。
	if err := s.UpsertFlowInstance(7001, 1, SMStatusCompleted); err != nil {
		t.Fatalf("同键 upsert 失败: %v", err)
	}
	done, err := s.CompletedFlowInstanceIDs([]int64{7001})
	if err != nil {
		t.Fatal(err)
	}
	if tid, ok := done[7001]; !ok || tid != 1 {
		t.Fatalf("completed 应只命中租户1，实得 %v", done)
	}
	// 租户2 仍是 running：直接查库核对，不许被租户1的收口串改。
	var status string
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT status FROM flow_instances WHERE id=? AND tenant_id=?`, 7001, 2).Scan(&status); err != nil {
		t.Fatalf("租户2的行应仍在: %v", err)
	}
	if status != SMStatusRunning {
		t.Fatalf("租户2不应被串改，实得 %s", status)
	}
}
