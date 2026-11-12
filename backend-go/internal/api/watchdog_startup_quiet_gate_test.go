// ============ watchdog_startup_quiet_gate_test.go · 职责说明 ============
// 三期 W2 A05「Q3 静默」锁（二期 §9.2 :318 / §14；施工_三期 §4.2 A05 行）：
// 流程状态机巡检（sm-sweep）必须"缺省完全不发生"，三条腿一起钉：
//   1) 静态腿·槽位形态：startWatchdog 体里 s.runSMSweepOnce 恰 1 次，且必须落在
//      for range ticker.C 之后——**禁止 @startup 启动即跑**（生产首跑由部署步显式拨，
//      测试环境构造后计数恒 0；本仓批任务「测试环境也先烧一遍」的惯病在此收口）；
//   2) 静态腿·顺序与开关：runSMSweepOnce 必须先问 kill-switch（smSweepEnabled）
//      再拨 SweepOnce；smSweepEnabled 只认显式配 "1"（缺省关是判据的一半）；
//   3) 行为腿：测试库不配键 ⇒ 构造后与拨动后 SweepOnce 调用计数恒 0；
//      显式 SetConfig("sm_sweep_enabled","1") 才真拨，且心跳超时档 = 15 分钟；
//      配 "0" 必须仍不拨（"0" 是显式关，不许被翻成真）。
// 反证＝变异体 M0211（把 @startup 加回 ⇒ 静态腿 1 红）、M0212（缺省档翻 true ⇒
// 行为腿"缺省恒 0"红），由 gate_mutants 执行器在 worktree 里真跑红在本文件消息上。
// 纯静态腿复用 watchdog_remain_batch_gate_test.go 的 watchdogFuncBody 样板与
// apiNonTestSources 全集扫描（AGENTS §三：判据级代替不了守卫级）。
// =============================================
package api

import (
	"context"
	"strings"
	"testing"
	"time"
)

// a05CountSweeper SweepOnce 计数桩：记录调用次数与每次收到的心跳超时档，
// 供行为腿判「真拨过几次、拨的时候给的档是不是 15 分钟」。
type a05CountSweeper struct {
	calls    int
	timeouts []time.Duration
}

// SweepOnce 记一次调用并回 0（不产生 requeue，行为腿只关心调用面）。
func (c *a05CountSweeper) SweepOnce(_ context.Context, heartbeatTimeout time.Duration) int {
	c.calls++
	c.timeouts = append(c.timeouts, heartbeatTimeout)
	return 0
}

// a05AssertQuiet 判「这一档之下一次都不许拨」。
func a05AssertQuiet(t *testing.T, phase string, sw *a05CountSweeper) {
	t.Helper()
	if sw.calls != 0 {
		t.Fatalf("[%s] SweepOnce 被拨了 %d 次，违反 Q3 静默（缺省/显式关档必须恒 0）", phase, sw.calls)
	}
}

