// ============ localized_claim_gate.go · 职责说明 ============
// 三期 W4：销售声称的**译文侧一致性纯判据**（断言 A14，照 knowledge_guard.go 的形状）。
//
//   - GuardLocalizedClaim：模型用 A 语种答、事实集是 B 语种时，输出里的「事实性锚点」
//     （数值／认证编号／型号码）必须能在任一已准入译文值里找到同值来源；对不上 ⇒ 走回避。
//     ★ 锚点翻译不改变 ⇒ 归一化后等值；描述类只校锚点、不逐字比对（翻译措辞必然不同，§0.4 第 6 条）。
//   - LocalizedAnchorsAlign：一条译文与原文在锚点＋否定词上是否等值——防「30 天」被译成
//     「20 天」、否定句「不含」被译丢（M0242/M0243）。供译文落库／上线自检。
//   - PickSegmentByLang：五段提示词本地化的「按 lang 取译文、取不到回落中文并留痕」纯判据；
//     缺 lang 的 KB 不许跨语种放行。把它接进 buildSystemPrompt 属主 agent 在共享 engine.go 的挂接。
//
// ★ 本文件是**纯函数**：不碰 ctx、不碰 DB、不调模型，天然满足 §7.3 零 LLM 判据。
//
//	engine 包（sales_claim_guard.go）只做薄适配：ctx → Reply，绝不在这里再抄第二把尺子。
//
// ================================================
package engine

import (
	"regexp"
	"strings"

	mainengine "translator/internal/engine"
)

// 译文侧一致性分档名（对外契约：守卫与测试共用同一组字面）。
const (
	LocalizedReasonOK              = "" // 空串＝放行（对齐 knowledge_guard 的 KnowReasonOK 口径）
	LocalizedReasonUnsourcedAnchor = "localized_unsourced_anchor"
	LocalizedReasonAnchorAltered   = "localized_anchor_altered"
	LocalizedReasonNegationLost    = "localized_negation_lost"
)

// lcNumberRe 数值锚点（含小数）。型号/报价/认证编号里的数字翻译不改变，是跨语种对齐的支点。
var lcNumberRe = regexp.MustCompile(`\d+(?:\.\d+)?`)

// lcCertRe 认证／合规编号锚点（与 internal/engine/knowledge_guard.go 的 cert 词表同族，受控档）。
var lcCertRe = regexp.MustCompile(`(?i)\b(CE|FDA|ISO\s?\d{4,5}|UL|RoHS|CCC|SGS|REACH|FCC|KC|PSE|UKCA|GS|TUV|TÜV)\b`)

// lcNegationRe 否定词（中英双档）。★ 只判「否定有没有在译文里丢」，
// 不判具体词形：原译两侧同时出现「无线/无法」这类含「无」的非否定词也是对称命中，不会误报。
var lcNegationRe = regexp.MustCompile(`(?i)(不|没|未|免|非|无|别|莫|勿|not\b|no\b|without|never|free|non-|n't)`)

// anchorTokens 抽一段文本里的全部「事实性锚点」并归一（数值原样、认证编号大写去空格）。
// 参数：s=任意语种文本；返回锚点集合（map 当 set 用）。
func anchorTokens(s string) map[string]bool {
	// 千分位逗号先抹掉：否则 "1,000" 会被切成 "1"+"000" 两个锚点，与事实里的 "1000" 对不上。
	clean := strings.ReplaceAll(s, ",", "")
	out := map[string]bool{}
	for _, m := range lcNumberRe.FindAllString(clean, -1) {
		out[m] = true
	}
	for _, m := range lcCertRe.FindAllString(clean, -1) {
		out[strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(m), " ", ""))] = true
	}
	return out
}

// admittedAnchors 汇总事实集里所有已准入译文值的锚点（任一译文命中即算有来源）。
// 参数：set=已准入事实集；返回锚点集合。
func admittedAnchors(set mainengine.SalesFactsSet) map[string]bool {
	out := map[string]bool{}
	for _, f := range set.Facts {
		for a := range anchorTokens(f.Value) {
			out[a] = true
		}
	}
	return out
}

// GuardLocalizedClaim 校验模型输出的声称锚点能否与任一已准入译文对齐。
// ★ 纯函数：描述类（无锚点）一律放行，不做逐字比对（同义不同措辞的合法译文须放行）。
// 参数：text=模型输出正文；set=已准入事实集；返回（是否放行, 不放行分档）。
func GuardLocalizedClaim(text string, set mainengine.SalesFactsSet) (bool, string) {
	if strings.TrimSpace(text) == "" {
		return true, LocalizedReasonOK
	}
	src := admittedAnchors(set)
	for a := range anchorTokens(text) {
		if !src[a] {
			return false, LocalizedReasonUnsourcedAnchor
		}
	}
	return true, LocalizedReasonOK
}

// equalAnchorSet 两个锚点集合是否等值（map 当 set，只比键）。
// 参数：a/b=锚点集合；返回是否等值。
func equalAnchorSet(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

// LocalizedAnchorsAlign 校一条译文与原文在「锚点＋否定词」上是否等值。
// 参数：original=中文原文；translated=译文；返回（是否对齐, 不对齐分档）。
func LocalizedAnchorsAlign(original, translated string) (bool, string) {
	if !equalAnchorSet(anchorTokens(original), anchorTokens(translated)) {
		return false, LocalizedReasonAnchorAltered
	}
	if lcNegationRe.MatchString(original) != lcNegationRe.MatchString(translated) {
		return false, LocalizedReasonNegationLost
	}
	return true, LocalizedReasonOK
}

// PickSegmentByLang 按 lang 取一段已落库译文；取不到回落中文并置 fallback=true 供调用方 WARN 留痕。
// ★ 缺 lang 的 KB 不许跨语种放行：译文段不在本语种表里就绝不用别的语种顶替。
// 参数：segs=语种码→译文段；lang=本轮语种；fallbackZH=中文兜底段；返回段文本、是否回落。
func PickSegmentByLang(segs map[string]string, lang, fallbackZH string) (string, bool) {
	c := canonicalLang(lang)
	if v := strings.TrimSpace(segs[c]); v != "" {
		return v, false
	}
	if c == "zh_hant" {
		if v := strings.TrimSpace(segs["zh"]); v != "" {
			return v, true // 繁体缺档回落简体（同一语种的另一书写档）
		}
	}
	if v := strings.TrimSpace(fallbackZH); v != "" {
		return fallbackZH, true // 取不到本语种译文 ⇒ 回落中文并留痕
	}
	if v := strings.TrimSpace(segs["zh"]); v != "" {
		return v, true
	}
	return "", true
}
