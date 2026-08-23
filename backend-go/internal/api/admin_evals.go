package api

// ============ 本文件职责中文说明 ============
// evals 看板 / 系统健康 / 审计（含 CSV 导出）/ 告警管理（handleEvalsList / handleSystemHealth / handleSystemAudit / handleAlerts 系列）
// 安全要点：evals 看板、审计日志、系统告警仅超管可访问（requireAdminUser）；
// 系统健康看板（handleSystemHealth）保留租户管理员及以上（总览页三角色共用，数据按生效租户隔离）。
// ========================================

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"translator/internal/engine"
	"translator/internal/store"
)

// ============ evals 看板 ============

// handleEvalsList 评估记录列表（仅超管）
func (s *Server) handleEvalsList(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	records, err := s.Store.ListEvalRecords(s.effTenant(r, u), 100)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "records": records})
}

// ============ 系统健康 ============

// handleSystemHealth 系统健康看板
func (s *Server) handleSystemHealth(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireTenantAdmin(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	total, perLang, _, _ := s.DB.Stats(s.effTenant(r, u))
	balance, _ := s.Store.GetBalance(s.effTenant(r, u))
	usage, _, _ := s.Store.UsageStats(s.effTenant(r, u))
	steps := flowStepsForTenant(s.Ten, s.effTenant(r, u))
	enabled := 0
	for _, st := range steps {
		if st.Enable {
			enabled++
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "health": map[string]interface{}{
		"version":            "2.0.0-go",
		"kb_entries":         total,
		"kb_lang_stats":      perLang,
		"balance":            balance,
		"usage":              usage,
		"flow_steps_enabled": enabled,
		"flow_steps_total":   len(steps),
		"breaker_open":       s.Engine != nil && s.Engine.BreakerOpen(),
		"llm_error_rate":     llmErrorRateStr(s.Engine),
	}})
}

// llmErrorRateStr 生成 LLM 错误率展示字符串（无样本显示 0.0%）
func llmErrorRateStr(eng *engine.Engine) string {
	if eng == nil {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", eng.ErrorRate()*100)
}

// handleSystemAudit 审计日志（仅超管；支持 action/resource/user/time 过滤；export=csv 导出）
func (s *Server) handleSystemAudit(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	q := r.URL.Query()
	logs, err := s.Store.ListAuditFilter(
		s.effTenant(r, u),
		q.Get("action"),
		q.Get("resource"),
		atol(q.Get("user_id")),
		q.Get("from"),
		q.Get("to"),
		atoiDef(q.Get("limit"), 100),
	)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// CSV 导出（ISO 17100 审计留痕）
	if q.Get("export") == "csv" {
		s.exportAuditCSV(w, logs)
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "logs": logs})
}

// handleAlerts 告警列表（仅超管，看全平台）
func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	tid := s.effTenant(r, u)
	if u.Role == "super_admin" || u.Role == "admin" {
		tid = 0 // 超管看全平台
	}
	q := r.URL.Query()
	status := q.Get("status")
	alerts, err := s.Store.ListAlerts(tid, status, atoiDef(q.Get("limit"), 100))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "alerts": alerts})
}

// handleAlertResolve 关闭告警（仅超管）
func (s *Server) handleAlertResolve(w http.ResponseWriter, r *http.Request) {
	u, err := s.requireAdminUser(r)
	if err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 待关闭告警 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	if err := s.Store.ResolveAlert(req.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "alert_resolve", "alerts", strconv.FormatInt(req.ID, 10))
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// exportAuditCSV 导出审计日志为 CSV
func (s *Server) exportAuditCSV(w http.ResponseWriter, logs []*store.AuditLog) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=audit.csv")
	var sb strings.Builder
	sb.WriteString("\xEF\xBB\xBF") // UTF-8 BOM（Excel 兼容）
	sb.WriteString("id,tenant_id,user_id,action,resource,detail,before_val,after_val,created_at\n")
	for _, a := range logs {
		sb.WriteString(fmt.Sprintf("%d,%d,%d,%s,%s,%s,%s,%s,%s\n",
			a.ID, a.TenantID, a.UserID, csvEscape(a.Action), csvEscape(a.Resource), csvEscape(a.Detail),
			csvEscape(a.BeforeVal), csvEscape(a.AfterVal), csvEscape(a.CreatedAt)))
	}
	_, _ = w.Write([]byte(sb.String()))
}

// csvEscape CSV 字段转义（逗号/引号/换行）
func csvEscape(s string) string {
	if strings.ContainsAny(s, ",\"\n\r") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}
