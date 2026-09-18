// ============ postprocess_pseudotag_test.go · 职责说明 ============
// 复现并守护「伪标签污染」缺陷（工单 PDF《翻译助手 2.0 · 业务集成方案》16 页 → 35 页那次）。
//
// 现场（交付 PDF 实测）：<only>•••</only> ×128、<target>#></target> ×2、
// <target language>N></target language> ×6、<tt>The content</tt> ×5、孤立 </t> ×1。
// 成因：输出契约要求模型用 <t>…</t> 包裹译文（指令里含 "wrap ONLY … in <t> and </t> tags"），
// 而 pdf2docx 把表格里**无上下文的短单元格**（序号列 "#"/"1"/"2"、项目符号 "•"）
// 当独立段落送翻，模型无法判断该翻什么，转而回显指令词元当标签。
// extractContractTranslation 只认 <t>/</t>，其余伪标签原样透传，一路写进交付文件。
//
// 断言覆盖：伪标签拆除、退化复读折叠、正常文本零回归、IsSuspectOutput 判定。
// =============================================
package engine

import (
	"strings"
	"testing"
)

// TestStripPseudoTagsPromptEcho 拆除指令词元回显的伪标签，保留标签体内的正文。
func TestStripPseudoTagsPromptEcho(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{
			"表格序号列：<target>#></target>",
			`<target>#></target>`,
			`#`,
		},
		{
			"表格序号列：<target language>1></target language>",
			`<target language>1></target language>`,
			`1`,
		},
		{
			"表格序号列：<target>5></target>",
			`<target>5></target>`,
			`5`,
		},
		{
			"重复表头：<tt>The content</tt>",
			`<tt>The content</tt>`,
			`The content`,
		},
		{
			"孤立闭合标签 </t>",
			`The business team provides the Chinese original text.`,
			`The business team provides the Chinese original text.`,
		},
		{
			"残留 <t> 包裹正文",
			`<t>One-click generation of text in more than 16 languages</t>`,
			`One-click generation of text in more than 16 languages`,
		},
		{
			"走形标签体（<T + 正文 + >）",
			`<The system matches the built-in automotive translation database (including over 3300 vehicle prompts>).</t>`,
			`The system matches the built-in automotive translation database (including over 3300 vehicle prompts).`,
		},
		{
			"多组伪标签串联",
			`<only>Term</only><target>Locking</target>`,
			`TermLocking`,
		},
	}
	for _, c := range cases {
		if got := stripPseudoTags(c.in); got != c.want {
			t.Errorf("[%s] stripPseudoTags(%q)=%q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestStripPseudoTagsNoFalsePositive 正常业务文本不得被误拆（小于号比较、尖括号代码样例）。
func TestStripPseudoTagsNoFalsePositive(t *testing.T) {
	keep := []string{
		"如果 a < b > c 则交换两者",
		"Use the template <div class=\"x\">Hello</div> in your page.",
		"Add the condition x < 100 > 0 to the spec.",
		"价格 ¥99 / 30 天 · 3,000 积分",
		"This kickoff meeting will funnel benchmarked sales leads into the CRM.",
	}
	for _, s := range keep {
		if got := stripPseudoTags(s); got != s {
			t.Errorf("误拆正常文本：stripPseudoTags(%q)=%q", s, got)
		}
	}
}

// TestCollapseDegenerateRepeats 退化复读折叠：<only>•••</only> 拆标签后残留的 384 个 "•"。
func TestCollapseDegenerateRepeats(t *testing.T) {
	// 现场形态：128 组标签拆除后剩 384 个连续 "•"。
	// 折叠语义：贪心从最短重复单元（u=1）起判，384 个同字符即 u=1 的退化复读 → 折叠为 1 个。
	// 交付文件里留 1 个项目符号与留 3 个同样无意义，留 1 个更干净。
	degenerate := strings.Repeat("•••", 128)
	if got := collapseDegenerateRepeats(degenerate); got != "•" {
		t.Errorf("退化复读未折叠：len=%d, got=%q", len([]rune(got)), got)
	}
	// 复读单元 >1 字符时按其真实周期折叠（"abc"×12 → "abc"）
	if got := collapseDegenerateRepeats(strings.Repeat("abc", 12)); got != "abc" {
		t.Errorf("多字符复读未按周期折叠：got=%q", got)
	}
	// 退化的完整伪标签串（走 PostProcessTranslation 全链）
	raw := strings.Repeat("<only>•••</only>", 128)
	if got := PostProcessTranslation(raw, "en"); got != "•" {
		t.Errorf("全链未清除退化伪标签：got=%q", got)
	}
	// 正常重复（低于阈值）不折叠
	normal := "术语库、术语库、术语库、术语库"
	if got := collapseDegenerateRepeats(normal); got != normal {
		t.Errorf("正常文本被折叠：%q", got)
	}
	// 分隔线不折叠（表格横线/分割线属正常排版）
	rule := strings.Repeat("-", 60)
	if got := collapseDegenerateRepeats(rule); got != rule {
		t.Errorf("分隔线被折叠：%q", got)
	}
}

// TestIsSuspectOutput 可疑输出判定：伪标签残留/超长复读/空内容 → true。
func TestIsSuspectOutput(t *testing.T) {
	suspect := []string{
		"",
		"   ",
		`<target>#></target>`,
		`<target language>1></target language>`,
		`<tt>The content</tt>`,
		strings.Repeat("<only>•••</only>", 128),
	}
	for _, s := range suspect {
		if !IsSuspectOutput(s) {
			t.Errorf("IsSuspectOutput(%q) = false, want true", s)
		}
	}
	clean := []string{
		"One-click generation of text in more than 16 languages",
		"术语全球一致，每句可追溯",
		"Release the overseas version and the domestic version in the same week",
		"Terminology is consistent globally, and each sentence can be traced back to its origin.",
	}
	for _, s := range clean {
		if IsSuspectOutput(s) {
			t.Errorf("IsSuspectOutput(%q) = true, want false", s)
		}
	}
}

// TestPostProcessPseudotagEndToEnd 端到端：交付 PDF 现场四种伪标签形态经 PostProcessTranslation
// 后必须得到干净译文（en 目标语，走完整清洗链）。
func TestPostProcessPseudotagEndToEnd(t *testing.T) {
	cases := []struct{ in, want string }{
		{`<target>#></target>`, `#`},
		{`<target language>3></target language>`, `3`},
		{`<tt>The content</tt>`, `The content`},
		{
			`<The system matches the built-in automotive translation database (including over 3300 vehicle prompts>).</t>`,
			`The system matches the built-in automotive translation database (including over 3300 vehicle prompts).`,
		},
	}
	for _, c := range cases {
		if got := PostProcessTranslation(c.in, "en"); got != c.want {
			t.Errorf("PostProcessTranslation(%q)=%q, want %q", c.in, got, c.want)
		}
	}
}
