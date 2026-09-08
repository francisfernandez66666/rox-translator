// ============ quota.go · 职责说明 ============
// billing 包内部实现文件。
// =============================================
// Package billing 提供租户配额限流（QPS / 并发 / 每日 token / 余额停服）与用量计量。
package billing

// ============ 本文件职责中文说明 ============
// 计费与配额：租户维度的内存实时限流（1 秒 QPS 滑动窗口、并发名额计数），
// 强制计费开关（billing_enforced=1 时扣余额、余额不足停服，否则仅 usage_ledger 留痕）、
// 用量计量（Meter，含供应商/模型维度的成本核算）、每日 token 上限与余额充足性检查。
// ========================================

import (
	"context"
	"strconv"
	"sync"
	"time"

	"translator/internal/infra/concurrency"
	"translator/internal/infra/redis"
	"translator/internal/store"
)

// Quota 租户配额（内存实时窗口 + Redis 全局聚合，2026-09-09 技术债①横向扩容改造）
// 并发计数：concurrency.Semaphore（Redis 槽位/SETNX 跨实例共享；未启用 Redis 降级本地 channel）。
// QPS 窗口：Redis 秒窗 INCR+EXPIRE（跨实例合计）；未启用 Redis 降级本地滑动窗口。
// 上限配置（qpsMax/concurrentMax）内存驻留，由 admin 调用持久化到 system_config 后回放。
type Quota struct {
	id int64      // 租户 ID（并发信号量键与回收时定位用）
	mu sync.Mutex // 保护配额字段的互斥锁

	// QPS 滑动窗口（1 秒，本地兜底路径使用）
	qpsWindow []time.Time // 近 1 秒内的时间戳窗口（用于 QPS 计数）
	qpsMax    int         // QPS 上限（默认 10）
	// 并发信号量（Redis 或本地；并发上限变化时由 SetConcurrent 重建）
	sem           concurrency.Semaphore
	concurrentMax int // 并发上限（默认 3）
}

// quotaByTenant 租户 ID → 配额对象（内存缓存）；quotaMu 保护 map 访问。
var (
	quotaByTenant = map[int64]*Quota{}
	quotaMu       sync.Mutex
)

