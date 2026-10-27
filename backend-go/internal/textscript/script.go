package textscript

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Script 一个书写体系。零值 Other 表示「既不是下列任何一种」，包括标点、符号与数字。
type Script uint8

// 书写体系枚举。顺序刻意稳定（Other 为 0），因为 Share/Profile 的输出会被日志与单测按值比较。
const (
	Other Script = iota
	Latin
	Han
	Hiragana
	Katakana
	Hangul
	Cyrillic
	Arabic
	Hebrew
	Greek
	Thai
	Devanagari
)

// 兜底阈值：画像判档与「某体系是否属于本文」的口径都在这里，调用方不许各写一份。
const (
	// TierSingleMin 最大体系占比达到这里即判单一语种档（≥95%）。
	TierSingleMin = 0.95
	// TierMixedMin 有限混编档的下界（最大体系 ≥50%）。
	TierMixedMin = 0.50
	// TierMixedSecondMax 有限混编档要求第二体系 <20%；到 20% 即真·多语种。
	TierMixedSecondMax = 0.20
	// RareShare 低于这个占比且只以孤立短行出现的体系，才算「本文本来没有它」。
	RareShare = 0.005
)

// scriptNames 对外名字（日志、画像读数、单测期望都用它，不许在别处另造一套拼法）。
var scriptNames = map[Script]string{
	Other:      "other",
	Latin:      "latin",
	Han:        "han",
	Hiragana:   "hiragana",
	Katakana:   "katakana",
	Hangul:     "hangul",
	Cyrillic:   "cyrillic",
	Arabic:     "arabic",
	Hebrew:     "hebrew",
	Greek:      "greek",
	Thai:       "thai",
	Devanagari: "devanagari",
}

// String 返回稳定的体系名（未知值回 "other"，不 panic：日志链路不许因为一个坏值中断）。
func (s Script) String() string {
	if n, ok := scriptNames[s]; ok {
		return n
	}
	return "other"
}

// Kana 是否属于假名家族（平假名或片假名）。
//
// 口径说明：日文行天然「汉字＋平假名＋片假名＋拉丁」混排，所以凡是问「这是不是假名」的判据
// 一律走本函数，不要拆成两个体系各判一次——拆开就会出现「半角片假名算不算」这类分歧。
func (s Script) Kana() bool { return s == Hiragana || s == Katakana }

// scriptExtend Unicode 里属于 Common/Inherited、但语义上明确属于某个体系的字符。
//
// 为什么必须有这张表：unicode.In('ー', unicode.Katakana) 返回 **false**（U+30FC 长音符是
// Script=Common），于是「日语行」会被判成「含 Other」，画像与 glyph_set 一起失准。
// 表里的每一项都配了逐码位单测（script_test.go），新增项必须同时加断言。
var scriptExtend = map[rune]Script{
	0x30FC: Katakana, // ー 片假名长音符（平假名语境同样用它）
	0x30FB: Katakana, // ・ 中点（历史实现 isLoneKana 的区间里就包含着它，行为保持不变）
	0x3005: Han,      // 々 叠字符号
	0x3006: Han,      // 〆
	0x309D: Hiragana, // ゝ
	0x309E: Hiragana, // ゞ
	0x30FD: Katakana, // ヽ
	0x30FE: Katakana, // ヾ
	0xFF9E: Katakana, // ﾞ 半角浊点
	0xFF9F: Katakana, // ﾟ 半角半浊点
}

