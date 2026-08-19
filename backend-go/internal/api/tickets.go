package api

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

// handleTickets 工单列表
func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	onlyMine := r.URL.Query().Get("mine") == "1"
	tickets, err := s.Store.ListTickets(s.effTenant(r, u), u.ID, onlyMine)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": tickets})
}

// handleTicketCreate 创建工单
func (s *Server) handleTicketCreate(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Title       string `json:"title"`
		SourceText  string `json:"source_text"`
		TargetLangs string `json:"target_langs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceText == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供源文本"})
		return
	}
	if req.TargetLangs == "" {
		req.TargetLangs = "en"
	}
	t, err := s.Store.CreateTicket(s.effTenant(r, u), u.ID, req.Title, req.SourceText, "", req.TargetLangs)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_create", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// handleTicketRun 运行工单流程（FlowDef 编排）
func (s *Server) handleTicketRun(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	wf := s.workflow()
	if wf == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工作流未初始化"})
		return
	}
	t.Status = store.TicketInProgress
	_ = s.Store.UpdateTicket(t)
	err = wf.Executor.Execute(r.Context(), t, func(step string, ok bool, errMsg string) {})
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error(), "ticket": t})
		return
	}
	// 驳回重翻循环结束：清空驳回意见，避免下次运行重复重翻
	if t.RejectReason != "" {
		t.RejectReason = ""
		_ = s.Store.UpdateTicket(t)
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_run", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// handleTicketDetail 工单详情（含状态轨迹）
func (s *Server) handleTicketDetail(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	t, err := s.Store.GetTicket(id, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	states, _ := s.Store.TicketStates(id)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t, "states": states})
}

// ============ 审批（approver + admin） ============

// handleApproveList 待审批列表
func (s *Server) handleApproveList(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	if err := auth.RequireRole(u, 2); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tickets, err := s.Store.ListPendingApproval(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": tickets})
}

// handleApproveAction 审批操作：approve（批准）/ reject（驳回，带意见）
func (s *Server) handleApproveAction(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	if err := auth.RequireRole(u, 2); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID           int64  `json:"id"`
		Action       string `json:"action"` // approve / reject
		Reason       string `json:"reason"`
		Suggestion   string `json:"suggestion"`
		ApprovedText string `json:"approved_text"` // 审批员可编辑终稿
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	if t.Status != store.TicketPendingAppr {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不在待审批状态"})
		return
	}
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
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_"+req.Action, "tickets", t.TicketNo)

	// ★ 批准 → 触发自迭代（feedback 步骤）
	if req.Action == "approve" {
		if wf := s.workflow(); wf != nil {
			_ = wf.Executor.Execute(r.Context(), t, func(step string, ok bool, errMsg string) {})
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// workflow 构建工作流（惰性，需 Store+Engine+Tenant 齐备）
func (s *Server) workflow() *orchestrator.Workflow {
	if s.Store == nil || s.Engine == nil {
		return nil
	}
	return orchestrator.NewWorkflow(s.Store, s.Engine, s.Ten, s.DB)
}