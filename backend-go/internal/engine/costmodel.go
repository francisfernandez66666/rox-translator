// ============ costmodel.go · 职责说明 ============
// ★ 2026-10-13 用户令⑦（任务 #31）：工作台「预计消耗」改按**真实链路结构**预估，
// 与实扣共用同一把尺子（markup 与积分折算都走扣费那一条解析腿）。
//
// 现网实证（用户截图，180 个拉丁字符的一段英文，单语种、专业校对）：
//
//	出栈横幅「预计消耗 ≈30~75 积分（约 60 句）」，而同一气泡的实收读数「本次消耗 1 积分」。
//
// 旧公式错在两处，且**两处都是结构错不是系数错**（所以调系数救不了）：
//
//	① 计量单位错：它按「源句数 × 语种数 × 每句 500 token × 2 轮」估，
//	   而即时翻译链是**整篇源文一次 prompt、每个语种一腿**（text.go 的 directOther 分支
//	   → singleLang / ReviewTranslation 都吃 cleanText 全文），句数根本不进成本；
//	   文件/工单链才逐段调用（file.go 每行一次 TranslateOne），那里「段」才有成本意义。
//	   同一条公式服务两个结构相反的入口，短文本必然被放大。
//	② 句数读数本身虚高：countSentences 以「. ! ? ; 换行」切分，
//	   "e.g." 与 "600912.SH" 这种缩写/文件名各切一刀，一段话被数成 8 句。
//	   （本批的处理是**让预估不再吃它的读数**——成本与句数彻底脱钩；
//	     至于"不动它是因为它还服务余额换算与额度发放"那句旧理由，2026-10-13 实读代码后核正为**不成立**，
//	     api 包那一份切分判据随后已单独重做，见 internal/api/sentence_count.go。）
//
// 新公式（全部常数**派生自真实提示词**，不写第二个字面量，防 §一·11 那一族「多把尺子」）：
//
//	初翻腿 = 固定指令开销(translateInstruction＋outputContractNote) ＋ 源文 token ＋ 译文 token
//	校对腿 = 固定指令开销(reviewPromptFmt) ＋ 源文 token ＋ 上一版译文 ＋ 本版译文
//	下限 = 每语种只走初翻那一腿（校对腿不采纳是真实路径：reviewOutputRejectReason 判不纯即退初翻）
//	上限 = 两腿都走；pro 再乘一档质检重译的期望余量
//	★ 2026-10-17：fast 档**不跑校对腿** ⇒ 其上限＝下限（区间收成一点），与 text.go 的实际链路一致。
//
// ⚠️ 这一份是**预估**口径，不是计费口径：真正扣多少仍然只由 llm.Client 回传的真实 usage
//
//	× markup 决定（billing_api.ChargeUsageRealtime 是全系统唯一决定扣费量的地方）。
//	预估与实扣「同一把尺子」指的是**折算规则同源**（markup 解析与 PointsFromTokens 共用），
//	不是拿预估当账单。
//
// ⚠️ 改本文件的 token 折算权重（ScriptTokens）必须同步改 costmodel_test.go 的量级锁：
// 权重抬高 → 所有短文本预估一起抬高，权重压低 → 客户按预估备的钱不够用。
// =============================================
package engine

import (
	"fmt"
	"strings"
	"unicode"

	"translator/internal/config"
)

// ScriptTokens 按**书写体系**把文本折算成 token 数（只用于预估，不进任何扣费路径）。
// 参数 text: 任意语种文本。返回: 折算 token 数（至少 0）。
//
// 两档权重（现代主流分词器的公开量级，不是某家厂商的精确值——预估要的是量级对，不是逐 token 对）：
//   - 宽体（汉字／假名／谚文／西里尔／希腊／阿拉伯／希伯来／泰文）：约 1 字符 ≈ 1 token；
//   - 窄体（拉丁字母／数字／标点／空白）：约 4 字符 ≈ 1 token。
//
// 为什么不能像旧公式那样「不分语种一律按字符数除一个系数」：同一段 180 字符，
// 英文约 45 token、中文约 180 token，差 4 倍；拿一个系数同时套两种输入，
// 必然一边虚高一边虚低（这正是「预估 30~75 vs 实扣 1」里虚高的那一半）。
func ScriptTokens(text string) int64 {
	var wide, narrow int64
	for _, r := range text {
		switch {
		case unicode.Is(unicode.Han, r), unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r), unicode.Is(unicode.Hangul, r),
			unicode.Is(unicode.Cyrillic, r), unicode.Is(unicode.Greek, r),
			unicode.Is(unicode.Arabic, r), unicode.Is(unicode.Hebrew, r),
			unicode.Is(unicode.Thai, r):
			wide++
		default:
			narrow++
		}
	}
	return wide + (narrow+3)/4
}

