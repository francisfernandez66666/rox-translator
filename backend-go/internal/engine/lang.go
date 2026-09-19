// ============ 本文件职责中文说明 ============
// 语言解析与指令剥离：维护语言代码 → 中文/英文别名的映射（KB 语言 + 其他语言两类），
// 从用户自然语言输入中识别目标语言（ParseTargetLangs / StripLangInstruction /
// LangCodeFromName），并把"翻译成英语：xxx"这类指令从正文中剥离干净。
// ========================================
package engine

import (
	"regexp"
	"strings"
	"sync"

	"translator/internal/config"
)

// LANG_ALIASES：语言代码 → 中文/英文别名（仅 KB 语言）
var langAliases = map[string][]string{
	"en":      {"en", "english", "eng", "英文", "英语"},
	"ru":      {"ru", "russian", "rus", "俄文", "俄语"},
	"ar":      {"ar", "arabic", "阿拉伯", "阿语"},
	"es":      {"es", "spanish", "esp", "西文", "西语", "西班牙"},
	"pt":      {"pt", "portuguese", "por", "葡文", "葡语", "葡萄牙"},
	"fr":      {"fr", "french", "fra", "法文", "法语"},
	"kk":      {"kk", "kazakh", "哈语", "哈萨克"},
	"de":      {"de", "german", "deu", "德文", "德语"},
	"zh_hant": {"zh_hant", "繁体", "繁中", "繁体中文", "traditional chinese", "tc"},
}

// 其他语言别名（前端子选单或 prompt 解析用）
var otherLangAliases = map[string][]string{
	// ★ #23（2026-09-19）：新增 "zh"——外语→简体中文方向的 prompt 识别。
	//   别名刻意不收「中国」「汉语方言」等宽泛词，避免 "中国制造" 一类正文误触发；
	//   与 zh_hant 的撞词（"繁体中文" 含 "中文"）由 ParseTargetLangs 的去重规则兜住。
	"zh":  {"zh", "chinese", "simplified chinese", "mandarin", "中文", "简体中文", "简体", "普通话", "汉语"},
	"ja":  {"ja", "japanese", "日语", "日文", "日本"},
	"ko":  {"ko", "korean", "韩语", "韩文", "韩国"},
	"th":  {"th", "thai", "泰语", "泰文", "泰国"},
	"vi":  {"vi", "vietnamese", "越南语", "越南"},
	"ms":  {"ms", "malay", "马来语", "马来"},
	"id":  {"id", "indonesian", "印尼语", "印尼"},
	"tr":  {"tr", "turkish", "土耳其语", "土耳其"},
	"it":  {"it", "italian", "意大利语", "意大利"},
	"pl":  {"pl", "polish", "波兰语", "波兰"},
	"sv":  {"sv", "swedish", "瑞典语", "瑞典"},
	"hi":  {"hi", "hindi", "印地语", "印地"},
	"fa":  {"fa", "persian", "farsi", "波斯语", "波斯"},
	"he":  {"he", "hebrew", "希伯来语"},
	"el":  {"el", "greek", "希腊语", "希腊"},
	"uk":  {"uk", "ukrainian", "乌克兰语"},
	"mn":  {"mn", "mongolian", "蒙古语", "蒙古"},
	"my":  {"my", "burmese", "缅甸语"},
	"km":  {"km", "khmer", "柬埔寨语"},
	"lo":  {"lo", "lao", "老挝语"},
	"tl":  {"tl", "filipino", "tagalog", "菲律宾语"},
	"bo":  {"bo", "tibetan", "藏语"},
	"ug":  {"ug", "uyghur", "维吾尔语"},
	"yue": {"yue", "cantonese", "粤语"},
}

// AllLangAlias 合并全部别名
func AllLangAlias() map[string][]string {
	m := map[string][]string{}
	for k, v := range langAliases {
		m[k] = append(m[k], v...)
	}
	for k, v := range otherLangAliases {
		m[k] = append(m[k], v...)
	}
	return m
}

// ParseTargetLangs 从用户输入识别目标语言，返回语言代码列表（按出现顺序）
// 覆盖 KB 语言 + 其他语言别名；其他语言代码以 "other:xx" 标记，由调用方决定
func ParseTargetLangs(userInput string) []string {
	inputLower := strings.ToLower(userInput)
	var found []string
	addUnique := func(lc string) {
		for _, f := range found {
			if f == lc {
				return
			}
		}
		found = append(found, lc)
	}
	for lc, aliases := range AllLangAlias() {
		for _, alias := range aliases {
			if isASCIIWord(alias) {
				// ASCII 别名走词边界匹配
				if wordBoundaryRe(alias).MatchString(inputLower) {
					addUnique(lc)
					break
				}
			} else if len([]rune(alias)) <= 2 {
				// 短中文别名（如"英语"）：直接 contains
				if strings.Contains(inputLower, strings.ToLower(alias)) {
					addUnique(lc)
					break
				}
			} else {
				if strings.Contains(inputLower, strings.ToLower(alias)) {
					addUnique(lc)
					break
				}
			}
		}
	}
	// ★ #23 撞词去重：zh_hant 的别名「繁体中文/traditional chinese」字面包含 zh 的
	//   别名「中文/chinese」，"翻成繁体中文" 会同时命中两者。map 遍历序随机，
	//   不做兜底则目标语言列表在 zh/zh_hant 间漂移——规则：两者同现时判定用户
	//   要的是繁体（zh 更宽的词是子串误伤），丢弃 zh。
	if hasLang(found, "zh") && hasLang(found, "zh_hant") {
		out := found[:0]
		for _, lc := range found {
			if lc != "zh" {
				out = append(out, lc)
			}
		}
		found = out
	}
	return found
}

