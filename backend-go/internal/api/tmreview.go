// ============ tmreview.go · 职责说明 ============
// TM 自闭环审核台接口（全部仅超管）：
//   - GET  /api/admin/tm-review/list?status=   候选列表
//   - POST /api/admin/tm-review/approve {id}   通过 → SaveBack(module='manual') 入正式库
//   - POST /api/admin/tm-review/reject  {id}   驳回
//   - POST /api/admin/tm-review/adopt   {feedback_id, zh, lang, trans}
//     反馈修正采纳：建候选即通过（超管点击通过即人工审核），关联反馈可溯源
//
// ★ F-62（2026-09-26 批 I-8）：租户侧看不到自己候选的进度，已由**同域新文件**
//
//	`tmreview_tenant.go` 补只读接口 GET /api/me/tm-review/list（按 token 内 tid 裁剪、
//	不外发 reviewer/内部主键）。本文件的四个写/审接口仍然只服务超管。
//
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"translator/internal/auth"
	apierrors "translator/internal/errors"
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
		// ★ F-64②（批 I-10）：原 200 承载失败 → 500：待审池列表读的是本进程存储层。
		//   只读列表无「记录不存在」一说，故不涉及 404；旧写法把 DB 故障显示成「池子是空的」，
		//   超管会以为没有候选要审，自闭环的这条腿就静默断了。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
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
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404「候选不存在」（文案原样）。
		//   id 为 0（body 没带 id 或不是合法 JSON，上面刻意不拦）与 id 查不到都归这一支：
		//   审批要有对象，对象读不到就是不存在的 404，不是参数格式错（400 会让「已删掉的候选」看起来像填错了）。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "候选不存在"))
		return
	}
	// 校验候选状态：仅 pending 状态可处理
	if cr.Status != "pending" {
		// ★ F-64②（批 I-10）：原 200 承载失败 → 409「该候选已处理」（文案原样）：
		//   记录在、也读得到，只是**当前状态**（approved/rejected）不允许再次审批——
		//   典型是两人同时开审核台点了同一个候选。这是状态冲突，既非参数错也非本层故障。
		s.writeError(w, r, apierrors.New(apierrors.ErrConflict, "该候选已处理"))
		return
	}
	// 写入正式翻译记忆库（module='manual'）
	if _, err := s.DB.SaveBack(cr.Zh, map[string]string{cr.Lang: cr.Trans}, "manual", cr.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		// ★ F-64②（批 I-10）：原 200 承载失败 → 404「候选不存在」（文案原样，与 approve 同一判据）：
		//   驳回同样要有对象；对象读不到就是不存在，重试同一 id 无意义（不像 503 那样承诺「等一下就好」）。
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "候选不存在"))
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
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	if _, err := s.DB.SaveBack(cr.Zh, map[string]string{cr.Lang: cr.Trans}, "manual", cr.TenantID); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.invKB() // ★ KB 内容变更：失效引擎 CJK 精确缓存（新术语立即可命中）
	_ = s.Store.SetTmReviewStatus(cr.ID, "approved", u.DisplayName)
	s.Store.LogAudit(cr.TenantID, u.ID, "tm_review_adopt", "tm_review", cr.Zh)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
