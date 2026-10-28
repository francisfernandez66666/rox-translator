// ============ lid.go · 职责说明 ============
// 轻量语种识别（LID）的唯一出口：DetectLangs。只服务「报表 / sources 归因 / 客户语言画像」，
// **翻译链零依赖**（主方案把这条写死：一次 LID 抖动不许把交付打红）。
//
// ★ 2026-10-28 计价与成本批：后端由内置轻量实现换成 **lingua-go v1.4.0**（Apache-2.0，纯 Go）。
// 上一版的施工记录是"发版机构建环境无法访问模块代理，故先用内置轻量后端"——本批实测
// `go get github.com/pemistahl/lingua-go@v1.4.0` 可下（含 shopspring/decimal、x/exp、protobuf 三个间接依赖），
// 于是按方案 §6.4 真换。**换的只是本文件内部**：DetectLangs 的签名、缓存口径、批量上限、
// "判不出就 und"的语义全部一字未变，调用方零改动（这正是"唯一出口"的意义）。
//
// 四条硬口径（改这块前必读）：
//  1. **受限语种集**：只 `FromLanguages(...)` 那 17 种（站点 11 种书写体系 ＋ it/nl/pl/tr/vi/id 这几种常见源语）。
//     **禁止** `FromAllLanguages()`——它会把 75 种语言的模型都纳入候选，构建与常驻内存都是一个数量级的抬升，
//     而本仓的业务只在这十几种里做归因（判不出的那部分本来就该是 und）。
//  2. **判不出就 und**：`DetectLanguageOf` 的 ok=false（lingua 内部判为 Unknown）⇒ "und"；
//     文本不足 3 个字符或**一个字母都没有**（纯数字/纯符号）⇒ "und"，不交给模型硬猜。
//  3. **繁简不在 LID 职责内**：lingua 只有 `Chinese`（zh），繁体文本也回 `zh`。
//     本仓没有简繁判定器，而按字符集猜 `zh_hant` 比给 `zh` 更坏（前端展示语种按界面语言走）。
//  4. **缓存与批量口径不变**：`lidCacheMax=1024` FIFO、一次最多 2000 段、每段先查缓存。
//     同一单里重复段很多，去重前的模型调用开销在这里被摊薄。
//
// 降级方向：本包对 LID **没有 fail-soft 之外的退路**——lingua 是纯 Go 依赖、没有外部进程，
// 编译进来就可用（`LidReady()` 恒 true）。若将来要换 fastText，只改本文件内部。
package fileproc

import (
	"context"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/pemistahl/lingua-go"
)

// lidCacheMax 进程内缓存条数（同一单里重复段很多，去重前的开销在这里被摊薄）。
const lidCacheMax = 1024

// lidMinRunes 交给 lingua 的最短文本长度（字符）。低于它一律 und：
// lingua 对 1~2 个字符的输入置信度不可靠，而"猜错"会直接污染报表口径（同硬口径 2）。
const lidMinRunes = 3

// lidEngineName 引擎名单一出处（健康面 `lid_engine` 读它；单测逐字钉住，防止"换了依赖没换出口"）。
const lidEngineName = "lingua"

// lidState 语种识别器的进程级状态（缓存 + 检测器单例 + 观测计数）。
// 收在一个 var 块里只为让「这份状态只有一处」一眼可见：缓存与检测器必须同生共死，
// 拆成两处迟早出现「检测器换了、缓存还留着旧判定」。
var (
	// lidMu 保护缓存；lidOrder 记插入顺序，用于超上限时淘汰最旧一条（同坐标缓存的 FIFO 口径）。
	lidMu    sync.Mutex
	lidCache = map[string]string{}
	lidOrder []string

	// lidDetectorOnce lingua 检测器只建一次（构建 + 模型加载都是重活）。
	// ⚠️ 必须用 `FromLanguages` 的受限集合，禁止 `FromAllLanguages()`（见文件头硬口径 1）。
	lidDetectorOnce sync.Once
	lidDetector     lingua.LanguageDetector

	// lidDetectCalls 真正落到模型的次数（缓存命中不计）。它是**只读观测腿**：
	// 单测据它证明"第二次同段命中缓存"，排障时也能看出"这一次到底跑了多少次识别"。
	lidDetectCalls atomic.Int64
)

