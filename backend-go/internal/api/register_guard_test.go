// ============ 本文件职责中文说明 ============
// 注册防薅护栏单元测试：24h 窗口次数上限、最小间隔、窗口过期重置。
package api

import (
	"testing"
	"time"
)

// TestRegisterGuard_DailyLimit 同 IP 达到每日上限后拒绝，并给出等待时间。
func TestRegisterGuard_DailyLimit(t *testing.T) {
	g := newRegisterGuard(nil)
	ip := "1.2.3.4"
	// 前 3 次放行
	for i := 0; i < 3; i++ {
		if ok, _ := g.allow(ip, 3, 0); !ok {
			t.Fatalf("第 %d 次注册不应被拒", i+1)
		}
		g.record(ip)
	}
	// 第 4 次拒绝且建议等待 ≥60s
	ok, wait := g.allow(ip, 3, 0)
	if ok {
		t.Fatal("达到每日上限后应拒绝注册")
	}
	if wait < 60 {
		t.Fatalf("等待时间应 ≥60s，实际 %d", wait)
	}
}

// TestRegisterGuard_MinInterval 最小间隔内拒绝，间隔过后放行。
func TestRegisterGuard_MinInterval(t *testing.T) {
	g := newRegisterGuard(nil)
	ip := "5.6.7.8"
	g.record(ip)
	// 刚注册完立即再注册：拒绝（minInterval=3600s 远未过）
	if ok, wait := g.allow(ip, 10, 3600); ok {
		t.Fatal("最小间隔内不应放行")
	} else if wait != 3600 {
		t.Fatalf("重试等待应≈3600，实际 %d", wait)
	}
	// 不同 IP 不受影响
	if ok, _ := g.allow("9.9.9.9", 3, 60); !ok {
		t.Fatal("其他 IP 应不受影响")
	}
}

// TestRegisterGuard_WindowReset 窗口过期后计数重置。
func TestRegisterGuard_WindowReset(t *testing.T) {
	g := newRegisterGuard(nil)
	ip := "7.7.7.7"
	for i := 0; i < 5; i++ {
		g.record(ip)
	}
	// 人为把窗口起点拨回 25 小时前 → 窗口过期
	g.mu.Lock()
	g.data[ip].firstAt = g.data[ip].firstAt.Add(-25 * time.Hour)
	g.mu.Unlock()
	if ok, _ := g.allow(ip, 3, 0); !ok {
		t.Fatal("窗口过期后应重新放行")
	}
}
