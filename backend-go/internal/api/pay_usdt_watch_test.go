// usdtReconcileTick nil-Store 防线回归测试（2026-09-16 P0 修复：
// 双实例 e2e 实测发现退化组装 Store=nil 时周期协程空指针 panic 杀进程）。
package api

import (
	"testing"
	"time"
)

// TestUsdtReconcileTickNilStore 防线生效：Store=nil 时静默返回，不 panic。
// 若回归（去掉 nil 判断），本测试会以 nil pointer dereference 崩溃整个测试进程。
func TestUsdtReconcileTickNilStore(t *testing.T) {
	s := &Server{} // Store 未装配（模拟退化启动/开发场景）
	done := make(chan struct{})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("usdtReconcileTick 空 Store 下 panic（P0 防线缺失）: %v", r)
			}
			close(done)
		}()
		s.usdtReconcileTick()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("usdtReconcileTick 空 Store 下未按时返回（疑似死等）")
	}
}
