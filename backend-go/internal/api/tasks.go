// ============ tasks.go · 职责说明 ============
// 任务中心 HTTP 接口（功能③：个人中心 → 任务中心）。
//   - GET  /api/admin/tasks            超管：任务列表（含停用项）
//   - POST /api/admin/tasks/save       超管：新增/更新任务（每日/一次性 + 永久 token 奖励）
//   - POST /api/admin/tasks/delete     超管：删除任务（连带清理领取记录）
//   - GET  /api/me/tasks               登录用户：启用任务 + 本人领取状态
//   - POST /api/me/tasks/claim         登录用户：一键领取奖励（永久 token 入账户）
//
// 安全要点：管理接口仅超管（requireAdminUser）；用户接口需登录（authUser 非空）。
package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"translator/internal/store"
)

// handleAdminTasks 任务列表（超管后台：含停用项，按 sort_order 排序）。
func (s *Server) handleAdminTasks(w http.ResponseWriter, r *http.Request) {
	_, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tasks": s.Store.ListUserTasks()})
}

// handleAdminTaskSave 新增/更新任务（超管）。
// body: id（>0 更新）/ task_type(daily|once) / title / description / reward_tokens / enabled / sort_order。
func (s *Server) handleAdminTaskSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req store.UserTask
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Title == "" {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "任务标题不能为空"})
		return
	}
	id, err := s.Store.SaveUserTask(&req)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "task_save", "user_tasks", req.Title)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleAdminTaskDelete 删除任务（超管，连带清理领取记录）。
func (s *Server) handleAdminTaskDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待删除任务 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少任务 id"})
		return
	}
	if err := s.Store.DeleteUserTask(req.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "task_delete", "user_tasks", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleMyTasks 用户视角任务列表（登录用户：启用任务 + 本人领取状态）。
func (s *Server) handleMyTasks(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tasks": s.Store.ListUserTaskViews(u.ID)})
}

// handleClaimTask 用户领取任务奖励（登录用户：每日任务当日一次 / 一次性任务终身一次）。
func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待领取任务 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少任务 id"})
		return
	}
	ok, tokens := s.Store.ClaimUserTask(u.ID, u.TenantID, req.ID)
	if !ok {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "任务不可领取（已领取/已停用/奖励为 0）"})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tokens": tokens})
}
