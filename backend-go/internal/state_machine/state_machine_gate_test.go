// ============ state_machine_gate_test.go · 服务层行为锁 ============
// 二期 S2 六包平移第二刀 state_machine 的服务层判据：
//   - F2 乐观锁：Advance 在 store 返回 0 行（版本冲突）时必须显式回 ErrVersionConflict，不能静默吞。
//   - 跨租户安全：终态收口按 (instanceID, tenantID) 配对，禁止跨租户误标 completed。
//   - 巡检 requeue 开关：禁用仅记录+标记；启用才发 flow_drive 断点续跑。
//
// 配变异体 M0187（把 n==0 判据翻成 n!=0 ⇒ 冲突被静默吞、F2 必红）、
// M0188（反转 tid 配对判据 ⇒ 该收口的不收、跨租户误标，本锁必红）、
// M0189/M0190（把 requeue 开关写死 true/false ⇒ 两个分支必各红其一）。
// ================================================
package state_machine

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"
)

// fakeConfig 注入式 ConfigReader：仅 sm_sweep_requeue_enabled 可控。
type fakeConfig struct{ requeue bool }

func (c fakeConfig) GetBool(key string, def bool) bool {
	if key == "sm_sweep_requeue_enabled" {
		return c.requeue
	}
	return def
}

// fakePublisher 注入式 Publisher：记录是否被调用及最近一次参数。
type fakePublisher struct {
	called      bool
	lastTenant  int64
	lastFlowID  string
	lastPayload map[string]any
}

func (p *fakePublisher) PublishFlowDrive(ctx context.Context, tid int64, _ string, fiid string, _ string, payload map[string]any) error {
	p.called = true
	p.lastTenant = tid
	p.lastFlowID = fiid
	p.lastPayload = payload
	return nil
}

// newTestService 构建基于内存 SQLite 的 store + 注入式 Service。
func newTestService(t *testing.T) (*store.Store, *Service) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return st, NewService(st, nil, nil)
}

// TestServiceAdvanceVersionConflict F2：版本冲突（store 返回 0 行）必须回 ErrVersionConflict。
func TestServiceAdvanceVersionConflict(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Init(ctx, 1, "o", 1001, "start"); err != nil {
		t.Fatal(err)
	}
	// 首推进：当前版本 1，成功。
	if err := svc.Advance(ctx, 1, 1001, "mid", "{}", 1); err != nil {
		t.Fatalf("首推进应成功: %v", err)
	}
	// 用过期版本号再推：store 命中 0 行（乐观锁冲突）。
	err := svc.Advance(ctx, 1, 1001, "end", "{}", 1)
	if err == nil || !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("Advance 版本冲突必须显式返回 ErrVersionConflict，实得 err=%v", err)
	}
}

