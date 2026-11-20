// ============ target_consistency_test.go · 职责说明 ============
// 目标语言脚本一致性判据的单测（P2，2026-11-20）：品牌拉丁白名单不误杀，
// 韩文/西里尔/汉字混入非目标语言必须拦截，目标体系缺失必须拦截，未知语言 fail-soft。
// 配 LangToScript 的语种映射覆盖测试。
// ============================================
package textscript

import "testing"

// TestTargetConsistency 目标语言脚本一致性判据：品牌拉丁白名单不误杀，
// 韩文/西里尔/汉字混入非目标语言必须拦截，目标体系缺失必须拦截。
func TestTargetConsistency(t *testing.T) {
	const minWant, maxForeign = 0.15, 0.02
	cases := []struct {
		name string
		text string
		want Script
		ok   bool
	}{
		{"he_品牌白名单不误杀", "שלום עולם Facebook", Hebrew, true},
		{"he_纯希伯来", "שלום עולם", Hebrew, true},
		{"he_韩文污染", "שלום 신호 עולם", Hebrew, false},
		{"he_汉字残留", "שלום קאנל约 20%", Hebrew, false},
		{"ru_正常西里尔", "Привет мир", Cyrillic, true},
		{"ru_韩文污染", "Привет 신호 мир", Cyrillic, false},
		{"ru_希伯来对西里尔是外来", "פרIvет мир", Cyrillic, false},
		{"en_正常拉丁", "Hello world", Latin, true},
		{"en_汉字污染", "Hello 世界 world", Latin, false},
		{"ja_汉字假名", "こんにちは世界", Han, true},
		{"未知语言_fail_soft", "anything", Other, true},
		{"纯数字_fail_open", "12345", Hebrew, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TargetConsistency(c.text, c.want, minWant, maxForeign); got != c.ok {
				t.Errorf("TargetConsistency(%q, %s) = %v, want %v", c.text, c.want, got, c.ok)
			}
		})
	}
}

// TestLangToScript 语言→脚本映射：覆盖关键语种与 fail-soft。
func TestLangToScript(t *testing.T) {
	for lang, want := range map[string]Script{
		"he": Hebrew, "ar": Arabic, "ru": Cyrillic, "ja": Han, "zh": Han,
		"zh_hant": Han, "ko": Hangul, "th": Thai, "el": Greek,
		"en": Latin, "de": Latin, "fr": Latin, "unknown": Other, "my": Other,
	} {
		if got := LangToScript(lang); got != want {
			t.Errorf("LangToScript(%q) = %s, want %s", lang, got, want)
		}
	}
}
