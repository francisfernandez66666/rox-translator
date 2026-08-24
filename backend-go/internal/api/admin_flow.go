// ============ admin_flow.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 流程引擎设置（handleFlowConfig / handleFlowSave / handleFlowRunTicket）
// 安全要点：全部接口仅超管可访问（requireAdminUser）；流程编排属平台级配置，
// 租户管理员无权查看/修改（含工单手动执行流程入口）。
// ========================================

import (
	"encoding/json"
	"net/http"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 流程引擎设置 ============

// handleFlowConfig 读取流程步骤配置（仅超管；经 X-Tenant-ID 切换生效租户，未配置回退默认定义）
func (s *Server) handleFlowConfig(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	steps := flowStepsForTenant(s.Ten, s.effTenant(r, u))
	writeJSON(w, 200, map[string]interface{}{"success": true, "steps": steps})
}

// flowStepsForTenant 组装租户流程步骤：租户 flow_config 启停 × 默认定义
func flowStepsForTenant(ts *tenant.Store, tid int64) []store.FlowStep {
	cfg := tenant.FlowConfig{}
	if ts != nil {
		cfg, _ = ts.GetFlowConfig(tid)
	}
	out := make([]store.FlowStep, 0, len(store.DefaultFlowSteps))
	for _, d := range store.DefaultFlowSteps {
		enable := d.Enable
		if on, ok := cfg.Steps[d.Key]; ok {
			enable = on
		}
		out = append(out, store.FlowStep{Key: d.Key, Name: d.Name, Enable: enable})
	}
	return out
}

// handleFlowSave 保存流程步骤启停（仅超管）
func (s *Server) handleFlowSave(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		Steps []store.FlowStep `json:"steps"` // 流程步骤启停配置数组（含 key/name/enable）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if s.Ten == nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "租户存储未初始化"})
		return
	}
	cfg := tenant.FlowConfig{Steps: map[string]bool{}}
	for _, st := range req.Steps {
		cfg.Steps[st.Key] = st.Enable
	}
	if err := s.Ten.SetFlowConfig(s.effTenant(r, u), cfg); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "flow_save", "tenants", "流程步骤配置更新")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleFlowRunTicket 直接对指定工单执行流程（仅超管触发）
func (s *Server) handleFlowRunTicket(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待执行流程的工单 ID
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
	if err := wf.Executor.Execute(r.Context(), t, nil); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error(), "ticket": t})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "flow_run", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}
