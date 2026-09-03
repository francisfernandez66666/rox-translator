// ============ detect.go · 职责说明 ============
// crawler 包「源语言自动检测」（2026-09-02 新增，支撑采集数据自动清洗）。
// 背景：此前 tier1/2/3 一律把源语言硬编码为 zh，导致 LLM/网页抓取产出的英文源文本
// 被误标为中文（如 "blended learning" 被记 src_lang=zh），污染正式库判重键与展示。
// 本文件按字符脚本粗略判定源语言，在落库前自动纠正 src_lang：
//
//	汉字/假名/谚文 → zh/ja/ko（中文按简体/繁体特征细分）
//	西里尔字母     → ru（乌克兰/哈萨克等同属西里尔，脚本层面无法细分，回退俄语）
//	阿拉伯字母     → ar，天城文 → hi，泰文 → th
//	拉丁字母       → en（英/西/法/葡等同属拉丁，回退英语）
//	其它/无脚本    → ""（调用方保留默认或空）
//
// 说明：脚本检测是「保守回退」策略——仅纠正能确定脚本的文本，无法区分同脚本语言时
// 回退最通用语言，宁可少数误判也不引入注入风险（src_lang 不拼 SQL 列名，仅入 kb_entries.source_lang）。
// =============================================
package crawler

import "unicode"

// DetectSourceLang 按字符脚本粗略检测源文本语言代码。
// 返回检测到的语言代码；无法确定（混合/纯符号/空白）时返回 ""。
// 逐字扫描统计各脚本命中数，取占优脚本决定语言。
func DetectSourceLang(text string) string {
	var han, hira, kata, hangul, cyr, arab, thai, deva, latin, other int
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Hiragana, r):
			hira++
		case unicode.Is(unicode.Katakana, r):
			kata++
		case unicode.Is(unicode.Hangul, r):
			hangul++
		case unicode.Is(unicode.Cyrillic, r):
			cyr++
		case unicode.Is(unicode.Arabic, r):
			arab++
		case unicode.Is(unicode.Thai, r):
			thai++
		case unicode.Is(unicode.Devanagari, r):
			deva++
		case unicode.Is(unicode.Latin, r):
			latin++
		default:
			other++ // 空白/标点/数字等不计入脚本证据
		}
	}
	total := han + hira + kata + hangul + cyr + arab + thai + deva + latin
	if total == 0 {
		return "" // 纯符号/空白，无法判定
	}
	// 日文特征：假名（平/片）存在即优先判定 ja（即便混有汉字）
	if hira > 0 || kata > 0 {
		return "ja"
	}
	if hangul > 0 {
		return "ko"
	}
	if han > 0 {
		return "zh" // 汉字：简体/繁体细分见 detectHanVariant（此处统一 zh）
	}
	if cyr > 0 {
		return "ru"
	}
	if arab > 0 {
		return "ar"
	}
	if thai > 0 {
		return "th"
	}
	if deva > 0 {
		return "hi"
	}
	if latin > 0 {
		return "en"
	}
	return ""
}

// detectHanVariant 汉字文本简体/繁体细分：统计常见繁体独用字，命中达到阈值判 zh_hant。
// 繁体独用字集合（简体已简化的字，如「們車國東學」等；每字出现一次即累加）。
// 阈值：繁体字比例 >= 20% 判 zh_hant，否则 zh（简体）。
// 说明：zh_hant 在 tm_segments 白名单列（AllLangs），源语言写 zh/zh_hant 均不拼列名，
// 此处细分仅用于更精确的源语言标注，不参与安全相关逻辑。
func detectHanVariant(text string) string {
	traditional := []rune("們車國東學會進過發開區時問來說愛後書員臺灣長條銀錢電腦電風問題雜誌點鐘測試電話詞匯")
	tradSet := make(map[rune]bool, len(traditional))
	for _, r := range traditional {
		tradSet[r] = true
	}
	var hanCount, tradCount int
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			hanCount++
			if tradSet[r] {
				tradCount++
			}
		}
	}
	if hanCount == 0 {
		return "zh"
	}
	if float64(tradCount)/float64(hanCount) >= 0.2 {
		return "zh_hant"
	}
	return "zh"
}