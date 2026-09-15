// ============ 本文件职责中文说明 ============
// watchdog runExclusive 单元测试（★ P1 多实例闭环 2026-09-15）：
// 验证 Redis 单例未启用（测试进程恒为 nil）时，周期任务降级为本地必执行，
// 保证「锁不可用 ≠ 任务停摆」的兜底语义成立。
// =============================================
package api

import (
	"testing"
	"time"
)

// TestRunExclusiveWithoutRedis 无 Redis 环境：runExclusive 必须执行 fn（降级本地路径）。
func TestRunExclusiveWithoutRedis(t *testing.T) {
	s := &Server{}
	calls := 0
	s.runExclusive("unit-test", time.Second, func() { calls++ })
	if calls != 1 {
		t.Fatalf("无 Redis 时应本地执行一次, 实得 %d", calls)
	}
	// 不同 key 互不影响
	s.runExclusive("unit-test-b", time.Second, func() { calls++ })
	if calls != 2 {
		t.Fatalf("第二个任务也应执行, 实得 %d", calls)
	}
}