// TestWatchdogSMSweepStartupQuiet A05 静态两腿：槽位只在 ticker 循环里、
// 顺序先 kill-switch 后 SweepOnce、开关只认显式 "1"。
func TestWatchdogSMSweepStartupQuiet(t *testing.T) {
	pinSQLite(t) // 硬约定：单测自钉方言（本用例纯静态，不连库）
	sources := apiNonTestSources(t)

	wd, ok := sources["watchdog.go"]
	if !ok {
		t.Fatal("api 包里找不到 watchdog.go，A05 射程失效（靶文件搬家了，锁要跟着搬）")
	}
	startBody := watchdogFuncBody(wd, "func (s *Server) startWatchdog()")
	if startBody == "" {
		t.Fatal("截不出 startWatchdog 函数体，A05 静态腿失明（函数签名漂了）")
	}
	// 腿 1：s.runSMSweepOnce 恰 1 次（加回 @startup 即变 2 次 ⇒ M0211 红）。
	if n := strings.Count(startBody, "s.runSMSweepOnce"); n != 1 {
		t.Fatalf("startWatchdog 体里 s.runSMSweepOnce 出现 %d 次（期望恰 1）：出现多次＝@startup 启动即跑复活，0 次＝槽被摘了", n)
	}
	// 腿 1b：唯一那一次必须在 ticker 循环之后（位置钉死，不许提前执行）。
	idxCall := strings.Index(startBody, "s.runSMSweepOnce")
	idxLoop := strings.Index(startBody, "for range ticker.C")
	if idxLoop < 0 || idxCall < idxLoop {
		t.Fatalf("sm-sweep 槽不在 ticker 循环之后（loop@%d call@%d）＝@startup 启动即跑形态", idxLoop, idxCall)
	}

	wire, ok := sources["sales_mq_wiring.go"]
	if !ok {
		t.Fatal("api 包里找不到 sales_mq_wiring.go，A05 顺序腿失明（靶文件搬家了，锁要跟着搬）")
	}
	onceBody := watchdogFuncBody(wire, "func (s *Server) runSMSweepOnce()")
	if onceBody == "" {
		t.Fatal("截不出 runSMSweepOnce 函数体，A05 顺序腿失明")
	}
	// 腿 2：先问 kill-switch 再拨 SweepOnce（顺序颠倒＝开关做摆设）。
	// ⚠️ 靶子必须锚真调用点 s.smSweeper.SweepOnce(——函数签名 runSMSweepOnce( 里
	// 本来就含 "SweepOnce(" 子串，拿短锚会拿签名当调用点（本仓"判据对形状做假设"那一族）。
	idxGate := strings.Index(onceBody, "s.smSweepEnabled()")
	idxSweep := strings.Index(onceBody, "s.smSweeper.SweepOnce(")
	if idxGate < 0 || idxSweep < 0 || idxGate > idxSweep {
		t.Fatalf("runSMSweepOnce 必须先问 kill-switch 再拨 SweepOnce（gate@%d sweep@%d）", idxGate, idxSweep)
	}
	enabledBody := watchdogFuncBody(wire, "func (s *Server) smSweepEnabled()")
	if enabledBody == "" {
		t.Fatal("截不出 smSweepEnabled 函数体，A05 开关腿失明")
	}
	// 腿 3：开关只认显式 "1"（把缺省档翻 true ＝ M0212 的靶）。
	if !strings.Contains(enabledBody, `"sm_sweep_enabled"`) || !strings.Contains(enabledBody, `== "1"`) {
		t.Fatal("smSweepEnabled 必须读 sm_sweep_enabled 且只认显式 == \"1\"（缺省关）")
	}
}

// TestWatchdogSMSweepDefaultOff A05 行为腿：缺省恒 0、显式 "1" 才真拨且档位 15 分钟。
func TestWatchdogSMSweepDefaultOff(t *testing.T) {
	pinSQLite(t)
	st := newTestAPIStore(t)
	s := &Server{Store: st}

	// ① 构造静默：NewServer 等价的构造动作不许触发任何巡检（Q3 的本义）。
	sw := &a05CountSweeper{}
	s.smSweeper = sw
	a05AssertQuiet(t, "构造后", sw)

	// ② 缺省关：测试库不配 sm_sweep_enabled ⇒ 拨动函数也不许真拨。
	s.runSMSweepOnce()
	a05AssertQuiet(t, "缺省档", sw)

	// ③ 显式关（"0"）：同样不许拨——"0" 是显式关档，不许被翻成真。
	if err := st.SetConfig("sm_sweep_enabled", "0"); err != nil {
		t.Fatalf("写 kill-switch 失败: %v", err)
	}
	s.runSMSweepOnce()
	a05AssertQuiet(t, "显式关档", sw)

	// ④ 显式开（"1"）：真拨恰 1 次，心跳超时档 = 15 分钟（与 watchdog 每小时一轮配对）。
	if err := st.SetConfig("sm_sweep_enabled", "1"); err != nil {
		t.Fatalf("写 kill-switch 失败: %v", err)
	}
	s.runSMSweepOnce()
	if sw.calls != 1 {
		t.Fatalf("显式开档后 SweepOnce 应拨 1 次，实拨 %d 次", sw.calls)
	}
	if len(sw.timeouts) != 1 || sw.timeouts[0] != 15*time.Minute {
		t.Fatalf("心跳超时档必须 = 15 分钟，实得 %v", sw.timeouts)
	}
}
