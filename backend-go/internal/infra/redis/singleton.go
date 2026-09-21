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

// Redis 客户端单例：双检锁惰性建连，全进程共享同一连接池。
var (
	mu       sync.RWMutex
	instance *Client
	// #40（2026-09-21）：启动期判定出的「分布式能力」结论，供 /api/health 暴露。
	// 只在 main 启动闸门写一次，读多写少，复用上面同一把锁。
	availability = "unknown"
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

// SetAvailability 记录启动闸门判定的分布式能力结论（redis / in-process / unreachable）。
// 由 cmd/server 启动期调用一次；运行期只读，避免每次健康检查都去打 TCP 探活。
func SetAvailability(v string) {
	mu.Lock()
	defer mu.Unlock()
	availability = v
}

// Availability 返回启动期判定的分布式能力结论。
// 取值：redis（跨实例聚合可用）/ unreachable（配了地址但探活失败，各组件按命令失败逐次降级）/
// in-process（未配置地址，进程内实现，仅单副本安全）。
func Availability() string {
	mu.RLock()
	defer mu.RUnlock()
	return availability
}

// errDisabled 是 ErrDisabled 哨兵错误的具体类型（Redis 未启用时返回）。
type errDisabled struct{}

// Error 实现 error 接口：返回未启用 Redis 的降级说明。
func (errDisabled) Error() string { return "redis 未启用（降级进程内实现）" }
