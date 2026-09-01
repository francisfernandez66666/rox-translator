// ============ 本文件职责中文说明 ============
// Workflow 硬闸自动重译（M4）单元测试：校验失败项→修正意见拼装、
// 硬闸重试上限配置读取、以及 gateRetranslateFeedback 对失败明细的正确抽取。
package orchestrator

import (
	"strings"
	"testing"

	"translator/internal/gate"
)

// TestGateRetranslateFeedback 抽取全部失败项并拼接修正意见。
func TestGateRetranslateFeedback(t *testing.T) {
	checks := []gate.Check{
		{Name: "非空", Pass: true},
		{Name: "残留中文", Pass: false, Detail: "译文残留中文"},
		{Name: "数字保持", Pass: false, Detail: "数字由 3 变为 4"},
	}
	got := gateRetranslateFeedback(checks)
	for _, want := range []string{"残留中文", "数字保持", "译文残留中文"} {
		if !strings.Contains(got, want) {
			t.Fatalf("修正意见应包含 %q，实际: %q", want, got)
		}
	}
	// 通过项不进入修正意见
	if strings.Contains(got, "非空") {
		t.Fatalf("通过项不应出现在修正意见: %q", got)
	}
}

// TestGateRetranslateFeedbackAllPass 全部通过时返回兜底提示。
func TestGateRetranslateFeedbackAllPass(t *testing.T) {
	fb := gateRetranslateFeedback([]gate.Check{{Name: "非空", Pass: true}})
	if strings.TrimSpace(fb) == "" {
		t.Fatalf("全部通过时仍应返回兜底提示")
	}
}

// TestGateRetryMaxDefault 未配置时默认 8 次。
func TestGateRetryMaxDefault(t *testing.T) {
	w := &Workflow{Store: nil}
	if got := w.gateRetryMax(); got != 8 {
		t.Fatalf("gateRetryMax 默认应为 8，实际 %d", got)
	}
}