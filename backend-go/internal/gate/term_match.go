// ============ 本文件职责中文说明 ============
// 术语匹配的唯一入口（2026-10-26 P2-03）。
//
// 缺陷背景：gate.RunWithTerms 第 9 项与 ForceTerms 过去都用 strings.Contains 直接比，
// 对拉丁目标语是**大小写敏感**的。现网知识库有正常术语「服务 → Service」，
// 而通顺译文写的是 "services" / "service"，于是被判「译文未体现规定译法」，
// 在 pro 模式还会触发一次无意义的反馈重译（可能把正确译文改坏）。
//
// 口径：
//   - 术语与文本都含拉丁字母时忽略大小写（Service / service / services 都算体现）；
//   - 纯 CJK / 纯符号术语保持逐字子串（中文没有大小写，也不该做全半角折叠）；
//   - 只修「是否体现」的判定，不改变替换时写回的 KB 原文大小写（品牌名仍按库里的写法输出）。
//
// ============================================
package gate

import (
	"fmt"
	"strings"
)

// hasLatinLetter 判断字符串里是否有 a-z/A-Z。
// 只用于决定「是否走大小写不敏感比较」，不参与任何其它字符分类。
func hasLatinLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// termContains 判断 text 是否体现了术语 term。
// 拉丁术语/文本走大小写不敏感子串；CJK 等保持原样逐字子串。
func termContains(text, term string) bool {
	if term == "" {
		return false
	}
	if hasLatinLetter(term) && hasLatinLetter(text) {
		return strings.Contains(strings.ToLower(text), strings.ToLower(term))
	}
	return strings.Contains(text, term)
}

// CheckTermConsistency 术语一致性后检（2026-11-20 P3）：同一源术语在全篇译文里
// 译出多种写法即判失败。用于高频词表（非 KB 词）——KB 词由 ForceTerms/RunWithTerms 强制管，
// 不进本检查（调用方只传 FromHighfreq=true 的词）。
//
// 参数：
//
//	source      —— 源文（判断哪些术语适用）
//	translation —— 全篇译文（必须整篇传，逐段看不到跨段分歧）
//	snapshot    —— 高频词统一译法表（每条 FromHighfreq=true，Target 为建议译法、Aliases 为合法变体）
//
// 返回：不一致的术语各一条 Check（Pass=false，Detail 列出所有实际出现的变体）。
// 无快照/源文不含该词 → 跳过（fail-open，与 glossary 纪律一致）。
func CheckTermConsistency(source, translation string, snapshot []TermRequirement) []Check {
	var out []Check
	if strings.TrimSpace(translation) == "" || len(snapshot) == 0 {
		return out
	}
	for _, tm := range snapshot {
		if !tm.FromHighfreq || strings.TrimSpace(tm.Source) == "" || strings.TrimSpace(tm.Target) == "" {
			continue
		}
		srcCount := strings.Count(source, tm.Source)
		if srcCount == 0 {
			continue
		}
		// 合法译法集合：主译法 + 别名（大小写不敏感）。
		// 判据：源词在源文出现 N 次，译文里"合法译法"的出现次数应 >= N；
		// 不足说明有一部分源词被译成了别的写法（漂移）或残留了源词。
		legal := append([]string{tm.Target}, tm.Aliases...)
		legalHits := 0
		for _, l := range legal {
			if l == "" {
				continue
			}
			cnt := strings.Count(strings.ToLower(translation), strings.ToLower(l))
			legalHits += cnt
		}
		sourceResidue := strings.Count(translation, tm.Source)
		if legalHits < srcCount || sourceResidue > 0 {
			out = append(out, Check{Name: "术语一致性", Pass: false,
				Detail: strings.TrimSpace(fmt.Sprintf("「%s」在源文出现 %d 次，译文仅 %d 处用统一译法「%s」%s（应统一为 %s）",
					tm.Source, srcCount, legalHits, tm.Target,
					map[bool]string{true: "，且有源词残留", false: ""}[sourceResidue > 0], tm.Target))})
		}
	}
	return out
}
