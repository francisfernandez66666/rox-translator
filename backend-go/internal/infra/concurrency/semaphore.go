// ============ semaphore.go · 职责说明 ============
// concurrency 包并发信号量抽象实现。
// 跨多实例共享的并发上限（大语言模型 chat/embed 调用并发）。
//   - Redis 实现：list 令牌桶（RPUSH 预充令牌 / BLPOP 获取 / LPUSH 释放），天然原子，
//     无需 Lua；容量为「全局上限」，避免 N 实例各持一份导致总并发 = N×上限。
//   - 进程内实现：带缓冲 channel，单实例兼容（无 Redis 时自动降级）。
//   - AcquireEither：交互式请求在「本地保留槽」与「全局槽」间二选一竞争，保证前台不饿死。
// =============================================
package concurrency

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"translator/internal/infra/redis"
)

// Semaphore 并发信号量接口：成功获取返回释放函数；ctx 取消/超时返回 error。
type Semaphore interface {
	Acquire(ctx context.Context) (func(), error)
	TryAcquire() (func(), bool)
}

// New 返回 Redis 实现（rdb 非 nil）或进程内实现（rdb nil / Redis 未启用）。
func New(key string, capacity int, rdb *redis.Client) Semaphore {
	if rdb != nil {
		// 槽 TTL = 看门狗窗口：持有者崩溃后槽位自动回收，避免容量永久泄漏。
		ttl := 10 * time.Minute
		if capacity < 1 {
			capacity = 1
		}
		return &redisSem{rdb: rdb, key: key, cap: capacity, ttl: ttl}
	}
	return newChanSem(capacity)
}

// randToken 生成随机持有者令牌（释放/看门狗校验用）。
func randToken() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "tok"
	}
	return hex.EncodeToString(b)
}

// itoa 整数转字符串（槽位键构造）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := make([]byte, 0, 12)
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}

// ---- 进程内实现 ----

type chanSem struct{ ch chan struct{} }

// newChanSem 基于带缓冲 channel 创建进程内信号量（容量=缓冲区大小）。
// 容量 <1 时按 1 兜底，保证至少允许一个并发。
func newChanSem(capacity int) *chanSem {
	if capacity < 1 {
		capacity = 1
	}
	return &chanSem{ch: make(chan struct{}, capacity)}
}

// Acquire 阻塞获取一个许可；ctx 取消则立即返回错误。
// 返回的释放函数必须被调用一次归还许可。
func (s *chanSem) Acquire(ctx context.Context) (func(), error) {
	select {
	case s.ch <- struct{}{}:
		return func() { <-s.ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TryAcquire 非阻塞尝试获取一个许可：立即成功返回 (释放函数,true)，失败返回 (nil,false)。
func (s *chanSem) TryAcquire() (func(), bool) {
	select {
	case s.ch <- struct{}{}:
		return func() { <-s.ch }, true
	default:
		return nil, false
	}
}

// ---- Redis 信号量实现（per-slot SETNX，原子无预充竞态）----
// 把容量 cap 拆为 cap 个槽位键 key:0..key:cap-1，获取=对某个槽 SETNX 成功，
// 释放=DEL 该槽；槽带 TTL 作看门狗（持有者崩溃后自动回收）。多实例天然共享上限，
// 无「每实例各预充 cap 个令牌导致 2×cap」的竞态。

type redisSem struct {
	rdb  *redis.Client
	key  string
	cap  int
	ttl  time.Duration
}

// slotKey 生成第 i 个槽位的 Redis 键（key:0..key:cap-1）。
func (s *redisSem) slotKey(i int) string {
	return s.key + ":" + itoa(i)
}

// Acquire 阻塞获取一个全局许可（Redis SETNX 抢占任一槽位），ctx 取消或超时返回错误。
// 槽位带 TTL 作看门狗，持有者崩溃后自动回收，不产生永久泄漏。
func (s *redisSem) Acquire(ctx context.Context) (func(), error) {
	token := randToken()
	deadline := time.Now().Add(acquireTimeout(ctx))
	for {
		for i := 0; i < s.cap; i++ {
			if ok, _ := s.rdb.SetNX(ctx, s.slotKey(i), token, s.ttl); ok {
				slot := s.slotKey(i)
				return func() { _ = s.rdb.Del(context.Background(), slot) }, nil
			}
		}
		if time.Now().After(deadline) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, ErrAcquireTimeout
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// TryAcquire 非阻塞尝试获取一个全局许可：任一槽位 SETNX 成功即返回 (释放函数,true)。
func (s *redisSem) TryAcquire() (func(), bool) {
	token := randToken()
	for i := 0; i < s.cap; i++ {
		if ok, _ := s.rdb.SetNX(context.Background(), s.slotKey(i), token, s.ttl); ok {
			slot := s.slotKey(i)
			return func() { _ = s.rdb.Del(context.Background(), slot) }, true
		}
	}
	return nil, false
}

// AcquireEither 在 x / y 两个信号量间竞争获取其一（交互式 QoS：本地保留槽优先但可被全局槽满足）。
func AcquireEither(ctx context.Context, x, y Semaphore) (func(), error) {
	type res struct {
		which int // 0=x, 1=y
		rel   func()
		acq   bool
		err   error
	}
	resCh := make(chan res, 2)
	var xr, yr func()
	var xAcq, yAcq bool
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		r, e := x.Acquire(ctx)
		xAcq = e == nil
		if xAcq {
			xr = r
		}
		wg.Done()
		resCh <- res{0, r, xAcq, e}
	}()
	go func() {
		r, e := y.Acquire(ctx)
		yAcq = e == nil
		if yAcq {
			yr = r
		}
		wg.Done()
		resCh <- res{1, r, yAcq, e}
	}()
	for i := 0; i < 2; i++ {
		o := <-resCh
		if o.acq && o.rel != nil {
			// 胜者已得；落败者若也已获取，稍后释放，避免令牌泄漏
			go func() {
				wg.Wait()
				if o.which == 0 { // x 胜，释放 y（如其已获取）
					if yAcq && yr != nil {
						yr()
					}
				} else { // y 胜，释放 x
					if xAcq && xr != nil {
						xr()
					}
				}
			}()
			return o.rel, nil
		}
		if o.err != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, ctx.Err()
}

// ErrAcquireTimeout 信号量获取超时（容量耗尽）。
var ErrAcquireTimeout = errAcqTimeout{}

type errAcqTimeout struct{}

// Error 实现 error 接口：返回信号量获取超时的提示。
func (errAcqTimeout) Error() string { return "信号量获取超时（容量可能已耗尽）" }

// acquireTimeout 计算信号量获取的等待上限：优先沿用 ctx 截止时间，否则默认 90 秒。
func acquireTimeout(ctx context.Context) time.Duration {
	if dl, ok := ctx.Deadline(); ok {
		return time.Until(dl)
	}
	return 90 * time.Second
}
