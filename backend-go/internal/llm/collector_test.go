// ============ 本文件职责中文说明 ============
// llm.UsageCollector 单元测试：并发累加安全、nil 接收者容错、ctx 注入读取。
package llm

import (
	"context"
	"sync"
	"testing"
)

// TestUsageCollectorConcurrent 多 goroutine 并发累加不丢计数。
func TestUsageCollectorConcurrent(t *testing.T) {
	uc := &UsageCollector{}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			uc.Add(10, 5)
		}()
	}
	wg.Wait()
	p, c := uc.Totals()
	if p != 500 || c != 250 {
		t.Fatalf("并发累加结果错误: prompt=%d completion=%d，期望 500/250", p, c)
	}
	if uc.Total() != 750 {
		t.Fatalf("Total 应为 750，实得 %d", uc.Total())
	}
}

// TestUsageCollectorNilSafe nil 收集器与未注入 ctx 均安全。
func TestUsageCollectorNilSafe(t *testing.T) {
	var uc *UsageCollector
	uc.Add(1, 1) // 不应 panic
	if p, c := uc.Totals(); p != 0 || c != 0 {
		t.Fatalf("nil 收集器 Totals 应返回 0")
	}
	if got := CollectorFrom(context.Background()); got != nil {
		t.Fatalf("未注入 ctx 应返回 nil 收集器")
	}
}

// TestWithUsageCollector ctx 注入后可取出同一实例（跨层归集的前提）。
func TestWithUsageCollector(t *testing.T) {
	uc := &UsageCollector{}
	ctx := WithUsageCollector(context.Background(), uc)
	got := CollectorFrom(ctx)
	if got != uc {
		t.Fatalf("CollectorFrom 应返回注入的同一实例")
	}
}
