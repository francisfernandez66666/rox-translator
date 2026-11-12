// ============ sales_inbox_api.go · 职责说明 ============
// 三期 W2 六包接线：事件收件箱的**只读管理面**（/api/admin/sales/inbox/event）。
// 幂等消费写口（TryProcessInboxEvent/MarkInboxProcessed）属 mq 消费者装配，不开 HTTP——
// 收件箱状态不许外部改写，本面只读单条事件的处理状态（排障「这条事件消费了没」）。
// 鉴权：读 ≥2（requireDeptAdmin，部门管理员合法读口）。
// ================================================
package api

import (
	"net/http"
	"strings"

	apierrors "translator/internal/errors"
)

// routesSalesInbox 注册收件箱只读端点（由 routesSales 调用）。
func (s *Server) routesSalesInbox() {
	s.mux.HandleFunc("/api/admin/sales/inbox/event", s.handleSalesInboxEvent)
}

// handleSalesInboxEvent 单条事件状态（?event_id=；不存在 found=false，不是 404——
// 「查无此事件」与「事件还没到」在排障语义里不同，前者不该按错误告警）。
func (s *Server) handleSalesInboxEvent(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireDeptAdmin(r)
	if err != nil {
		s.writeAuthzError(w, r, err)
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	eventID := strings.TrimSpace(r.URL.Query().Get("event_id"))
	if eventID == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 event_id"))
		return
	}
	status, processedAt, found, err := s.Store.GetInboxEvent(s.effTenant(r, u), eventID)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{
		"success": true, "found": found, "status": status, "processed_at": processedAt,
	})
}