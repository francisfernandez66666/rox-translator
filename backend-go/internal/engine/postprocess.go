// ============ 本文件职责中文说明 ============
// 翻译后处理清洗链（复刻 Python lib.py）：所有翻译路径的最终清洗入口。
// 包括：去除开头语言名前缀、非 CJK 目标语删除残留中文字符、
// 按书写系统过滤非目标语言段落（拉丁系按"语言名独占一行"截断）、
// 品牌替换（Jishi→ROX）、en 专属短语修正，以及源语言检测（DetectSourceLang）
// 与汉字抽取（ExtractCJK）等工具函数。
// 2026-09-18 追加：模型「伪标签回显」与「退化复读」清洗（stripPseudoTags /
// collapseDegenerateRepeats），以及仅供观测计数的 IsSuspectOutput（不得据此丢弃译文）。
// ========================================
package engine

import (
	"log"
	"regexp"
	"strings"
	"sync"
)

// ============ 退化输出观测计数（★ P0-4，2026-09-18） ============
// IsSuspectOutput 的检出结果除了日志，还必须进入 /metrics——否则模型退化只能靠
// 人工翻日志发现。本包是无 ctx 纯函数链，故用包级锁保护计数，由 api/metrics.go
// 拉快照渲染成 translator_suspect_output_total{lang}。

// suspectMu 保护 suspectCounts 的写与快照拷贝。
var suspectMu sync.Mutex

// suspectCounts：目标语言码 → 退化输出检出次数。
var suspectCounts = map[string]int64{}

// recordSuspectOutput 累加一次某语言的退化检出。参数 langCode: 目标语言码。
func recordSuspectOutput(langCode string) {
	suspectMu.Lock()
	defer suspectMu.Unlock()
	suspectCounts[langCode]++
}

// SuspectOutputSnapshot 返回退化输出计数快照（拷贝，调用方可安全遍历）。
func SuspectOutputSnapshot() map[string]int64 {
	suspectMu.Lock()
	defer suspectMu.Unlock()
	out := make(map[string]int64, len(suspectCounts))
	for k, v := range suspectCounts {
		out[k] = v
	}
	return out
}

// ============ 语言后处理清洗链（复刻 Python lib.py） ============

var (
	// 汉字字符
	cjkRe = regexp.MustCompile("[\u4e00-\u9fff\u3400-\u4dbf]")

	// 语言名称前缀（strip_lang_prefix）
	langPrefixRe = map[string]*regexp.Regexp{
		"en":      regexp.MustCompile(`(?i)^\s*(en|english|英语|英文)\s*[：:]\s*`),
		"ru":      regexp.MustCompile(`(?i)^\s*(ru|russian|俄语|俄文|俄)\s*[：:]\s*`),
		"ar":      regexp.MustCompile(`(?i)^\s*(ar|arabic|阿拉伯语|阿拉伯|阿语)\s*[：:]\s*`),
		"es":      regexp.MustCompile(`(?i)^\s*(es|spanish|西班牙语|西语|西文)\s*[：:]\s*`),
		"pt":      regexp.MustCompile(`(?i)^\s*(pt|portuguese|葡萄牙语|葡语|葡文)\s*[：:]\s*`),
		"fr":      regexp.MustCompile(`(?i)^\s*(fr|french|法语|法文|法)\s*[：:]\s*`),
		"kk":      regexp.MustCompile(`(?i)^\s*(kk|kazakh|哈萨克语|哈语)\s*[：:]\s*`),
		"de":      regexp.MustCompile(`(?i)^\s*(de|german|德语|德文|德)\s*[：:]\s*`),
		"zh_hant": regexp.MustCompile(`(?i)^\s*(zh[-_]hant|繁体|繁体中文|tc)\s*[：:]\s*`),
	}

	// 非 CJK 目标语删除中文字符（_strip_chinese_in_non_zh）
	zhCharRe = regexp.MustCompile("[\u4e00-\u9fff\u3400-\u4dbf]+")

	// 拉丁系语言（用语言名独占一行截断）
	latinScriptLangs = map[string]bool{"en": true, "es": true, "pt": true, "fr": true,
		"de": true, "vi": true, "ms": true, "id": true, "it": true, "pl": true,
		"sv": true, "nl": true, "cs": true, "ro": true, "hu": true, "fi": true,
		"da": true, "no": true, "tr": true, "fil": true}

	// 书写系统正则
	cyrillicRe = regexp.MustCompile("[\u0400-\u04FF]")
	arabicRe   = regexp.MustCompile("[\u0600-\u06FF]")
	hangulRe   = regexp.MustCompile("[\uAC00-\uD7AF]")
	thaiRe     = regexp.MustCompile("[\u0E00-\u0E7F]")
	hebrewRe   = regexp.MustCompile("[\u0590-\u05FF]")
	greekRe    = regexp.MustCompile("[\u0370-\u03FF]")
	latnRe     = regexp.MustCompile("[A-Za-z]")

	// 语言→书写系统
	scriptMap = map[string]string{
		"en": "latin", "es": "latin", "pt": "latin", "fr": "latin", "de": "latin",
		"vi": "latin", "ms": "latin", "id": "latin", "it": "latin", "pl": "latin",
		"sv": "latin", "nl": "latin", "tr": "latin", "fil": "latin",
		"ru": "cyrillic", "kk": "cyrillic", "uk": "cyrillic", "mn": "cyrillic",
		"ar": "arabic", "fa": "arabic", "ur": "arabic",
		"zh_hant": "cjk", "ja": "cjk",
		"ko": "hangul",
		"th": "thai",
		"he": "hebrew",
		"el": "greek",
	}
)

