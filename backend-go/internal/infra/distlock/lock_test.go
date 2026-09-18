// ============ 本文件职责中文说明 ============
// distlock 进程内锁单元测试（★ P1 多实例闭环配套 2026-09-15）：
// 验证 localLock 的 TryLock 非阻塞语义——同一实例互斥、持锁期间第二请求失败、
// 释放后可再次获取；以及 redis 客户端不可用时 New 回退进程内锁（不 panic）。
// =============================================
package distlock

import (
	"context"
	"testing"
	"time"

	"translator/internal/infra/redis"
)

func TestLocalLockTryLockRelease(t *testing.T) {
	l := New("uat:lock:test", nil) // nil redis → 进程内实现
	ctx := context.Background()

	ok1, release1, err := l.TryLock(ctx, time.Second)
	if err != nil || !ok1 {
		t.Fatalf("首次应获锁: ok=%v err=%v", ok1, err)
	}
	// 持锁期间再试：必须失败且无错误（非阻塞跳过语义）
	l2 := l // 同一实例才有互斥（New 每次返回独立 localLock，跨实例互斥依赖 Redis 实现）
	ok2, rel2, err2 := l2.TryLock(ctx, time.Second)
	if ok2 || err2 != nil {
		t.Fatalf("持锁期间应失败: ok=%v err=%v", ok2, err2)
	}
	if rel2 != nil {
		t.Fatal("失败路径不应返回释放函数")
	}
	release1()
	ok3, release3, err3 := l.TryLock(ctx, time.Second)
	if !ok3 || err3 != nil {
		t.Fatalf("释放后应可重获: ok=%v err=%v", ok3, err3)
	}
	release3()
}

func TestNewNilRedisFallsBackLocal(t *testing.T) {
	// 单例未初始化（redis.Get()=nil）时 watchdog 等调用方拿到的是 localLock：
	// 必须立即可锁且释放无副作用，保证「Redis 抖动降级本地执行」路径永不卡死。
	if _, isLocal := New("k", nil).(*localLock); !isLocal {
		t.Fatal("nil redis 应回退 localLock")
	}
	ok, rel, err := New("k2", nil).TryLock(context.Background(), time.Second)
	if !ok || err != nil {
		t.Fatalf("localLock 首次必成: ok=%v err=%v", ok, err)
	}
	rel()
}

// ★ P1-4（2026-09-18）：Redis 可达性异常必须作为 err 透出（调用方据此保守降级本进程执行），
// 且每次异常计入 ErrCount（/metrics translator_distlock_errors_total 可告警）。
// 用死地址客户端（127.0.0.1:1 立即拒连）复现「Redis 抖动」，区分于「他人持锁」的 (false,nil,nil)。
func TestRedisLockErrorSurfacedAndCounted(t *testing.T) {
	dead := redis.New("127.0.0.1:1", "") // 构造不拨号成功也返回客户端，命令期报错
	if dead == nil {
		t.Fatal("dead client 不应为 nil")
	}
	l := New("uat:lock:dead", dead)
	if _, isRedis := l.(*redisLock); !isRedis {
		t.Fatal("非 nil redis 应走 redisLock")
	}
	before := ErrCount()
	ok, rel, err := l.TryLock(context.Background(), time.Second)
	if err == nil {
		t.Fatal("死地址 TryLock 必须返回 err（供调用方降级），不能与「他人持锁」混同")
	}
	if ok || rel != nil {
		t.Fatal("错误路径不应给锁/释放函数")
	}
	if got := ErrCount(); got != before+1 {
		t.Fatalf("ErrCount 应 +1: before=%d after=%d", before, got)
	}
}
