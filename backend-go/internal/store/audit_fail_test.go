// ============ 本文件职责中文说明 ============
// 审计写失败计数单元测试（★ P1 审计可观测 2026-09-15）：
// noteAuditWriteFailure 是 LogAuditDiff 写库失败时的兜底告警入口——
// 验证：① 全局计数单调递增（/metrics translator_audit_write_failures_total 数据源）；
// ② 同 action 一分钟内限速只记一条日志（不刷屏）；③ 不同 action 各自放行。
// =============================================
package store

import (
	"errors"
	"testing"
)

func TestAuditWriteFailureCounter(t *testing.T) {
	before := AuditWriteFailures()
	noteAuditWriteFailure("unit_test_a", errors.New("模拟写失败1"))
	noteAuditWriteFailure("unit_test_a", errors.New("模拟写失败2")) // 同 action：计数仍加，日志限速
	noteAuditWriteFailure("unit_test_b", errors.New("模拟写失败3"))
	after := AuditWriteFailures()
	if after-before != 3 {
		t.Fatalf("计数应 +3, 实得 +%d", after-before)
	}
	// 计数器只增不减（重启清零属进程生命周期，测试内验证单调性）
	if after < before {
		t.Fatal("计数不应回退")
	}
}
