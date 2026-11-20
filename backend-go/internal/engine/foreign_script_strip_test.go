// ============ foreign_script_strip_test.go · 职责说明 ============
// P2 字符级外来脚本清洗的单测（2026-11-20）：韩文/西里尔整词删除、目标脚本/拉丁品牌/
// 数字/标点/孤立字母保留；拉丁目标与未知语言 fail-soft 不清洗；反劣化确保正常译文过
// PostProcessTranslation 后数字不丢。
// ============================================
package engine

import (
	"strings"
	"testing"
)

// TestStripForeignScriptsInNonTarget 字符级外来脚本清洗：韩文/西里尔整词删除，
// 目标脚本/拉丁品牌/数字/标点/孤立字母保留。
func TestStripForeignScriptsInNonTarget(t *testing.T) {
	cases := []struct {
		lang string
		in   string
		want string
	}{
		{"he", "דרך 신호 פרסום", "דרך  פרסום"},
		{"he", "דרך канал פרסום", "דרך  פרסום"},
		{"he", "דרך Facebook פרסום 30%", "דרך Facebook פרסום 30%"}, // 拉丁品牌+数字保留
		{"he", "שלום עולם", "שלום עולם"},                            // 纯目标不动
		{"ar", "عبر القناة 신호", "عبر القناة "},
		{"ru", "Через 신호 канал", "Через  канал"}, // 西里尔目标：韩文删、西里尔留
		{"en", "Hello 世界", "Hello 世界"},            // 拉丁目标不清洗（中文由 StripChineseInNonZh 管）
		{"xx", "anything 신호", "anything 신호"},     // 未知语言 fail-soft
	}
	for _, c := range cases {
		if got := StripForeignScriptsInNonTarget(c.in, c.lang); got != c.want {
			t.Errorf("StripForeignScriptsInNonTarget(%q, %s) = %q, want %q", c.in, c.lang, got, c.want)
		}
	}
}

// TestPostProcessTranslationNoFalseStrip 反劣化：正常目标语译文过清洗后内容不丢。
func TestPostProcessTranslationNoFalseStrip(t *testing.T) {
	for _, c := range []struct{ lang, in string }{
		{"he", "דרך Facebook ו-Instagram, ההמרה = 30%"},
		{"ar", "عبر القناة.rpm 30%"},
		{"ru", "Через канал 30%"},
	} {
		got := PostProcessTranslation(c.in, c.lang)
		if !strings.Contains(got, "30") {
			t.Errorf("清洗误删数字：lang=%s in=%q got=%q", c.lang, c.in, got)
		}
	}
}
