// ============ avoid_phrases_gate_test.go · 职责说明 ============
// A13：回避句 12 语种齐全、逐语种不含任何事实值（数值/认证编号/品牌名）、
// 与 12 语种单一真值同源；未知/空语种回落中文并留痕（fallback 标记）。
// ================================================
package engine

import (
	"regexp"
	"strings"
	"testing"
)

// TestAvoidPhrasesFullsetNoFactValues 12 档逐条扫「不含事实值」，且与 12 语种单一真值同源。
func TestAvoidPhrasesFullsetNoFactValues(t *testing.T) {
	langs := AvoidPhraseLangs()
	if len(langs) != 12 {
		t.Fatalf("回避句应覆盖 12 语种，实际 %d", len(langs))
	}
	digitRe := regexp.MustCompile(`\d`)
	for _, l := range langs {
		if _, ok := langLabels[l]; !ok {
			t.Errorf("语种 %s 不在 reply_lang.go 的 12 语种单一真值里", l)
		}
		p, fallback := AvoidPhrase(l)
		if fallback {
			t.Errorf("已收录语种 %s 不应回落中文", l)
		}
		if strings.TrimSpace(p) == "" {
			t.Errorf("语种 %s 回避句为空", l)
		}
		if digitRe.MatchString(p) {
			t.Errorf("语种 %s 回避句含数值事实值: %q", l, p)
		}
		for _, bad := range []string{"LangCross", "能言", "CE", "FDA", "ISO"} {
			if strings.Contains(p, bad) {
				t.Errorf("语种 %s 回避句含事实值/品牌/认证编号 %q: %q", l, bad, p)
			}
		}
	}
}

// TestAvoidPhrasesFallbackZH 未知/空语种回落中文并置 fallback 供调用方 WARN 留痕。
func TestAvoidPhrasesFallbackZH(t *testing.T) {
	for _, l := range []string{"xx-unknown", "", "zz"} {
		p, fallback := AvoidPhrase(l)
		if !fallback {
			t.Errorf("语种 %q 应回落中文并置 fallback", l)
		}
		if p != avoidPhrases["zh"] {
			t.Errorf("语种 %q 未回落中文回避句: %q", l, p)
		}
	}
	// 已收录语种不回落。
	if _, fb := AvoidPhrase("ja"); fb {
		t.Fatal("ja 是收录语种，不应回落")
	}
}
