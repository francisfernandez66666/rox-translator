package engine

import (
	"strings"
	"testing"
)

// TestTranslateInstructionMutual 验证任意方向互译指令：英文源→中文 / 英文源→法语等。
func TestTranslateInstructionMutual(t *testing.T) {
	cases := []struct {
		source, target string
		wantContains   string
	}{
		{"en", "zh", "简体中文"},
		{"en", "fr", "法语"},
		{"zh", "en", "英语"},
		{"zh", "zh_hant", "繁体中文"},
		{"fr", "en", "英语"},
		{"en", "ja", "日语"},
	}
	for _, c := range cases {
		instr := translateInstruction(c.source, c.target)
		if !strings.Contains(instr, c.wantContains) {
			t.Errorf("translateInstruction(%q,%q)=%q 缺少 %q", c.source, c.target, instr, c.wantContains)
		}
	}
	// 未知源语言不得断言语言名（回退"文本"）
	instr := translateInstruction("", "de")
	if !strings.Contains(instr, "文本") {
		t.Errorf("未知源语言应回退文本，got %q", instr)
	}
	if strings.Contains(instr, "中文") {
		t.Errorf("未知源语言不应断言中文，got %q", instr)
	}
}

// TestSrcName 源语言名称解析。
func TestSrcName(t *testing.T) {
	if got := srcName("zh"); got != "中文" {
		t.Errorf("srcName(zh)=%q", got)
	}
	if got := srcName("en"); got != "英语" {
		t.Errorf("srcName(en)=%q", got)
	}
	if got := srcName("unknown-xyz"); got == "" {
		t.Error("未知语言应回退非空名称")
	}
}
