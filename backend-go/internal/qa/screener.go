// ============================================================================
// H4 TM 自动审核预筛：对「工单反馈/审批终稿 → TM」的每一对句做质量打分，
// 高分直写、低分转人审（tm_review 池），避免把带错误的终稿沉淀进翻译记忆。
// 复用 qa.Check 全套规则（empty/same/number/placeholder/length/punctuation），
// 折算 0-100 分：error 每项 -40，warning 每项 -8（长度比/占位符已含在规则内）。
// ============================================================================
package qa

import "fmt"

// ScreenResult 预筛结果。
type ScreenResult struct {
	Score   int      // 0-100（是否自动入库由调用方与阈值比较）
	Reasons []string // 扣分原因（最多 3 条，人审展示用）
}

// ScreenPair 对单对句打分（不判阈值，Pass 由调用方按配置比较 Score）。
func ScreenPair(source, target string) ScreenResult {
	r := Check(source, map[string]string{"_": target})
	score := 100 - 40*r.Errors - 8*r.Warnings
	if score < 0 {
		score = 0
	}
	var reasons []string
	for _, iss := range r.Issues {
		if len(reasons) >= 3 {
			break
		}
		reasons = append(reasons, fmt.Sprintf("%s:%s", iss.Rule, iss.Detail))
	}
	return ScreenResult{Score: score, Reasons: reasons}
}
