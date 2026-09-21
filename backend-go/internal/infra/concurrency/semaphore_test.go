// ============ semaphore_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-6）：internal/infra/concurrency 的回归断言。
// 本包是 LLM 并发闸的唯一实现（Redis 全局槽 + 进程内 channel 降级 + AcquireEither 前台不饿死），
// 230 行此前**零测试**——「容量兜底为 1」「ctx 取消立即返回」「释放归还许可」这类分支
// 一旦被改坏，表现是全站并发上限失控（打爆供应商 QPS）或请求永久挂起。
// 覆盖：
//
//	① 进程内信号量容量边界（capacity<1 兜底为 1、满员 TryAcquire 失败、释放后可再获取）；
//	② ctx 取消/超时路径（已取消 ctx 不阻塞、等待中被取消返回 ctx.Err()）；
//	③ AcquireEither 二选一语义（胜者拿到许可、落败者不泄漏槽位、双双不可用时返回错误）；
//	④ Redis 实现的降级与失败路径（不可达地址 ⇒ TryAcquire 立即 false、Acquire 携带 ctx 错误退出），
//	   不依赖真实 redis-server（跨实例共享上限由 internal/infra/infra_integration_test.go 覆盖）。
//
// 另附 Benchmark 两个：槽位获取/释放是每次 LLM 调用都会走的热点。
// =============================================
package concurrency

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/infra/redis"
)

// TestChanSemCapacityAndRelease 容量边界：占满即拒、释放即通、重复释放不得凭空造槽。
func TestChanSemCapacityAndRelease(t *testing.T) {
	s := newChanSem(2)
	rel1, ok := s.TryAcquire()
	if !ok {
		t.Fatal("空信号量首次获取应成功")
	}
	rel2, ok := s.TryAcquire()
	if !ok {
		t.Fatal("容量 2 时第二次获取应成功")
	}
	if _, ok := s.TryAcquire(); ok {
		t.Fatal("★ 回归：容量已满仍能获取（上限失控，会把供应商 QPS 打爆）")
	}
	rel1()
	rel1() // 幂等性观察：多次调用释放函数最多把槽放空，不得让并发数超过容量
	if _, ok := s.TryAcquire(); !ok {
		t.Fatal("释放一次后应可再次获取")
	}
	rel2()
}

// TestNewCapacityFloor 对外构造入口：capacity<1 必须兜底为 1（否则任何请求都拿不到槽）。
func TestNewCapacityFloor(t *testing.T) {
	for _, cap := range []int{0, -5} {
		s := New("k", cap, nil)
		if s == nil {
			t.Fatalf("New 不应返回 nil（cap=%d）", cap)
		}
		rel, ok := s.TryAcquire()
		if !ok {
			t.Fatalf("容量兜底为 1 时首次获取应成功（cap=%d）", cap)
		}
		if _, ok := s.TryAcquire(); ok {
			t.Fatalf("兜底容量应为 1，第二次不应成功（cap=%d）", cap)
		}
		rel()
	}
	// rdb 非 nil 时走 Redis 实现（此处只验证类型分派，不连真实服务）
	if _, ok := New("k", 2, redis.New("127.0.0.1:1", "")).(*redisSem); !ok {
		t.Fatal("传入 Redis 客户端时应返回 Redis 信号量实现")
	}
}

// TestChanSemAcquireContextPaths 已取消 ctx 立即返回错误；占满后等待中被取消同样返回 ctx.Err()。
func TestChanSemAcquireContextPaths(t *testing.T) {
	s := newChanSem(1)
	if _, err := s.Acquire(cancelledCtx()); err == nil {
		t.Fatal("已取消 ctx 不应拿到许可")
	} else if !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回 context.Canceled，实得 %v", err)
	}
	// 占满后阻塞，再从另一协程取消
	rel, ok := s.TryAcquire()
	if !ok {
		t.Fatal("前置：首次获取应成功")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := s.Acquire(ctx); err == nil {
		t.Fatal("容量占满时不应成功")
	} else if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应返回 DeadlineExceeded，实得 %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("等待超过 ctx 超时上限，说明取消信号未被响应：%v", time.Since(start))
	}
	rel()
	// 释放后带正常 ctx 的 Acquire 必须成功（防止「取消路径污染内部状态」的隐性回归）
	rel2, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatalf("释放后应可再次获取: %v", err)
	}
	rel2()
}

