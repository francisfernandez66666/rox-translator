// ============ types.go · 职责说明 ============
// 二期六包平移第三刀（strategytypes 纯函数层）：锚常量、策略输出类型与文本判定纯函数。
// ★ 迁入即换：外仓写死在代码里的中文词表（到店／语气／情绪／行业词）全部外提到
// sales_lexicon 配置层，本包只收 []string 参数；长短判据改成按词数且由调用方按语种给档
// （§5.2 L3：按字符数在拉丁／泰文下量纲不同）。
// 本包零 IO、零全局状态、零框架依赖，可被任何层安全引用而不成环。
// ================================================
package strategytypes

import (
	"strings"
	"unicode"
)

// 锚类型常量（ aggressiveness 序列：不抛 < 同类≈场景 < 拆解 < 对比 < 损失 < 稀缺 < 代价自担）。
const (
	// AnchorNoThrow 不抛锚。
	AnchorNoThrow = 0
	// AnchorSameKind 同类锚。
	AnchorSameKind = 1
	// AnchorScene 场景锚（与同类锚同级）。
	AnchorScene = 1
	// AnchorDisassemble 拆解锚。
	AnchorDisassemble = 2
	// AnchorCompare 对比锚。
	AnchorCompare = 3
	// AnchorLoss 损失锚。
	AnchorLoss = 4
	// AnchorScarcity 稀缺锚。
	AnchorScarcity = 5
	// AnchorSelfPay 代价自担锚。
	AnchorSelfPay = 6
)

// AnchorCount 锚档位总数（同类与场景同级，合并算一档）。
const AnchorCount = 7

// AnchorNames 锚档位稳定编码（对外排障契约；不进 i18n，也不许在代码里换成第二套名字）。
var AnchorNames = [AnchorCount]string{
	"no_throw", "same_kind_or_scene", "disassemble", "compare", "loss", "scarcity", "self_pay",
}

// AnchorAggressiveness 各锚档位的强度等级（数组下标＝锚类型）。
var AnchorAggressiveness = [AnchorCount]int{0, 1, 2, 3, 4, 5, 6}

// GetAnchorName 取锚档位稳定编码；越界返回 unknown（不许静默取第 0 档）。
// 参数：anchorType=锚类型；返回：稳定编码。
func GetAnchorName(anchorType int) string {
	if anchorType >= 0 && anchorType < AnchorCount {
		return AnchorNames[anchorType]
	}
	return "unknown"
}

// StrategyOutput 策略引擎一轮输出的结构化承载（供 llm／flow 共用，避免反向依赖）。
type StrategyOutput struct {
	// SelectedAnchor 本轮选中的锚档位。
	SelectedAnchor int
	// AnchorConfidence 选中锚的置信度。
	AnchorConfidence float64
	// StageDowngraded 是否因阶段锁降级。
	StageDowngraded bool
	// SoftDowngrade 是否触发软降级（接钩率低／沉默长／情绪负面）。
	SoftDowngrade bool
	// FinalAnchor 全部降级之后的最终锚。
	FinalAnchor int
	// DraftID 选中的说法草稿（一期 sales_drafts 的 id，不再叫模板）。
	DraftID string
	// PromptText 渲染后的抛话术。
	PromptText string
	// HookText 渲染后的钩话术。
	HookText string
	// UrgencyLevel 紧迫等级（L1/L2/L3）。
	UrgencyLevel string
	// RouteResult 路由结果（ai/human/fish）。
	RouteResult string
	// RouteReason 路由原因（排障契约）。
	RouteReason string
	// IntentDelta 意向分增量（反哺）。
	IntentDelta float64
}

// NormalizeText 统一大小写与首尾空白（词表命中前的唯一归一化口径）。
// 参数：text=原文本；返回：归一化文本。
func NormalizeText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

