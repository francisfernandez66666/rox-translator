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
	"strings"

	"translator/internal/store"
)

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

	// 1. 政治文化避雷词反查（译文侧命中即打回）
	if len(safety) > 0 {
		hit := []string{}
		for _, sp := range safety {
			if sp.Lang == target && sp.Phrase != "" {
				if strings.Contains(tr, strings.ToLower(sp.Phrase)) {
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
