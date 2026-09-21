// ============ tasks.go · 职责说明 ============
// 任务中心 HTTP 接口（功能③：个人中心 → 任务中心；★ #33 2026-09-21 任务系统扩容）。
//   - GET  /api/admin/tasks                   超管：任务列表（含停用项）
//   - POST /api/admin/tasks/save              超管：新增/更新任务（手工领取 or 事件自动发放）
//   - POST /api/admin/tasks/delete            超管：删除任务（连带清理领取记录）
//   - POST /api/admin/tasks/reset-consumption 超管：★#33 特殊任务——重置已订阅全部用户的任务积分消耗量
//   - GET  /api/me/tasks                      登录用户：启用任务 + 本人领取/发放进度
//   - POST /api/me/tasks/claim                登录用户：一键领取（仅手工任务；事件任务由系统自动发放）
//
// 安全要点：管理接口仅超管（requireAdminUser）；用户接口需登录（authUser 非空）。
// 出参一律积分口径（reward_points），零 token 裸值外发。
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/store"
)

// handleAdminTasks 任务列表（超管后台：含停用项，按 sort_order 排序）。
// ★ 2026-09-19 积分口径：reward_tokens 出参下线，一律折成 reward_points。
func (s *Server) handleAdminTasks(w http.ResponseWriter, r *http.Request) {
	_, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tasks": s.taskViews(s.Store.ListUserTasks())})
}

// taskJSON 任务定义出参视图（token 奖励折成积分，其余字段同名透传）。
type taskJSON struct {
	ID           int64  `json:"id"`
	TaskType     string `json:"task_type"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	RewardPoints int64  `json:"reward_points"` // ★ 积分口径（内部按汇率折回 token 记账）
	RewardKind   string `json:"reward_kind"`   // temporary=临时积分（带到期）/ permanent=永久积分
	Enabled      int    `json:"enabled"`
	SortOrder    int    `json:"sort_order"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
	// ★ #33：事件自动发放口径（grant_mode=auto 时由系统按事件触发，用户无需点击领取）
	TaskKey     string `json:"task_key"`
	GrantMode   string `json:"grant_mode"`
	Period      string `json:"period"`
	ValidDays   int    `json:"valid_days"`
	StackExpiry int    `json:"stack_expiry"`
	CapPerDay   int    `json:"cap_per_day"`
	CapPerWeek  int    `json:"cap_per_week"`
}

// taskViews 任务定义列表 → 积分口径出参。
func (s *Server) taskViews(list []*store.UserTask) []taskJSON {
	out := make([]taskJSON, 0, len(list))
	for _, t := range list {
		out = append(out, s.taskViewOf(t))
	}
	return out
}

// taskViewOf 单条任务定义 → 出参（临时/永久形态由 valid_days 判定，与发放口径一致）。
func (s *Server) taskViewOf(t *store.UserTask) taskJSON {
	kind := "permanent"
	if t.ValidDays > 0 {
		kind = "temporary"
	}
	return taskJSON{
		ID: t.ID, TaskType: t.TaskType, Title: t.Title, Description: t.Description,
		RewardPoints: s.Store.PointsFromTokens(t.RewardTokens), RewardKind: kind,
		Enabled: t.Enabled, SortOrder: t.SortOrder,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
		TaskKey: t.TaskKey, GrantMode: t.GrantMode, Period: t.Period,
		ValidDays: t.ValidDays, StackExpiry: t.StackExpiry, CapPerDay: t.CapPerDay, CapPerWeek: t.CapPerWeek,
	}
}

