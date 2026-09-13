// ============ placeholder_test.go · 职责说明 ============
// ★ D19 占位符掩码-回填纯函数单测（与 qa 检测同源性、乱序/变体括号回填、丢失判定）。
// =============================================
package engine

import (
	"strings"
	"testing"

	"translator/internal/qa"
)

func TestMaskUnmaskRoundTrip(t *testing.T) {
	src := `你好 {name}，折扣 {name} 再 {pct}%，格式 %s，标签 <b>粗</b> 与 </br>`
	masked, toks := maskPlaceholders(src)
	if strings.Contains(masked, "{name}") || strings.Contains(masked, "%s") || strings.Contains(masked, "<b>") {
		t.Fatalf("掩码未清除原始占位符: %s", masked)
	}
	// 重复占位符共享同一令牌（去重下标）
	if len(toks) != 6 { // {name}({pct} %s <b> </b> </br> ；{name} 去重共享
		t.Fatalf("令牌数=%d 期望6: %v", len(toks), toks)
	}
	out, missing := unmaskPlaceholders(masked, toks)
	if len(missing) != 0 {
		t.Fatalf("回填丢失: %v", missing)
	}
	// 与 qa 同源检测：回填后占位符集合应与源一致
	srcPh := qa.PlaceholderRe.FindAllString(src, -1)
	outPh := qa.PlaceholderRe.FindAllString(out, -1)
	if strings.Join(srcPh, "|") != strings.Join(outPh, "|") {
		t.Fatalf("qa 同源不一致: src=%v out=%v", srcPh, outPh)
	}
}

func TestUnmaskTolerantBrackets(t *testing.T) {
	toks := []string{"{a}", "%d"}
	out, missing := unmaskPlaceholders("结果 [P0] 与【 P1 】结束", toks)
	if len(missing) != 0 || !strings.Contains(out, "{a}") || !strings.Contains(out, "%d") {
		t.Fatalf("变体括号回填失败: %q missing=%v", out, missing)
	}
}

func TestUnmaskDetectsMissingAndFallback(t *testing.T) {
	toks := []string{"{x}", "{y}"}
	// 模型把 P1 丢了（整串没出现）
	out, missing := unmaskPlaceholders("只有 ⟦P0⟧ 在", toks)
	if len(missing) != 1 || missing[0] != 1 || !strings.Contains(out, "{x}") {
		t.Fatalf("丢失判定错误: %q missing=%v", out, missing)
	}
}

func TestMaskNoPlaceholderPassthrough(t *testing.T) {
	s, toks := maskPlaceholders("普通文本无占位符")
	if toks != nil || s != "普通文本无占位符" {
		t.Fatal("无占位符应原样透传")
	}
}
