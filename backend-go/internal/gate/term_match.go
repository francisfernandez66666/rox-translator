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

import "strings"

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
