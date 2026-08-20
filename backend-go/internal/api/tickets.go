package api

// ============ 本文件职责中文说明 ============
// 本文件实现工单流程与审批相关接口：
//   - 工单管理（tenant_admin+）：列表/创建/运行/详情（handleTickets / handleTicketCreate / handleTicketRun / handleTicketDetail）
//   - 审批（approver + admin）：待审批列表 / 审批操作（handleApproveList / handleApproveAction）
// 业务要点：
//   - handleTicketRun 执行前先过配额闸门（gateUsage），成功后按源文本字符数计量，失败计入指标
//   - 驳回重翻循环：重翻成功后清空驳回意见，避免下次运行重复重翻
//   - 批准审批后触发自迭代（feedback 步骤）；驳回时记录原因与建议
//   - 工单操作均写入审计；工单查询全部限定生效租户（租户隔离）

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"translator/internal/auth"
	"translator/internal/orchestrator"
	"translator/internal/store"
)

// ============ 工单 ============

// handleTickets 工单列表接口（tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 mine=1 仅显示本人创建的工单）。
// 返回: success=true 时携带 tickets 数组。
func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需租户管理员及以上权限
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 查询参数：mine=1 仅列出当前用户创建的工单
	onlyMine := r.URL.Query().Get("mine") == "1"
	// 租户隔离：仅查询生效租户下的工单
	tickets, err := s.Store.ListTickets(s.effTenant(r, u), u.ID, onlyMine)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": tickets})
}

// handleTicketCreate 创建工单接口（tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 title/source_text/target_langs）。
// 返回: success=true 时携带新工单对象。
func (s *Server) handleTicketCreate(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需租户管理员及以上权限
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Title       string `json:"title"`        // 工单标题
		SourceText  string `json:"source_text"`  // 待翻译源文本（必填）
		TargetLangs string `json:"target_langs"` // 目标语言列表（逗号分隔，默认 en）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceText == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供源文本"})
		return
	}
	// 默认目标语言 en
	if req.TargetLangs == "" {
		req.TargetLangs = "en"
	}
	// 创建工单（归属生效租户）
	t, err := s.Store.CreateTicket(s.effTenant(r, u), u.ID, req.Title, req.SourceText, "", req.TargetLangs)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 创建工单审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_create", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// handleTicketRun 运行工单流程接口（FlowDef 编排，tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 时携带运行后的工单对象（含流程结果）。
// 流程：取工单 → 配额闸门 → 置为进行中 → 执行编排 → 计量 → 清空驳回意见。
func (s *Server) handleTicketRun(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需租户管理员及以上权限
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 工单 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	// 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝运行）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": gateErr.Error()})
		return
	}
	wf := s.workflow()
	if wf == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工作流未初始化"})
		return
	}
	// 置为进行中状态
	t.Status = store.TicketInProgress
	_ = s.Store.UpdateTicket(t)
	// 执行流程编排（FlowDef 定义的各步骤）
	err = wf.Executor.Execute(r.Context(), t, func(step string, ok bool, errMsg string) {})
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error(), "ticket": t})
		// 失败计入指标（ticket 类型失败）
		s.metrics.countTranslate("ticket", false)
		return
	}
	// 计量：工单翻译按源文本字符数计量（含驳回重翻，每次运行都计量）
	s.meterUsage(r, tid, "translate", int64(len([]rune(t.SourceText))))
	s.metrics.countTranslate("ticket", true)
	// 驳回重翻循环结束：清空驳回意见，避免下次运行重复重翻
	if t.RejectReason != "" {
		t.RejectReason = ""
		_ = s.Store.UpdateTicket(t)
	}
	// 运行审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_run", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// handleTicketDetail 工单详情接口（含状态轨迹，tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 id）。
// 返回: success=true 时携带 ticket（工单）与 states（状态轨迹数组）。
func (s *Server) handleTicketDetail(w http.ResponseWriter, r *http.Request) {
	// 鉴权：需租户管理员及以上权限
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 解析工单 ID（来自查询参数）
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(id, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	// 附带状态轨迹（流程步骤执行历史）
	states, _ := s.Store.TicketStates(id)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t, "states": states})
}

// ============ 审批（approver + admin） ============

// handleApproveList 待审批列表接口（approver + admin 角色，权限等级 >=2）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回: success=true 时携带待审批 tickets 数组。
func (s *Server) handleApproveList(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 权限校验：需角色等级 >= 2（approver/admin/tenant_admin/super_admin）
	if err := auth.RequireRole(u, 2); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 租户隔离：仅列出生效租户下待审批工单
	tickets, err := s.Store.ListPendingApproval(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": tickets})
}

// handleApproveAction 审批操作接口：approve（批准）/ reject（驳回，带意见）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/action/reason/suggestion/approved_text）。
// 返回: success=true 表示审批完成；批准会触发自迭代（feedback 步骤）。
func (s *Server) handleApproveAction(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 权限校验：需角色等级 >= 2
	if err := auth.RequireRole(u, 2); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID           int64  `json:"id"`            // 工单 ID
		Action       string `json:"action"`        // approve / reject
		Reason       string `json:"reason"`        // 驳回原因
		Suggestion   string `json:"suggestion"`    // 修改建议（附加到驳回原因）
		ApprovedText string `json:"approved_text"` // 审批员可编辑终稿
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	// 状态机约束：仅待审批状态的工单可审批
	if t.Status != store.TicketPendingAppr {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不在待审批状态"})
		return
	}
	// 根据 action 更新工单状态与审批字段
	switch req.Action {
	case "approve":
		t.Status = store.TicketApproved
		t.ApproverID = u.ID
		if req.ApprovedText != "" {
			t.FinalResult = req.ApprovedText
		}
	case "reject":
		t.Status = store.TicketRejected
		t.ApproverID = u.ID
		t.RejectReason = req.Reason
		if req.Suggestion != "" {
			t.RejectReason = strings.TrimSpace(req.Reason + "；建议: " + req.Suggestion)
		}
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "action 仅支持 approve/reject"})
		return
	}
	if err := s.Store.UpdateTicket(t); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 审批审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_"+req.Action, "tickets", t.TicketNo)

	// 批准 → 触发自迭代（feedback 步骤）：批准后自动执行一次流程做质量反馈
	if req.Action == "approve" {
		if wf := s.workflow(); wf != nil {
			_ = wf.Executor.Execute(r.Context(), t, func(step string, ok bool, errMsg string) {})
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// workflow 构建工作流（惰性，需 Store+Engine+Tenant 齐备）。
// 返回: 编排工作流实例；存储或引擎未初始化时返回 nil。
func (s *Server) workflow() *orchestrator.Workflow {
	if s.Store == nil || s.Engine == nil {
		return nil
	}
	return orchestrator.NewWorkflow(s.Store, s.Engine, s.Ten, s.DB)
}
