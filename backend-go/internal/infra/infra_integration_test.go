// ============ 阶段二 联调测试 ============
// 启动本地 redis-server（若 PATH 中存在），验证 Redis 后端的三类分布式原语：
//   - concurrency.Semaphore（list 令牌桶）：全局上限跨「多实例」生效；
//   - ratelimit.Daily：原子日计数跨实例聚合；
//   - distlock.Lock：同一时刻仅一个持有者。
// 另验证未启用 Redis 时自动降级进程内实现（单实例兼容）。
// 运行：go test ./internal/infra/ -run TestRedisBackedInfra -v
// =============================================
package infra_test

import (
	"context"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/infra/concurrency"
	"translator/internal/infra/distlock"
	"translator/internal/infra/ratelimit"
	"translator/internal/infra/redis"
)

// startRedis 若在 PATH 中找到 redis-server 则拉起一个临时实例，返回 addr 与清理函数。
func startRedis(t *testing.T) (string, func()) {
	t.Helper()
	bin, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server 不在 PATH，跳过联调测试")
	}
	port := 16399
	addr := "127.0.0.1:" + strconv.Itoa(port)
	cmd := exec.Command(bin, "--port", strconv.Itoa(port), "--save", "", "--appendonly", "no", "--bind", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Skip("启动 redis-server 失败，跳过联调测试: " + err.Error())
	}
	// 等待就绪
	ready := false
	for i := 0; i < 50; i++ {
		c := redis.New(addr, "")
		if c != nil && c.Ping(context.Background()) == nil {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		_ = cmd.Process.Kill()
		t.Skip("redis-server 未就绪，跳过联调测试")
	}
	return addr, func() { _ = cmd.Process.Kill() }
}

// TestRedisBackedInfra Redis 后端联调测试：起临时 redis-server 验证
// 信号量全局上限、每日计数器、分布式锁三者在多「实例」下共享生效。
func TestRedisBackedInfra(t *testing.T) {
	addr, cleanup := startRedis(t)
	defer cleanup()

	redis.Init(addr, "")
	if !redis.Enabled() {
		t.Fatal("Redis 应已启用")
	}

	// ---- 1. 信号量：全局上限跨「多实例」生效 ----
	t.Run("semaphore_global_cap", func(t *testing.T) {
		key := "sem:test:cap"
		// 两个独立「实例」共享同一 Redis key，容量 2
		semA := concurrency.New(key, 2, redis.Get())
		semB := concurrency.New(key, 2, redis.Get())
		var concurrent int64
		var maxConcurrent int64
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem := semA
				if i%2 == 1 {
					sem = semB
				}
				rel, err := sem.Acquire(context.Background())
				if err != nil {
					t.Errorf("acquire 失败: %v", err)
					return
				}
				cur := atomic.AddInt64(&concurrent, 1)
				for {
					old := atomic.LoadInt64(&maxConcurrent)
					if cur <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, cur) {
						break
					}
				}
				time.Sleep(30 * time.Millisecond)
				atomic.AddInt64(&concurrent, -1)
				rel()
			}()
		}
		wg.Wait()
		if maxConcurrent > 2 {
			t.Fatalf("并发超过全局上限 2：实得 %d", maxConcurrent)
		}
		t.Logf("信号量全局上限校验通过，峰值并发=%d", maxConcurrent)
	})

	// ---- 2. 日配额计数：跨实例原子聚合 ----
	t.Run("daily_counter", func(t *testing.T) {
		c := ratelimit.Daily()
		if c == nil {
			t.Fatal("Daily 计数器应为 Redis 实现")
		}
		key := ratelimit.KeyForAKQuota(999, "2099-01-01")
		// 模拟两个实例各 +5
		for i := 0; i < 5; i++ {
			if _, err := c.Incr(key); err != nil {
				t.Fatalf("incr A: %v", err)
			}
			if _, err := c.Incr(key); err != nil {
				t.Fatalf("incr B: %v", err)
			}
		}
		n, err := c.Get(key)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if n != 10 {
			t.Fatalf("跨实例计数应为 10，实得 %d", n)
		}
		t.Logf("日配额跨实例聚合通过，计数=%d", n)
	})

	// ---- 3. 分布式锁：同时仅一个持有者 ----
	t.Run("distlock_mutual", func(t *testing.T) {
		lk := distlock.New("lock:test", redis.Get())
		got1, rel1, err := lk.TryLock(context.Background(), 5*time.Second)
		if err != nil || !got1 {
			t.Fatalf("首个锁应获取成功: got=%v err=%v", got1, err)
		}
		defer rel1()
		got2, _, err := lk.TryLock(context.Background(), 5*time.Second)
		if err != nil {
			t.Fatalf("第二个锁查询不应报错: %v", err)
		}
		if got2 {
			t.Fatal("第二个锁不应获取成功（应互斥）")
		}
		t.Log("分布式锁互斥校验通过")
	})

	// ---- 4. 队列唤醒信号器：Signal 后 Wait 可即时返回（多 AZ worker 唤醒）----
	t.Run("queue_notifier_wakeup", func(t *testing.T) {
		n := redis.NewQueueNotifier(redis.Get())
		// 无信号时 Wait 应在超时后返回（不阻塞）
		start := time.Now()
		n.Wait(context.Background(), 200*time.Millisecond)
		if time.Since(start) < 150*time.Millisecond {
			t.Fatalf("无信号时 Wait 应约在超时返回，实得 %v", time.Since(start))
		}
		// 发信号后 Wait 应立即返回
		if err := n.Signal(context.Background()); err != nil {
			t.Fatalf("signal: %v", err)
		}
		start = time.Now()
		n.Wait(context.Background(), 2*time.Second)
		if time.Since(start) > 1*time.Second {
			t.Fatalf("有信号时 Wait 应立即返回，实得 %v", time.Since(start))
		}
		t.Log("队列唤醒信号器校验通过")
	})
}

// TestFallbackInProcess 验证未启用 Redis 时降级进程内实现（单实例行为不变）。
func TestFallbackInProcess(t *testing.T) {
	redis.Init("", "") // 禁用
	if redis.Enabled() {
		t.Fatal("禁用后 Enabled 应为 false")
	}
	// 信号量进程内：容量 1，两并发仅一个在途
	sem := concurrency.New("sem:noop", 1, nil)
	var conc int64
	var max int64
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rel, err := sem.Acquire(context.Background())
			if err != nil {
				t.Errorf("acquire: %v", err)
				return
			}
			cur := atomic.AddInt64(&conc, 1)
			for {
				o := atomic.LoadInt64(&max)
				if cur <= o || atomic.CompareAndSwapInt64(&max, o, cur) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			atomic.AddInt64(&conc, -1)
			rel()
		}()
	}
	wg.Wait()
	if max > 1 {
		t.Fatalf("进程内信号量容量应为 1，实得 %d", max)
	}
	// 日配额降级为 nil（调用方走 SQLite 原逻辑，此处仅确认不panic）
	if ratelimit.Daily() != nil {
		t.Fatal("禁用 Redis 时 Daily 应返回 nil")
	}
	t.Log("进程内降级校验通过")
}
