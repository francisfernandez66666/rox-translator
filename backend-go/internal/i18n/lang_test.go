// ============================================================================
// lang_test.go — 请求语种判定回归（★ 2026-09-24 〇-S #12）
// 锁定口径：X-App-Lang > Accept-Language > 默认 zh；zh 系（含繁体）→ zh、其余语种 → en、
// 无头请求（curl/UAT 脚本）必须保持 zh——全量 UAT 断言依赖中文 message，翻红即回归。
// ============================================================================
package i18n

import (
	"context"
	"testing"
)

func TestDetectPriorityAndFallback(t *testing.T) {
	cases := []struct {
		name, xAppLang, accept string
		want                   string
	}{
		{"无头默认中文", "", "", "zh"},
		{"X-App-Lang 优先于 Accept", "en", "zh-CN", "en"},
		{"X-App-Lang 简中", "zh", "", "zh"},
		{"繁体归简中", "zh_TW", "", "zh"},
		{"繁体 tag 归简中", "zh-Hant", "", "zh"},
		{"英文带地区", "en-US", "", "en"},
		{"小写混写", "En", "", "en"},
		{"第三语种走英文", "ja", "", "en"},
		{"Accept 日文", "", "ja,en;q=0.9,zh;q=0.8", "en"},
		{"Accept 简中", "", "zh-CN,zh;q=0.9", "zh"},
		{"Accept 通配符跳过", "", "*", "zh"},
		{"Accept 通配后带明确项", "", "*;q=0.1, fr;q=0.9", "en"},
		{"Accept 乱值回落", "", ",,", "zh"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Detect(c.xAppLang, c.accept); got != c.want {
				t.Fatalf("Detect(%q,%q)=%q，期望 %q", c.xAppLang, c.accept, got, c.want)
			}
		})
	}
}

func TestLangFromContextDefault(t *testing.T) {
	if got := LangFrom(context.Background()); got != "zh" {
		t.Fatalf("ctx 未注入时应默认 zh，实际 %q", got)
	}
	if got := LangFrom(WithLang(context.Background(), "en")); got != "en" {
		t.Fatalf("注入 en 应取回 en，实际 %q", got)
	}
	if got := LangFrom(nil); got != "zh" {
		t.Fatalf("nil ctx 应回落 zh，实际 %q", got)
	}
}
