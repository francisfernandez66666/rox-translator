// ============ 本文件职责中文说明 ============
// 文本切分与拼回的**唯一实现**（★ DEF-P1-05 段落能力批，2026-11-01）。
//
// 为什么要在 engine 包里再造一份：切分这件事原来只有工单链有（`orchestrator/text_lines.go`
// 的 splitLines/nonEmptyLines/composeLineOutput），即时链（对话/工作台）根本没有段这一维——
// 长文在 `singleLang` 里按**句**切完直接**无分隔拼接**，于是模型就算翻对了，段落结构也在拼回那一步被抹平。
// 两条链各写一套切分＝本仓反复登记的「多把尺子」形态（AGENTS §一·5④／§一·11／§23），
// 所以这里把两份收成一个文件：
//
//	· **按行**（工单侧）：\n 即边界，空行保留占位——因为工单要"第 i 行对第 i 行"的逐行补译；
//	· **按段**（即时侧）：空行才是边界，连续空行只算一个界——因为散文里的换行不是新段。
//
// 两者共用同一对"切/拼"实现与同一条"非空计数"判据，差别只写进各自的常量与门限。
//
// ⚠️ 拼回的**分隔符是这一族缺陷的核心**：段间必须补 \n\n，行与行必须补 \n。
// 少补一次分隔符，症状不是报错而是"译文挤成一坨／段落数变 1"——客户看不出哪里错了半截，
// 而日志一行都不红（AGENTS §一·12 那一族「有降级链的静默失败」）。
// =============================================
package engine

import "strings"

// ============ 按行（工单链口径）============

// SplitLines 按 \n 切行并保留空行占位（末尾空行也保留，保证行数与源文一致）。
func SplitLines(s string) []string { return strings.Split(s, "\n") }

// NonEmptyLines 统计非空行数（与 qa.nonEmptyLineCount 同口径）。
// 「≥2 才值得逐行折腾」这条门限的判据就是这里这个数——两处各数一遍迟早分家。
func NonEmptyLines(s string) int {
	n := 0
	for _, l := range SplitLines(s) {
		if strings.TrimSpace(l) != "" {
			n++
		}
	}
	return n
}

// ComposeLineOutput 把逐行译文拼回整篇（行数与源文一致；行内换行压成空格，防一段变多段）。
func ComposeLineOutput(lines []string) string {
	out := make([]string, len(lines))
	for i, v := range lines {
		out[i] = strings.ReplaceAll(strings.TrimSpace(v), "\n", " ")
	}
	return strings.Join(out, "\n")
}

// ============ 按段（即时/对话链口径，★ DEF-P1-05）============

// paraSeparator 段间分隔符。**只许通过本常量拼回**——写成字面量就会长出第二把尺子
// （审校逐段化之后"段数不许变"这条判据靠的就是它）。
const paraSeparator = "\n\n"

// SplitParagraphs 按**空行**切段：一个或多个连续空行都只算一个段界（空行不独立成段），
// 段内的单换行原样保留（散文里"一行写完换行"不是新段）。返回的段都不带首尾空白。
func SplitParagraphs(s string) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		p := strings.TrimSpace(strings.Join(cur, "\n"))
		if p != "" {
			out = append(out, p)
		}
		cur = cur[:0]
	}
	for _, ln := range SplitLines(s) {
		if strings.TrimSpace(ln) == "" {
			flush()
			continue
		}
		cur = append(cur, ln)
	}
	flush()
	return out
}

// NonEmptyParagraphs 数非空段数（"单段一律不折腾"那道门吃的就是这个数）。
func NonEmptyParagraphs(s string) int { return len(SplitParagraphs(s)) }

// ComposeParagraphOutput 把逐段译文按段界拼回整篇（段数与源文一致）。
// 空段一律跳过——它本来就没有内容，硬拼会在成品里留下连续空行。
func ComposeParagraphOutput(paras []string) string {
	parts := make([]string, 0, len(paras))
	for _, p := range paras {
		if strings.TrimSpace(p) == "" {
			continue
		}
		parts = append(parts, strings.TrimSpace(p))
	}
	return strings.Join(parts, paraSeparator)
}

// ParagraphRoundTripStable 判"切完拼回去，段数不许变"——给门禁当一条可复算的不变式用，
// 也让实现者知道**这段代码的意义所在**（切分与拼回必须互逆，否则逐段补译永远对不齐段号）。
func ParagraphRoundTripStable(s string) bool {
	return NonEmptyParagraphs(ComposeParagraphOutput(SplitParagraphs(s))) == NonEmptyParagraphs(s)
}

// leadWhitespaceOf 取一段文字**开头**的空白（含换行），用于句级切分拼回时把源文的
// 段界/句间空白**原样搬回**译文（DEF-P1-05 的第二条成因：句级腿无分隔拼接把段落抹平）。
// 返回的串只可能由空格/制表/换行/回车组成；不含其它字符。
func leadWhitespaceOf(s string) string {
	i := 0
	for i < len(s) {
		switch s[i] {
		case ' ', '\t', '\n', '\r':
			i++
			continue
		}
		return s[:i]
	}
	return s[:i]
}

// trimLeadWhitespace 剥掉段首空白（与 leadWhitespaceOf 成对：一段 = 引导空白 + 正文）。
func trimLeadWhitespace(s string) string { return s[len(leadWhitespaceOf(s)):] }