// StripLangPrefix 去除翻译结果开头的语言名前缀（如 "英语："、"ru:"）
func StripLangPrefix(text, langCode string) string {
	if re, ok := langPrefixRe[langCode]; ok {
		return re.ReplaceAllString(text, "")
	}
	return text
}

// markerBracketRe 匹配单条审校模板标记方括号（【原文】/【译文】/【待审校译文】/【待審校譯文】
// 及 [] 变体，标记内仅允许空白），用于剥离 LLM 误带入输出中的模板标记。
// 仅匹配「纯标记」（括号内只有标记词），绝不误删正常译文中的普通中括号内容（如【贵宾】【2】）。
var markerBracketRe = regexp.MustCompile(`[\[【]\s*(?:原文|译文|譯文|待审校译文|待審校譯文)\s*[\]】]`)

// stripReviewMarkers 剥离 LLM 误带入单句译文的批量审校模板标记（【原文】/【译文】/【待審校譯文】）。
// 规则：若最后一个标记之后仍有非空内容，说明那是模型输出的审校后译文，直接取其后续内容；
// 否则整段都是模板残留，删除全部标记后返回剩余文本。[] 与【】变体统一处理。
func stripReviewMarkers(text string) string {
	idx := markerBracketRe.FindAllStringIndex(text, -1)
	if len(idx) == 0 {
		return text
	}
	last := idx[len(idx)-1]
	if after := strings.TrimSpace(text[last[1]:]); after != "" {
		return after
	}
	return strings.TrimSpace(markerBracketRe.ReplaceAllString(text, ""))
}

// emptyBracketRe 匹配成对的空占位方括号（【】，【 】，[]，[ ]，中间可含空白），
// 用于剥离模型误输出的占位符号。带内容（非空白字符）的方括号不受影响。
var emptyBracketRe = regexp.MustCompile(`[\[【][\s\x{3000}]*[\]】]`)

// cjkPunctRe 匹配 CJK 全角/中文标点（全角括号、引号、逗号、句号、冒号等）。
// 用于在非 CJK 目标语中识别「编辑注释/术语对照」残留块并整体截断（见 stripTrailingCJKNotes）。
var cjkPunctRe = regexp.MustCompile("[\u3000-\u303f\uff00-\uffef\u2018\u2019\u201c\u201d\u2026]")

// stripEmptyPlaceholderBrackets 剥离成对的空占位方括号（含内部纯空白），
// 修复 LLM 在译文开头输出『【】』占位符未被清理的问题。
func stripEmptyPlaceholderBrackets(text string) string {
	return emptyBracketRe.ReplaceAllString(text, "")
}