// TestServiceAdvanceConcurrentSingleWinner F2 并发分相（§9.2 口径）：
// 先把版本读到 1（分相一），再让 8 个 goroutine 同时拿版本 1 推进（分相二），
// 断言恰好一个成功、其余全部 ErrVersionConflict、终态版本=2——
// 不把断言交给「goroutine 刚好来得及」，输家必须拿到可辨错误而不是被吞成成功。
// 用文件库：内存库多连接各自建库，并发用例会假绿（照 sales_reserve 先例）。
func TestServiceAdvanceConcurrentSingleWinner(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := db.Open(db.Config{Driver: db.DriverSQLite, DSN: filepath.Join(t.TempDir(), "fsm.db")})
	if err != nil {
		t.Fatalf("文件库建连失败: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	svc := NewService(st, nil, nil)
	ctx := context.Background()
	if _, err := svc.Init(ctx, 1, "o", 1101, "start"); err != nil {
		t.Fatal(err)
	}
	row, err := svc.Get(ctx, 1, 1101)
	if err != nil || row == nil || row.Version != 1 {
		t.Fatalf("分相一：应读到版本1，实得 row=%v err=%v", row, err)
	}

	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins, conflicts, other := 0, 0, []error{}
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := svc.Advance(ctx, 1, 1101, "mid", "{}", 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				wins++
			case errors.Is(err, ErrVersionConflict):
				conflicts++
			default:
				other = append(other, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(other) != 0 {
		t.Fatalf("F2：败方只许拿 ErrVersionConflict，不许上抛其它错误：%v", other)
	}
	if wins != 1 || conflicts != n-1 {
		t.Fatalf("F2：%d 并发必须恰好 1 赢家＋%d 冲突，实得 wins=%d conflicts=%d", n, n-1, wins, conflicts)
	}
	got, err := svc.Get(ctx, 1, 1101)
	if err != nil || got == nil || got.Version != 2 {
		t.Fatalf("F2：终态版本应为2（只自增一次），实得 row=%v err=%v", got, err)
	}
}

// TestServiceTerminalReconcileCrossTenant 跨租户安全：同 fiid 不同租户，仅已完成侧收口。
func TestServiceTerminalReconcileCrossTenant(t *testing.T) {
	st, svc := newTestService(t)
	ctx := context.Background()
	// 两租户同 fiid=9001，状态机都停在 end（终态候选）。
	if _, err := svc.Init(ctx, 1, "o1", 9001, "end"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Init(ctx, 2, "o2", 9001, "end"); err != nil {
		t.Fatal(err)
	}
	// 租户1的流程实例已 completed；租户2的仍 running（未完成）。
	if err := st.UpsertFlowInstance(9001, 1, store.SMStatusCompleted); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertFlowInstance(9001, 2, store.SMStatusRunning); err != nil {
		t.Fatal(err)
	}
	a, _ := st.GetFlowStateMachine(1, 9001)
	b, _ := st.GetFlowStateMachine(2, 9001)
	stale := []store.FlowStateMachineRow{*a, *b}

	svc.terminalReconcile(ctx, stale)

	got1, _ := st.GetFlowStateMachine(1, 9001)
	got2, _ := st.GetFlowStateMachine(2, 9001)
	if got1.Status != store.SMStatusCompleted {
		t.Fatalf("租户1已完成流程，状态机应被收口为 completed，实得 %s", got1.Status)
	}
	if got2.Status != store.SMStatusRunning {
		t.Fatalf("跨租户误标：租户2未完成却被收口为 %s", got2.Status)
	}
}

// TestServiceSweepOnceRequeueDisabled 巡检禁用 requeue：仅记录+标记，不发续跑。
func TestServiceSweepOnceRequeueDisabled(t *testing.T) {
	st, svc := newTestService(t)
	ctx := context.Background()
	if _, err := svc.Init(ctx, 1, "o", 5001, "start"); err != nil {
		t.Fatal(err)
	}
	// 负超时 ⇒ 扫描 deadline 推到未来 ⇒ 所有 running 行变 stale（无需改历史心跳）。
	svc.SweepOnce(ctx, -1*time.Hour)
	got, _ := st.GetFlowStateMachine(1, 5001)
	if got == nil {
		t.Fatal("状态机行应存在")
	}
	if got.LastEventID != "sweep_noticed" {
		t.Fatalf("requeue未启用时巡检应标记 sweep_noticed，实得 %q", got.LastEventID)
	}
	if got.Status != store.SMStatusRunning {
		t.Fatalf("requeue未启用时状态机不应被改终态，实得 %s", got.Status)
	}
}

// TestServiceSweepOnceRequeueEnabled 巡检启用 requeue：发 flow_drive 断点续跑，回填数=1。
func TestServiceSweepOnceRequeueEnabled(t *testing.T) {
	_, svc := newTestService(t)
	ctx := context.Background()
	pub := &fakePublisher{}
	svc.pub = pub
	svc.cfg = fakeConfig{requeue: true}
	if _, err := svc.Init(ctx, 1, "o", 6001, "start"); err != nil {
		t.Fatal(err)
	}
	requeued := svc.SweepOnce(ctx, -1*time.Hour)
	if !pub.called {
		t.Fatal("requeue启用时应调用 Publisher 发 flow_drive")
	}
	if requeued != 1 {
		t.Fatalf("requeue启用时本轮回填数应为1，实得 %d", requeued)
	}
	if pub.lastFlowID != "6001" {
		t.Fatalf("发布 payload 的 flow_instance_id 应为 6001，实得 %s", pub.lastFlowID)
	}
	if pub.lastTenant != 1 {
		t.Fatalf("发布 tenant 应为 1，实得 %d", pub.lastTenant)
	}
}
