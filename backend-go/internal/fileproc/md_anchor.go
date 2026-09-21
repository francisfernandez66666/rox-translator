// ============ md_anchor.go · 职责说明 ============
// Markdown 内部锚点（目录跳转）同步重写（★ 2026-09-22，缺陷 RC-6）。
//
// 问题：产品文档开头普遍有目录 `- [一、项目背景与目标](#一项目背景与目标)`。
// span 写回模型把链接目标（URL/锚点）当骨架「挖洞保护」起来（mdProtectLinkTargets），
// 于是**链接文字翻成了英文、括号里的锚点还是中文** ⇒ 译文目录全部点不动。
// 链接目标绝不能进翻译键（防模型改写真实 URL），所以这里不做保护豁免，
// 而是在整文件写完后**按标题成对重算锚点**：目标语标题 → 目标语 slug。
//
// 口径与 GitHub 的 heading anchor 生成规则对齐（小写、非「字母/数字/空格/-/_」剔除、
// 空格转 `-`、同名标题追加 `-1`/`-2`），因为中文文档目录锚点就是按该规则手写的；
// 不追求覆盖所有渲染器（GitLab/VSCode 规则略有差异，重算后最差退化为「跳不动」，
// 与修前的必坏相比仍是净收益）。
// =============================================
package fileproc

import (
	"net/url"
	"strings"
	"unicode"
)

// mdRemapAnchors 用「原文标题 ↔ 译文标题」的配对，把产物里指向中文锚点的链接改成
// 指向译文锚点。参数：src/out 必须**行号一一对应**（span 写回模型保证 1:1，见 text_formats.go）。
// 无任何可映射锚点时原样返回 out（不制造无意义差异）。
func mdRemapAnchors(src, out []string) []string {
	mapping := mdAnchorMapping(src, out)
	if len(mapping) == 0 {
		return out
	}
	res := make([]string, len(out))
	for i, line := range out {
		holes := mdLinkTargetHoles(line)
		if len(holes) == 0 {
			res[i] = line
			continue
		}
		newLine := line
		// 行尾→行首替换：holes 按升序，倒序替换才不会让前面 hole 的偏移量被后面的替换打断
		for j := len(holes) - 1; j >= 0; j-- {
			h := holes[j]
			target := newLine[h[0]:h[1]] // 含左右括号，形如 `(#锚点)`
			if len(target) < 3 || target[1] != '#' {
				continue // 外部 URL / 相对路径：一个字都不动
			}
			nk, ok := lookupAnchor(mapping, target[2:len(target)-1])
			if !ok {
				continue
			}
			// 保持原 href 的编码风格：原文写 %E4%B8%80… 就继续百分号编码，写裸中文就继续裸中文
			// （两种渲染器都吃，擅自改风格反而可能把它写坏）。
			if strings.Contains(nk, "%") || strings.Contains(target, "%") {
				nk = encodeAnchorKeepStyle(nk)
			}
			newLine = newLine[:h[0]] + "(#" + nk + ")" + newLine[h[1]:]
		}
		res[i] = newLine
	}
	return res
}

// mdAnchorMapping 生成「原锚点 slug → 译锚点 slug」映射（同名标题按 GitHub 规则带 -1/-2 后缀）。
// 两侧必须用**同一套**去围栏/取标题文本逻辑，否则映射表本身就错位。
func mdAnchorMapping(src, out []string) map[string]string {
	mapping := map[string]string{}
	seen := map[string]int{} // 原 slug 已出现次数：重复标题的后缀计数与 GitHub 一致
	inFence := false
	for i := range src {
		if _, _, isFence := mdFenceLine(strings.TrimSpace(src[i])); isFence {
			inFence = !inFence // 围栏内的 `#` 是注释/井号，不是标题（否则映射表凭空多条目）
			continue
		}
		if inFence || i >= len(out) || !mdIsHeadingLine(src[i]) {
			continue
		}
		oldSlug := mdSlug(mdHeadingText(src[i]))
		if oldSlug == "" {
			continue
		}
		// 重复标题：第 2 次起加 -1/-2 后缀（计数按**基础 slug** 累加，按最终 slug 累加会让
		// 第 3 次仍算出 -1 而互相撞车——GitHub 的 anchor 就是基础名计数）
		n := seen[oldSlug]
		seen[oldSlug]++
		if n > 0 {
			oldSlug += "-" + itoaSmall(n)
		}
		newSlug := mdSlug(mdHeadingText(out[i]))
		if newSlug == "" {
			continue // 译文行没解析出标题（该行未命中）：不动
		}
		// 重复标题的两侧后缀用**同一个序号 n**：译文标题同样可能重复（如两节都叫 Overview），
		// 渲染器给第二个生成 overview-1，序号与源侧出现顺序天然一致，故可直接复用。
		if n > 0 {
			newSlug += "-" + itoaSmall(n)
		}
		if newSlug != oldSlug {
			mapping[oldSlug] = newSlug
		}
	}
	return mapping
}

