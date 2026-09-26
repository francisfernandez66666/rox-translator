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
	apierrors "translator/internal/errors"
	"translator/internal/store"
	"translator/internal/tenant"
)

// ============ 流程引擎设置 ============

// handleFlowConfig 读取流程步骤配置（仅超管；经 X-Tenant-ID 切换生效租户，未配置回退默认定义）
func (s *Server) handleFlowConfig(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
		// F-64②：流程配置落库失败是服务端出错（500），旧 200 壳让管理台提示「已保存」但库里没动
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "flow_save", "tenants", "流程步骤配置更新")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleFlowRunTicket 直接对指定工单执行流程（仅超管触发）
func (s *Server) handleFlowRunTicket(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		// 未登录 401／等级不足 403（★ F-64③ 批 I-10：旧写法两条都回 403，前端只在 401 走重登录链路）
		s.writeAuthzError(w, r, err)
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
		// F-64②：GetTicket 带租户过滤，查不到＝本租户视角下工单不存在 → 404（ErrTicketNotFound）；
		// 跨租户单也落到这里，天然不泄露存在性。本接口是已登录管理台入口，严禁用 401——
		// 401 会触发前端 handleUnauthorized 清登录态，而这里用户明明在线。
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	wf := s.workflow()
	if wf == nil {
		// F-64②：工作流引擎未初始化＝依赖未就绪（503），稍后重试有意义；
		// 旧 200 壳让管理台把「服务没起来」当业务失败弹普通 toast。
		s.writeError(w, r, apierrors.New(apierrors.ErrServiceUnavailable, "工作流未初始化"))
		return
	}
	if err := wf.Executor.Execute(r.Context(), t, nil); err != nil {
		// F-64②：流程执行中途失败（模型不可达/落库出错等真因已被 publicErrMessage 收敛成对外文案）
		// 是本层处理失败 → 500 兜底；handler 前置无「状态不允许运行」闸门（步骤禁用会在引擎内 skipped），
		// 故此处不存在 409 分支。原响应体带 ticket 快照字段，用 WithDetails 承接，别丢。
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)).
			WithDetails(map[string]interface{}{"ticket": s.ticketJSON(t)}))
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "flow_run", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": s.ticketJSON(t)})
}
