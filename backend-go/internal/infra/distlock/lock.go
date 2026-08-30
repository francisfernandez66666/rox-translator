// ============ lock.go · 职责说明 ============
// distlock 包分布式锁抽象实现。
// 多实例下保证某段逻辑（如工单卡死巡检重排）同一时刻仅一个实例执行。
//   - Redis 实现：SET NX PX 获取 + 看门狗续期 + 释放时校验持有者 token（防误删他人锁）；
//   - 进程内实现：sync.Mutex，单实例兼容。
//
// 锁为「尽力而为」：获取失败仅意味着其他实例正在执行，调用方应直接跳过（非阻塞）。
// =============================================
package distlock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"translator/internal/infra/redis"
)

// Lock 分布式锁接口。
type Lock interface {
	// TryLock 非阻塞获取；成功返回 true 与释放函数，失败返回 false。
	TryLock(ctx context.Context, ttl time.Duration) (bool, func(), error)
}

// New 返回 Redis 锁（rdb 非 nil）或进程内锁。
func New(key string, rdb *redis.Client) Lock {
	if rdb != nil {
		return &redisLock{rdb: rdb, key: key}
	}
	return &localLock{mu: &sync.Mutex{}}
}

// ---- 进程内实现 ----

type localLock struct{ mu *sync.Mutex }

func (l *localLock) TryLock(ctx context.Context, ttl time.Duration) (bool, func(), error) {
	if !l.mu.TryLock() {
		return false, nil, nil
	}
	return true, func() { l.mu.Unlock() }, nil
}

// ---- Redis 实现 ----

type redisLock struct {
	rdb *redis.Client
	key string
}

func (l *redisLock) TryLock(ctx context.Context, ttl time.Duration) (bool, func(), error) {
	token := randToken()
	ok, err := l.rdb.SetNX(ctx, l.key, token, ttl)
	if err != nil || !ok {
		return false, nil, err
	}
	// 看门狗：ttl 过半续期，避免长任务持锁过期被他人抢占
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(ttl / 2)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// 仅当仍持有（值未变）才续期
				if v, _ := l.rdb.Get(context.Background(), l.key); v == token {
					_ = l.rdb.Expire(context.Background(), l.key, ttl)
				}
			}
		}
	}()
	release := func() {
		close(stop)
		// 校验持有者后删除（非原子，但看门狗保证过期前不会易主）
		if v, _ := l.rdb.Get(context.Background(), l.key); v == token {
			_ = l.rdb.Del(context.Background(), l.key)
		}
	}
	return true, release, nil
}

func randToken() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "lock"
	}
	return hex.EncodeToString(b)
}
