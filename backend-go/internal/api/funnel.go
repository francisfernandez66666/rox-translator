// ============ 本文件职责中文说明 ============
// S4 增长漏斗 API（★ 2026-09-14）：超管看板「注册→激活→耗尽→首购→续费」
// 按渠道（utm_source / 裂变码）聚合，数据源 registration_attribution。
// GET /api/admin/funnel?days=30（默认 30 天，上限 365）。
package api

import (
	"net/http"
	"strconv"
	"time"
	"translator/internal/auth"
)

// handleAdminFunnel GET /api/admin/funnel：注册 cohort 五环节增长漏斗（仅超管，S4）。
func (s *Server) handleAdminFunnel(w http.ResponseWriter, r *http.Request) {
	if u, err := s.requireAdminUser(r); err != nil || !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超级管理员可见"})
		return
	}
	days := 30
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 && d <= 365 {
		days = d
	}
	rows, err := s.Store.FunnelStats(time.Now().AddDate(0, 0, -days))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": publicErrMessage(r.Context(), err)})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "days": days, "rows": rows})
}