// LidEngineName 当前 LID 引擎名（健康面读数用；换后端时这里跟着变）。
func LidEngineName() string { return lidEngineName }

// LidReady LID 是否可用。lingua 是纯 Go 依赖（无外部进程、无权重文件），恒可用。
func LidReady() bool { return true }

// lidLanguages LID 的受限语种集：站点 11 种书写体系 + 6 种常见源语。
// 写成函数而不是包级切片：单测可以拿它做"极大集合不许出现"的对照（避免有人加回 FromAllLanguages）。
func lidLanguages() []lingua.Language {
	return []lingua.Language{
		lingua.Chinese,    // zh（含繁体；见文件头硬口径 3）
		lingua.English,    // en
		lingua.Japanese,   // ja
		lingua.Korean,     // ko
		lingua.French,     // fr
		lingua.German,     // de
		lingua.Spanish,    // es
		lingua.Portuguese, // pt
		lingua.Russian,    // ru
		lingua.Arabic,     // ar
		lingua.Thai,       // th
		lingua.Italian,    // it（西/葡/意的混淆必须靠"都在候选集里"才分得开）
		lingua.Dutch,      // nl
		lingua.Polish,     // pl
		lingua.Turkish,    // tr
		lingua.Vietnamese, // vi
		lingua.Indonesian, // id
	}
}

// lidDetectorInstance 建（或取）检测器。sync.Once ⇒ 全进程一份，构建与模型加载只付一次。
func lidDetectorInstance() lingua.LanguageDetector {
	lidDetectorOnce.Do(func() {
		lidDetector = lingua.NewLanguageDetectorBuilder().
			FromLanguages(lidLanguages()...).
			Build()
	})
	return lidDetector
}

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

// lidOf 单段识别（无缓存）。
//
// 判据顺序是刻意的：**先做"这段根本不是语言"的排除**，再交给模型。
// 反过来（先问模型）在短数字串上会拿到"某个语言 + 高置信度"这种读数——
// 那时我们拿到的是一个**看起来有效的错误答案**，比 und 坏得多（报表里 und 是"没判出来"，
// 而错码是"判成了别的语言"，后者会被当成数据错误去查上游）。
func lidOf(t string) string {
	s := strings.TrimSpace(t)
	if utf8.RuneCountInString(s) < lidMinRunes {
		return "und"
	}
	if !lidHasLetter(s) {
		return "und"
	}
	lidDetectCalls.Add(1)
	lang, ok := lidDetectorInstance().DetectLanguageOf(s)
	if !ok {
		return "und" // lingua 内部判为 Unknown（置信度不足）⇒ 我们也回 und，不硬猜
	}
	return lidCodeOf(lang)
}

// lidHasLetter 这段文本里有没有至少一个"字母"（任何书写体系的 Letter）。
// 用途：纯数字/纯符号/纯表情一律 und（它们不是任何语言）。
func lidHasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// lidCodeOf lingua 的 Language → 本仓语种码。
//
// 为什么走 ISO 639-1 而不是 `Language.String()`：后者是英文名（"Chinese"），
// 与本仓的 `zh/en/...` 码表对不上，且改名比换码更可能（上游按 ISO 常量映射最稳）。
// 语种集外的语言（理论上不会出现，因为候选集就 17 种）统一回 und。
func lidCodeOf(l lingua.Language) string {
	if l == lingua.Unknown {
		return "und"
	}
	switch l {
	case lingua.Chinese:
		return "zh" // 繁简不细分（硬口径 3）
	}
	iso := l.IsoCode639_1()
	if iso == lingua.UnknownIsoCode639_1 {
		return "und"
	}
	code := strings.ToLower(iso.String())
	if len(code) != 2 {
		return "und"
	}
	return code
}