// isASCIIWordByte 判定字节是否属于 ASCII 单词字符（词边界判据用）。
// 参数：b=字节；返回：是否单词字符。
func isASCIIWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// isASCIIWord 判定整词是否全由 ASCII 单词字符组成（决定要不要按词边界命中）。
// 参数：word=配置词；返回：是否纯 ASCII 单词。
func isASCIIWord(word string) bool {
	if word == "" {
		return false
	}
	for i := 0; i < len(word); i++ {
		if !isASCIIWordByte(word[i]) {
			return false
		}
	}
	return true
}

// countOccurrences 统计单个配置词在归一化文本里的出现次数（非重叠）。
// ★ 纯 ASCII 单词按**词边界**命中（防 "hi" 把 "nothing" 判成寒暄）；
// 短语／CJK 词按子串命中（它们本身没有空白词边界）。
// 参数：text=归一化文本；word=配置词；返回：出现次数。
func countOccurrences(text, word string) int {
	if word == "" {
		return 0
	}
	boundary := isASCIIWord(word)
	n := 0
	for i := 0; i+len(word) <= len(text); {
		at := strings.Index(text[i:], word)
		if at < 0 {
			break
		}
		start := i + at
		end := start + len(word)
		leftOK := start == 0 || !isASCIIWordByte(text[start-1])
		rightOK := end == len(text) || !isASCIIWordByte(text[end])
		if !boundary || (leftOK && rightOK) {
			n++
			i = end
		} else {
			i = start + 1
		}
	}
	return n
}

// CountHits 统计一组配置词在文本里的总命中次数（大小写不敏感）。
// 参数：text=原文本；words=配置词表；返回：命中次数。
func CountHits(text string, words []string) int {
	t := NormalizeText(text)
	n := 0
	for _, w := range words {
		n += countOccurrences(t, NormalizeText(w))
	}
	return n
}

// HasAnyHit 判定文本是否命中任一配置词（通用型替代外仓的到店／行业硬编码判定）。
// 参数：text=原文本；words=配置词表；返回：是否命中。
func HasAnyHit(text string, words []string) bool {
	t := NormalizeText(text)
	for _, w := range words {
		if countOccurrences(t, NormalizeText(w)) > 0 {
			return true
		}
	}
	return false
}

// CountWords 按词数计量文本长度：有空白分隔的按词数；无空白的 CJK 文本按字符数。
// ★ 语种档由调用方配置（minWordCount），本函数只提供量纲，不在代码里按语种分支。
// 参数：text=原文本；返回：词数（无空白脚本退化为字符数）。
func CountWords(text string) int {
	t := strings.TrimSpace(text)
	if t == "" {
		return 0
	}
	fields := strings.Fields(t)
	if len(fields) > 1 {
		return len(fields)
	}
	if isCJKRunes(t) {
		return len([]rune(t)) // 无空白脚本按字符计量，门槛由调用方按语种给档
	}
	return 1
}

// isCJKRunes 判定一段无空白文本是否以中日韩字符为主（决定按字符计量的合理性）。
// 参数：text=无空白文本；返回：是否含 CJK 字符。
func isCJKRunes(text string) bool {
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return true
		}
	}
	return false
}

// CheckHooked 接钩判定：纯语气词精确相等不算；其余按配置的最小词数门槛判。
// 参数：text=客户回复；noHookWords=纯语气词表；minWordCount=语种档位门槛（<=0 时取 3）；返回：是否接钩。
func CheckHooked(text string, noHookWords []string, minWordCount int) bool {
	t := NormalizeText(text)
	if t == "" {
		return false
	}
	for _, w := range noHookWords {
		if w != "" && t == NormalizeText(w) {
			return false
		}
	}
	threshold := minWordCount
	if threshold <= 0 {
		threshold = 3
	}
	if CountWords(t) < threshold {
		return false
	}
	return true
}

// DetectEmotion 情绪判定：负／正配置词命中数比较，平局或零命中为 neutral。
// 参数：text=原文本；negative=负面词表；positive=正面词表；返回：negative/positive/neutral。
func DetectEmotion(text string, negative, positive []string) string {
	neg, pos := CountHits(text, negative), CountHits(text, positive)
	if neg > pos && neg > 0 {
		return "negative"
	}
	if pos > neg && pos > 0 {
		return "positive"
	}
	return "neutral"
}
