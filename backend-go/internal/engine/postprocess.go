// ============ 本文件职责中文说明 ============
// 翻译后处理清洗链（复刻 Python lib.py）：所有翻译路径的最终清洗入口。
// 包括：去除开头语言名前缀、非 CJK 目标语删除残留中文字符、
// 按书写系统过滤非目标语言段落（拉丁系按"语言名独占一行"截断）、
// 品牌替换（Jishi→ROX）、en 专属短语修正，以及源语言检测（DetectSourceLang）
// 与汉字抽取（ExtractCJK）等工具函数。
// ========================================
package engine

import (
	"regexp"
	"strings"
)

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

// StripChineseInNonZh 非 CJK 目标语删除所有中文字符
func StripChineseInNonZh(text, langCode string) string {
	if langCode == "zh_hant" || langCode == "ja" || langCode == "ko" {
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

// PostProcessTranslation 最终后处理（所有翻译路径必须经过）
func PostProcessTranslation(text, langCode string) string {
	// 品牌替换：Jishi/jishi → ROX
	re := regexp.MustCompile(`(?i)\bJishi\b`)
	text = re.ReplaceAllString(text, "ROX")

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
	return strings.TrimSpace(text)
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