// Of 归一个字符的书写体系。
//
// 判定顺序：先查 scriptExtend（Common 的显式归属），再按 unicode.In 的 Script 表判。
// 拉丁必须放在最后判：Fullwidth 区里的 Ａ（U+FF21）与 ﾊ（U+FF8A）在同一个区段，
// 但前者是 Latin、后者是 Katakana——按区段一刀切会同时判错两者。
func Of(r rune) Script {
	if s, ok := scriptExtend[r]; ok {
		return s
	}
	switch {
	case unicode.In(r, unicode.Han):
		return Han
	case unicode.In(r, unicode.Hiragana):
		return Hiragana
	case unicode.In(r, unicode.Katakana):
		return Katakana
	case unicode.In(r, unicode.Hangul):
		return Hangul
	case unicode.In(r, unicode.Cyrillic):
		return Cyrillic
	case unicode.In(r, unicode.Arabic):
		return Arabic
	case unicode.In(r, unicode.Hebrew):
		return Hebrew
	case unicode.In(r, unicode.Greek):
		return Greek
	case unicode.In(r, unicode.Thai):
		return Thai
	case unicode.In(r, unicode.Devanagari):
		return Devanagari
	case unicode.In(r, unicode.Latin):
		return Latin
	}
	return Other
}

// Has 判断 s 里有没有 want 这个体系的**字母**。
//
// 只数字母是刻意的：汉字判据（hanResidueRuns / StripChineseInNonZh）要问的是「有没有汉字」，
// 而不是「有没有落在这个码段里的符号」；后者会把「々」这类叠字符也当成漏翻证据。
func Has(s string, want Script) bool {
	for _, r := range s {
		if unicode.IsLetter(r) && Of(r) == want {
			return true
		}
	}
	return false
}

// ScriptsOf 返回 s 里出现过的体系集合（只统计字母）。
func ScriptsOf(s string) map[Script]bool {
	out := map[Script]bool{}
	for _, r := range s {
		if unicode.IsLetter(r) {
			out[Of(r)] = true
		}
	}
	return out
}

// Share 统计一段文本里各书写体系的**字母数**占比。
//
// 分母＝所有 unicode.IsLetter 字符（数字、标点、空白一律不进分母）；没有字母时返回空表。
// 返回值按体系固定顺序排列（供日志与单测稳定比较，map 本身无序）。
func Share(s string) map[Script]int {
	out := map[Script]int{}
	for _, r := range s {
		if unicode.IsLetter(r) {
			out[Of(r)]++
		}
	}
	return out
}

// Profile 一篇文档的书写体系画像（清洗层的三档判定就吃它）。
type Profile struct {
	// Tier 三档：single（单一语种）/ mixed（有限混编）/ multi（真·多语种）。
	Tier string
	// Share 各体系占比，分母＝全篇字母数；空文档为空表。
	Share map[Script]float64
	// Isolated 该体系是否「只以孤立短行出现」：含该体系的每一行，其该体系字母数都 ≤2。
	Isolated map[Script]bool
	// Letters 全篇字母数（画像的分母，读出来给排障用）。
	Letters int
}

// OfLines 计算全篇画像。
//
// 三档判定（从上到下先命中先取，边界值归上一档）：
//   - single：最大体系占比 ≥ TierSingleMin（0.95）；
//   - mixed：最大占比 ∈ [TierMixedMin, TierSingleMin) 且第二体系 < TierMixedSecondMax；
//   - multi：其余（含最大占比 < TierMixedMin 的一切形态）。
//
// 「第二体系」只在有多于一个体系时才有意义；空表/单体系时按 0 处理。
func OfLines(lines []string) Profile {
	counts := map[Script]int{}
	letters := 0
	// 孤立性统计：对每个体系记录「是否出现过」与「是否每一行都只有 ≤2 个该体系字母」。
	seen := map[Script]bool{}
	short := map[Script]bool{}
	for _, ln := range lines {
		perLine := map[Script]int{}
		for _, r := range ln {
			if !unicode.IsLetter(r) {
				continue
			}
			s := Of(r)
			counts[s]++
			perLine[s]++
			letters++
		}
		for s, n := range perLine {
			if !seen[s] {
				seen[s] = true
				short[s] = true
			}
			if n > 2 {
				short[s] = false
			}
		}
	}
	p := Profile{Tier: "single", Share: map[Script]float64{}, Isolated: map[Script]bool{}, Letters: letters}
	if letters == 0 {
		return p
	}
	// 占比与排序：按字母数降序、同数按体系序，保证"最大/第二"是确定值。
	type kv struct {
		s Script
		n int
	}
	arr := make([]kv, 0, len(counts))
	for s, n := range counts {
		arr = append(arr, kv{s, n})
		p.Isolated[s] = short[s]
	}
	sort.Slice(arr, func(i, j int) bool {
		if arr[i].n != arr[j].n {
			return arr[i].n > arr[j].n
		}
		return arr[i].s < arr[j].s
	})
	for _, it := range arr {
		p.Share[it.s] = float64(it.n) / float64(letters)
	}
	maxShare := p.Share[arr[0].s]
	secondShare := 0.0
	if len(arr) > 1 {
		secondShare = p.Share[arr[1].s]
	}
	switch {
	case maxShare >= TierSingleMin:
		p.Tier = "single"
	case maxShare >= TierMixedMin && secondShare < TierMixedSecondMax:
		p.Tier = "mixed"
	default:
		p.Tier = "multi"
	}
	return p
}

