package api

// ============ 本文件职责中文说明 ============
// 翻译前报价预览接口（estimate）：按 Token 实费口径预估本次翻译的消耗区间，
// 只读不扣减。前端在工作台/建单页提交前展示「预计消耗 X~Y token ≈ A 句 · 余额 N token」。
// 估算公式（经验值，实际以任务完成后真实聚合为准）：
//   fast：每源句每语言 ≈ 初翻+校对两轮 ≈ 2×rate 基准
//   pro ：fast 基础上叠加 Judge×2 + 文化反查 + KB 检索 ≈ ×2.5
// =============================================

import (
	"encoding/json"
	"net/http"
)

// handleTranslationEstimate 报价预估接口（登录用户，纯只读）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 text 或 segment_count、target_langs、mode）。
// 返回: success=true 时携带：
//
//	tokens_min/tokens_max=预估消耗区间（已含均摊系数）；cost_sentences≈句数换算；
//	balance_tokens=当前 token 余额；balance_sentences_approx=余额≈句数；
//	activated=租户是否已开通；hint=未开通提示。
func (s *Server) handleTranslationEstimate(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		Text         string   `json:"text"`          // 文本内容（与 segment_count 二选一）
		SegmentCount int64    `json:"segment_count"` // 文件提取段数（文件场景直传）
		TargetLangs  []string `json:"target_langs"`  // 目标语言列表
		Mode         string   `json:"mode"`          // fast | pro（默认 pro）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 源句数：优先按文本实时计算，否则用文件段数
	sents := req.SegmentCount
	if req.Text != "" {
		sents = countSentences(req.Text)
	}
	if sents < 1 {
		sents = 1
	}
	langs := int64(len(req.TargetLangs))
	if langs < 1 {
		langs = 1
	}
	units := sents * langs // 翻译工作量单位（源句×语言）
	rate := s.Store.TokenSentenceRate()
	markup := s.markupMultiplier()
	// 经验基准：每「源句×语言」两轮调用约 2×rate token（初翻+校对，含提示词开销）
	base := int64(2*rate) * units
	minTokens := int64(float64(base) * markup)
	maxTokens := minTokens
	if normalizeTaskMode(req.Mode) != "fast" {
		// 专业模式叠加 Judge×2/文化反查/KB 检索：经验放大 ~2.5×
		maxTokens = int64(float64(minTokens) * 2.5)
	}
	tokens, approxBal := s.balancePayload(s.effTenant(r, u))
	tid := s.effTenant(r, u)
	activated := true
	hint := ""
	if tid > 0 {
		perms, err := s.Store.GetTenantPerms(tid)
		if err == nil && perms.PackageCode == "" && tokens <= 0 {
			activated = false
			hint = "额度已用尽或尚未开通，请充值或升级套餐"
			if v, _ := s.Store.GetConfig("registration_review"); v == "1" {
				hint = "等待管理员审核发放试用额度"
			}
		}
	}
	writeJSON(w, 200, map[string]interface{}{
		"success":                  true,
		"sentences":                units,
		"tokens_min":               minTokens,
		"tokens_max":               maxTokens,
		"cost_sentences_approx":    maxTokens / rate, // 上限≈句数（保守展示）
		"balance_tokens":           tokens,
		"balance_sentences_approx": approxBal,
		"sufficient":               !s.Bill.Enabled() || tokens > 0,
		"activated":                activated,
		"hint":                     hint,
	})
}
