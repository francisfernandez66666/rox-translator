// ============ 本文件职责中文说明 ============
// sink_test.go · 实时计量批量缓冲 Flush 回归测试
// 回归锁定（P3 修复）：交互路径响应前 billing.Flush() 应同步把内存缓冲计量落库，
// 消除 2s 周期 flush 造成的余额/台账可见延迟。
// =============================================
package billing

import (
	"path/filepath"
	"testing"

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