// leadingCJKNoteRe 匹配以 CJK 全角标点开头的行——中文「编辑注释/术语对照」残留块
// 经 StripChineseInNonZh 去汉字后的骨架行（如 （：1. "…"；2. "…"。）行首仍保留
// 全角括号/冒号，而正常目标语（拉丁系）译文行绝不会以全角标点开头。
var leadingCJKNoteRe = regexp.MustCompile(`^[\x{3000}-\x{303f}\x{ff00}-\x{ffef}\x{2018}\x{2019}\x{201c}\x{201d}\x{2026}]`)

// leadingCJKNoteStrictRe 严格版行首特征（CJK 目标语专用）：行首为「可选开括号+全角冒号」
// 的纯标点骨架（（：/：/【：）。中文/日文正常译文行几乎不可能以全角冒号开头，
// 而模型注释块（（：1. "…"；2. "…"）以此开头。
// ★ 刻意不含「注：/说明：」等中文词头——源文本本身可能含有「说明：xxx」行，剥离会误删正文。
var leadingCJKNoteStrictRe = regexp.MustCompile(`^[（\[【]?\s*：`)

// stripTrailingCJKNotes 剥离非 CJK 目标语译文末尾的「编辑注释/术语对照」残留块。
// 现象：模型在译文后追加中文说明（如 术语对照/替换说明），经 StripChineseInNonZh
// 去掉汉字后可能剩两种形态：
//   - 纯全角标点骨架（如 "（：，：\n1. ：…"），不含字母/数字；
//   - 讲解式骨架（如 （：1. "verification marks""verification code"；2. …），
//     含英文单词与序号数字——旧判定（不含字母且不含数字）漏网，混入译文触发
//     QA 数字一致性误报（源=[] 译=[1 2 3]，工单 T20260909111254JZ7）。
//
// 规则：逐行扫描，命中以下任一特征即视为注释块起始行，从该行起截断丢弃：
//   - 不含拉丁字母且不含数字、但含 CJK 全角标点（纯骨架行）；
//   - 以 CJK 全角标点开头（讲解式残留行，其后仍含英文/数字）。
//
// 不影响正常目标语行（含字母/数字、且不以全角标点开头）。
func stripTrailingCJKNotes(text string) string {
	lines := strings.Split(text, "\n")
	digitRe := regexp.MustCompile(`[0-9]`)
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if (!latnRe.MatchString(t) && !digitRe.MatchString(t) && cjkPunctRe.MatchString(t)) ||
			leadingCJKNoteRe.MatchString(t) {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\n")
		}
	}
	return text
}

// stripTrailingCJKNotesStrict CJK 目标语（zh/zh_hant/ja/ko）专用注释块截断：
// 这些目标语的译文本身就是 CJK 文字，无法用「去中文后剩骨架」的特征识别注释块，
// 仅以严格版行首特征（开括号+全角冒号的纯标点骨架）截断——正常译文行不会这样开头。
// 参数：text=译文；返回截断后的译文。
func stripTrailingCJKNotesStrict(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if leadingCJKNoteStrictRe.MatchString(t) {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), " \t\n")
		}
	}
	return text
}

// contractTagRe 输出契约提取正则：<t>（容忍属性与大小写）…</t>，跨行匹配。
// 用于白名单提取译文：标签内是译文本体，标签外的注释/术语对照/说明天然被丢弃。
var contractTagRe = regexp.MustCompile(`(?is)<t\b[^>]*>(.*?)</t\s*>`)

// extractContractTranslation 输出契约白名单提取：
// 模型按契约把最终译文用 <t>…</t> 包裹时，只取标签内内容（标签外的注释块整体丢弃）；
// 未命中契约（模型违约）时原样返回，交由后续黑名单清洗链兜底——不会比无契约时更差。
// 多个 <t> 块（如按段分块）以换行拼接保留。参数：text=模型原始输出；返回提取后的译文。
func extractContractTranslation(text string) string {
	if !strings.Contains(strings.ToLower(text), "<t") {
		return text
	}
	matches := contractTagRe.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return text
	}
	var parts []string
	for _, m := range matches {
		if s := strings.TrimSpace(m[1]); s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return text
	}
	return strings.Join(parts, "\n")
}

