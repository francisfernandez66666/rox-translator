// ============ sales_crm_api.go · 职责说明 ============
// 三期 W6：CRM 域的管理面（/api/admin/sales/leads*、/api/admin/sales/inbox*、
// /api/admin/sales/handoff*、/api/admin/sales/funnel）。
//
// ★ 硬口径：
//  1. 鉴权统一 ≥3（salesConfigAdmin → requireTenantAdmin）：线索行含客户邮箱与会话维度，
//     比草稿读口更敏感，不开放给部门管理员档（A21 白名单自动扫描）。
//  2. **MarkPendingHuman 的生产调用唯一走 writeBackAttribution 的会话回填路径**（A24/M0263）；
//     本面只读转人工会话 + 人工完结（resolve），**不提供**把 pending_human 置 1 的写口
//     ——「待人工」只允许由守卫判定产生，人工管理面只能消它，不能造它。
//  3. 漏斗扩段（A22）：销售域五阶段计数（new→engaged→quoted→won/lost + pending_human），
//     平台注册漏斗（/api/admin/funnel）零改动——两把尺不互串。
// ================================================
package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	apierrors "translator/internal/errors"
)

// routesSalesCrm 注册 CRM 管理面路由（由 routesSales 调用）。
func (s *Server) routesSalesCrm() {
	s.mux.HandleFunc("/api/admin/sales/leads", s.handleSalesLeads)
	s.mux.HandleFunc("/api/admin/sales/inbox", s.handleSalesInboxList)
	s.mux.HandleFunc("/api/admin/sales/handoff", s.handleSalesHandoff)
	s.mux.HandleFunc("/api/admin/sales/handoff/resolve", s.handleSalesHandoffResolve)
	s.mux.HandleFunc("/api/admin/sales/funnel", s.handleSalesFunnel)
}

// handleSalesLeads 线索分页（?stage=&pending=1&limit=；stage 非法一律 400）。
func (s *Server) handleSalesLeads(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	stage := strings.TrimSpace(r.URL.Query().Get("stage"))
	limit, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("limit")), 10, 64)
	rows, err := s.Store.ListLeads(tid, stage, r.URL.Query().Get("pending") == "1", limit)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "rows": rows})
}

// handleSalesInboxList 收件箱事件分页（?status=&limit=；排障面）。
func (s *Server) handleSalesInboxList(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("limit")), 10, 64)
	rows, err := s.Store.ListInboxEvents(tid, status, limit)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "rows": rows})
}

// handleSalesHandoff 转人工会话分页（?pending=1&limit=；只读 open 态）。
func (s *Server) handleSalesHandoff(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	limit, _ := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("limit")), 10, 64)
	rows, err := s.Store.ListHandoffSessions(tid, r.URL.Query().Get("pending") == "1", limit)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "rows": rows})
}

// handleSalesHandoffResolve 人工完结（body: session_id；只降权——把待人工清掉并置 resolved）。
func (s *Server) handleSalesHandoffResolve(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	var body struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.SessionID) == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 session_id"))
		return
	}
	if err := s.Store.ResolveHandoffSession(tid, body.SessionID); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	s.Store.LogAudit(tid, 0, "sales_handoff_resolve", "handoff_sessions", body.SessionID)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleSalesFunnel 漏斗扩段读数（五阶段计数 + 待人工数；平台漏斗零改动）。
func (s *Server) handleSalesFunnel(w http.ResponseWriter, r *http.Request) {
	_, tid, ok := s.salesConfigAdmin(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "方法不支持"))
		return
	}
	counts, err := s.Store.LeadFunnelCounts(tid)
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, publicErrMessage(r.Context(), err)))
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "counts": counts})
}
