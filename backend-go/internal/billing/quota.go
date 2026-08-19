// Package billing 提供租户配额限流（QPS / 并发 / 每日 token / 余额停服）与用量计量。
package billing

import (
	"sync"
	"time"

	"translator/internal/store"
)

// Quota 租户配额（内存实时窗口）
type Quota struct {
	mu sync.Mutex

	// QPS 滑动窗口（1 秒）
	qpsWindow   []time.Time
	qpsMax      int
	// 并发计数
	concurrent  int
	concurrentMax int
}

var quotaByTenant = map[int64]*Quota{}
var quotaMu sync.Mutex

func getQuota(tid int64) *Quota {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	q, ok := quotaByTenant[tid]
	if !ok {
		q = &Quota{qpsMax: 10, concurrentMax: 3}
		quotaByTenant[tid] = q
	}
	return q
}

// SetQPS 设置租户 QPS 上限（admin 配置，持久化在 system_config）
func SetQPS(tid int64, qps int) {
	if qps <= 0 {
		qps = 10
	}
	getQuota(tid).setQPS(qps)
}
func (q *Quota) setQPS(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.qpsMax = v
}

// SetConcurrent 设置租户并发上限
func SetConcurrent(tid int64, n int) {
	if n <= 0 {
		n = 3
	}
	getQuota(tid).setConcurrent(n)
}
func (q *Quota) setConcurrent(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.concurrentMax = v
}

// TryAcquire 尝试获取并发名额；返回是否允许继续
func (s *Service) TryAcquire(tid int64) bool {
	q := getQuota(tid)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.concurrent >= q.concurrentMax {
		return false
	}
	q.concurrent++
	return true
}

// Release 释放并发名额
func (s *Service) Release(tid int64) {
	q := getQuota(tid)
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.concurrent > 0 {
		q.concurrent--
	}
}

// TryQPS 检查 QPS 窗口
func (s *Service) TryQPS(tid int64) bool {
	q := getQuota(tid)
	q.mu.Lock()
	defer q.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Second)
	kept := q.qpsWindow[:0]
	for _, t := range q.qpsWindow {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	q.qpsWindow = kept
	if len(q.qpsWindow) >= q.qpsMax {
		return false
	}
	q.qpsWindow = append(q.qpsWindow, now)
	return true
}

// Service 计费服务
type Service struct {
	Store *store.Store
}

// NewService 创建计费服务
func NewService(st *store.Store) *Service {
	return &Service{Store: st}
}

// CheckDailyQuota 检查每日 token 上限（来自租户 permissions.max_daily_chars）
func (s *Service) CheckDailyQuota(tid int64, maxDaily int64) error {
	if maxDaily <= 0 {
		return nil
	}
	used, err := s.Store.DailyUsage(tid)
	if err != nil {
		return nil
	}
	if used >= maxDaily {
		return &quotaErr{"已达到今日用量上限"}
	}
	return nil
}

// CheckBalance 检查余额是否充足
func (s *Service) CheckBalance(tid int64) error {
	b, err := s.Store.GetBalance(tid)
	if err != nil {
		return err
	}
	if b.Balance <= 0 {
		return &quotaErr{"余额不足，请充值"}
	}
	return nil
}

type quotaErr struct{ s string }

func (e *quotaErr) Error() string { return e.s }