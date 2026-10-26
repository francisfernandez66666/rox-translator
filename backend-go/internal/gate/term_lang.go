// ============ 本文件职责中文说明 ============
// 术语目标文本的「书写系统与目标语言是否明显不符」校验（2026-10-26 P2-03）。
//
// 现网脏数据形态：kb_entries 里有一行 source_lang=zh、target_lang=en、
// source_text=中文、target_text=英语。它会让任何含「中文」二字的源文都命中该术语，
// 而译文（英文）当然不会包含中文「英语」二字 ⇒ 每条都报「术语遵循」失败。
//
// 本校验只拦「明显不符」：目标语言是拉丁系却给了整段 CJK、目标语言是 CJK 却给了
// 纯西里尔/阿拉伯文等。未知语言、纯数字/纯符号、空值一律放行（fail-soft）——
// 这里的目标是清脏数据，不是把术语表管死。
// 复用 gate.targetCharsReasonable 的书写系统判据（与质量闸门第 8 项同一把尺子）。
// ============================================
package gate

import (
	"strings"
	"unicode"
)

// hasLetter 判断字符串里是否有任何书写体系的字母；纯数字/纯符号返回 false。
func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// TargetTextMatchesLang 判断术语的目标文本是否像目标语言。
// 未知语言/空值/纯数字/纯符号一律放行（fail-soft）；只拦书写系统明显不符的脏数据。
func TargetTextMatchesLang(targetLang, text string) bool {
	t := strings.TrimSpace(text)
	if t == "" || strings.TrimSpace(targetLang) == "" {
		return true
	}
	if !hasLetter(t) {
		return true
	}
	return targetCharsReasonable(t, targetLang)
}
