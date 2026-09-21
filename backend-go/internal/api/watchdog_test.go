// ============ watchdog_test.go · 职责说明 ============
// ★ #55 缺口批（2026-09-22，报告 §4.1-10「看门狗后台协程直接 os.Exit」）的行为回归测试。
//
// 被测契约（internal/api/watchdog.go 的 requestGracefulSelfRestart）：
//  1. 存在优雅停机钩子（cmd/server/main.go 注册）时，只委托钩子，**绝不**在库代码里 os.Exit；
//  2. 未注册钩子时（其他二进制复用 api 包 / 单测环境），退化为「flush 计量后硬退」兜底；
//  3. 钩子 panic 不能吞掉重启本身——立即走 flush + 硬退；
//  4. 钩子走完但进程没退（信号被吞）时，宽限期到点后仍要兜底硬退，自愈能力不能变成永久卡死。
//
// 测试手段：真实 gracefulExit 含 os.Exit（直接跑会杀死测试进程），故替换为计数桩；
// 宽限期兜底函数 graceFallback 显式收参（见 watchdog.go 注释），因此可直接验证「到点必退」而不挂 25 秒。
// ===================================================
package api

import (
	"errors"
	"testing"
	"time"
)

// withStubbedExit 替换 gracefulExit 桩并保证复原，返回退出码接收通道。
// 用 buffered chan(4)：兜底路径可能在测试结束后仍有协程触发，避免阻塞泄漏协程。
func withStubbedExit(t *testing.T) chan int {
	t.Helper()
	prev := gracefulExit
	ch := make(chan int, 4)
	gracefulExit = func(code int) { ch <- code }
	t.Cleanup(func() { gracefulExit = prev })
	return ch
}

// withShortGrace 调短宽限期，便于覆盖「到点兜底硬退」这条真实路径。
func withShortGrace(t *testing.T, d time.Duration) {
	t.Helper()
	prev := selfRestartGrace
	selfRestartGrace = d
	t.Cleanup(func() { selfRestartGrace = prev })
}

// withHook 注册自愈重启钩子并在用例结束后清空（包级状态必须复原，否则污染其他用例）。
func withHook(t *testing.T, hook func(reason string)) {
	t.Helper()
	RegisterSelfRestartHook(hook)
	t.Cleanup(func() { RegisterSelfRestartHook(nil) })
}

// exited 非阻塞判断是否发生了硬退出（有则说明未走优雅路径）。
func exited(ch chan int) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// TestRequestGracefulSelfRestartDelegatesToHook 有钩子时：委托钩子、不带退出。
// 这正是报告 §4.1-10 要求的修复效果——优雅停机链路（http.Shutdown → DefaultSink.Stop）不再被跳过。
func TestRequestGracefulSelfRestartDelegatesToHook(t *testing.T) {
	// 宽限期保持默认 25 秒：本用例只验证「委托钩子、同步段不退出」，
	// 后台兜底定时器不可能在这几十毫秒内触发（其自身行为由下面两个用例覆盖）。
	ch := withStubbedExit(t)
	got := make(chan string, 1)
	withHook(t, func(reason string) { got <- reason })

	requestGracefulSelfRestart("自检连续超时")

	select {
	case r := <-got:
		if r != "自检连续超时" {
			t.Errorf("钩子收到的原因 = %q，期望原样透传 %q", r, "自检连续超时")
		}
	case <-time.After(time.Second):
		t.Fatal("钩子未被调用：自愈重启仍绕过优雅停机路径")
	}
	if exited(ch) {
		t.Error("已注册优雅停机钩子时不应在同步段直接硬退出")
	}
}

// TestRequestGracefulSelfRestartWithoutHook 无钩子时：仍必须 flush + 退出（自愈能力不丢）。
func TestRequestGracefulSelfRestartWithoutHook(t *testing.T) {
	ch := withStubbedExit(t)
	RegisterSelfRestartHook(nil) // 显式清零，防其他用例漏复原
	t.Cleanup(func() { RegisterSelfRestartHook(nil) })

	requestGracefulSelfRestart("无钩子环境")

	select {
	case code := <-ch:
		if code != 1 {
			t.Errorf("退出码 = %d，期望 1（非零退出以便 systemd 判定异常并拉起）", code)
		}
	case <-time.After(time.Second):
		t.Fatal("未注册钩子时未兜底退出：进程会带着锁饥饿一直挂死")
	}
}

// TestRequestGracefulSelfRestartHookPanic 钩子 panic：转兜底硬退，不能让 panic 吞掉重启。
// panic 若外溢会把看门狗协程一起打死（进程既不重启也不告警），因此必须在此处吞掉。
func TestRequestGracefulSelfRestartHookPanic(t *testing.T) {
	ch := withStubbedExit(t)
	withHook(t, func(string) { panic(errors.New("钩子内部故障")) })

	requestGracefulSelfRestart("钩子会 panic")

	select {
	case <-ch:
		// 期望路径：panic → flush + 硬退
	case <-time.After(2 * time.Second):
		t.Fatal("钩子 panic 后未走兜底退出，自愈重启能力丢失")
	}
}

// TestGraceFallbackExitsAfterGrace 兜底定时器本身：宽限到点必退、退出码为 1。
// graceFallback 显式收参（不起协程、不改包级变量），因此这条真实路径可同步验证且无数据竞争。
func TestGraceFallbackExitsAfterGrace(t *testing.T) {
	var codes []int
	start := time.Now()
	graceFallback(15*time.Millisecond, func(code int) { codes = append(codes, code) })

	if len(codes) != 1 || codes[0] != 1 {
		t.Fatalf("兜底退出调用 = %v，期望恰好一次且退出码 1", codes)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond {
		t.Errorf("未等满宽限期就退出（耗时 %v），优雅停机窗口形同虚设", elapsed)
	}
}

// TestRequestGracefulSelfRestartWiresGraceFallback 端到端接线：钩子「调了但没真退」（信号被吞 / 钩子实现有 bug）时，
// 自愈重启仍必须在宽限期后兜底退出——否则委托反而把原有的自愈能力弄丢，比修复前更糟。
// 这里把宽限期调到 30ms 来验证接线（read 快照发生在调用方协程内，无数据竞争）。
func TestRequestGracefulSelfRestartWiresGraceFallback(t *testing.T) {
	withShortGrace(t, 30*time.Millisecond)
	ch := withStubbedExit(t)
	withHook(t, func(string) {}) // 空钩子：等于「信号发了但没人退」

	requestGracefulSelfRestart("钩子空转")

	select {
	case code := <-ch:
		if code != 1 {
			t.Errorf("兜底退出码 = %d，期望 1", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("宽限期到点未兜底退出：go graceFallback 接线丢失")
	}
}

// TestRegisterSelfRestartHookRoundTrip 钩子注册/注销的可见性（RegisterSelfRestartHook(nil) 必须真的清空）。
func TestRegisterSelfRestartHookRoundTrip(t *testing.T) {
	t.Cleanup(func() { RegisterSelfRestartHook(nil) })
	RegisterSelfRestartHook(func(string) {})
	selfRestartMu.RLock()
	hooked := selfRestartHook != nil
	selfRestartMu.RUnlock()
	if !hooked {
		t.Fatal("RegisterSelfRestartHook 注册后读取为空：钩子根本没存进去")
	}

	RegisterSelfRestartHook(nil)
	selfRestartMu.RLock()
	hooked = selfRestartHook != nil
	selfRestartMu.RUnlock()
	if hooked {
		t.Fatal("传 nil 未能清空钩子：单测会污染后续用例")
	}
}
