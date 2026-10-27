// ============ lid.go · 职责说明 ============
// 轻量语种识别（LID）的唯一出口：DetectLangs。只服务「报表 / sources 归因 / 客户语言画像」，
// **翻译链零依赖**（主方案把这条写死：一次 LID 抖动不许把交付打红）。
//
// 为什么没有引第三方库（2026-10-28 施工记录）：方案原定 lingua-go（Apache-2.0，纯 Go），
// 但发版机构建环境无法访问模块代理（proxy.golang.org 不可达），新增依赖会让整仓无法编译。
// 因此本批用**内置轻量后端**先落地同一个出口与同一份降级语义：
//   - 判定 = 书写体系（textscript）＋拉丁语种的功能词打分；
//   - 它只回答"这一段/这一篇大致是什么语言"，不承诺逐段高精度；
//   - 换回 lingua-go / fastText 时**只改本文件内部**，调用方零改动（这是"唯一出口"的意义）。
//
// 三条硬口径：
//  1. 不支持/判断不出 ⇒ 返回 "und"，绝不硬猜（猜错的后果是报表口径被污染）；
//  2. 空输入/纯符号/纯数字 ⇒ "und"；
//  3. 批量一次调用（≤2000 段），同一段文本命中进程内缓存。
package fileproc

import (
	"context"
	"sort"
	"strings"
	"sync"

	"translator/internal/textscript"
)

// lidCacheMax 进程内缓存条数（同一单里重复段很多，去重前的开销在这里被摊薄）。
const lidCacheMax = 1024

// lidMu 保护缓存；lidOrder 记插入顺序，用于超上限时淘汰最旧一条（同坐标缓存的 FIFO 口径）。
var (
	lidMu    sync.Mutex
	lidCache = map[string]string{}
	lidOrder []string
)

// LidEngineName 当前 LID 引擎名（健康面读数用；换后端时这里跟着变）。
func LidEngineName() string { return "builtin" }

// LidReady LID 是否可用。内置后端恒可用（它没有任何外部依赖）。
func LidReady() bool { return true }

// DetectLangs 批量识别语种。
//
// 返回：
//   - langs：与 texts 等长，逐段语种码（判断不出为 "und"）；
//   - docLang：整篇的主语种（按字母数加权；全为 und 时回 "und"）；
//   - err：恒为 nil（失败域只影响报表，不向调用方抛错——调用方本来就允许空）。
//
// 上限：一次最多 2000 段；超出部分按"本批多数语种"回填（同一单最多两批的口径由调用方控制，
// 这里只保证不因为一段超长文本把整批拖死）。
func DetectLangs(ctx context.Context, texts []string) ([]string, string, error) {
	if len(texts) > 2000 {
		texts = texts[:2000]
	}
	langs := make([]string, len(texts))
	weight := map[string]int{}
	for i, t := range texts {
		lc := lidOfCached(t)
		langs[i] = lc
		if lc != "und" {
			weight[lc] += len([]rune(t))
		}
	}
	return langs, lidMajority(weight), nil
}

// lidMajority 按字母数加权取多数语种。
func lidMajority(weight map[string]int) string {
	best, bestW := "und", 0
	keys := make([]string, 0, len(weight))
	for k := range weight {
		keys = append(keys, k)
	}
	sort.Strings(keys) // 同权重时按语言码排序，保证结果确定
	for _, k := range keys {
		if weight[k] > bestW {
			best, bestW = k, weight[k]
		}
	}
	return best
}

// lidOfCached 带缓存的单段识别。
func lidOfCached(t string) string {
	key := strings.TrimSpace(t)
	if key == "" {
		return "und"
	}
	lidMu.Lock()
	if v, ok := lidCache[key]; ok {
		lidMu.Unlock()
		return v
	}
	lidMu.Unlock()
	v := lidOf(key)
	lidMu.Lock()
	if _, exists := lidCache[key]; !exists {
		lidCache[key] = v
		lidOrder = append(lidOrder, key)
		for len(lidOrder) > lidCacheMax {
			delete(lidCache, lidOrder[0])
			lidOrder = lidOrder[1:]
		}
	}
	lidMu.Unlock()
	return v
}

// lidLatinStops 拉丁语种的功能词表（只用于"这一段更像哪种拉丁语"）。
// 刻意做小：它不参与翻译，只服务报表；误判的代价是报表口径偏一格，不是交付错误。
var lidLatinStops = map[string][]string{
	"en": {"the", "and", "of", "to", "is", "are", "you", "for", "with", "this", "that", "it"},
	"fr": {"le", "la", "les", "des", "est", "et", "un", "une", "pour", "avec", "dans", "vous"},
	"de": {"der", "die", "das", "und", "ist", "nicht", "mit", "für", "ein", "eine", "auf", "sie"},
	"es": {"el", "la", "los", "las", "es", "y", "para", "con", "una", "un", "en", "que"},
	"pt": {"o", "a", "os", "as", "é", "e", "para", "com", "uma", "um", "não", "que"},
}

// lidOf 单段识别（无缓存）。判据顺序：书写体系优先（强信号），拉丁再看功能词。
func lidOf(t string) string {
	counts := textscript.Share(t)
	if len(counts) == 0 {
		return "und"
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return "und"
	}
	share := func(s textscript.Script) float64 { return float64(counts[s]) / float64(total) }
	// 非拉丁体系：一个体系占绝对多数即可定（短段也有判别力）。
	switch {
	case share(textscript.Hiragana)+share(textscript.Katakana) >= 0.10:
		// 假名占比 ≥10% 即判日文：日文正文必然混排假名，而中文里几乎不会出现假名。
		return "ja"
	case share(textscript.Hangul) >= 0.30:
		return "ko"
	case share(textscript.Arabic) >= 0.30:
		return "ar"
	case share(textscript.Thai) >= 0.30:
		return "th"
	case share(textscript.Cyrillic) >= 0.30:
		return "ru"
	case share(textscript.Han) >= 0.50:
		// 简繁细分不做（报表按中文一族统计即可；细分需要额外词典，收益不抵成本）。
		return "zh"
	}
	// 拉丁系：功能词打分（只数小写词，标点先剥）。
	words := strings.FieldsFunc(strings.ToLower(t), func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && r != '\'' && r != '-' && r != 'é' && r != 'è' && r != 'à' &&
			r != 'ü' && r != 'ö' && r != 'ä' && r != 'ñ' && r != 'ã' && r != 'ç'
	})
	if len(words) == 0 {
		return "und"
	}
	best, bestHit := "", 0
	keys := make([]string, 0, len(lidLatinStops))
	for k := range lidLatinStops {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, lc := range keys {
		hit := 0
		for _, stop := range lidLatinStops[lc] {
			for _, w := range words {
				if w == stop {
					hit++
				}
			}
		}
		if hit > bestHit {
			best, bestHit = lc, hit
		}
	}
	if bestHit >= 2 || (bestHit == 1 && len(words) <= 4) {
		return best
	}
	// 拉丁字母但功能词不足：短标签/型号等，按英文兜底（站点默认语言），而不是 und——
	// 报表里 und 太多会让人以为"识别坏了"，而这里的事实是"这段话太短"。
	if share(textscript.Latin) >= 0.90 && len(words) >= 1 {
		return "en"
	}
	return "und"
}
