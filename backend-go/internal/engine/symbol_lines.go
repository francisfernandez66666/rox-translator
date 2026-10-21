// ============ symbol_lines.go · 职责说明 ============
// 「这一行根本不是文字」的**唯一一把尺子**（★ 2026-10-21 UAT 收口 M9）。
//
// 由来：UAT 截图里上下箭头图标被 OCR 读成 `1` / `<`，回填脚本把它当普通文本又画回原位，
// 客户看到的是「原文根本没有的字盖住了图标」。这条链上有三个时刻要回答同一个问题，
// 过去它们各写各的（或干脆不判）：
//
//	① 送翻：`segmentNeedsTranslation`（本文件给出 `IsSymbolOnlyLine` 快判）；
//	② 交付：`ocr_translate_image.py` 的回填门（Python 侧同源常量，见下）；
//	③ 即时翻译：`api/chat_image.go` 的逐行对齐（Go 侧直接调本包的 NeedsIdentityDelivery）。
//
// ★ 清单只有一份：`graphicSymbolRunes` / `contentSymbolRunes` 两个常量是 Go 与 Python 的
// **共同事实源**，Python 侧的同名字面量与它逐字等值（锁＝
// `internal/engine/symbol_table_parity_test.go`，形制同 fileproc/tts_lang_coverage_gate_test.go）。
// 新增/删除任何一枚码位都必须同时改两侧，否则闸门当场红。
//
// ★ 为什么区分「图形」与「内容」：
//   - 内容符号（货币/百分号/日期标点）是**金额与时间的一部分**，必须原样保留：
//     `¥1003.00`、`-11312.00`、`09-17 09:40:33` 在任何语言里都是同一串字符；
//   - 图形符号（箭头/三角/圆点/emoji）不是语言内容，模型对它们的任何输出都是噪声，
//     整行连画都不该画（原图标必须逐像素保住）。
//
// ★ 判据取向是**结构**而不是词表：SymbolOnlyLine 只问「有没有任何书写体系的字母」，
//
//	不问具体是哪个符号——OCR 明天把箭头读成 `^`、读成 `~`，这一行仍然不会被重画。
//
// =============================================
package engine

import (
	"strings"
	"unicode"
)

// graphicSymbolRunes 图形符号（不可译；整行命中时连回填都不做，原图标逐像素保留）。
// ★ 与 ocr_translate_image.py 的 GRAPHIC_SYMBOLS **逐字等值**（parity 锁会现读比对）。
// 这里收的是「常用档」，Extended_Pictographic 的主体由 isGraphicRune 的码位区间覆盖。
const graphicSymbolRunes = "↑↓⇅⇵⌃⌄▲▼◀▶●○◤◥■□◆◇★☆✓✔✗✘→←↔↕⇒⇐·•—–…™®©"

// contentSymbolRunes 内容符号（金额/日期/标点的一部分，必须**原样**保留；可重画为同样的字）。
// ★ 与 ocr_translate_image.py 的 CONTENT_SYMBOLS **逐字等值**。
// 全角档一并收进来：OCR 对全角标点的识别与半角同形，分类上不该分两套。
const contentSymbolRunes = "¥￥$€£％%‰.,:;，。：；、-+／/()（）[]【】{}「」『』"

// isGraphicRune 单个码位是否属「图形符号」。
// 三条腿与 Python 侧逐条同源：
//   - 显式名单（常用箭头/三角/圆点/商标/勾叉）；
//   - 变体选择符（VS15/VS16）、零宽连接符（ZWJ）、键帽组合符（U+20E3）——
//     它们是 emoji 序列的连接件，整行判据里必须与图形一等对待，否则「⭐️」「👨‍👩‍👧」会被拆开；
//   - Extended_Pictographic 的主体区间（emoji/杂项符号/装饰符号/旗帜）。
func isGraphicRune(r rune) bool {
	if strings.ContainsRune(graphicSymbolRunes, r) {
		return true
	}
	switch r {
	case 0xFE0E, 0xFE0F, 0x200D, 0x20E3: // VS15 / VS16 / ZWJ / 键帽
		return true
	}
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF: // emoji、象形符号、旗帜（含 1F1E6–1F1FF 区域指示符）
		return true
	case r >= 0x2600 && r <= 0x27BF: // Misc Symbols + Dingbats（☀★✓ 一族）
		return true
	case r >= 0x2B00 && r <= 0x2BFF: // Miscellaneous Symbols and Arrows（⬆⬇ 一族）
		return true
	}
	return false
}

// IsGraphicOnlyLine 整行（去空白后）非空且**每个**字符都是图形符号 ⇒ 这是一枚图标而不是文字。
func IsGraphicOnlyLine(s string) bool {
	has := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if !isGraphicRune(r) {
			return false
		}
		has = true
	}
	return has
}

// IsContentOnlyLine 整行（去空白后）非空且只由数字与内容符号组成 ⇒ 金额/日期/编号行。
// ★ 这类行「不需要翻译」，但**不是**图形：它本身就是要交付的内容，原文才是唯一正确形态。
func IsContentOnlyLine(s string) bool {
	has := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if unicode.IsDigit(r) || strings.ContainsRune(contentSymbolRunes, r) {
			has = true
			continue
		}
		return false
	}
	return has
}

// IsSymbolOnlyLine 整行没有任何「文字」（任何书写体系的字母：汉字/假名/谚文/西里尔/阿拉伯/泰文/拉丁…）。
//
// 这是三个时刻共用的那个问题的最小形态：没有文字的行不可能有译文，模型对它的输出只可能是
// 幻觉（工单里整排变成 "and"），回填时也不该拿模型的输出盖掉原图标。
//
// ★ 刻意不拿「是否落在某个符号名单」判：OCR 把箭头读成 `<`、`1`、`^` 都是真实发生过的形态，
//
//	名单永远追不上识别器的想象；「没有任何字母」才是这件事的**结构**。
func IsSymbolOnlyLine(s string) bool {
	has := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			continue
		}
		if unicode.IsLetter(r) {
			return false
		}
		has = true
	}
	return has
}
