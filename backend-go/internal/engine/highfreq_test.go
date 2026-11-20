// ============ highfreq_test.go · 职责说明 ============
// P3 高频词统一译法表的纯逻辑单测（2026-11-20，不依赖 LLM）：
// 中文滑窗+拉丁整词抽取、频次门槛、停用词过滤；统一译法表的提示词段（"建议"措辞+别名展示+
// 空表返回空）；模型输出 ```json 包裹的剥离。模型判定与 KB 剔除的接线由回归脚本覆盖。
// ============================================
package engine

import (
	"strings"
	"testing"

	"translator/internal/gate"
)

// TestHighfreqCandidates 高频词抽取：中文滑窗 + 拉丁整词，停用词过滤，频次门槛。
func TestHighfreqCandidates(t *testing.T) {
	texts := []string{
		"销售线索的线索管理，线索转化与线索跟进",
		"经销商与经销商网络，API API API",
	}
	got := highfreqCandidates(texts)
	joined := strings.Join(got, "|")
	for _, want := range []string{"线索", "经销商", "api"} {
		if !strings.Contains(joined, want) {
			t.Errorf("高频候选缺 %q，实得 %v", want, got)
		}
	}
	// 停用词不入表
	for _, bad := range []string{"我们", "公司", "通过", "管理"} {
		if strings.Contains(joined, bad) {
			t.Errorf("停用词 %q 不应入候选：%v", bad, got)
		}
	}
}

// TestHighfreqCandidatesMinCount 只出现 1 次的词不入表。
func TestHighfreqCandidatesMinCount(t *testing.T) {
	got := highfreqCandidates([]string{"这是一次性的独特表述"})
	if len(got) != 0 {
		t.Errorf("无重复词应返回空候选，实得 %v", got)
	}
}

// TestHighfreqTermTablePromptBlock 统一译法表的提示词段："建议"措辞、别名展示、空表返回空。
func TestHighfreqTermTablePromptBlock(t *testing.T) {
	if got := (HighfreqTermTable{}).PromptBlock(); got != "" {
		t.Errorf("空表的提示词段应为空串，实得 %q", got)
	}
	tbl := HighfreqTermTable{terms: []gate.TermRequirement{
		{Source: "线索", Target: "lead", FromHighfreq: true},
		{Source: "经销商", Target: "dealer", Aliases: []string{"distributor"}, FromHighfreq: true},
	}}
	block := tbl.PromptBlock()
	if !strings.Contains(block, "建议") {
		t.Errorf("提示词段应含'建议'措辞（区别于 KB 强制）：%q", block)
	}
	if !strings.Contains(block, "distributor") {
		t.Errorf("提示词段应展示别名：%q", block)
	}
	if len(tbl.AsSnapshot()) != 2 {
		t.Errorf("AsSnapshot 应返回 2 条，实得 %d", len(tbl.AsSnapshot()))
	}
}

// TestStripCodeFence 模型输出 JSON 的 ```json 包裹剥离。
func TestStripCodeFence(t *testing.T) {
	in := "```json\n[{\"source\":\"线索\"}]\n```"
	want := `[{"source":"线索"}]`
	if got := stripCodeFence(in); got != want {
		t.Errorf("stripCodeFence = %q, want %q", got, want)
	}
	if got := stripCodeFence(`[{"a":1}]`); got != `[{"a":1}]` {
		t.Errorf("无包裹输入不应改动：%q", got)
	}
}
