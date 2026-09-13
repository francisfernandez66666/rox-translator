// ============ sentence.go · 职责说明 ============
// ★ D22（2026-09-12）：超长文本句级切分（CJK 感知）+ 段落预算。
// 对话模式此前无独立切分器——超长句整体进 prompt，触发截断自修复/丢尾。
// 现按句界（。！？；!?;\n 及缩写保护）切分并按预算聚合；超长单句按 rune 硬切。
// =============================================
package engine

// chatSegmentBudgetRunes 对话翻译单段 rune 预算（约 600 汉字/句段，留足译文输出空间）
const chatSegmentBudgetRunes = 600

// chatSplitTriggerRunes 超过该长度才启用句级切分（短文本直接整译，避免无谓拆分）
const chatSplitTriggerRunes = 1200

// sentenceEndRunes 句子终止符（CJK 感知：全角优先，含换行即段落界）
var sentenceEndRunes = map[rune]bool{'。': true, '！': true, '？': true, '；': true, '\n': true, '!': true, '?': true, ';': true}

// splitSentencesCJK 按句界切分为段（保留分隔符），并按 budget 聚合。
// 缩写保护：西文句末「大写字母+.」不当作句号（如 "Dr. Smith"、"U.S.A."）。
func splitSentencesCJK(text string, budget int) []string {
	if budget <= 0 {
		budget = chatSegmentBudgetRunes
	}
	runes := []rune(text)
	var raw []string
	start := 0
	for i, r := range runes {
		if !sentenceEndRunes[r] {
			continue
		}
		if (r == '.' || r == '!') && i > 0 {
			prev := runes[i-1]
			if (prev >= 'A' && prev <= 'Z') || prev == ' ' && i > 1 && runes[i-2] >= 'A' && runes[i-2] <= 'Z' {
				continue // 缩写/序号形如 "U.S." "A ."：不切
			}
		}
		if r == ';' && i > 0 && (runes[i-1] >= '0' && runes[i-1] <= '9') {
			continue // 数字后分号（如 HTML 实体残留 &amp; 边界）防误切：保守跳过
		}
		raw = append(raw, string(runes[start:i+1]))
		start = i + 1
	}
	if start < len(runes) {
		raw = append(raw, string(runes[start:]))
	}
	// 聚合到预算 + 超长单句硬切
	var out []string
	acc := ""
	for _, s := range raw {
		for len([]rune(s)) > budget { // 单句超预算：硬切
			if acc != "" {
				out = append(out, acc)
				acc = ""
			}
			sr := []rune(s)
			out = append(out, string(sr[:budget]))
			s = string(sr[budget:])
		}
		if acc == "" {
			acc = s
			continue
		}
		if len([]rune(acc))+len([]rune(s)) <= budget {
			acc += s
		} else {
			out = append(out, acc)
			acc = s
		}
	}
	if acc != "" {
		out = append(out, acc)
	}
	return out
}

// splitForChatTranslate 对话翻译入口切分：短文本原样单段；超长按句切分。
func splitForChatTranslate(text string) []string {
	if len([]rune(text)) <= chatSplitTriggerRunes {
		return []string{text}
	}
	return splitSentencesCJK(text, chatSegmentBudgetRunes)
}