// hasLang 目标语言列表内是否含指定代码（ParseTargetLangs 去重规则用）
func hasLang(langs []string, lc string) bool {
	for _, l := range langs {
		if l == lc {
			return true
		}
	}
	return false
}

// isASCIIWord 判断字符串是否仅由 ASCII 字母/数字组成（用于区分词边界匹配与 contains 匹配）
func isASCIIWord(s string) bool {
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return len(s) > 0
}

// wordBoundaryCache 语言别名 → 词边界正则缓存（进程级复用）。
//
// ★ 并发安全（2026-08-26 全仓评审 A5）：chat/SSE 会并发触发 ParseTargetLangs，
// 此前为裸 map 并发读写，存在 `concurrent map writes` panic 面，现以互斥锁保护读改写。
var (
	wordBoundaryMu    sync.Mutex
	wordBoundaryCache = map[string]*regexp.Regexp{}
)

// wordBoundaryRe 返回 `(?i)\b<alias>\b`（RE2 支持词边界，不支持 lookbehind）
func wordBoundaryRe(alias string) *regexp.Regexp {
	aliasLower := strings.ToLower(alias)
	wordBoundaryMu.Lock()
	defer wordBoundaryMu.Unlock()
	if re, ok := wordBoundaryCache[aliasLower]; ok {
		return re
	}
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(aliasLower) + `\b`)
	wordBoundaryCache[aliasLower] = re
	return re
}

// StripLangInstruction 分离语言指令与正文，等价 lib.strip_lang_instruction
// 返回 (clean_text, langs)。langs 为 nil 表示未识别语言。
func StripLangInstruction(userInput string) (string, []string) {
	langs := ParseTargetLangs(userInput)
	if len(langs) == 0 {
		return userInput, nil
	}
	clean := userInput
	// 策略1：冒号切分（"翻译成英语：xxx"）
	if i := strings.Index(userInput, "："); i >= 0 {
		prefix := userInput[:i]
		content := strings.TrimSpace(userInput[i+3:])
		if ParseTargetLangs(prefix) != nil && content != "" {
			return content, langs
		}
	}
	if i := strings.Index(userInput, ":"); i >= 0 {
		prefix := userInput[:i]
		content := strings.TrimSpace(userInput[i+1:])
		if ParseTargetLangs(prefix) != nil && content != "" {
			return content, langs
		}
	}
	// 策略0："把X翻译成语言" / "请把X翻译成语言" 句型 —— 提取引号/书名号内的 X
	if strings.Contains(userInput, "把") || strings.Contains(userInput, "将") {
		for _, q := range []string{`"`, `“`, `「`, `『`, `《`, `<`, `'`, `‘`} {
			s := strings.Index(userInput, q)
			if s < 0 {
				continue
			}
			e := strings.Index(userInput[s+1:], q)
			if e < 0 {
				continue
			}
			inner := strings.TrimSpace(userInput[s+1 : s+1+e])
			if inner != "" {
				return inner, langs
			}
		}
	}
	// 策略2：指令动词+语言关键词剥离
	verbs := []string{"翻译成", "翻成", "译成", "翻译为", "翻为", "译为", "翻译", "翻"}
	allAliases := AllLangAlias()
	for _, v := range verbs {
		if i := strings.Index(userInput, v); i >= 0 {
			clean = userInput[i+len(v):]
			// 剥离语言名
			for _, lc := range langs {
				for _, alias := range allAliases[lc] {
					clean = strings.Replace(clean, alias, "", 1)
				}
			}
			clean = strings.TrimSpace(clean)
			clean = strings.TrimPrefix(clean, "：")
			clean = strings.TrimPrefix(clean, ":")
			clean = strings.TrimSpace(clean)
			break
		}
	}
	if clean == "" {
		return userInput, langs
	}
	return clean, langs
}

// LangCodeFromName 中文/英文名 → 语言代码
func LangCodeFromName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	for lc, aliases := range AllLangAlias() {
		for _, a := range aliases {
			if strings.ToLower(a) == name {
				return lc
			}
		}
	}
	return ""
}

// IsKBLang 是否知识库语言
func IsKBLang(lc string) bool {
	for _, l := range config.TranslateLangs {
		if l == lc {
			return true
		}
	}
	return false
}
