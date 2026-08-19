package api

import (
	"fmt"
	"log"
	"strconv"
	"time"

	"translator/internal/tenant"
)

// startWatchdog 启动监控看门狗：周期检查租户余额阈值与模型可用性，写入告警表。
// 检查项可通过 system_config 配置：
//   - alert_balance_threshold: 余额低于该值触发告警（默认 1000 token）
//   - alert_interval_sec: 检查周期（默认 300 秒）
func (s *Server) startWatchdog() {
	if s.Store == nil {
		return
	}
	go func() {
		interval := 300
		if v, _ := s.Store.GetConfig("alert_interval_sec"); v != "" {
			if n, err := parseInt(v); err == nil && n > 0 {
				interval = n
			}
		}
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.runWatchdogCheck()
		}
	}()
	log.Println("监控看门狗已启动")
}

// runWatchdogCheck 执行一轮检查
func (s *Server) runWatchdogCheck() {
	// 1. 余额阈值告警：遍历所有租户，低于阈值或余额为 0 的告警
	threshold := int64(1000)
	if v, _ := s.Store.GetConfig("alert_balance_threshold"); v != "" {
		if n, err := parseInt64(v); err == nil && n >= 0 {
			threshold = n
		}
	}
	if s.Store != nil && s.Ten != nil {
		tenants, err := s.Ten.List()
		if err == nil {
			for _, t := range tenants {
				if t.Status != tenant.StatusActive {
					continue
				}
				bal, err := s.Store.GetBalance(t.ID)
				if err != nil {
					continue
				}
				if bal.Balance <= 0 {
					_ = s.Store.CreateAlert(t.ID, "critical", "balance", "租户余额已耗尽，翻译服务将被暂停")
				} else if bal.Balance < threshold {
					_ = s.Store.CreateAlert(t.ID, "warning", "balance", "租户余额低于阈值")
				}
			}
		}
	}

	// 2. 模型可用性告警：主模型熔断状态
	if s.Engine != nil && s.Engine.BreakerOpen() {
		_ = s.Store.CreateAlert(0, "critical", "model", "主翻译模型已熔断，正在使用备用模型")
	} else {
		// 熔断已恢复 → 自动关闭历史 model 告警
		alerts, _ := s.Store.ListAlerts(0, "open", 100)
		for _, a := range alerts {
			if a.Kind == "model" {
				_ = s.Store.ResolveAlert(a.ID)
			}
		}
	}

	// 3. 错误率告警：LLM 调用窗口错误率过高（默认 >40% 触发）
	if s.Engine != nil {
		rate := s.Engine.ErrorRate()
		if rate > 0.4 {
			_ = s.Store.CreateAlert(0, "warning", "error_rate", fmt.Sprintf("LLM 调用错误率 %.0f%% 超出阈值", rate*100))
		} else {
			alerts, _ := s.Store.ListAlerts(0, "open", 100)
			for _, a := range alerts {
				if a.Kind == "error_rate" {
					_ = s.Store.ResolveAlert(a.ID)
				}
			}
		}
	}
}

// parseInt 解析 int
func parseInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	return n, err
}

func parseInt64(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err
}