// ChatLegFixedTokens 即时翻译两条腿各自的**固定指令开销**（token 口径）。
// 参数 sourceLang/targetLang: 语言代码（sourceLang 空串按「文本」处理，与 srcName 同口径）；
// uiLang: 界面语言（决定中/英文提示词那一套，与 translateInstruction 同一个入参）。
// 返回 initial=初翻腿固定开销，review=校对腿固定开销。
//
// ★ 这两个数**必须是算出来的**：初翻腿取真实的 translateInstruction＋outputContractNote，
// 校对腿取真实的 reviewPromptFmt 模板（把可变的原文/译文位留空）。于是运营/开发一改提示词，
// 预估当场跟着变——旧公式钉死「每句 500 token」时，提示词涨了一倍预估却一行不动，
// 那是「同一份成本两处读数」的又一族（等值锁＝TestChatLegFixedTokensDeriveFromRealPrompts）。
func ChatLegFixedTokens(sourceLang, targetLang, uiLang string) (initial, review int64) {
	instr := translateInstruction(sourceLang, targetLang, uiLang) + outputContractNote(uiLang)
	// 初翻腿除指令外还带一句「【原文】」定位标签（user 消息里的骨架，见 assembleTranslateMessages）
	initial = ScriptTokens(instr) + ScriptTokens("【原文】")

	// 与 ReviewTranslation 同一取法：语言名查不到就用代码本身，别让两边各拼一套
	langName := config.LangNames[targetLang]
	if langName == "" {
		langName = targetLang
	}
	rev := fmt.Sprintf(reviewPromptFmt, langName, "", "", "", contractNoteZh())
	review = ScriptTokens(rev)
	// ★ 2026-10-24 N-01：文化规则块按上限进 prompt，预估必须把它计入，否则「预估 24、实扣 103」
	//   的偏离永远存在（AGENTS §一·5④ 同一把尺子）。上限默认 2000 字符，按目标语书写体系折算。
	allow := CulturePromptAllowanceTokens(targetLang)
	initial += allow
	review += allow
	return initial, review
}

// CulturePromptAllowanceTokens 文化规则块进 prompt 的 token 预估（按默认上限 2000 字符）。
// 规则文本是**目标语**：CJK/西里尔/阿拉伯/泰文等宽体约 1 字符 1 token，拉丁约 4 字符 1 token。
// 只用于预估；运行时上限由 culture_rules_max_chars 控制。
func CulturePromptAllowanceTokens(targetLang string) int64 {
	const maxChars = 2000
	sample := "字"
	switch strings.ToLower(strings.TrimSpace(targetLang)) {
	case "en", "fr", "de", "es", "pt", "it", "nl", "sv", "id_lang", "vi", "tl", "ms":
		sample = "a"
	}
	return ScriptTokens(strings.Repeat(sample, maxChars))
}

// EstimateChatTokens 预估一次即时翻译的**真实 token** 消耗区间（未乘 markup、未折积分）。
// 参数 sourceText: 待译源文；targetLangs: 目标语种列表（每语种一腿，列表长度即成本倍数）；
// mode: "fast"=快速，其余（含空）按专业校对档；sourceLang/uiLang: 见 ChatLegFixedTokens。
// 返回 (minTokens, maxTokens)，两者都 ≥1（零消耗不该出现在预估里，见下面兜底注释）。
//
// 区间两端的语义（对外文案要能一句话解释，否则用户又会问「为什么是个区间」）：
//   - min = 每个语种只走初翻那一腿：校对腿产物被判不纯/失败而退回初翻，是现网真实路径；
//   - max = 初翻＋校对两腿都走，pro 档再乘一档质检重译的期望余量（applyOutputGates 打回重译）。
//
// ★ 2026-10-17 用户令：fast **去掉校对腿** ⇒ 快速档没有 leg2，min/max 同一条腿（区间收成一点）。
func EstimateChatTokens(sourceText string, targetLangs []string, mode, sourceLang, uiLang string) (int64, int64) {
	src := ScriptTokens(sourceText)
	if src < 1 {
		src = 1 // 空文本兜底 1 token：预估出 0 会让「零消耗」冒充合法读数（同 countSentences 的兜底方向）
	}
	// 译文长度按源文 1:1 折中：英→中变短、中→英变长，两侧互相抵；
	// 残余不确定性由 min~max 区间吸收，不靠把系数调高来「保险」（那正是旧公式吹到 75 的路径）。
	out := src
	fast := isFastMode(mode)
	var minTok, maxTok int64
	for _, lc := range normalizeLangList(targetLangs) {
		initial, review := ChatLegFixedTokens(sourceLang, lc, uiLang)
		leg1 := initial + src + out
		minTok += leg1
		if fast {
			maxTok += leg1 // 快速档不做校对 ⇒ 上限就是初翻那一腿
			continue
		}
		leg2 := review + src + out + out // 校对腿输入＝原文＋上一版译文，输出＝本版译文
		maxTok += leg1 + leg2
	}
	if maxTok < minTok {
		maxTok = minTok
	}
	if !fast {
		// 专业档的质检重译（applyOutputGates 在 pro 下才开）是**可能多跑一腿**的，
		// 取 1.5 而不是 2：重译只在闸门打回时才发生，按现网打回率它不是每次都跑。
		maxTok = maxTok + maxTok/2
	}
	if minTok < 1 {
		minTok = 1
	}
	return minTok, maxTok
}

