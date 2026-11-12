// ============ sales_attribution_api.go · 职责说明 ============
// 三期 W2 六包接线：回复归因与素材效果的**只读管理面**（/api/admin/sales/attribution/*）。
// 数据口早已存在（store.ListReplyAttributions / GetPackStats），本文件只做暴露，零新功能。
// 鉴权：两个端点都 ≥3（salesConfigAdmin → requireTenantAdmin）——归因行含客户会话维度，
// 比报价读口更敏感，不开放给部门管理员档。
// 写侧不在本文件：reply_attributions 的唯一生产写口是代理层 writeBackAttribution（A04/A08）。
// ================================================
package api

import (
	"net/http"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
)

// routesSalesAttribution 注册归因只读端点（由 routesSales 调用）。
func (s *Server) routesSalesAttribution() {
	s.mux.HandleFunc("/api/admin/sales/attribution/replies", s.handleSalesAttributionReplies)
	s.mux.HandleFunc("/api/admin/sales/attribution/pack-stats", s.handleSalesAttributionPackStats)
}

// handleSalesAttributionReplies 归因行分页列表（?limit=&offset=，缺省由 store 层回落）。
func (s *Server) handleSalesAttributionReplies(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	limit, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("limit")), 10, 64)
	offset, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("offset")), 10, 64)
	rows, err := s.Store.ListReplyAttributions(tid, limit, offset)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "rows": rows})
}

// handleSalesAttributionPackStats 单组素材效果快照（?package_id=&version=&draft_id=）。
// 未物化时 stats=null（不是错误）：聚合由 SyncPackStats 现算，读口不现场算（读写分离）。
func (s *Server) handleSalesAttributionPackStats(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	q := r.URL.Query()
	row, err := s.Store.GetPackStats(tid, q.Get("package_id"), q.Get("version"), q.Get("draft_id"))
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "stats": row})
}