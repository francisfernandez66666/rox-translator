package api

// ============ 本文件职责中文说明 ============
// 公开服务状态端点（/status）：SLA 证据支撑，免鉴权且不含敏感信息。
// 输出：版本、运行时长、工单队列深度（排队/执行中）、模型熔断状态、
// 近 24h 未处理告警数与整体健康位。Caddy 已按 /metrics 同样方式反代 /status。
// =============================================

import (
	"context"
	"net/http"
	"time"
)

// handleStatusStatus 公开状态接口（无需登录）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。
// 返回: success=true 时携带 version/uptime_sec/queue/breaker_open/alerts_24h/ok。
func (s *Server) handlePublicStatus(w http.ResponseWriter, r *http.Request) {
	uptime := int64(0)
	if !s.startedAt.IsZero() {
		uptime = int64(time.Since(s.startedAt).Seconds())
	}
	resp := map[string]interface{}{
		"success":      true,
		"version":      "v3",
		"service":      "translator-saas",
		"uptime_sec":   uptime,
		"breaker_open": s.Engine != nil && s.Engine.BreakerOpen(),
	}
	// 工单队列深度：jobs 表 queued/running 计数（存储不可用则省略）
	if s.Store != nil {
		var queued, running int64
		_ = s.Store.DB().QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM jobs WHERE status='queued'").Scan(&queued)
		_ = s.Store.DB().QueryRowContext(context.Background(),
			"SELECT COUNT(*) FROM jobs WHERE status='running'").Scan(&running)
		resp["queue"] = map[string]int64{"queued": queued, "running": running}
	}
	// 近 24h 开放告警数
	openAlerts := 0
	if s.Store != nil {
		alerts, err := s.Store.ListAlerts(0, "open", 100)
		if err == nil {
			dayAgo := time.Now().Add(-24 * time.Hour)
			for _, a := range alerts {
				if t, perr := time.Parse(time.RFC3339, a.CreatedAt); perr == nil && t.After(dayAgo) {
					openAlerts++
				}
			}
		}
	}
	resp["alerts_24h"] = openAlerts
	// 健康位：熔断中或有 critical 告警堆积视为降级（ok=false 但仍返回 200，供外部监控判断）
	ok := !(resp["breaker_open"].(bool)) && openAlerts < 10
	resp["ok"] = ok
	writeJSON(w, http.StatusOK, resp)
}