// mdIsHeadingLine 判断是否 ATX 标题行（前缀解析复用 mdSplitPrefix，与提取侧同一口径）
func mdIsHeadingLine(line string) bool {
	n := mdSplitPrefix(line)
	if n == 0 {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(line[:n]), "#")
}

// mdHeadingText 取标题的「渲染后纯文本」（GitHub 锚点正是按它算的）：
// 剥结构前缀、去图片、链接只留文字、去行内强调与代码标记。
func mdHeadingText(line string) string {
	n := mdSplitPrefix(line)
	return mdAnchorPlainText(strings.TrimSpace(line[n:]))
}

// mdAnchorPlainText 剥行内 markdown 语法：![图](url) 整体删、[文字](url)→文字、
// **粗**/ __粗__ / *斜* / _斜_ / `码` / ~~删~~ 只去标记（与旧 emphasisRe 的区别：
// 这里**绝不删内容**，标记是成对剥的）。
func mdAnchorPlainText(s string) string {
	// 图片先删（alt 文字不参与锚点，GitHub 同口径）
	for {
		i := strings.Index(s, "![")
		if i < 0 {
			break
		}
		open := strings.Index(s[i:], "(")
		if open < 0 {
			s = s[:i] + s[i+2:]
			continue
		}
		open += i
		if close, ok := mdMatchParen(s, open); ok {
			s = s[:i] + s[close+1:]
		} else {
			s = s[:i] + s[i+2:]
		}
	}
	// 链接 [文字](url) → 文字
	for {
		i := strings.Index(s, "[")
		if i < 0 {
			break
		}
		mid := strings.Index(s[i:], "](")
		if mid < 0 {
			s = s[:i] + s[i+1:]
			continue
		}
		mid += i
		open := mid + 1
		if close, ok := mdMatchParen(s, open); ok {
			s = s[:i] + s[i+1:mid] + s[close+1:]
		} else {
			s = s[:i] + s[i+1:mid] + s[open:]
		}
	}
	return strings.NewReplacer("**", "", "__", "", "~~", "", "`", "").Replace(s)
}

// lookupAnchor 按原 href 查映射：先原样查，再按百分号解码查
// （目录里写 `[标题](#%E4%B8%80%E3%80%81…)` 与写裸中文两种都见过，映射表只存裸 slug）。
func lookupAnchor(mapping map[string]string, href string) (string, bool) {
	if v, ok := mapping[href]; ok {
		return v, true
	}
	if dec, err := url.PathUnescape(href); err == nil && dec != href {
		if v, ok := mapping[dec]; ok {
			return v, true
		}
	}
	return "", false
}

// encodeAnchorKeepStyle 百分号编码锚点：字母/数字/`-`/`_` 原样，其余逐 rune 转义
// （空格已由 mdSlug 变成 `-`，不会写出 %20）
func encodeAnchorKeepStyle(slug string) string {
	var b strings.Builder
	for _, r := range slug {
		if r == '-' || r == '_' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		b.WriteString(url.PathEscape(string(r)))
	}
	return b.String()
}

// itoaSmall 小整数转字符串（重复标题后缀 1..n）
func itoaSmall(n int) string {
	if n <= 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// mdSlug 按 GitHub 规则把标题纯文本转成锚点：小写 → 剔除标点（保留字母/数字/空格/-/_）→ 空格转 `-`。
// 字母判定用 unicode.IsLetter，故 CJK/西里尔/假名都保留（中文文档的锚点本来就是汉字）。
func mdSlug(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r == ' ' || r == '\t':
			b.WriteRune('-')
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		}
		// 其余（，。、：（）「」` *` 等）一律丢弃，与 GitHub 行为一致
	}
	return b.String()
}
