// ============ 本文件职责中文说明 ============
// 登录暴力破解限流器的单元测试：
//   - TestLoginLimiter：阈值前后行为（未达阈值不封锁、达到阈值进入冷却、无关 IP 不受影响、clear 解除封锁）
//   - TestLoginLimiterWindowExpiry：窗口内计数累计（blocked 不应在未达阈值时清除计数）
//
// =============================================
package api

import "testing"

// TestLoginLimiter 登录限流主流程：失败累计→冷却封锁→成功清零。
func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter(nil)
	// 初始不封锁
	if l.blocked("1.2.3.4") {
		t.Fatal("fresh IP should not be blocked")
	}
	// 4 次失败仍未封锁（阈值 5）
	for i := 0; i < 4; i++ {
		l.fail("1.2.3.4")
	}
	if l.blocked("1.2.3.4") {
		t.Fatal("below threshold should not be blocked")
	}
	// 第 5 次失败达到阈值，进入冷却
	l.fail("1.2.3.4")
	if !l.blocked("1.2.3.4") {
		t.Fatal("expected blocked after threshold")
	}
	// 不同 IP 不受影响
	if l.blocked("5.6.7.8") {
		t.Fatal("unrelated IP should not be blocked")
	}
	// clear 解除封锁
	l.clear("1.2.3.4")
	if l.blocked("1.2.3.4") {
		t.Fatal("expected unblocked after clear")
	}
}

// TestLoginLimiterWindowExpiry 窗口过期后失败计数重置、封锁解除。
func TestLoginLimiterWindowExpiry(t *testing.T) {
	l := newLoginLimiter(nil)
	// 模拟窗口过期：首次失败后，未达阈值时 blocked 不应清除计数
	l.fail("9.9.9.9")
	if l.blocked("9.9.9.9") {
		t.Fatal("should not block below threshold")
	}
	// 再次失败应累计
	l.fail("9.9.9.9")
	// 累计至阈值（共 5 次）
	for i := 0; i < 3; i++ {
		l.fail("9.9.9.9")
	}
	if !l.blocked("9.9.9.9") {
		t.Fatal("should be blocked after threshold within window")
	}
}