// getQuota 获取指定租户的配额对象（不存在则用默认上限创建）。
func getQuota(tid int64) *Quota {
	quotaMu.Lock()
	defer quotaMu.Unlock()
	q, ok := quotaByTenant[tid]
	if !ok {
		q = &Quota{id: tid, qpsMax: 10, concurrentMax: 3}
		q.sem = concurrency.New("quota:conc:"+itoa64(tid), q.concurrentMax, redis.Get())
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
func (q *Quota) setQPS(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.qpsMax = v
}

// SetConcurrent 设置租户并发上限（重建信号量以套用新容量，Redis/本地一致）
func SetConcurrent(tid int64, n int) {
	if n <= 0 {
		n = 3
	}
	getQuota(tid).setConcurrent(n)
}

// setConcurrent 设置租户并发上限（加锁写，供 SetConcurrent 调用）。
func (q *Quota) setConcurrent(v int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.concurrentMax = v
	q.sem = concurrency.New("quota:conc:"+itoa64(q.id), v, redis.Get())
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

// TryAcquire 尝试获取并发名额；返回 (是否允许, 释放函数)。
// 释放函数必须被调用（往往 defer）；成功返回 (true, rel)，失败返回 (false, nil)。
// 并发计数跨实例共享（Redis 信号量），多实例合计不超过上限。
func (s *Service) TryAcquire(tid int64) (bool, func()) {
	q := getQuota(tid)
	q.mu.Lock()
	rel, ok := q.sem.TryAcquire()
	q.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, rel
}

// Release 释放并发名额（兼容旧签名：内部语义已由 TryAcquire 返回的闭包承担，
// 保留此方法供历史调用方在获取失败时的安全空操作）。
func (s *Service) Release(tid int64) {
	// 并发名额的释放由 TryAcquire 返回的闭包负责；此方法保留仅为向后兼容（空操作）。
}

// TryQPS 检查 QPS 窗口（Redis 秒窗跨实例合计；未启用 Redis 降级本地滑动窗口）。
func (s *Service) TryQPS(tid int64) bool {
	q := getQuota(tid)
	q.mu.Lock()
	max := q.qpsMax
	q.mu.Unlock()
	if max <= 0 {
		max = 10
	}
	// Redis 已启用：跨实例秒窗计数（INCR + 首次 EXPIRE 2s 防键泄漏）
	if rdb := redis.Get(); rdb != nil {
		epoch := time.Now().Unix()
		key := "quota:qps:" + itoa64(tid) + ":" + itoa64(epoch)
		n, err := rdb.Incr(context.Background(), key)
		if err != nil {
			return q.tryQPSSlow(max) // Redis 短暂故障 → 降级本地窗口（尽力而为）
		}
		if n == 1 {
			_ = rdb.Expire(context.Background(), key, 2*time.Second)
		}
		return n <= int64(max)
	}
	return q.tryQPSSlow(max)
}

// tryQPSSlow 本地滑动窗口 QPS 判定（单实例兜底；Redis 未启用或故障时使用）。
func (q *Quota) tryQPSSlow(max int) bool {
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
	if len(q.qpsWindow) >= max {
		return false
	}
	q.qpsWindow = append(q.qpsWindow, now)
	return true
}

// itoa64 int64→string 键缀（复用 strconv，避免重复实现）。
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// Service 计费服务
type Service struct {
	Store *store.Store // 持久化存储（读余额/记录用量/配置开关）
}

// NewService 创建计费服务
func NewService(st *store.Store) *Service {
	return &Service{Store: st}
}

// Enabled 是否强制计费（扣余额 + 余额不足停服）。唯一开关：billing_enforced=1。
// 注意：token 迁移标记（billing_token_migrated）仅用于迁移幂等，
// 不得参与本判断——否则后台关闭强制计费后将不生效。
// 迁移时的开关接续见 migrate.go：老句数强制开启会一次性搬运到 billing_enforced。
func (s *Service) Enabled() bool {
	if s.Store == nil {
		return false
	}
	v, _ := s.Store.GetConfig("billing_enforced")
	return v == "1"
}

// Meter 计量一次用量。强制计费时扣余额；否则仅记录 usage_ledger 留痕。
// 返回 error（强制计费且余额不足时返回）。provider/model 用于多供应商成本核算；
// bizKind=text|file、bizMode=fast|pro 用于用量看板标注（2026-08-26 需求）。
func (s *Service) Meter(tid, userID int64, taskType, provider, model string, quantity int64, bizKind, bizMode string) error {
	if s.Store == nil || quantity <= 0 {
		return nil
	}
	if s.Enabled() {
		_, err := s.Store.RecordUsage(tid, userID, taskType, provider, model, quantity, bizKind, bizMode)
		return err
	}
	return s.Store.LogUsage(tid, userID, taskType, provider, model, quantity, bizKind, bizMode)
}

// MeterDeferred 计量失败不阻断业务（记录后返回错误供日志，但调用方按需忽略）。
func (s *Service) MeterDeferred(tid, userID int64, taskType, provider, model string, quantity int64, bizKind, bizMode string) error {
	return s.Meter(tid, userID, taskType, provider, model, quantity, bizKind, bizMode)
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

// CheckBalance 检查余额是否充足。
// ★ 双桶口径（2026-08-26 评审整改 A1）：可用额度 = 未过期台账 + 永久余额——
// 只看永久桶会把「仅有体验台账的新租户」误判为耗尽（fail-closed 误伤），
// 与实扣入口 DeductWithGrants（台账→永久顺序扣减）口径保持一致。
func (s *Service) CheckBalance(tid int64) error {
	grants, permanent, err := s.Store.TenantRemainTotal(tid)
	if err != nil {
		return err
	}
	if grants+permanent <= 0 {
		return &quotaErr{"额度不足，请充值"}
	}
	return nil
}

// quotaErr 配额类错误（含今日用量超限/余额不足）。s: 面向用户的中文错误描述。
type quotaErr struct{ s string }

// Error 实现 error 接口：返回配额错误描述信息。
func (e *quotaErr) Error() string { return e.s }
