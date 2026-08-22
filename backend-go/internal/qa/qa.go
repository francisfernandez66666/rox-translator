// ============ 本文件职责中文说明 ============
// 确定性 QA 质检包：零 LLM 调用的纯规则译文检查。
// 检查项：
//   - empty        译文为空（error）
//   - same         译文与源文完全相同（warning，含字母/CJK 才报）
//   - number       数字集合不一致——漏译/错译数字（error）
//   - placeholder  {xx}/%s/%d/<tag> 占位符缺失或多出（error）
//   - length       译文长度 < 源文 30% 且源文 >20 字符，疑似漏翻（warning）
//   - punctuation  zh/ja 目标语言句末应为全角标点却为半角（warning）
//
// 输出 Report（总数/错误数/警告数/明细），供工单流水线 qa 步骤与下载对照表消费。
// =============================================
package qa

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Issue 单条质检问题
type Issue struct {
	Lang   string `json:"lang"`   // 目标语言代码
	Rule   string `json:"rule"`   // 规则名：empty/same/number/placeholder/length/punctuation
	Level  string `json:"level"`  // 级别：error / warning
	Detail string `json:"detail"` // 人读说明
}

// Report 一份工单的质检汇总
type Report struct {
	Errors   int     `json:"errors"`           // error 级问题数
	Warnings int     `json:"warnings"`         // warning 级问题数
	Issues   []Issue `json:"issues,omitempty"` // 问题明细（上限 50 条截断）
	Pass     bool    `json:"pass"`             // 无 error 视为通过
}

// 数字正则：整数/小数/千分位（如 2026、3.14、1,000）
var numRe = regexp.MustCompile(`[0-9]+(?:[.,][0-9]+)*`)

// 占位符正则：{name} / %s %d %f / HTML/XML 标签
var phRe = regexp.MustCompile(`\{[^{}]*\}|%[sdf]|</?[a-zA-Z][a-zA-Z0-9]*[^<>]*/?>`)

// maxIssues 明细截断上限（防超大报告撑爆 payload）
const maxIssues = 50

// Check 对「源文→各语言译文」执行全部规则检查。
// 参数：source=源文本；translations=语言 → 译文。
// 返回：质检报告（Pass = Errors == 0）。
func Check(source string, translations map[string]string) *Report {
	r := &Report{}
	langs := make([]string, 0, len(translations))
	for lc := range translations {
		langs = append(langs, lc)
	}
	sort.Strings(langs) // 输出顺序稳定
	for _, lc := range langs {
		target := translations[lc]
		for _, iss := range checkOne(source, target, lc) {
			if len(r.Issues) < maxIssues {
				r.Issues = append(r.Issues, iss)
			}
			if iss.Level == "error" {
				r.Errors++
			} else {
				r.Warnings++
			}
		}
	}
	r.Pass = r.Errors == 0
	return r
}

// checkOne 对单语言对执行六项规则。
func checkOne(source, target, lang string) []Issue {
	var out []Issue
	src := strings.TrimSpace(source)
	tgt := strings.TrimSpace(target)

	// empty：译文为空
	if tgt == "" {
		out = append(out, Issue{Lang: lang, Rule: "empty", Level: "error", Detail: "译文为空"})
		return out
	}

	// same：译文与源文完全相同（源文需含文字内容才有意义）
	if tgt == src && containsLetter(src) {
		out = append(out, Issue{Lang: lang, Rule: "same", Level: "warning", Detail: "译文与源文相同，疑似未翻译"})
	}

	// number：数字集合必须一致（排序后逐位比较）
	sn, tn := numRe.FindAllString(src, -1), numRe.FindAllString(tgt, -1)
	sort.Strings(sn)
	sort.Strings(tn)
	if fmt.Sprint(sn) != fmt.Sprint(tn) {
		out = append(out, Issue{Lang: lang, Rule: "number", Level: "error",
			Detail: fmt.Sprintf("数字不一致：源=%v 译=%v", sn, tn)})
	}

	// placeholder：占位符集合必须一致
	sp, tp := phRe.FindAllString(src, -1), phRe.FindAllString(tgt, -1)
	sort.Strings(sp)
	sort.Strings(tp)
	if fmt.Sprint(sp) != fmt.Sprint(tp) {
		out = append(out, Issue{Lang: lang, Rule: "placeholder", Level: "error",
			Detail: fmt.Sprintf("占位符不一致：源=%v 译=%v", sp, tp)})
	}

	// length：疑似漏翻（译文过短）
	srcRunes, tgtRunes := len([]rune(src)), len([]rune(tgt))
	if srcRunes > 20 && tgtRunes*10 < srcRunes*3 {
		out = append(out, Issue{Lang: lang, Rule: "length", Level: "warning",
			Detail: fmt.Sprintf("译文长度仅为源文 %d%%，疑似漏翻", tgtRunes*100/srcRunes)})
	}

	// punctuation：zh/ja 句末半角句号提示（应全角）
	if (lang == "zh" || lang == "zh_hant" || lang == "ja") && strings.HasSuffix(tgt, ".") {
		out = append(out, Issue{Lang: lang, Rule: "punctuation", Level: "warning",
			Detail: "句末为半角句号，目标语言应使用全角标点"})
	}
	return out
}

// containsLetter 判断字符串是否含字母或 CJK 字符（区分纯数字/符号行）。
func containsLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || r >= 0x4E00 {
			return true
		}
	}
	return false
}
