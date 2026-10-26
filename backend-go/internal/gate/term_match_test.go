// ============ term_match_test.go · 职责说明 ============
// P2-03（2026-10-25 云端 UAT）术语误报的回归锁：
//
//	① 拉丁术语匹配忽略大小写：正常译文写 services/service 不算“未体现”；
//	② CJK 术语仍逐字子串；
//	③ ForceTerms 的“已合规”判定同步放宽；
//	④ 目标文本书写系统校验能拦住「target_lang=en、target_text=英语」，
//	   但对正常英文、纯数字/符号、CJK→CJK 放行（fail-soft）。
//
// ============================================
package gate

import "testing"

// TestTermContainsLatinCaseInsensitive 锁大小写不敏感，且不把 CJK 折叠成别的形态。
func TestTermContainsLatinCaseInsensitive(t *testing.T) {
	cases := []struct {
		name string
		text string
		term string
		want bool
	}{
		{"复数小写", "We provide translation services.", "Service", true},
		{"单数小写", "Our service is fast.", "Service", true},
		{"全大写", "SERVICE", "Service", true},
		{"品牌大小写", "ROX Motors", "ROX", true},
		{"中文逐字", "我们提供翻译服务", "服务", true},
		{"中文不命中", "我们提供翻译", "服务", false},
		{"空术语", "anything", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := termContains(c.text, c.term); got != c.want {
				t.Fatalf("termContains(%q,%q)=%v want %v", c.text, c.term, got, c.want)
			}
		})
	}
}

// TestRunWithTermsAcceptsLowercaseService 复现 UAT V4 的误报：正确译文写 services 也必须通过。
func TestRunWithTermsAcceptsLowercaseService(t *testing.T) {
	terms := []TermRequirement{{Source: "服务", Target: "Service"}}
	src := "我们提供多语种文件翻译服务，覆盖合同、说明书与技术文档。"
	tr := "We provide multilingual document translation services for contracts, manuals, and technical documents."
	if g := RunWithTerms(src, "en", tr, terms); !g.Pass {
		t.Fatalf("正确译文（services）不应被判术语未体现，checks=%v", g.Checks)
	}
	// 负向对照：译文完全没提“服务/Service”时仍要判不通过（判据没有放空）。
	if g := RunWithTerms(src, "en", "We translate documents for global teams.", terms); g.Pass {
		t.Fatal("译文完全没体现术语时应判不通过（负向对照失效）")
	}
}

// TestForceTermsCaseInsensitiveCompliance 已合规判定：小写 service 不应被再次强制替换。
func TestForceTermsCaseInsensitiveCompliance(t *testing.T) {
	terms := []TermRequirement{{Source: "服务", Target: "Service"}}
	if got, n := ForceTerms("我们提供服务", "We provide services.", terms); n != 0 || got != "We provide services." {
		t.Fatalf("小写 services 已合规，不应改写，got %q n=%d", got, n)
	}
}

// TestTargetTextMatchesLang 锁目标书写系统校验的放行/拦截边界。
func TestTargetTextMatchesLang(t *testing.T) {
	cases := []struct {
		name   string
		lang   string
		text   string
		expect bool
	}{
		{"脏术语 en 目标写中文", "en", "英语", false},
		{"正常英文", "en", "Service", true},
		{"纯数字符号放行", "en", "3M / 2026-10-26", true},
		{"中文目标写中文", "zh", "中文", true},
		{"日文目标写日文", "ja", "これは翻訳です", true},
		{"空值放行", "en", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TargetTextMatchesLang(c.lang, c.text); got != c.expect {
				t.Fatalf("TargetTextMatchesLang(%q,%q)=%v want %v", c.lang, c.text, got, c.expect)
			}
		})
	}
}