// TestAcquireEitherPrefersAvailableSide 一侧可用即成功；两侧都可用时落败侧不得泄漏槽位。
func TestAcquireEitherPrefersAvailableSide(t *testing.T) {
	// 场景一：x 满、y 空 ⇒ 由 y 满足（交互式请求不被本地保留槽卡死）
	x := newChanSem(1)
	xRel, ok := x.TryAcquire() // 占满 x
	if !ok {
		t.Fatal("前置：x 首次获取应成功")
	}
	y := newChanSem(1)
	rel, err := AcquireEither(context.Background(), x, y)
	if err != nil {
		t.Fatalf("y 侧可用时应获取成功: %v", err)
	}
	xRel() // 归还本测试占掉的 x 槽
	rel()  // 归还本次胜者（y 侧）
	// 两侧此刻都应空闲；若落败侧（x 的异步争抢）泄漏了槽位，waitFree 会超时红灯
	waitFree(t, x, y)

	// 场景二：两侧都满 ⇒ 带超时的 ctx 必须及时返回错误，不能永久挂起
	full1 := newChanSem(1)
	full2 := newChanSem(1)
	r1, _ := full1.TryAcquire()
	r2, _ := full2.TryAcquire()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if _, err := AcquireEither(ctx, full1, full2); err == nil {
		t.Fatal("两侧全满时不应成功")
	}
	r1()
	r2()
}

// waitFree 轮询等待两侧信号量都空闲（AcquireEither 对落败侧的释放是异步的）。
func waitFree(t *testing.T, sems ...*chanSem) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allFree := true
		for _, s := range sems {
			rel, ok := s.TryAcquire()
			if !ok {
				allFree = false
				break
			}
			rel()
		}
		if allFree {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("等待信号量空闲超时：疑似槽位泄漏（落败侧未归还）")
}

// TestRedisSemUnreachableFailsFast Redis 不可达时的失败路径（生产降级由调用方兜底）：
// TryAcquire 立即 false，Acquire 在 ctx 结束前不阻塞成功。
func TestRedisSemUnreachableFailsFast(t *testing.T) {
	dead := redis.New("127.0.0.1:1", "") // 端口 1 必然拒连
	s := New("test:sem", 2, dead)
	if _, ok := s.TryAcquire(); ok {
		t.Fatal("Redis 不可达时 TryAcquire 不得谎报成功")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if _, err := s.Acquire(ctx); err == nil {
		t.Fatal("Redis 不可达时 Acquire 应返回错误")
	}
}

// TestAcquireTimeoutAndSentinels 等待上限计算与哨兵错误文案（日志/告警按文案排障，需稳定）。
func TestAcquireTimeoutAndSentinels(t *testing.T) {
	if got := acquireTimeout(context.Background()); got != 90*time.Second {
		t.Fatalf("无截止时间应回退默认 90s，实得 %v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if got := acquireTimeout(ctx); got <= 0 || got > 500*time.Millisecond {
		t.Fatalf("有截止时间应取剩余时长，实得 %v", got)
	}
	if !strings.Contains(ErrAcquireTimeout.Error(), "超时") {
		t.Fatalf("哨兵错误文案应含「超时」，实得 %q", ErrAcquireTimeout.Error())
	}
}

// TestItoaEdgeCases 槽位键构造的整数转字符串（自实现无 strconv 依赖，边界必须钉住）。
func TestItoaEdgeCases(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 9: "9", 10: "10", 105: "105", -7: "-7"}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d)=%q，期望 %q", in, got, want)
		}
	}
	if got := (&redisSem{key: "k", cap: 3}).slotKey(2); got != "k:2" {
		t.Fatalf("slotKey 拼接不符，实得 %q", got)
	}
	if r := randToken(); len(r) != 24 {
		t.Fatalf("持有者令牌应为 24 位 hex，实得 %q", r)
	}
}

// BenchmarkChanSemAcquireRelease 进程内信号量获取+释放（每次 LLM 调用都走的热点）。
func BenchmarkChanSemAcquireRelease(b *testing.B) {
	s := newChanSem(1024)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rel, err := s.Acquire(ctx)
		if err != nil {
			b.Fatal(err)
		}
		rel()
	}
}

// BenchmarkAcquireEither 交互式 QoS 双槽竞争（前台/后台共用路径）。
func BenchmarkAcquireEither(b *testing.B) {
	x, y := newChanSem(1024), newChanSem(1024)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		rel, err := AcquireEither(ctx, x, y)
		if err != nil {
			b.Fatal(err)
		}
		rel()
	}
}

// cancelledCtx 返回一个已取消的 context（构造取消路径的确定性输入）。
func cancelledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// 并发安全冒烟：多线程同时抢/放不应 panic 也不应死锁（-race 下尤其有价值）。
func TestChanSemConcurrentUse(t *testing.T) {
	s := newChanSem(4)
	var wg sync.WaitGroup
	var peak, cur int
	var mu sync.Mutex
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			rel, err := s.Acquire(ctx)
			if err != nil {
				return
			}
			mu.Lock()
			cur++
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			time.Sleep(time.Millisecond)
			mu.Lock()
			cur--
			mu.Unlock()
			rel()
		}()
	}
	wg.Wait()
	if peak > 4 {
		t.Fatalf("并发峰值 %d 超过容量 4（上限失控）", peak)
	}
}
