// ============ 本文件职责中文说明 ============
// sink_test.go · 实时计量批量缓冲 Flush 回归测试
// 回归锁定（P3 修复）：交互路径响应前 billing.Flush() 应同步把内存缓冲计量落库，
// 消除 2s 周期 flush 造成的余额/台账可见延迟。
// =============================================
package billing

import (
	"path/filepath"
	"testing"
	"time"

	"translator/internal/kb"
	"translator/internal/store"
)

// sinkEnv 建立共享 SQLite 的 Store + Service 测试环境（与生产装配一致）。
func sinkEnv(t *testing.T) (*store.Store, *Service) {
	t.Helper()
	kdb, err := kb.Open(filepath.Join(t.TempDir(), "sink.db"))
	if err != nil {
		t.Fatalf("打开 KB 失败: %v", err)
	}
	t.Cleanup(func() { kdb.Close() })
	st, err := store.New(kdb.RawDB())
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return st, NewService(st)
}

// TestSinkFlushSynchronous P3 核心：Record 后未到周期前 ledger 无行，Flush() 后立即可见。
func TestSinkFlushSynchronous(t *testing.T) {
	st, svc := sinkEnv(t)
	// 绑定全局 sink（此单测使用独立实例而非全局单例，避免影响并行测试）
	s := &UsageSink{
		svc: svc, shadow: map[int64]int64{}, shadowOk: map[int64]bool{},
		flushInterval: 0, maxBatch: 100, wake: make(chan struct{}, 1), stop: make(chan struct{}),
	}
	// 租户 88 先发放余额，避免 balance 校验介入
	if err := st.EnsureBalance(88); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	// 计量入缓冲（模拟一次翻译 100 token）
	s.Record(usageRecord{Tid: 88, UID: 1, TaskType: "translate", Provider: "x", Model: "m",
		Quantity: 100, BizKind: "text", BizMode: "fast"})
	// Flush 前：ledger 不应有行（缓冲未落库）
	if n := countLedger(t, st, 88); n != 0 {
		t.Fatalf("Flush 前不应有 ledger 行, 实得 %d", n)
	}
	// Flush 后：立即有行
	s.Flush()
	if n := countLedger(t, st, 88); n != 1 {
		t.Fatalf("Flush 后应有 1 行 ledger, 实得 %d", n)
	}
	// 空缓冲 Flush 幂等
	s.Flush()
	if n := countLedger(t, st, 88); n != 1 {
		t.Fatalf("空缓冲 Flush 不应重复落行, 实得 %d", n)
	}
}

func countLedger(t *testing.T, st *store.Store, tid int64) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRow("SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=?", tid).Scan(&n); err != nil {
		t.Fatalf("统计 ledger 失败: %v", err)
	}
	return n
}

// TestShadowTTLReseed ★ P1 多实例闭环（2026-09-15）影子 TTL 重播种语义：
// TTL 内信任影子不回读（他实例充值不可见属预期窗口）；TTL 过期强制重 seed，
// 且必须扣除本实例缓冲内未落库量（防止把自家消费看丢导致少扣费）；
// Invalidate 立即清影子（等价收到跨实例广播后的本地动作）。
func TestShadowTTLReseed(t *testing.T) {
	st, svc := sinkEnv(t)
	s := &UsageSink{
		svc: svc, shadow: map[int64]int64{}, shadowOk: map[int64]bool{}, shadowAt: map[int64]time.Time{},
		flushInterval: 0, maxBatch: 100, wake: make(chan struct{}, 1), stop: make(chan struct{}),
	}
	if err := st.EnsureBalance(88); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec("UPDATE balance_accounts SET balance=5000 WHERE tenant_id=88"); err != nil {
		t.Fatal(err)
	}
	// ① 首次 Record：seed 5000，扣 100 → 影子 4900
	s.Record(usageRecord{Tid: 88, UID: 1, TaskType: "translate", Quantity: 100})
	if s.shadow[88] != 4900 {
		t.Fatalf("首 seed 后影子应 4900, 实得 %d", s.shadow[88])
	}
	// ② 模拟他实例充值（直接改 DB 到 9000）：TTL 内仍信任影子
	if _, err := st.DB().Exec("UPDATE balance_accounts SET balance=9000 WHERE tenant_id=88"); err != nil {
		t.Fatal(err)
	}
	s.Record(usageRecord{Tid: 88, UID: 1, TaskType: "translate", Quantity: 50})
	if s.shadow[88] != 4850 {
		t.Fatalf("TTL 内应不回读（影子 4850）, 实得 %d", s.shadow[88])
	}
	// ③ 把 seed 时间拨回 TTL 之外 → 下一条强制重 seed：
	//    新影子 = 9000(真实) - 150(本实例缓冲 100+50 未落库) - 10(本条) = 8840
	s.shadowAt[88] = time.Now().Add(-shadowReseedTTL - time.Second)
	s.Record(usageRecord{Tid: 88, UID: 1, TaskType: "translate", Quantity: 10})
	if s.shadow[88] != 8840 {
		t.Fatalf("TTL 过期应重 seed 并扣缓冲量（8840）, 实得 %d", s.shadow[88])
	}
	// ④ Invalidate（等价收到 Redis 失效广播）：影子标记清除，下一条立即回读
	s.Invalidate(88)
	if s.shadowOk[88] {
		t.Fatal("Invalidate 后 shadowOk 应清除")
	}
	s.Record(usageRecord{Tid: 88, UID: 1, TaskType: "translate", Quantity: 5})
	// 缓冲累计 165 未落库：9000-160-5=8835（①②③ 的量都在缓冲里）
	if s.shadow[88] != 8835 {
		t.Fatalf("Invalidate 后回读应 8835, 实得 %d", s.shadow[88])
	}
}

// TestInvalidateShadowNoRedis 未启用 Redis（测试进程单例为 nil）时，
// 进程级 InvalidateShadow 必须安全（只清本进程影子，广播静默跳过，不 panic）。
func TestInvalidateShadowNoRedis(t *testing.T) {
	_, _ = sinkEnv(t)
	InvalidateShadow(777) // 不应 panic；无缓冲租户也应幂等
}
