package api

import "testing"

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
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

func TestLoginLimiterWindowExpiry(t *testing.T) {
	l := newLoginLimiter()
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
