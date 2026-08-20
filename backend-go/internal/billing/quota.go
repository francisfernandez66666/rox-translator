// Package billing 提供租户配额限流（QPS / 并发 / 每日 token / 余额停服）与用量计量。
package billing

// ============ 本文件职责中文说明 ============
// 计费与配额：租户维度的内存实时限流（1 秒 QPS 滑动窗口、并发名额计数），
// 强制计费开关（billing_enforced=1 时扣余额、余额不足停服，否则仅 usage_ledger 留痕）、
// 用量计量（Meter，含供应商/模型维度的成本核算）、每日 token 上限与余额充足性检查。
// ========================================

import (
	"sync"
	"time"

	"translator/internal/store"
)

// Quota 租户配额（内存实时窗口）
type Quota struct {
	mu sync.Mutex // 保护配额字段的互斥锁

	// QPS 滑动窗口（1 秒）
	qpsWindow []time.Time // 近 1 秒内的时间戳窗口（用于 QPS 计数）
	qpsMax    int         // QPS 上限（默认 10）
	// 并发计数
	concurrent    int // 当前并发调用数
	concurrentMax int // 并发上限（默认 3）
}

// quotaByTenant 租户 ID → 配额对象（内存缓存）
var quotaByTenant = map[int64]*Quota{}
var quotaMu sync.Mutex // 保护 quotaByTenant 的互斥锁

// getQuota 获取指定租户的配额对象（不存在则用默认上限创建）
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

// setQPS 设置租户 QPS 上限（加锁写，供 SetQPS 调用）。
// 参数 v: 目标 QPS 值；无返回。
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

// setConcurrent 设置租户并发上限（加锁写，供 SetConcurrent 调用）。
// 参数 v: 目标并发值；无返回。
func (q *Quota) setConcurrent(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.concurrentMax = v
}

// QPS 返回租户 QPS 上限（admin 读取用）
func (s *Service) QPS(tid int64) int {
	q := getQuota(tid)
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.qpsMax
}

// Concurrent 返回租户并发上限（admin 读取用）
func (s *Service) Concurrent(tid int64) int {
	q := getQuota(tid)
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.concurrentMax
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
	Store *store.Store // 持久化存储（读余额/记录用量/配置开关）
}

// NewService 创建计费服务
func NewService(st *store.Store) *Service {
	return &Service{Store: st}
}

// Enabled 是否强制计费（扣余额 + 余额不足停服）。默认关闭，仅留痕计量。
// 由系统配置 billing_enforced=1 开启（用于 SaaS 销售模式）。
func (s *Service) Enabled() bool {
	if s.Store == nil {
		return false
	}
	v, _ := s.Store.GetConfig("billing_enforced")
	return v == "1"
}

// Meter 计量一次用量。强制计费时扣余额；否则仅记录 usage_ledger 留痕。
// 返回 error（强制计费且余额不足时返回）。provider/model 用于多供应商成本核算。
func (s *Service) Meter(tid, userID int64, taskType, provider, model string, quantity int64) error {
	if s.Store == nil || quantity <= 0 {
		return nil
	}
	if s.Enabled() {
		_, err := s.Store.RecordUsage(tid, userID, taskType, provider, model, quantity)
		return err
	}
	return s.Store.LogUsage(tid, userID, taskType, provider, model, quantity)
}

// MeterDeferred 计量失败不阻断业务（记录后返回错误供日志，但调用方按需忽略）。
func (s *Service) MeterDeferred(tid, userID int64, taskType, provider, model string, quantity int64) error {
	return s.Meter(tid, userID, taskType, provider, model, quantity)
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

type quotaErr struct{ s string } // 配额类错误（含今日用量超限/余额不足）

// Error 实现 error 接口：返回配额错误描述信息。
func (e *quotaErr) Error() string { return e.s }
