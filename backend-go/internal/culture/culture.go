// ============ culture.go · 职责说明 ============
// culture 包内部实现文件。
// =============================================
// Package culture 提供语言文化习惯包输出闸门。
// 反查译文：是否符合语言表达习惯、是否触发政治文化避雷词、度量衡/数字格式/语气是否合规。
package culture

// ============ 本文件职责中文说明 ============
// 语言文化包输出闸门（软/硬校验组合）：对目标语言译文执行反向质检——
// ① 政治文化避雷词反查（命中租户配置的译文侧避雷词即打回）；
// ② 数字格式（全角数字需改半角）；
// ③ 语气合规（含明显粗鲁词则打回）；
// ④ 表达习惯（译文非空）。任一不通过则 Pass=false 并附 Reasons 打回原因。
// ========================================

import (
	"regexp"
	"strings"

	"translator/internal/store"
)

// neutralEnWords 英语常见中性词豁免表：这类词即便被误采集为 forbidden（如 LLM 自动
// 采集把 control/hot 等普通词汇当敏感词）也不作避雷拦截，避免「无限扩大中性词的负面
// 概念」影响翻译质量。仅对英语生效，其他语言不豁免。
var neutralEnWords = map[string]bool{
	"control": true, "hot": true,
	// 常用中性词（防止 LLM 采集器把普通词汇误判为敏感词后打回正常译文）
	"open": true, "close": true, "start": true, "stop": true, "run": true,
	"make": true, "take": true, "get": true, "set": true, "check": true,
	"light": true, "water": true, "power": true, "high": true, "low": true,
	"cold": true, "cool": true, "fast": true, "slow": true, "left": true,
	"right": true, "front": true, "back": true, "top": true, "bottom": true,
	"off": true, "on": true, "up": true, "down": true, "over": true, "under": true,
	"out": true, "in": true, "near": true, "far": true, "hard": true, "soft": true,
	"wet": true, "dry": true, "clean": true, "dirty": true, "empty": true, "full": true,
	// ★ 2026-10-24 用户令「正常沟通交流的，不需要避雷」：情态动词与日常功能词一律豁免。
	//   这几类词在英语里是语法骨架，不是敏感概念；把它们当避雷词会让所有正常译文都命中
	//   （现网工单 114/115 的 `must,should` 连打 13 次就是这一形态）。
	"must": true, "should": true, "can": true, "could": true, "will": true,
	"would": true, "may": true, "might": true, "shall": true, "ought": true,
	"need": true, "dare": true, "help": true, "use": true, "want": true,
	"like": true, "work": true, "play": true, "go": true, "come": true,
	"see": true, "look": true, "say": true, "tell": true, "ask": true,
	"answer": true, "give": true, "find": true, "think": true, "know": true,
	"feel": true, "try": true, "call": true, "keep": true, "let": true,
	"put": true, "bring": true, "buy": true, "sell": true, "pay": true,
	"read": true, "write": true, "speak": true, "listen": true, "watch": true,
	"move": true, "turn": true, "show": true, "walk": true, "drive": true,
	"be": true, "am": true, "is": true, "are": true, "was": true, "were": true,
	"been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "done": true,
	"i": true, "you": true, "he": true, "she": true, "it": true, "we": true,
	"they": true, "me": true, "him": true, "her": true, "us": true, "them": true,
	"my": true, "your": true, "his": true, "their": true, "our": true,
	"this": true, "that": true, "these": true, "those": true,
	"and": true, "or": true, "but": true, "if": true, "because": true,
	"when": true, "while": true, "before": true, "after": true, "for": true,
	"with": true, "without": true, "from": true, "to": true, "of": true,
	"at": true, "by": true, "as": true, "about": true, "into": true,
	"between": true, "among": true, "through": true, "during": true,
	"against": true, "within": true, "upon": true,
	// ★ 2026-10-24 生产英语 forbidden 盘点后的日常/社会议题词：正常沟通不避雷。
	//   保留 white power（仇恨口号）与「蕃夷」（历史贬称）等真正的敏感/文化词。
	"blackmail": true, "stereotype": true, "blasphemy": true, "racist": true,
	"obnoxious": true, "stupid": true, "sexism": true, "divorce": true,
	"cheater": true, "colorism": true, "exotic": true, "n/a": true,
}

