// ============ daily.go · 职责说明 ============
// ratelimit 包速率/配额计数抽象实现。
// API Key 日配额等需跨多实例聚合的场景。
//   - Redis 实现：INCR 原子自增 + EXPIRE 至次日零点，天然跨实例聚合；
//   - 返回 nil 表示未启用 Redis——调用方降级为既有 SQLite 字段逻辑（PG 下 UPDATE 亦原子）。
// =============================================
package ratelimit

import (
	"context"
	"time"

	"translator/internal/infra/redis"
)

// Counter 每日计数接口。
type Counter interface {
	Incr(key string) (int64, error) // 自增并返回新值
	Get(key string) (int64, error)  // 读取当前值
}

// Daily 返回 Redis 每日计数器（Redis 启用时）；否则返回 nil（调用方降级 SQLite）。
func Daily() Counter {
	if c := redis.Get(); c != nil {
		return &redisCounter{rdb: c}
	}
	return nil
}

type redisCounter struct{ rdb *redis.Client }

func (r *redisCounter) Incr(key string) (int64, error) {
	n, err := r.rdb.Incr(context.Background(), key)
	if err != nil {
		return 0, err
	}
	// 仅在首次创建时设过期（INCR 不覆盖已有 TTL；此处每次补过期以兜底内存）
	_ = r.rdb.Expire(context.Background(), key, untilNextMidnight())
	return n, nil
}

func (r *redisCounter) Get(key string) (int64, error) {
	return r.rdb.GetInt(context.Background(), key)
}

// untilNextMidnight 距本地次日零点的时长（含 1 分钟缓冲）。
func untilNextMidnight() time.Duration {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).AddDate(0, 0, 1)
	return time.Until(next) + time.Minute
}

// KeyForAKQuota 构造 API Key 日配额键。
func KeyForAKQuota(id int64, date string) string {
	return "ak:quota:" + itoa(id) + ":" + date
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 20)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
