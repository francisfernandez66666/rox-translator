// ============ 本文件职责中文说明 ============
// quota_reset_test.go · 套餐月度用量重置回归测试
// 锁定：ResetCurrentPackageGrants 仅恢复 kind='plan' 未过期且已消耗（left<total）的行，
// 未消耗/已过期/非 plan 行不受影响；幂等。
// =============================================
package store

import (
	"testing"
	"time"
)

// TestResetCurrentPackageGrants 重置语义与幂等性。
func TestResetCurrentPackageGrants(t *testing.T) {
	env := newScopeEnv(t)
	tid := int64(77)
	now := time.Now()
	// plan 有效台账：先消耗 60%（模拟已用）
	if err := env.st.CreateQuotaGrant(tid, "plan", 100000, now.Add(30*24*time.Hour), "order", 1); err != nil {
		t.Fatalf("建 plan 台账失败: %v", err)
	}
	if _, err := env.st.db.Exec("UPDATE quota_grants SET \"left\"=40000 WHERE tenant_id=? AND kind='plan'", tid); err != nil {
		t.Fatalf("模拟消耗失败: %v", err)
	}
	// trial 台账（未消耗）与已过期 plan 台账：均不应被重置
	_ = env.st.CreateQuotaGrant(tid, "trial", 50000, now.Add(14*24*time.Hour), "register", 0)
	_ = env.st.CreateQuotaGrant(tid, "plan", 20000, now.Add(-1*time.Hour), "order", 2) // 已过期

	cnt, err := env.st.ResetCurrentPackageGrants(tid)
	if err != nil {
		t.Fatalf("重置失败: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("应重置 1 行（有效的 plan 台账）, 实得 %d", cnt)
	}
	// 断言：plan 有效台账恢复满额；trial 不动；过期 plan 不动
	var left int64
	if err := env.st.db.QueryRow("SELECT \"left\" FROM quota_grants WHERE tenant_id=? AND kind='plan' AND ref_id=1", tid).Scan(&left); err != nil {
		t.Fatalf("读回 plan 台账失败: %v", err)
	}
	if left != 100000 {
		t.Fatalf("有效 plan 应恢复满额 100000, 实得 %d", left)
	}
	if err := env.st.db.QueryRow("SELECT \"left\" FROM quota_grants WHERE tenant_id=? AND kind='trial'", tid).Scan(&left); err != nil {
		t.Fatalf("读回 trial 台账失败: %v", err)
	}
	if left != 50000 {
		t.Fatalf("trial 不应被重置, 实得 %d", left)
	}
	if err := env.st.db.QueryRow("SELECT \"left\" FROM quota_grants WHERE tenant_id=? AND kind='plan' AND ref_id=2", tid).Scan(&left); err != nil {
		t.Fatalf("读回过期 plan 失败: %v", err)
	}
	if left != 20000 {
		t.Fatalf("过期 plan 不应被重置, 实得 %d", left)
	}
	// 幂等：再次重置无待重置行
	cnt2, _ := env.st.ResetCurrentPackageGrants(tid)
	if cnt2 != 0 {
		t.Fatalf("幂等重置应返回 0, 实得 %d", cnt2)
	}
}