// neutralEnPhrases 英语常见多词短语豁免（单字表覆盖不到的组合；同样只对 en 生效）。
var neutralEnPhrases = map[string]bool{
	"native language": true, "discriminate against": true, "black friday": true,
	"christmas tree": true, "racial discrimination": true, "black sheep": true,
	"politically correct": true, "racial profiling": true, "culture clash": true,
	"white guy": true,
}

// IsNeutralForbidden 判断短语是否为应豁免的中性常见词（不作为避雷词拦截）。
// 参数：target=目标语言，phrase=避雷词；返回 true=豁免。
func IsNeutralForbidden(target, phrase string) bool {
	if target != "en" && target != "en_US" {
		return false
	}
	low := strings.ToLower(strings.TrimSpace(phrase))
	return neutralEnWords[low] || neutralEnPhrases[low]
}

// IsBlockingForbiddenRule 判断一条安全句是否应参与「避雷拦截」。
//
// ★ 2026-10-24 用户令：英语避雷词要收缩，正常沟通交流的词不需要避雷。
// 三条口径（只对 en 生效，其他语种保持原样）：
//  1. 只有 kind=forbidden（以及历史 kind 为空的行）参与拦截；style/replace 不再被当成避雷词
//     （旧 Run 不判 kind，style 里的长句/普通词也会被当 forbidden 子串匹配，是误报来源之一）。
//  2. 单字来自 LLM/自动采集（source=llm）⇒ 只观测不拦截；人工确认后才升级为拦截。
//  3. 单字若在常见词/中性词表里 ⇒ 无论来源都不拦截；多词短语、专有名词、真实敏感词保留。
//
// 返回 true 才允许进入词命中判定。
func IsBlockingForbiddenRule(target string, sp *store.KBSafetyPhrase) bool {
	if sp == nil {
		return false
	}
	return IsBlockingForbiddenFields(target, sp.Kind, sp.Phrase, sp.Source)
}

// IsBlockingForbiddenFields 与 IsBlockingForbiddenRule 同口径，但接收字段本身——
// 引擎侧 cultureRule 不带 store 结构体，用这个入口避免两套判定（同 §一·11 多把尺子）。
func IsBlockingForbiddenFields(target, kind, phrase, source string) bool {
	if strings.TrimSpace(phrase) == "" {
		return false
	}
	k := strings.ToLower(strings.TrimSpace(kind))
	if k != "" && k != "forbidden" {
		return false // style/replace 不参与避雷拦截
	}
	if target != "en" && target != "en_US" {
		return true // 非英语：保持既有行为
	}
	low := strings.ToLower(strings.TrimSpace(phrase))
	if neutralEnWords[low] || neutralEnPhrases[low] {
		return false
	}
	singleWord := !strings.ContainsAny(low, " \t")
	if singleWord && strings.EqualFold(strings.TrimSpace(source), "llm") {
		return false // 自动采集的单字只观测
	}
	return true
}

// ForbiddenHitRule 按「规则级」判定译文是否命中避雷词（含 kind/source/常见词策略）。
func ForbiddenHitRule(target, translation string, sp *store.KBSafetyPhrase) bool {
	if !IsBlockingForbiddenRule(target, sp) {
		return false
	}
	return wordHit(strings.ToLower(translation), strings.ToLower(strings.TrimSpace(sp.Phrase)))
}

