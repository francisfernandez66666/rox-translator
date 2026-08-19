// Package culture 提供语言文化习惯包输出闸门。
// 反查译文：是否符合语言表达习惯、是否触发政治文化避雷词、度量衡/数字格式/语气是否合规。
package culture

import (
	"strings"

	"translator/internal/store"
)

// CultureResult 闸门检查结果
type CultureResult struct {
	Pass    bool     `json:"pass"`
	Checks  []Check  `json:"checks"`
	Reasons []string `json:"reasons"` // 打回原因
}

// Check 单项检查
type Check struct {
	Name   string `json:"name"`
	Pass   bool   `json:"pass"`
	Detail string `json:"detail"`
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