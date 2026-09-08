// ============ 本文件职责中文说明 ============
// billing 包配额限流回归测试（2026-09-09 技术债①横向扩容改造）：
// 覆盖无 Redis（本地兜底）下 QPS 秒窗与并发信号量的上限语义——
// 改造后并发计数改用 concurrency.Semaphore（Redis 槽位/SETNX 或本地 channel），
// QPS 走 Redis 秒窗或本地滑动窗口；本套用例在无 Redis 环境中锁定其行为不回归。
// ========================================
package billing

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestQuotaConcurrentLocal 无 Redis：本地并发信号量按上限拒绝超额、释放后恢复。
func TestQuotaConcurrentLocal(t *testing.T) {
	Service := NewService(nil)
	// 上限 3
	SetConcurrent(70001, 3)
	defer cleanupQuota(70001)
	// 取满 3 个并保持
	rels := make([]func(), 0, 3)
	for i := 0; i < 3; i++ {
		ok, rel := Service.TryAcquire(70001)
		if !ok {
			t.Fatalf("第 %d 次应能取得并发名额（上限3）", i+1)
		}
		rels = append(rels, rel)
	}
	// 第 4 次应拒绝
	if ok, _ := Service.TryAcquire(70001); ok {
		t.Fatal("并发超上限应拒绝")
	}
	// 释放 1 个后可再次取得
	rels[0]()
	defer func() {
		for i := 1; i < 3; i++ {
			rels[i]()
		}
	}()
	if ok, rel := Service.TryAcquire(70001); !ok {
		t.Fatal("释放后应可重新取得并发名额")
	} else {
		rel()
	}
}

// TestQuotaQPSLocal 无 Redis：QPS 秒窗按上限拒绝第 max+1 个、窗口滚动后放行。
func TestQuotaQPSLocal(t *testing.T) {
	s := NewService(nil)
	SetQPS(70002, 3)
	defer cleanupQuota(70002)
	// 前 3 个放行
	for i := 0; i < 3; i++ {
		if !s.TryQPS(70002) {
			t.Fatalf("第 %d 次 QPS 应放行（上限3）", i+1)
		}
	}
	if s.TryQPS(70002) {
		t.Fatal("QPS 超上限应拒绝")
	}
	// 等待窗口滚动（>1s），恢复放行
	time.Sleep(1100 * time.Millisecond)
	if !s.TryQPS(70002) {
		t.Fatal("窗口滚动后应放行")
	}
}

// TestQuotaConcurrentCrossTenant 不同租户并发计数相互隔离。
func TestQuotaConcurrentCrossTenant(t *testing.T) {
	s := NewService(nil)
	SetConcurrent(71001, 1)
	SetConcurrent(71002, 1)
	defer cleanupQuota(71001)
	defer cleanupQuota(71002)
	okA1, relA := s.TryAcquire(71001)
	if !okA1 {
		t.Fatal("A 租户首个名额应取得")
	}
	okB1, relB := s.TryAcquire(71002)
	if !okB1 {
		t.Fatal("B 租户应独立取得名额（跨租户隔离）")
	}
	defer relA()
	defer relB()
	if ok, _ := s.TryAcquire(71001); ok {
		t.Fatal("A 租户已占满，应拒绝")
	}
	if ok, _ := s.TryAcquire(71002); ok {
		t.Fatal("B 租户已占满，应拒绝")
	}
}

// TestQuotaConcurrentConcurrentSafety 并发取放无数据竞争（-race 下验证）。
func TestQuotaConcurrentConcurrentSafety(t *testing.T) {
	s := NewService(nil)
	SetConcurrent(72001, 5)
	defer cleanupQuota(72001)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				if ok, rel := s.TryAcquire(72001); ok {
					rel()
				}
			}
		}()
	}
	wg.Wait()
}

// cleanupQuota 清掉测试租户的配额缓存，避免污染后续用例。
func cleanupQuota(tid int64) {
	quotaMu.Lock()
	delete(quotaByTenant, tid)
	quotaMu.Unlock()
	_ = fmt.Sprintf
}