// ShareOf 取某体系占比（缺失回 0）。
func (p Profile) ShareOf(s Script) float64 { return p.Share[s] }

// IsRare 该体系是否「本文本来没有它」：占比 < RareShare 且只以孤立短行出现。
//
// 这是 glyph_set 的核心判据——它问的是「这份文档本来有没有这个书写体系」，
// 而不是「这一行和同列多数是否一致」。后者在日文混排里必然批量误伤（见主方案 §6.3）。
func (p Profile) IsRare(s Script) bool {
	if p.Letters == 0 {
		return false
	}
	// 真·多语种档一律不判 Rare：这一档 glyph_set 整条关闭（主方案 §7.2）。
	// 编码放在这里而不是让每个调用方各判一次——两个调用方迟早会有一处忘了判。
	if p.Tier == "multi" {
		return false
	}
	// 全篇没出现过该体系 ⇒ 占比 0、且"只以孤立短行出现"是空真（vacuous truth）。
	// 这一档正是 glyph_set 的主场景：换任何误识字形都成立，不依赖具体字符。
	if p.ShareOf(s) == 0 {
		return true
	}
	return p.ShareOf(s) < RareShare && p.Isolated[s]
}

// IsLoneForeign 判断一个短串是不是「本文本来没有的书写体系的孤立短行」。
//
// 前置：长度 1~2 个字母（含 0 个字母一律不算——纯符号/数字归符号门，不归本条）。
// 命中条件：这些字母同属一个体系，且该体系满足 IsRare。
// 典型命中：英文文档里一枚绿色开关被识别成「の」；典型不命中：日文文档正文里的「の」。
func IsLoneForeign(s string, p Profile) bool {
	var letters []rune
	for _, r := range strings.TrimSpace(s) {
		if unicode.IsLetter(r) {
			letters = append(letters, r)
		}
	}
	if len(letters) == 0 || len(letters) > 2 {
		return false
	}
	first := Of(letters[0])
	for _, r := range letters[1:] {
		if Of(r) != first {
			return false
		}
	}
	if first == Other || first == Latin {
		return false // 拉丁是绝大多数文档的主体系，永不当异常；Other 归符号门
	}
	return p.IsRare(first)
}

// Hash 返回画像的稳定指纹（供缓存/日志判定"这份画像变了没有"）。
// 只包含判档与占比，不含 Isolated（它只在 glyph_set 用，且会放大日志体积）。
func (p Profile) Hash() string {
	keys := make([]Script, 0, len(p.Share))
	for s := range p.Share {
		keys = append(keys, s)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var b strings.Builder
	b.WriteString(p.Tier)
	for _, s := range keys {
		// 保留 4 位小数：画像只用于分档，不需要更高精度。
		v := strconv.FormatFloat(p.Share[s], 'f', 4, 64)
		v = strings.TrimRight(strings.TrimRight(v, "0"), ".")
		b.WriteString("|")
		b.WriteString(s.String())
		b.WriteString("=")
		b.WriteString(v)
	}
	return b.String()
}
