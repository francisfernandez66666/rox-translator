// ============================================================================
// usage_single_source_test.go — 用量「一处累计、一处取数」回归锁
// ★ 〇-U 批 I-4（2026-09-26 UAT）缺陷对应：
//
//	F-49②：WithUsageRecorder 无条件再造一只空收集器，把外层（API/service）注入的那只遮蔽掉
//	  ⇒ LLM 侧只写内层、外层读回恒 0 ⇒ /openapi/v1/translate 出参 points_used 恒 0，
//	  且 09-25 轮 R-L1「在 API 层补注入」的修复从未生效。
//	F-49①：实收（真实用量×均摊系数）只有扣费现场知道；展示侧再乘一次 markup 就是「少报一半」的根因，
//	  故收集器新增 billed 通道，出参只读它。
//	F-50①：对外文案（模式标注）不得再出现 token 裸值（AGENTS §一·5「公开接口零 token 裸值」）。
//
// 运行：cd backend-go && go test ./internal/engine/ -run 'Usage|ModeBadge'
// ============================================================================
package engine

import (
	"context"
	"strings"
	"sync"
	"testing"

	"translator/internal/llm"
)

// TestWithUsageRecorderReusesOuterCollector 内层（引擎）再注入一次也不得遮蔽外层（API/service）。
// 这是 F-49② 的机制锁：旧写法下第二只收集器把第一只盖住，外层恒读 0。
func TestWithUsageRecorderReusesOuterCollector(t *testing.T) {
	e := &Engine{}
	outer := e.WithUsageRecorder(context.Background())
	inner := e.WithUsageRecorder(outer) // 模拟 handleTextCore / HandleFile 内部的重复注入

	ucOut, ucIn := llm.CollectorFrom(outer), llm.CollectorFrom(inner)
	if ucOut == nil || ucIn == nil {
		t.Fatalf("两侧都必须有收集器，实际 outer=%v inner=%v", ucOut, ucIn)
	}
	if ucOut != ucIn {
		t.Fatalf("内层必须复用外层收集器（同一只指针），实际 outer=%p inner=%p —— 遮蔽即 F-49② 复发", ucOut, ucIn)
	}
	// 写内层、读外层：等值才算「全链一处累计」
	ucIn.Add(120, 30)
	if p, c := e.UsageTokens(outer); p != 120 || c != 30 {
		t.Fatalf("外层读回应为 (120,30)，实际 (%d,%d)", p, c)
	}
	// 中止函数同样只能有一只：内层若另造，余额耗尽时取消的就只是内层子树
	if llm.AbortFromCtx(outer) == nil || llm.AbortFromCtx(inner) == nil {
		t.Fatalf("outer/inner 都必须能取到余额中止函数")
	}
	// 首次注入（无外层）仍须自带中止能力
	fresh := e.WithUsageRecorder(context.Background())
	if llm.AbortFromCtx(fresh) == nil {
		t.Fatalf("首次注入必须创建 abort（实时计费余额耗尽要靠它中止）")
	}
}

// TestUsageBilledAndDisplayTokens 实收口径取数与保守回退。
func TestUsageBilledAndDisplayTokens(t *testing.T) {
	e := &Engine{}
	ctx := e.WithUsageRecorder(context.Background())
	uc := llm.CollectorFrom(ctx)

	// ① 只有真实用量、还没有计量钩子写过实收：不得把「取不到」当 0 报给客户
	uc.Add(100, 60)
	if n, ok := e.UsageBilledTokens(ctx); n != 0 || ok {
		t.Fatalf("未记实收时应返回 (0,false)，实际 (%d,%v)", n, ok)
	}
	if got := e.UsageDisplayTokens(ctx); got != 160 {
		t.Fatalf("取不到实收时应保守回退真实用量 160，实际 %d（回退成 0 就是 F-49 的恒零报文）", got)
	}
	// ② 扣费现场记了实收（含 markup）：出参必须按实收，且不再等于裸用量
	uc.AddBilled(320)
	if n, ok := e.UsageBilledTokens(ctx); n != 320 || !ok {
		t.Fatalf("应读到实收 320，实际 (%d,%v)", n, ok)
	}
	if got := e.UsageDisplayTokens(ctx); got != 320 {
		t.Fatalf("展示取数必须等于实收 320（旧形态在此再乘 markup ⇒ 两条折算链漂移），实际 %d", got)
	}
	// ③ 并发累加不丢账（多语言并发翻译同时写同一只）
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); uc.AddBilled(10) }()
	}
	wg.Wait()
	if n, _ := e.UsageBilledTokens(ctx); n != 400 {
		t.Fatalf("并发 AddBilled 丢账，期望 320+80=400，实际 %d", n)
	}
	// ④ 没有收集器（后台任务直调引擎）：Display 走 UsageTokens 的 0 值路径，不得 panic
	if got := e.UsageDisplayTokens(context.Background()); got != 0 {
		t.Fatalf("无收集器时应返回 0，实际 %d", got)
	}
}

// TestModeBadgeLabelContract 模式标注：文案一处常量、按流水线真实差异描述、零 token 裸值（F-50）。
func TestModeBadgeLabelContract(t *testing.T) {
	fast, pro := ModeBadgeLabel(true), ModeBadgeLabel(false)
	// 等值锁（不是「包含」单向锁）：口径要改就改这一处常量，界面与 OpenAPI 出参同步跟随
	if want := " | ⚡快速模式（初翻+校对+质检，不走知识库直配与质量评估）"; fast != want {
		t.Fatalf("快速模式标注漂移\nwant=%q\ngot =%q", want, fast)
	}
	if want := " | 🎓专业校对模式（全流水线：知识库直配+初翻+校对+质量评估+文化闸）"; pro != want {
		t.Fatalf("专业模式标注漂移\nwant=%q\ngot =%q", want, pro)
	}
	// 两型必须能讲清差别：旧文案「快速模式（AI初翻+校对）」与注释「fast/pro 均含校对」
	// 自相矛盾（客户读不出两者区别），故 fast 侧必须显式写出它**关掉**了什么
	if !strings.Contains(fast, "不走") {
		t.Fatalf("快速模式标注必须说明与专业模式的实际差异，实际 %q", fast)
	}
	// 对外载荷零 token 裸值（AGENTS §一·5）：这两串会进 res.Data.Mode 随 OpenAPI 出参外发
	for _, s := range []string{fast, pro} {
		if strings.Contains(strings.ToLower(s), "token") {
			t.Fatalf("模式标注里出现 token 裸值口径：%q", s)
		}
	}
}