// EstimateSegmentTokens 预估文件/工单场景的 token 消耗区间（按段数，无源文可读时的兜底腿）。
// 参数 segmentCount: 提取出的段数；其余同 EstimateChatTokens。
// 返回 (minTokens, maxTokens)。
//
// ★ 为什么这一档**仍然按段**乘固定开销：文件链是逐段调用模型的（file.go 每行一次 TranslateOne），
// 每一段都要重发一遍指令 ⇒ 段数在这里是真成本，在即时翻译里不是。
// 每段源文长度没有读数（只有段数），按 estSegmentSourceTokens 这一档平均长度折中，
// 并在出参文案里把它标成「≈」而不是精确值。
func EstimateSegmentTokens(segmentCount int64, targetLangs []string, mode, sourceLang, uiLang string) (int64, int64) {
	if segmentCount < 1 {
		segmentCount = 1
	}
	langs := normalizeLangList(targetLangs)
	src := estSegmentSourceTokens
	fast := isFastMode(mode)
	var minTok, maxTok int64
	for i := int64(0); i < segmentCount; i++ {
		for _, lc := range langs {
			initial, review := ChatLegFixedTokens(sourceLang, lc, uiLang)
			minTok += initial + src + src
			if fast {
				maxTok += initial + src + src // 快速档不做校对 ⇒ 上限＝初翻那一腿
				continue
			}
			maxTok += initial + src + src + review + src + src + src
		}
	}
	if !fast {
		maxTok = maxTok + maxTok/2
	}
	if minTok < 1 {
		minTok = 1
	}
	return minTok, maxTok
}

// isFastMode 归一模式串后再判快速档（调用方可能传 "FAST"/" fast "，与 ModeFromOptions 同一口径）。
// 参数 mode: 原始模式串；返回: 是否快速档。
func isFastMode(mode string) bool {
	return strings.ToLower(strings.TrimSpace(mode)) == "fast"
}

// estSegmentSourceTokens 段数兜底档里「一段源文」的平均 token 读数。
// 取 40 的依据：工单/文件的一行通常是术语行或一句短说明（中文 30–50 字、英文 150–200 字符，
// 经 ScriptTokens 分别落 30–50 与 40–50）。它只影响「只有段数、没有正文」这一条兜底路径
// （工作台传的是全文，走 EstimateChatTokens）。
const estSegmentSourceTokens int64 = 40

// normalizeLangList 归一目标语种列表：空列表按单个未知语种处理（别让零语种估出零消耗）。
// 参数 langs: 原始列表。返回: 至少长度 1 的列表。
func normalizeLangList(langs []string) []string {
	out := make([]string, 0, len(langs))
	for _, l := range langs {
		if s := strings.TrimSpace(l); s != "" {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// OcrFixAllowanceTokens 一次文件工单的 OCR 理解/纠错预估 allowance（主方案 §8.5）。
//
// 为什么要它：③ 的用量与翻译同一条通道（CallChat 自动计量与实时扣费），若预估侧不计入，
// 预估与实扣就变成两把尺子（AGENTS §一·5④）。这里给的是**上界口径**：
// 每份文件最多 2 次调用、每次最多 60 行，按"固定指令开销（从真提示词现量）＋每行载荷"折算。
//
// ⚠️ 改 ocrFixPrompt 必须同步改这条 allowance，并由 TestOcrFixAllowanceDerivesFromRealPrompt
// 等值锁钉住（改模板即改预估，同 §一·5④ 那条）。
func OcrFixAllowanceTokens(imageFiles int) int64 {
	if imageFiles <= 0 {
		return 0
	}
	fixed := ScriptTokens(ocrFixPrompt) // 现量，不写第二份字面量
	// 每行：原识别 + 候选/邻居/JSON 骨架（输入侧约 40 token）＋ 回执（输出侧约 16 token）。
	const perLineInput, perLineOutput = int64(40), int64(16)
	calls := int64(imageFiles) * 2
	return calls*fixed + int64(imageFiles)*2*60*(perLineInput+perLineOutput)
}
