// ============ sales_flow_api.go · 职责说明 ============
// 三期 W2 六包接线：流程状态机的**只读排障面**（/api/admin/sales/flow/state）。
// 状态机本体按设计不暴露 HTTP 写口（二期 §9.2 :317 已定：推进只走
// store.AdvanceFlowStateMachine 的乐观锁，由 flow 引擎与 sweep 槽调用），
// 本面只读单条状态机行，服务「这条流程实例卡在哪了」的排障。
//
// ★ 查无回 200 {found:false}，不按 404（先例 sales_inbox_api.go）：
// 「查无此实例」与「流程还没推进到」在排障语义里不同——前者才是错误，
// 后者是正常态，404 会把它打成告警噪音。状态机在 Init 时才建行，
// 未初始化的实例查不到是设计内形态。
//
// 鉴权：读 ≥2（requireDeptAdmin，先例 sales_inbox_api.go）。
// ================================================
package api

import (
	"net/http"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// routesSalesFlow 注册流程状态机只读路由（由 routesSales() 调用）。
func (s *Server) routesSalesFlow() {
	s.mux.HandleFunc("/api/admin/sales/flow/state", s.handleSalesFlowState)
}

// handleSalesFlowState 读单条状态机（?fiid=流程实例 ID；查无 found=false 而非 404）。
func (s *Server) handleSalesFlowState(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	fiidStr := strings.TrimSpace(r.URL.Query().Get("fiid"))
	if fiidStr == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 fiid"))
		return
	}
	fiid, err := strconv.ParseInt(fiidStr, 10, 64)
	if err != nil || fiid <= 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "fiid 必须是正整数"))
		return
	}
	row, err := s.Store.GetFlowStateMachine(s.effTenant(r, u), fiid)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	if row == nil {
		writeJSON(w, 200, map[string]interface{}{"success": true, "found": false})
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "found": true,
		"state": map[string]interface{}{
			"id":               row.ID,
			"tenant_id":        row.TenantID,
			"one_id":           row.OneID,
			"flow_instance_id": row.FlowInstanceID,
			"current_node":     row.CurrentNode,
			"state_json":       row.StateJSON,
			"heartbeat_ts":     row.HeartbeatTS,
			"status":           row.Status,
			"version":          row.Version,
			"last_event_id":    row.LastEventID,
		},
	})
}

// 编译期引用自证：store.FlowStateMachineRow 的字段面被本面消费（锁引它的类型，防改名漂移）。
var _ store.FlowStateMachineRow
