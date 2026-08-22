package api

// ============ 本文件职责中文说明 ============
// 翻译前报价预览接口（estimate）：与计量同口径预估本次翻译将消耗的句数，
// 只读不扣减。前端在工作台/建单页提交前展示「预计消耗 X 句 · 余额 Y 句」，
// 余额不足时前置拦截引导订阅，避免翻译后才发现额度不够。
// 口径：句数 = countSentences(源文本 或 文件段数) × 目标语言数（与 meterSentences 完全一致）。
// =============================================

import (
	"encoding/json"
	"net/http"
)

// handleTranslationEstimate 报价预估接口（登录用户，纯只读）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 text 或 segment_count 与 target_langs）。
// 返回: success=true 时携带：
//
//	sentences=源句数；langs=目标语言数；cost=预计消耗句数；
//	balance=当前句数余额（平台上下文返回 -1 表示不限）；sentence_enforced=是否强制句数计费；
//	activated=租户是否已开通（有包身份或余额>0）；hint=未开通时的提示文案。
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 源句数：优先按文本实时计算（同计量口径），否则用文件段数
	sents := req.SegmentCount
	if req.Text != "" {
		sents = countSentences(req.Text)
	}
	if sents < 1 {
		sents = 1 // 空文本兜底为 1 句（与计量兜底一致）
	}
	langs := int64(len(req.TargetLangs))
	if langs < 1 {
		langs = 1
	}
	cost := sents * langs
	tid := s.effTenant(r, u)
	enforced := false
	if v, _ := s.Store.GetConfig("sentence_enforced"); v == "1" && tid > 0 {
		enforced = true
	}
	balance := int64(-1) // 平台上下文（tid<=0）无计费概念：-1 表示不限
	activated := true
	hint := ""
	if tid > 0 {
		perms, err := s.Store.GetTenantPerms(tid)
		if err == nil {
			balance = perms.SentenceBalance
			activated = perms.PackageCode != "" || perms.SentenceBalance > 0
		}
		// 未开通且审核模式开启：提示等待发放
		if !activated {
			hint = "额度已用尽或尚未开通"
			if v, _ := s.Store.GetConfig("registration_review"); v == "1" {
				hint = "等待管理员审核发放试用额度"
			}
		}
	}
	resp := map[string]interface{}{
		"success":           true,
		"sentences":         sents,
		"langs":             langs,
		"cost":              cost,
		"balance":           balance,
		"sentence_enforced": enforced,
		"activated":         activated,
		"hint":              hint,
	}
	writeJSON(w, 200, resp)
}
