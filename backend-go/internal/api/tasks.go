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
// ★ 2026-09-19 积分口径：reward_tokens 出参下线，一律折成 reward_points。
func (s *Server) handleAdminTasks(w http.ResponseWriter, r *http.Request) {
	_, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tasks": s.taskViews(s.Store.ListUserTasks())})
}

// taskJSON 任务定义出参视图（token 奖励折成积分，其余字段同名透传）。
type taskJSON struct {
	ID          int64  `json:"id"`
	TaskType    string `json:"task_type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	RewardPoints int64  `json:"reward_points"` // ★ 积分口径（内部按汇率折回 token 记账）
	Enabled     int    `json:"enabled"`
	SortOrder   int    `json:"sort_order"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// taskViews 任务定义列表 → 积分口径出参。
func (s *Server) taskViews(list []*store.UserTask) []taskJSON {
	out := make([]taskJSON, 0, len(list))
	for _, t := range list {
		out = append(out, taskJSON{
			ID: t.ID, TaskType: t.TaskType, Title: t.Title, Description: t.Description,
			RewardPoints: s.Store.PointsFromTokens(t.RewardTokens),
			Enabled:      t.Enabled, SortOrder: t.SortOrder,
			CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		})
	}
	return out
}

// handleAdminTaskSave 新增/更新任务（超管）。
// body: id（>0 更新）/ task_type(daily|once) / title / description / reward_points / enabled / sort_order。
// ★ 积分口径：reward_points 入参，服务端按汇率折算成永久 token 落库。
func (s *Server) handleAdminTaskSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID           int64  `json:"id"`
		TaskType     string `json:"task_type"`
		Title        string `json:"title"`
		Description  string `json:"description"`
		RewardPoints *int64 `json:"reward_points"`
		Enabled      *int   `json:"enabled"`
		SortOrder    *int   `json:"sort_order"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Title == "" {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "任务标题不能为空"})
		return
	}
	task := store.UserTask{ID: req.ID, TaskType: req.TaskType, Title: req.Title, Description: req.Description}
	if req.RewardPoints != nil {
		task.RewardTokens = s.Store.TokensFromPoints(*req.RewardPoints)
	}
	if req.Enabled != nil {
		task.Enabled = *req.Enabled
	}
	if req.SortOrder != nil {
		task.SortOrder = *req.SortOrder
	}
	id, err := s.Store.SaveUserTask(&task)
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
// ★ 运营策略总开关（2026-09）：task.enabled=false 时任务中心整体隐藏（发放中台关闸）。
func (s *Server) handleMyTasks(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	tid := s.effTenant(r, u)
	if !s.effectivePolicyCached(tid).Task.Enabled { // ★ C31
		writeJSON(w, 200, map[string]interface{}{"success": true, "tasks": []interface{}{}, "disabled": true})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tasks": s.taskViewList(s.Store.ListUserTaskViews(u.ID))})
}

// taskViewJSON 用户视角任务出参（积分口径 + 本人领取状态）。
type taskViewJSON struct {
	taskJSON
	Claimed   bool   `json:"claimed"`
	ClaimedAt string `json:"claimed_at"`
}

// taskViewList 用户任务视图列表 → 积分口径出参。
func (s *Server) taskViewList(list []*store.UserTaskView) []taskViewJSON {
	out := make([]taskViewJSON, 0, len(list))
	for _, v := range list {
		tv := taskViewJSON{Claimed: v.Claimed, ClaimedAt: v.ClaimedAt}
		if len(s.taskViews([]*store.UserTask{&v.UserTask})) > 0 {
			tv.taskJSON = s.taskViews([]*store.UserTask{&v.UserTask})[0]
		}
		out = append(out, tv)
	}
	return out
}

// handleClaimTask 用户领取任务奖励（登录用户：每日任务当日一次 / 一次性任务终身一次）。
// ★ 运营策略总开关（2026-09）：task.enabled=false 时拒绝领取（发放中台关闸）。
func (s *Server) handleClaimTask(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	if !s.effectivePolicyCached(s.effTenant(r, u)).Task.Enabled { // ★ C31
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "任务奖励暂未开放"})
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
	writeJSON(w, 200, map[string]interface{}{"success": true, "points": s.Store.PointsFromTokens(tokens)})
}
