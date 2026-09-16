// ============ 本文件职责中文说明 ============
// S9 监控收口（★ 2026-09-14 最小点亮）：接收同机 Alertmanager 的 webhook，
// 把 firing 告警写入平台告警中心（CreateAlert，tid=0 平台级）并推送运营群
// （notifyBots，含静默期由 Alertmanager group_interval 控制）。
// 鉴权：X-Admin-Token == cfg.AdminToken（与人工确认收款同一管理凭证，常量时间比较）。
// POST /api/alerts/alertmanager  （仅监听回环，见部署指南 §S9）
package api

import (
	"encoding/json"
	"net/http"
	"strings"
)

// amAlert Alertmanager webhook v2 告警报文（group 内单条；status=firing/resolved）。
type amAlert struct {
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	StartsAt    string            `json:"startsAt"`
	Fingerprint string            `json:"fingerprint"`
}

// handleAlertmanagerWebhook POST /api/alerts/alertmanager：Alertmanager 告警收口。
// 鉴权双通道（X-Admin-Token 头或 ?token= 查询参数，常量时间比对）；
// firing→落 alerts 表（kind=prom:<alertname>）并机器人双推；resolved 忽略（仅审计留痕）。
func (s *Server) handleAlertmanagerWebhook(w http.ResponseWriter, r *http.Request) {
	// 凭证：X-Admin-Token 头优先；Alertmanager webhook 场景允许 ?token= 查询参兜底
	//（回环监听 + 常量时间比较 + 只进不出，风险可控）
	tok := r.Header.Get("X-Admin-Token")
	if tok == "" {
		tok = r.URL.Query().Get("token")
	}
	if tok == "" || !constantTimeTokenEqual(tok, s.Cfg.AdminToken) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "管理凭证无效"})
		return
	}
	var payload struct {
		Alerts []amAlert `json:"alerts"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	fired := 0
	var sb strings.Builder
	for _, a := range payload.Alerts {
		if a.Status != "firing" {
			continue
		}
		name := a.Labels["alertname"]
		if name == "" {
			name = "unnamed"
		}
		sev := a.Labels["severity"]
		level := "warning"
		if sev == "critical" || sev == "crit" {
			level = "critical"
		}
		summary := a.Annotations["summary"]
		if summary == "" {
			summary = a.Annotations["description"]
		}
		if summary == "" {
			summary = name
		}
		if len(summary) > 300 {
			summary = summary[:300]
		}
		if s.Store != nil {
			_ = s.Store.CreateAlert(0, level, "prom:"+name, summary+"（起于 "+strings.TrimSpace(a.StartsAt)+"）")
		}
		fired++
		sb.WriteString("· " + name + "｜" + summary + "\n")
	}
	if fired > 0 {
		s.notifyBots("🚨 监控告警（Prometheus）", strings.TrimRight(sb.String(), "\n"))
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "accepted": fired})
}