// handleAdminTaskSave 新增/更新任务（超管）。
// body: id（>0 更新）/ task_type(daily|once) / title / description / reward_points / enabled / sort_order。
// ★ 积分口径：reward_points 入参，服务端按汇率折算成永久 token 落库。
func (s *Server) handleAdminTaskSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		// ★ #33：事件自动发放口径（不传即保持手工领取语义）
		TaskKey     string `json:"task_key"`
		GrantMode   string `json:"grant_mode"`   // manual|auto
		Period      string `json:"period"`       // daily|weekly|once|event
		ValidDays   *int   `json:"valid_days"`   // >0=临时积分天数，0=永久
		StackExpiry *int   `json:"stack_expiry"` // 1=到期叠加，0=固定到期
		CapPerDay   *int   `json:"cap_per_day"`  // 每日上限（0=不限）
		CapPerWeek  *int   `json:"cap_per_week"` // 每周上限（0=不限）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if req.Title == "" {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "任务标题不能为空"})
		return
	}
	task := store.UserTask{ID: req.ID, TaskType: req.TaskType, Title: req.Title, Description: req.Description,
		TaskKey: strings.TrimSpace(req.TaskKey)}
	if req.RewardPoints != nil {
		task.RewardTokens = s.Store.TokensFromPoints(*req.RewardPoints)
	}
	if req.Enabled != nil {
		task.Enabled = *req.Enabled
	}
	if req.SortOrder != nil {
		task.SortOrder = *req.SortOrder
	}
	task.GrantMode = req.GrantMode
	task.Period = req.Period
	if req.ValidDays != nil {
		task.ValidDays = *req.ValidDays
	}
	if req.StackExpiry != nil {
		task.StackExpiry = *req.StackExpiry
	} else {
		task.StackExpiry = 1 // 新建缺省按叠加口径
	}
	if req.CapPerDay != nil {
		task.CapPerDay = *req.CapPerDay
	}
	if req.CapPerWeek != nil {
		task.CapPerWeek = *req.CapPerWeek
	}
	// ★ 更新场景：请求未下发的字段以库内原值兜底——超管只改标题或启停时，
	// 不能把奖励额清零、也不能把事件任务（grant_mode=auto）悄悄退回手工任务而让钩子失效。
	if req.ID > 0 {
		if old := s.Store.GetUserTask(req.ID); old != nil {
			if task.TaskKey == "" {
				task.TaskKey = old.TaskKey
			}
			if req.RewardPoints == nil {
				task.RewardTokens = old.RewardTokens
			}
			if req.GrantMode == "" {
				task.GrantMode = old.GrantMode
			}
			if req.Period == "" {
				task.Period = old.Period
			}
			if req.ValidDays == nil {
				task.ValidDays = old.ValidDays
			}
			if req.StackExpiry == nil {
				task.StackExpiry = old.StackExpiry
			}
			if req.CapPerDay == nil {
				task.CapPerDay = old.CapPerDay
			}
			if req.CapPerWeek == nil {
				task.CapPerWeek = old.CapPerWeek
			}
			// enabled/sort_order 同为「不传即不变」：缺省写 0 会把任务静默停用（历史踩坑）
			if req.Enabled == nil {
				task.Enabled = old.Enabled
			}
			if req.SortOrder == nil {
				task.SortOrder = old.SortOrder
			}
		}
	} else if req.Enabled == nil {
		task.Enabled = 1 // 新建缺省启用（与超管面板默认一致）
	}
	id, err := s.Store.SaveUserTask(&task)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "task_save", "user_tasks", req.Title)
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id})
}

// handleAdminTaskDelete 删除任务（超管，连带清理领取记录）。
func (s *Server) handleAdminTaskDelete(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
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
	Claimed   bool                  `json:"claimed"`
	ClaimedAt string                `json:"claimed_at"`
	Reward    *store.TaskRewardStat `json:"reward,omitempty"` // ★ #33 事件任务的周期进度（手工任务为 null）
}

// taskViewList 用户任务视图列表 → 积分口径出参。
func (s *Server) taskViewList(list []*store.UserTaskView) []taskViewJSON {
	out := make([]taskViewJSON, 0, len(list))
	for _, v := range list {
		out = append(out, taskViewJSON{
			taskJSON:  s.taskViewOf(&v.UserTask),
			Claimed:   v.Claimed,
			ClaimedAt: v.ClaimedAt,
			Reward:    v.Reward,
		})
	}
	return out
}

// handleAdminTaskResetConsumption ★#33 特殊任务：超管手动「重置已订阅全部用户的积分消耗量」。
//
//	body: subscribed_only（默认 true=仅重置存在未过期订阅台账的租户；false=全部租户的任务台账）
//	效果：把未过期的任务临时积分台账 left 拉回 total（消耗完的一并重置），有效期 expires_at 不改写；
//	      付费/体验台账（kind=plan/trial）与永久余额不受影响。
//
// 幂等性：重复调用只影响仍未拉满的行，第二次通常 reset_rows=0。
func (s *Server) handleAdminTaskResetConsumption(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	var req struct {
		SubscribedOnly *bool `json:"subscribed_only"`
	}
	if r.Body != nil {
		if e := json.NewDecoder(r.Body).Decode(&req); e != nil {
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
			return
		}
	}
	subscribedOnly := true
	if req.SubscribedOnly != nil {
		subscribedOnly = *req.SubscribedOnly
	}
	tenants, rows, e := s.Store.ResetTaskGrantConsumption(subscribedOnly)
	if e != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), e)})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "task_reset_consumption", "quota_grants",
		fmt.Sprintf("subscribed_only=%v tenants=%d rows=%d", subscribedOnly, tenants, rows))
	writeJSON(w, 200, map[string]interface{}{
		"success": true,
		"message": "已重置任务积分消耗量（有效期保持不变）",
		"tenants": tenants, "reset_rows": rows,
	})
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
