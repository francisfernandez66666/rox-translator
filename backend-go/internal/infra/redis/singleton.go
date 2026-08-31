// ============ singleton.go · 职责说明 ============
// redis 包单例管理。
// 进程内唯一客户端，由配置的 REDIS_ADDR 初始化。
// 返回 nil 表示未启用 Redis——各上层组件（distlock/ratelimit/concurrency）据此降级为
// 进程内实现，保证「无 Redis 也能跑单实例」，延续系统单二进制零依赖哲学。
// =============================================
package redis

import (
	"context"
	"sync"
)

var (
	mu       sync.RWMutex
	instance *Client
)

// Init 依据地址/密码初始化单例；addr 为空则置 nil（降级进程内）。
func Init(addr, password string) {
	mu.Lock()
	defer mu.Unlock()
	if addr == "" {
		instance = nil
		return
	}
	instance = New(addr, password)
}

// Get 返回当前单例（可能为 nil）。
func Get() *Client {
	mu.RLock()
	defer mu.RUnlock()
	return instance
}

// Enabled 是否已启用 Redis（单例非 nil）。
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return instance != nil
}

// Ping 探活单例（未启用返回 error）。
func Ping() error {
	mu.RLock()
	c := instance
	mu.RUnlock()
	if c == nil {
		return ErrDisabled
	}
	return c.Ping(context.Background())
}

// ErrDisabled 表示未启用 Redis（降级路径）。
var ErrDisabled = errDisabled{}

type errDisabled struct{}

// Error 实现 error 接口：返回未启用 Redis 的降级说明。
func (errDisabled) Error() string { return "redis 未启用（降级进程内实现）" }
