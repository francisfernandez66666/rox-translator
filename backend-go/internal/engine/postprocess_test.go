// ============ 本文件职责中文说明 ============
// 后处理清洗链（postprocess）的单元测试：审校模板标记剥离与空占位方括号清扫，
// 覆盖需求3「单句翻译误带入【原文】/【待審校譯文】等标记」与 2026-09-02 实测的
// 『【】Please perform…』符号残留根因，防止回归。
// ============================================
package engine

import "testing"

// TestStripReviewMarkers 验证各类审校模板残留都能被剥离成干净译文。
func TestStripReviewMarkers(t *testing.T) {
	cases := []struct{ in, want string }{
		// 完整模板：取最后标记之后的译文
		{"【原文】你好\n【待審校譯文】Hello", "Hello"},
		{"【原文】你好\n【译文】Hello", "Hello"},
		{"【原文】你好\n【待审校译文】你好\n【译文】Hello", "Hello"},
		{"[原文]你好\n[译文]Hello", "Hello"},
		// 仅一个标记 + 译文：直接取译文
		{"【译文】Hello", "Hello"},
		{"【待審校譯文】Hello", "Hello"},
		{"【原文】Please perform the translation", "Please perform the translation"},
		// 无后续译文：删除全部标记，保留残余文本
		{"【原文】你好【待審校譯文】", "你好"},
		{"【原文】你好", "你好"},
		// 无标记：原样返回（不误删正常内容）
		{"【贵宾】请进", "【贵宾】请进"},
		{"普通文本", "普通文本"},
	}
	for _, c := range cases {
		if got := stripReviewMarkers(c.in); got != c.want {
			t.Errorf("stripReviewMarkers(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestStripEmptyPlaceholderBrackets 验证成对空占位方括号（含内部空白/换行）全部剥离。
func TestStripEmptyPlaceholderBrackets(t *testing.T) {
	cases := []struct{ in, want string }{
		{"【】Please perform the translation", "Please perform the translation"},
		{"【 】Please perform", "Please perform"},
		{"【\n】Please perform", "Please perform"},
		{"[]Please", "Please"},
		{"[ ]Please", "Please"},
		{"Hello 【】", "Hello "},
		{"【】【】Please", "Please"},
		// 带内容的方括号不受影响
		{"【贵宾】请进", "【贵宾】请进"},
		{"【2】", "【2】"},
	}
	for _, c := range cases {
		if got := stripEmptyPlaceholderBrackets(c.in); got != c.want {
			t.Errorf("stripEmptyPlaceholderBrackets(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}

// TestPostProcessSymbolRemnant 端到端回归：2026-09-02 实测 xlsx 译文单元格
// 『【】Please perform…』式残留，以及中文删除后新生成空占位括号（【原文】→【】）的二次清扫。
func TestPostProcessSymbolRemnant(t *testing.T) {
	cases := []struct {
		in, lang, want string
	}{
		// en 目标：实测残留 → 必须完全干净
		{"【】Please perform the translation", "en", "Please perform the translation"},
		{"【原文】Please perform the translation", "en", "Please perform the translation"},
		{"【译文】Please perform the translation", "en", "Please perform the translation"},
		// en 目标：中文删除后不得残留空方括号
		{"【原文】你好", "en", ""},
		// zh_hant 目标：标记整块剥离，保留译文
		{"【原文】你好\n【待審校譯文】您好", "zh_hant", "您好"},
		// 带内容的正常方括号不受影响
		{"【贵宾】請進", "zh_hant", "【贵宾】請進"},
	}
	for _, c := range cases {
		if got := PostProcessTranslation(c.in, c.lang); got != c.want {
			t.Errorf("PostProcessTranslation(%q,%q)=%q, want %q", c.in, c.lang, got, c.want)
		}
	}
}
