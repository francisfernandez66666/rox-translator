// ============ tmreview.go · 职责说明 ============
// TM 自闭环审核台接口（全部仅超管）：
//   - GET  /api/admin/tm-review/list?status=   候选列表
//   - POST /api/admin/tm-review/approve {id}   通过 → SaveBack(module='manual') 入正式库
//   - POST /api/admin/tm-review/reject  {id}   驳回
//   - POST /api/admin/tm-review/adopt   {feedback_id, zh, lang, trans}
//     反馈修正采纳：建候选即通过（超管点击通过即人工审核），关联反馈可溯源
//
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"translator/internal/auth"
	"translator/internal/store"
)

// requireSuperJSON 超管鉴权（JSON 响应风格）：未登录 401 / 非超管 403，均返回 (nil,false) 终止后续处理。
// 参数 w: HTTP 响应写入器；r: HTTP 请求；返回: 当前用户与是否放行。
func (s *Server) requireSuperJSON(w http.ResponseWriter, r *http.Request) (*store.User, bool) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return nil, false
	}
	if !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超级管理员可操作"})
		return nil, false
	}
	return u, true
}

// handleTmReviewList 候选列表。
func (s *Server) handleTmReviewList(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireSuperJSON(w, r); !ok {
		return
	}
	list, err := s.Store.ListTmReviews(r.URL.Query().Get("status"))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if list == nil {
		list = []*store.TmReview{}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "candidates": list})
}

// handleTmReviewApprove 通过：入正式库（module='manual'）并标记 approved。
func (s *Server) handleTmReviewApprove(w http.ResponseWriter, r *http.Request) {
	// 超管鉴权
	u, ok := s.requireSuperJSON(w, r)
	if !ok {
		return
	}
	// 解析候选 ID
	var req struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	// 查询候选记录
	cr, err := s.Store.GetTmReview(req.ID)
	if err != nil || cr == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "候选不存在"})
		return
	}
	// 校验候选状态：仅 pending 状态可处理
	if cr.Status != "pending" {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "该候选已处理"})
		return
	}
	// 写入正式翻译记忆库（module='manual'）
	if _, err := s.DB.SaveBack(cr.Zh, map[string]string{cr.Lang: cr.Trans}, "manual", cr.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.invKB() // ★ KB 内容变更：失效引擎 CJK 精确缓存（新术语立即可命中）
	// 更新候选状态为已通过
	_ = s.Store.SetTmReviewStatus(cr.ID, "approved", u.DisplayName)
	// 记录审计日志
	s.Store.LogAudit(cr.TenantID, u.ID, "tm_review_approve", "tm_review", cr.Zh)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleTmReviewReject 驳回。
func (s *Server) handleTmReviewReject(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireSuperJSON(w, r)
	if !ok {
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	cr, err := s.Store.GetTmReview(req.ID)
	if err != nil || cr == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "候选不存在"})
		return
	}
	_ = s.Store.SetTmReviewStatus(cr.ID, "rejected", u.DisplayName)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleTmReviewAdopt 反馈修正采纳：建候选即通过（超管已在本页完成人工判断）。
func (s *Server) handleTmReviewAdopt(w http.ResponseWriter, r *http.Request) {
	u, ok := s.requireSuperJSON(w, r)
	if !ok {
		return
	}
	var req struct {
		FeedbackID int64  `json:"feedback_id"`
		Zh         string `json:"zh"`
		Lang       string `json:"lang"`
		Trans      string `json:"trans"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Zh) == "" || strings.TrimSpace(req.Trans) == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "参数缺失"})
		return
	}
	cr := &store.TmReview{
		TenantID: s.effTenant(r, u), Zh: strings.TrimSpace(req.Zh), Lang: req.Lang,
		Trans: strings.TrimSpace(req.Trans), Source: "feedback", RefType: "feedback", RefID: req.FeedbackID,
	}
	if err := s.Store.CreateTmReview(cr); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	if _, err := s.DB.SaveBack(cr.Zh, map[string]string{cr.Lang: cr.Trans}, "manual", cr.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.invKB() // ★ KB 内容变更：失效引擎 CJK 精确缓存（新术语立即可命中）
	_ = s.Store.SetTmReviewStatus(cr.ID, "approved", u.DisplayName)
	s.Store.LogAudit(cr.TenantID, u.ID, "tm_review_adopt", "tm_review", cr.Zh)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