// StripChineseInNonZh 非 CJK 目标语删除所有中文字符
func StripChineseInNonZh(text, langCode string) string {
	// ★ 2026-09-09 修复：守卫遗漏 "zh"——互译（如 en→zh）目标为简体中文时，
	//   汉字是译文本体，漏加 zh 会把整段中文译成只剩标点（实测 "…该操作。"→"，。"）。
	if langCode == "zh" || langCode == "zh_hant" || langCode == "ja" || langCode == "ko" {
		return text
	}
	return zhCharRe.ReplaceAllString(text, "")
}

// StripForeignParagraphs 删除不属于目标语言书写系统的段落
func StripForeignParagraphs(text, langCode string) string {
	script, ok := scriptMap[langCode]
	if !ok {
		return text
	}
	if script == "latin" {
		return stripLangNameSections(text, langCode)
	}
	lines := strings.Split(text, "\n")
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if lineBelongsToScript(line, script) {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

// lineBelongsToScript 判断某行是否属于目标语言书写系统（按各书写系统正则检查；
// 西里尔/阿拉伯语额外要求不含汉字，CJK 允许含谚文）
func lineBelongsToScript(line, script string) bool {
	switch script {
	case "cyrillic":
		return cyrillicRe.MatchString(line) && !cjkRe.MatchString(line)
	case "arabic":
		return arabicRe.MatchString(line) && !cjkRe.MatchString(line)
	case "cjk":
		return cjkRe.MatchString(line) || hangulRe.MatchString(line)
	case "hangul":
		return hangulRe.MatchString(line)
	case "thai":
		return thaiRe.MatchString(line)
	case "hebrew":
		return hebrewRe.MatchString(line)
	case "greek":
		return greekRe.MatchString(line)
	}
	return true
}

// 拉丁系：按"语言名独占一行"截断
var latinLangNameLines = map[string][]string{
	"en": {"英语", "英文", "俄语", "俄文", "阿拉伯语", "西班牙语", "葡萄牙语", "法语", "德语"},
}

// stripLangNameSections 拉丁系语言截断处理：逐行遍历，若某行是纯"语言名"行
// （如"英语""俄语"）且非目标语言，则视为模型输出的多余开头，截断至此；
// 目标语言名行则跳过。用于清理模型额外输出的语言名称。
func stripLangNameSections(text, langCode string) string {
	// 简单实现：遍历行，若某行是"语言名"且非目标语言，截断于此
	names := latinLangNameLines[langCode]
	_ = names
	lines := strings.Split(text, "\n")
	var out []string
	targetCN := map[string]bool{}
	switch langCode {
	case "en":
		targetCN = map[string]bool{"英语": true, "英文": true}
	case "es":
		targetCN = map[string]bool{"西班牙语": true, "西语": true}
	case "pt":
		targetCN = map[string]bool{"葡萄牙语": true, "葡语": true}
	case "fr":
		targetCN = map[string]bool{"法语": true}
	case "de":
		targetCN = map[string]bool{"德语": true}
	}
	allNames := []string{"英语", "英文", "俄语", "俄文", "阿拉伯语", "西班牙语", "西语", "葡萄牙语", "葡语", "法语", "德语"}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isName := false
		for _, n := range allNames {
			if trimmed == n {
				isName = true
				break
			}
		}
		if isName && !targetCN[trimmed] {
			break // 遇到非目标语言名行，截断
		}
		if isName && targetCN[trimmed] {
			continue // 目标语言名行，跳过
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// pseudoTagRe 伪标签（模型回显指令词元当标签）识别正则。
// 背景：输出契约要求模型用 <t>…</t> 包裹译文（含 "wrap ONLY the final translation in
// <t> and </t> tags" 这类指令）。当待译文本是**无上下文的短串**（表格序号列 "#"、"1"、
// "2"，项目符号 "•"）时，模型无法判断该翻什么，转而回显指令里的词元当标签。
// 实测工单（PDF《翻译助手 2.0 · 业务集成方案》，16 页 → 35 页那次）交付 PDF 里
// 残留：<only>•••</only> ×128、<target>#></target>、<target language>1></target language>、
// <tt>The content</tt>、孤立 </t> —— 全部是 ASCII 标签，StripChineseInNonZh 删不掉。
//
// pseudoTagName 可拆词元的**白名单**（而非「见到尖括号标签就拆」的通用清洗）：
// 只收 prompt 契约里可能被模型回显成标签的词元。译文中合法出现的 HTML/XML 样例
// （<div class="x">、<b>、SDK 文档片段）必须原样交付，通用标签清洗会把它们一并拆没，
// 故新增词元时须逐个确认它确实出现在指令文本里，不要图省事改成 `[^<>]+`。
const pseudoTagName = `t|tt|target|source|only|output|result|translate|translation|answer|instruction|prompt|system|user|assistant|text|lang|language`

// pseudoTagRe 的 \b 用于「按完整词元匹配」：否则 `<target>` 会被 alternation 里的 `t`
// 抢下前半，留下 `arget>` 残渣反而污染正文；[^<>]{0,60} 则容忍 `<target language>`
// 这类带空格/属性的写法（词元后杂串），只剥标签本身、不动标签外的任何正文字符。
var pseudoTagRe = regexp.MustCompile(`(?i)</?\s*(?:` + pseudoTagName + `)\b[^<>]{0,60}>`)

// pseudoPairRe 成对但走形的伪标签：`<TAG>BODY></TAG>`（模型启止标签之间多打一个 `>`）。
// 实测 `<target>#></target>`、`<target language>1></target language>` —— 表格序号列的值。
// 注：Go RE2 单次重复上限为 1000，标签体长度上限取 900（远超实际表格单元格取值）。
var pseudoPairRe = regexp.MustCompile(`(?is)<\s*(?:` + pseudoTagName + `)\b[^<>]{0,60}>\s*([^<>]{0,900}?)\s*>\s*</\s*(?:` + pseudoTagName + `)\b[^<>]{0,60}>`)

// strayTagBodyRe 走形标签体：模型把契约标签与译文黏在一起，正文被吞进标签体内。
// 实测 `<The system matches the built-in automotive translation database (including over
// 3300 vehicle prompts>).</t>` —— `<T` 起手、正文全在体内、末尾一个 `>` 收口。
// 该形态极易与「小于号比较」混淆（如 "a < b > c"），故仅在标签体严格符合
// 「首字符是字母/汉字 + 含空格 + 长度 20–400 + 首尾非空格」时才认定（见 stripPseudoTags）。
var strayTagBodyRe = regexp.MustCompile(`<([^<>]*\s[^<>]*)>`)

// stripPseudoTags 拆除模型回显/走形的伪标签，保留标签体内的正文。
// 正常译文（中/英/日/韩/德…）不含尖括号标签；「小于号比较」这类正文由
// strayTagBodyRe 的严格签名字形把关，故本规则对正常输出零影响。
func stripPseudoTags(text string) string {
	// 快路径：绝大多数译文不含 '<'，直接返回，避免三条正则在百万级段落上空转
	if !strings.Contains(text, "<") {
		return text
	}
	// 1. 成对走形伪标签：<target>#></target> → #（必须早于第 3 步，否则标签先被剥成
	//    `#>` 或整体吞掉，正文丢失）
	text = pseudoPairRe.ReplaceAllString(text, "$1")
	// 2. 走形标签体：<正文…> → 正文（严格签名，杜绝误拆小于号）
	text = strayTagBodyRe.ReplaceAllStringFunc(text, func(m string) string {
		body := m[1 : len(m)-1]
		runes := []rune(body)
		// 长度门槛 20–400（按 rune 计，CJK 一字一算，按字节会把中文短句虚判成 60 字符）：
		// 被吞进体内的是「整句译文」，短于此的尖括号串多为真比较式或属性标签
		// （如 `<div class="x">` 含空格但仅 13 字符）。
		if len(runes) < 20 || len(runes) > 400 {
			return m
		}
		// 必须含空格且首尾非空格：单 token 的尖括号串（`<a>`、`<100>`）不是被吞的正文。
		if !strings.Contains(body, " ") || strings.HasPrefix(body, " ") || strings.HasSuffix(body, " ") {
			return m
		}
		// 首字符须是拉丁字母或 CJK（U+2E80 起为部首/假名/汉字区）：以数字、符号起手的
		// 尖括号串是数值区间或代码片段，不属于「模型把译文塞进标签体」的形态。
		first := runes[0]
		isLetter := (first >= 'a' && first <= 'z') || (first >= 'A' && first <= 'Z') || first > 0x2E80
		if !isLetter {
			return m
		}
		return body
	})
	// 3. 规范伪标签与孤立开/闭标签（<only> / <target language> / <tt> / 残留 <t> / 孤立 </t>）
	text = pseudoTagRe.ReplaceAllString(text, "")
	return text
}

// collapseDegenerateRepeats 折叠退化复读：同一片段（1–8 字符）连续重复 ≥8 次时只保留一次。
// 实测 <only>•••</only> 拆标签后残留 "•••" ×128 —— 模型在无上下文短串上进入复读循环。
// 保守起见：仅当重复块总长 ≥24 字符、且重复单元不是纯分隔符（- = _ * # ~）时折叠。
func collapseDegenerateRepeats(text string) string {
	r := []rune(text)
	n := len(r)
	if n < 24 {
		return text
	}
	same := func(a, b, u int) bool {
		return string(r[a:a+u]) == string(r[b:b+u])
	}
	var b strings.Builder
	for i := 0; i < n; {
		done := false
		// u=重复单元长度，从 1 起**由短到长**试：贪心保证按最小周期折叠
		// （384 个连续 "•" 折成 1 个而不是 128 个 "•••"）；
		// 循环条件 i+u*8<=n 是「至少还容得下 8 次重复」的剪枝，越界直接进下一字符。
		for u := 1; u <= 8 && i+u*8 <= n; u++ {
			cnt := 1
			for i+(cnt+1)*u <= n && same(i, i+cnt*u, u) {
				cnt++
			}
			// 双重阈值（次数 ≥8 且重复块总长 ≥24）：只满足其一的多是正常排版
			// （连续 8 个破折号、重复几次的短语），长度门槛把误伤面压到最低。
			if cnt < 8 || cnt*u < 24 {
				continue
			}
			unit := string(r[i : i+u])
			if isSeparatorUnit(unit) {
				continue
			}
			b.WriteString(unit)
			i += cnt * u
			done = true
			break
		}
		if !done {
			b.WriteRune(r[i])
			i++
		}
	}
	return b.String()
}

// isSeparatorUnit 重复单元是否纯分隔符（正常文档里的分隔线/表格横线，不应折叠）
func isSeparatorUnit(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	// 白名单比函数头列举的多含 `·`（中文间隔号，如「翻译助手 · 业务集成方案」）、
	// `.` 与空格：表格横线、目录点引线、"...." 省略式排版都是成串合法重复。
	return strings.Trim(s, "-=_*#~·. ") == ""
}

// IsSuspectOutput 判定「模型原始输出被伪标签/退化复读污染」——**仅用于观测与诊断**。
//
// ⚠️ 不要据此丢弃译文。源文本身就是符号/编号的单元格（如 "#"、"1"、"•"）其正确译文
// 就是同一个符号；而模型在这类无上下文短串上最容易回显 `<target>#></target>` 这类伪
// 标签，清洗后恰好得到正确结果。若把这些判为可疑并丢弃，等于把正确译文判成未译出。
// 因此仅供日志/告警计数使用；译文的清洗与交付由 PostProcessTranslation 负责。
//
// 命中任一：空内容、伪标签残留、超长退化复读。注意此处复用 stripPseudoTags 的判定
// 口径（含走形标签体的严格签名字形校验），不要直接用 strayTagBodyRe.MatchString——
// 那会把 "a < b > c" 这类正常正文误判为可疑。
func IsSuspectOutput(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return true
	}
	if stripPseudoTags(t) != t {
		return true
	}
	return collapseDegenerateRepeats(t) != t
}

// PostProcessTranslation 最终后处理（所有翻译路径必须经过）
func PostProcessTranslation(text, langCode string) string {
	// ★ 输出契约白名单提取（2026-09-09）：prompt 要求模型用 <t>…</t> 包裹最终译文，
	//   命中契约时只取标签内内容——标签外的注释/术语对照块整体丢弃（结构性根治「译文后
	//   追加讲解」这一类问题，对 zh/zh_hant/ja/ko 等黑名单无法区分注释与正文的目标语同样有效）。
	//   未命中契约（模型违约）时原样返回，走既有黑名单清洗链兜底。
	text = extractContractTranslation(text)

	// ★ 污染观测哨（2026-09-18）：判定必须在**清洗前**做——清洗本身就是把噪声抹掉，
	//   抹干净后再判就永远判不出来；但必须在契约层剥离**之后**——<t>…</t> 是 prompt 要求
	//   的合法包裹，不算污染，若按原始响应判定则每条正常译文都会被误记为退化、日志失效。
	raw := text
	// 日志口径说明：本包未接 observability（其 Info/Warn 需要 ctx 取 trace_id），而
	// PostProcessTranslation 是无 ctx 的纯函数、调用点全在翻译主链内，为一条观测日志
	// 改签名不划算，故沿用包内既有的标准库 log（AGENTS 的 slog 收敛是独立改造项）。
	defer func() {
		if IsSuspectOutput(raw) {
			// ★ P0-4（2026-09-18）：日志之外同步计入 /metrics 计数器，供告警消费
			recordSuspectOutput(langCode)
			log.Printf("[postprocess] 检出疑似模型退化输出（已按规则清洗）lang=%s raw=%.80q", langCode, raw)
		}
	}()

	// ★ 伪标签拆除（2026-09-18）：上一步「模型违约即原样返回」是个漏洞——违约输出里
	//   常带指令词元回显（<only>/<target>/<tt>），是 ASCII 标签，后面所有黑名单清洗
	//   （去中文、剥空括号、注释截断）都拦不住，会一路写进交付文件。此处无条件拆除。
	text = stripPseudoTags(text)
	// ★ 退化复读折叠（2026-09-18）：同片段连续重复 ≥8 次（如 "•••" ×128）折叠为一次。
	text = collapseDegenerateRepeats(text)

	// 品牌替换：Jishi/Jieshi/Jixi 及变体（极石汽车拼音直译）→ ROX
	text = brandReplace(text)

	// ★ 重复连词折叠（2026-09-11）：模型偶发连续输出 "and and and" 等重复连词，
	//   折叠为单次（如 "and and and" → "and"），避免译文出现无意义重复。
	text = collapseRepeatedConjunctions(text)

	// 需求3：剥离误带入单句译文的批量审校模板残留（【原文】…【待審校譯文】…）
	text = stripReviewMarkers(text)

	// ★ 占位残留剥离（2026-09-02 实测：xlsx 译文单元格出现『【】Please perform…』——
	//   模型被要求"不要输出【】等占位符号"却回了个空方括号对）。仅剥离成对的空
	//   占位括号（含可选空格/换行），绝不误伤带内容的方括号（如【贵宾】【2】）。
	text = stripEmptyPlaceholderBrackets(text)

	// en 专属 6 组短语修正
	if langCode == "en" {
		enFixes := []struct{ from, to string }{
			{"shift to online", "shift online"},
			{"turn on air conditioning", "turn on the AC"},
			{"adjust the volume", "adjust volume"},
		}
		for _, f := range enFixes {
			text = strings.ReplaceAll(text, f.from, f.to)
		}
	}
	// 非 CJK 目标语删除中文字符
	text = StripChineseInNonZh(text, langCode)
	// ★ 二次空占位清扫（2026-09-02 符号残留根因修复）：中文删除可能把残留标记
	//   变成新的空方括号对（如【原文】→【】），兜底再剥离一次，确保成品无空占位括号残留。
	text = stripEmptyPlaceholderBrackets(text)
	// ★ 注释残留块截断（2026-09-03）：模型在译文后追加的中文「编辑注释/术语对照」
	//   经去中文后剩全角标点骨架，形如乱码——按行截断丢弃（不影响正常含字母/数字行）。
	// ★ CJK 目标语（zh/zh_hant/ja/ko，2026-09-09）：去中文语义不适用（译文即中文），
	//   改用严格版行首特征（开括号+全角冒号纯标点骨架，如 （：1. …）截断注释块。
	if langCode != "zh" && langCode != "zh_hant" && langCode != "ja" && langCode != "ko" {
		text = stripTrailingCJKNotes(text)
	} else {
		text = stripTrailingCJKNotesStrict(text)
	}
	return strings.TrimSpace(text)
}

// brandReplace 品牌替换：极石汽车（Jishi/Jieshi/Jixi 等拼音变体）→ ROX。
// 兼容模型常见的直译拼音写法，避免品牌名被音译成 jieshi/jixi 等未命中知识库。
func brandReplace(text string) string {
	// 覆盖：jishi / jieshi / jixi / ji shi / ji-shi（大小写不敏感）
	re := regexp.MustCompile(`(?i)\b(?:jishi|jieshi|jixi|ji[ -]?shi)\b`)
	text = re.ReplaceAllString(text, "ROX")
	// 中文品牌名直译残留（极石）也替换为 ROX（面向非中文目标语时中文残留本应被删）
	text = strings.ReplaceAll(text, "极石", "ROX")
	return text
}

// collapseRepeatedConjunctions 折叠译文中重复出现的连词（如 "and and and" → "and"）。
// 模型偶发在多句翻译时连续输出相同连词，导致译文出现无意义重复。覆盖常见西文连词
// （and/or/but/with/for/nor/yet/so），每词连续出现 2+ 次即折叠为单次。
func collapseRepeatedConjunctions(text string) string {
	conjunctions := []string{"and", "or", "but", "with", "for", "nor", "yet", "so", "the", "a", "an"}
	for _, conj := range conjunctions {
		// 连续重复：conj + (空格 + conj) * N（2+ 次重复 → 折叠为 1 次）
		pattern := regexp.MustCompile(`(?i)\b(` + regexp.QuoteMeta(conj) + `\b(?:\s+\b` + regexp.QuoteMeta(conj) + `\b){2,})`)
		text = pattern.ReplaceAllString(text, conj)
	}
	return text
}

// DetectSourceLang 检测源语言：CJK 占比 >25% 视为中文；否则按非空字符的主导文字
// 判定（韩/日/阿/俄），避免把俄/阿/韩/日误判为中文或英文（原实现只返回 zh/en）。
func DetectSourceLang(text string) string {
	cjk := 0
	hangul, jp, ar, ru, other := 0, 0, 0, 0, 0
	for _, c := range text {
		switch {
		case cjkRe.MatchString(string(c)):
			cjk++
		case c >= 0xAC00 && c <= 0xD7A3, c >= 0x1100 && c <= 0x11FF, c >= 0x3130 && c <= 0x318F:
			hangul++ // 谚文
		case c >= 0x3040 && c <= 0x30FF:
			jp++ // 平假名/片假名
		case c >= 0x0600 && c <= 0x06FF, c >= 0x0750 && c <= 0x077F, c >= 0x08A0 && c <= 0x08FF, c >= 0xFB50 && c <= 0xFDFF, c >= 0xFE70 && c <= 0xFEFF:
			ar++ // 阿拉伯文
		case c >= 0x0400 && c <= 0x052F:
			ru++ // 西里尔文
		case c >= 0x80:
			other++ // 全角标点/数字/重音字母/符号：非真实文字脚本
		}
	}
	// ★ 整改（成本表漏译根因）：other（全角数字/标点/符号）不应稀释中文判定。
	// 仅以真实文字脚本（CJK/谚文/假名/阿/俄）计总数，避免「单价￥１２３．４５」式
	// 中文单元格因全角数字占比高被误判为 en→en 导致模型原样回显、段未译出。
	scriptTotal := cjk + hangul + jp + ar + ru
	if scriptTotal == 0 {
		return "en" // 纯 ASCII / 纯符号默认英文（数字单元格本就不翻译）
	}
	if float64(cjk)/float64(scriptTotal) > 0.2 {
		return "zh"
	}
	// 否则取主导的非中文脚本
	bestN, bestCode := 0, "en"
	for _, s := range []struct {
		n    int
		code string
	}{{hangul, "ko"}, {jp, "ja"}, {ar, "ar"}, {ru, "ru"}} {
		if s.n > bestN {
			bestN, bestCode = s.n, s.code
		}
	}
	// 仅 other 而无真实脚本时回退英文，不把全角符号当外文脚本
	if bestN == 0 {
		return "en"
	}
	return bestCode
}

// HasCJK 是否含中文
func HasCJK(text string) bool {
	return cjkRe.MatchString(text)
}

// ExtractCJK 抽出全部汉字
func ExtractCJK(text string) string {
	return strings.Join(cjkRe.FindAllString(text, -1), "")
}
