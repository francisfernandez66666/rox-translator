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
}

// IsNeutralForbidden 判断短语是否为应豁免的中性常见词（不作为避雷词拦截）。
// 参数：target=目标语言，phrase=避雷词；返回 true=豁免。
func IsNeutralForbidden(target, phrase string) bool {
	if target != "en" && target != "en_US" {
		return false
	}
	return neutralEnWords[strings.ToLower(strings.TrimSpace(phrase))]
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
				if ForbiddenHit(target, tr, sp.Phrase) {
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
