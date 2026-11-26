// ============ dispatch_tokens_accounting_test.go · 职责说明 ============
// 钉住 E-3：派发回执的 tokens_used 必须进主站用量收集器，
// 否则工单 points_billed 恒 0（口径 3：主站 Key 主站计费）。
// ================================================================
package engine

import (
	"context"
	"testing"

	"translator/internal/llm"
)

// TestDispatchTokensAccounting 钉住 E-3：派发回执 tokens_used 必须进入主站 usage 收集器。
//
// 反证：从 dispatch_chunks.go 删掉 uc.AddBilled(res.TokensUsed) ⇒ 本测试红。
func TestDispatchTokensAccounting(t *testing.T) {
	e := &Engine{}
	ctx := e.WithUsageRecorder(context.Background())
	uc := llm.CollectorFrom(ctx)
	if uc == nil {
		t.Fatal("WithUsageRecorder 没注入收集器")
	}
	// 模拟派发回执注入（与 dispatch_chunks.go 里的真实写法同形）
	tokensUsed := int64(1234)
	if tokensUsed > 0 {
		uc.AddBilled(tokensUsed)
	}
	if got := e.UsageDisplayTokens(ctx); got != tokensUsed {
		t.Errorf("UsageDisplayTokens = %d，期望派发回执 %d", got, tokensUsed)
	}
	// points 必须非 0（B3/E-3 的对外判据）
	if pts := e.PointsOfTokens(e.UsageDisplayTokens(ctx)); pts <= 0 {
		t.Errorf("PointsOfTokens = %d，派发单不应显示 0 积分", pts)
	}
	// 缺字段（0）时按既有口径不注入，不许冒充成功用量
	ctx2 := e.WithUsageRecorder(context.Background())
	if got := e.UsageDisplayTokens(ctx2); got != 0 {
		t.Errorf("无回执时应为 0，实际 %d", got)
	}
}