// wordHit 对拉丁系单词做整词边界匹配，避免 control 命中 controller/controlling 等。
// 参数：text=译文，phrase=避雷词；返回是否命中。
func wordHit(text, phrase string) bool {
	// 整词边界匹配（单词型避雷词）；带空格/符号的短语走子串匹配
	if strings.ContainsAny(phrase, " \t") {
		return strings.Contains(text, phrase)
	}
	re := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(phrase) + `\b`)
	return re.MatchString(text)
}

// ForbiddenHit 判断译文是否命中 forbidden 避雷词（整词匹配 + 中性词豁免）。
// 参数：target=目标语言，translation=译文，phrase=避雷词；返回是否命中（豁免返回 false）。
func ForbiddenHit(target, translation, phrase string) bool {
	if phrase == "" {
		return false
	}
	if IsNeutralForbidden(target, phrase) {
		return false
	}
	return wordHit(strings.ToLower(translation), strings.ToLower(phrase))
}

// CultureResult 闸门检查结果
type CultureResult struct {
	Pass    bool     `json:"pass"`    // 是否全部通过
	Checks  []Check  `json:"checks"`  // 各单项检查明细
	Reasons []string `json:"reasons"` // 打回原因（供上游提示用户）
}

// Check 单项检查
type Check struct {
	Name   string `json:"name"`   // 检查项名称（如 政治文化避雷/数字格式/语气合规）
	Pass   bool   `json:"pass"`   // 该项是否通过
	Detail string `json:"detail"` // 失败时的详情（如命中的避雷词）
}

// Run 执行语言文化包输出闸门反查
// translation 是目标语言译文；safety 是该租户语言文化包中的避雷词/安全句（译文侧）
func Run(target string, translation string, safety []*store.KBSafetyPhrase) *CultureResult {
	res := &CultureResult{Pass: true}
	tr := strings.ToLower(strings.TrimSpace(translation))

	// 1. 政治文化避雷词反查（译文侧命中即打回；中性常见词豁免，避免 control/hot 等误拦）
	if len(safety) > 0 {
		hit := []string{}
		for _, sp := range safety {
			if sp.Lang == target && sp.Phrase != "" {
				// ★ 2026-10-24：规则级判定（kind/source/常见词策略）——style/replace 不再误当避雷词，
				//   自动采集的单字与日常词只观测不拦截（用户令：正常沟通交流不避雷）。
				if ForbiddenHitRule(target, tr, sp) {
					hit = append(hit, sp.Phrase)
				}
			}
		}
		pass := len(hit) == 0
		res.Checks = append(res.Checks, Check{"政治文化避雷", pass, strings.Join(hit, ",")})
		if !pass {
			res.Pass = false
			res.Reasons = append(res.Reasons, "命中避雷词: "+strings.Join(hit, ","))
		}
	}

	// 2. 度量衡/数字格式：中英文数字混排检查（弱校验）
	if strings.ContainsAny(tr, "０１２３４５６７８９") {
		res.Checks = append(res.Checks, Check{"数字格式", false, "译文含全角数字，应改为半角"})
		res.Pass = false
		res.Reasons = append(res.Reasons, "数字格式不合规")
	} else {
		res.Checks = append(res.Checks, Check{"数字格式", true, ""})
	}

	// 3. 语气礼貌检查（弱校验：含明显粗鲁词）
	rudeness := []string{"fuck", "shit", "bitch", "damn you"}
	bad := ""
	for _, w := range rudeness {
		if strings.Contains(tr, w) {
			bad = w
			break
		}
	}
	pass := bad == ""
	res.Checks = append(res.Checks, Check{"语气合规", pass, bad})
	if !pass {
		res.Pass = false
		res.Reasons = append(res.Reasons, "语气不合规")
	}

	// 4. 表达习惯（弱校验：译文不应为空且非全英文乱码已由 Gate 覆盖）
	res.Checks = append(res.Checks, Check{"表达习惯", tr != "", ""})
	if tr == "" {
		res.Pass = false
		res.Reasons = append(res.Reasons, "译文为空")
	}

	return res
}
