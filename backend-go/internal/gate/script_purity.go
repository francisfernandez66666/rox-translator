// ============ script_purity.go · 职责说明（2026-11-23 用户令三项升级之二）============
// 「无拉丁转写」与「标点体系」两道硬闸的唯一实现体（tv13 外部审计坐实：
// L151 `12-komponentnaya framework` 俄语转写、9 行全角标点残留）。
//
// 射程刻意收窄：只对**非拉丁书写系统**的目标语生效（he/ru/ar/fa/hi/th）——
// zh/ja/ko 的全角标点是目标语正常形态，绝不误杀；拉丁目标语天然无此问题。
// 白名单单一口径：内置技术词表 ∪ system_config `latin_allowlist_<lang>`（运营自助扩）
// ∪ 命中 KB 词条 target_text 里的拉丁串。全角标点容忍度走 `cjk_punct_max`（默认 0=全拦），
// 且 KB 词条 target_text 里出现过的标点字符按词条豁免。
// ================================================
package gate

import (
	"regexp"
	"strings"
	"sync"
)

// nonLatinScriptTarget 目标语书写系统不是拉丁字母的那些语种。
// 与 textscript.LangToScript 的口径对齐，但刻意只收「审计射程内」的语种：
// ko/ja/zh 归 CJK（全角标点是正常形态），不在此列。
func nonLatinScriptTarget(lang string) bool {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "he", "ru", "kk", "uk", "mn", "ar", "fa", "ur", "ug", "hi", "gu", "mr", "bn", "te", "ta", "th", "el":
		return true
	}
	return false
}

// builtinLatinAllow 内置技术词白名单（小写）。选词口径：现网文档真实出现、且是
// 「产品/品牌/协议名」而非可翻译词——像 framework/komponentnaya 这种可译词绝不入表。
var builtinLatinAllow = map[string]bool{
	"google": true, "facebook": true, "instagram": true, "tiktok": true,
	"yandex": true, "bing": true, "whatsapp": true, "linkedin": true,
	"lookalike": true, "customer": true, "match": true,
	"dify": true, "coze": true, "saas": true,
	"salesforce": true, "einstein": true, "hubspot": true,
}

// 拉丁白名单与全角标点容忍度按语种隔离；读写锁保证并发翻译请求不互相污染。
var (
	latinAllowMu      sync.RWMutex
	latinAllowByLang  = map[string]map[string]bool{}
	cjkPunctMaxMu     sync.RWMutex
	cjkPunctMaxByLang = map[string]int{}
)

// SetLatinAllowlist 设置某语种的**配置侧**拉丁白名单（运营自助扩）。
// 内置词表与本表在判定时合并；同名重复无副作用。
func SetLatinAllowlist(lang string, words []string) {
	m := map[string]bool{}
	for _, w := range words {
		if t := strings.TrimSpace(strings.ToLower(w)); t != "" {
			m[t] = true
		}
	}
	latinAllowMu.Lock()
	latinAllowByLang[strings.ToLower(strings.TrimSpace(lang))] = m
	latinAllowMu.Unlock()
}

// SetCJKPunctMax 设置某语种全角标点容忍数（默认 0=出现即拦）。
func SetCJKPunctMax(lang string, max int) {
	cjkPunctMaxMu.Lock()
	cjkPunctMaxByLang[strings.ToLower(strings.TrimSpace(lang))] = max
	cjkPunctMaxMu.Unlock()
}

// cjkPunctMaxFor 读取某语种当前全角标点容忍数；未配置回 0，即出现残留即拦。
func cjkPunctMaxFor(lang string) int {
	cjkPunctMaxMu.RLock()
	defer cjkPunctMaxMu.RUnlock()
	return cjkPunctMaxByLang[strings.ToLower(strings.TrimSpace(lang))]
}

// reLatinWord 提取连续拉丁字母串；长度门槛确保短缩写不触发转写误判。
var reLatinWord = regexp.MustCompile(`[A-Za-z]{4,}`)

// reCJKPunct 全角标点集合：只在非 CJK 目标语里才算残留。
// 刻意不含「·」与引号变体——希伯来语可用 ׳（U+05F3），别误伤。
var reCJKPunct = regexp.MustCompile(`[，；：。？！（）【】《》〈〉「」『』、——…]`)

// LatinTransliterationViolations 返回译文里未登记的拉丁串（≥4 连续字母）。
// 白名单三源合并：内置词表 ∪ 配置表（SetLatinAllowlist）∪ 命中 KB 词条 target_text 的拉丁串。
func LatinTransliterationViolations(target, tr string, terms []TermRequirement) []string {
	if strings.TrimSpace(tr) == "" || !nonLatinScriptTarget(target) {
		return nil
	}
	allow := map[string]bool{}
	for w := range builtinLatinAllow {
		allow[w] = true
	}
	latinAllowMu.RLock()
	for w := range latinAllowByLang[strings.ToLower(strings.TrimSpace(target))] {
		allow[w] = true
	}
	latinAllowMu.RUnlock()
	for _, tm := range terms {
		for _, w := range reLatinWord.FindAllString(tm.Target, -1) {
			allow[strings.ToLower(w)] = true
		}
	}
	seen := map[string]bool{}
	var out []string
	for _, w := range reLatinWord.FindAllString(tr, -1) {
		lw := strings.ToLower(w)
		if allow[lw] || seen[lw] {
			continue
		}
		seen[lw] = true
		out = append(out, w)
	}
	return out
}

// CJKPunctViolations 返回译文里的全角标点字符（KB 词条 target_text 中出现过的字符豁免）。
func CJKPunctViolations(target, tr string, terms []TermRequirement) []string {
	if strings.TrimSpace(tr) == "" || !nonLatinScriptTarget(target) {
		return nil
	}
	allowed := map[rune]bool{}
	for _, tm := range terms {
		for _, r := range tm.Target {
			allowed[r] = true
		}
	}
	var out []string
	for _, r := range tr {
		if strings.ContainsRune(reCJKPunct.String(), r) && !allowed[r] {
			out = append(out, string(r))
		}
	}
	return out
}

// dedupeJoin 按首次出现顺序去重拼接诊断项，保证同一组违规输出稳定可复核。
func dedupeJoin(items []string) string {
	seen := map[string]bool{}
	var out []string
	for _, s := range items {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return strings.Join(out, ", ")
}
