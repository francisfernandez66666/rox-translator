package api

// ============ 本文件职责中文说明 ============
// 本文件实现监控看门狗（Watchdog）：
//   - startWatchdog：服务启动时在后台 goroutine 按周期（alert_interval_sec，默认 300 秒）轮询
//   - runWatchdogCheck：每轮执行三类检查并写入告警表（alerts）：
//       1) 余额阈值告警：遍历所有启用租户，余额为 0 或低于阈值（alert_balance_threshold，默认 1000 token）
//       2) 模型可用性告警：主翻译模型熔断时告警，熔断恢复后自动关闭历史 model 告警
//       3) 错误率告警：LLM 调用窗口错误率 > 40% 时告警，恢复后自动关闭
// 告警数据由管理后台「系统告警」页面展示。

import (
	"fmt"
	"log"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"translator/internal/mail"
	"translator/internal/store"
	"translator/internal/tenant"
)

// startWatchdog 启动监控看门狗：后台 goroutine 周期检查租户余额阈值与模型可用性，写入告警表。
// 检查项可通过 system_config 配置：
//   - alert_balance_threshold: 余额低于该值触发告警（默认 1000 token）
//   - alert_interval_sec: 检查周期（默认 300 秒）
//
// 注意：平台存储（s.Store）未初始化时直接返回，不启动看门狗。
func (s *Server) startWatchdog() {
	// 存储未初始化无法读写告警，直接跳过
	if s.Store == nil {
		return
	}
	go func() {
		// 读取检查周期配置，默认 300 秒
		interval := 300
		if v, _ := s.Store.GetConfig("alert_interval_sec"); v != "" {
			if n, err := parseInt(v); err == nil && n > 0 {
				interval = n
			}
		}
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		// 定时触发一轮检查
		for range ticker.C {
			s.runWatchdogCheck()
		}
	}()
	// 数据库定时备份（默认每 24 小时，可配置 backup_interval_hours；0=关闭）
	go func() {
		backupHours := 24
		if v, _ := s.Store.GetConfig("backup_interval_hours"); v != "" {
			if n, err := parseInt(v); err == nil && n >= 0 {
				backupHours = n
			}
		}
		if backupHours <= 0 {
			log.Println("数据库定时备份已关闭（backup_interval_hours=0）")
			return
		}
		backupDir := filepath.Join(s.Cfg.UserDataDir, "backups")
		if v, _ := s.Store.GetConfig("backup_dir"); v != "" {
			backupDir = v
		}
		keep := 7
		if v, _ := s.Store.GetConfig("backup_keep"); v != "" {
			if n, err := parseInt(v); err == nil && n > 0 {
				keep = n
			}
		}
		ticker := time.NewTicker(time.Duration(backupHours) * time.Hour)
		defer ticker.Stop()
		// 启动后先备份一次，再按周期备份
		s.runBackup(backupDir, keep)
		for range ticker.C {
			s.runBackup(backupDir, keep)
		}
	}()
	// OOM 内存监控（默认每 60 秒采样，可配置 mem_monitor_interval_sec；0=关闭）
	s.startMemoryMonitor()
	log.Println("监控看门狗已启动")
}

// runBackup 执行一次数据库备份并清理旧备份。
func (s *Server) runBackup(backupDir string, keep int) {
	dest, err := s.Store.Backup(backupDir, s.Cfg.DBPath)
	if err != nil {
		log.Printf("数据库备份失败: %v", err)
		return
	}
	// 清理旧备份，仅保留最近 keep 份
	store.PruneBackups(backupDir, strings.TrimSuffix(filepath.Base(s.Cfg.DBPath), filepath.Ext(s.Cfg.DBPath)), keep)
	log.Printf("数据库已备份: %s（保留最近 %d 份）", dest, keep)
}

// runWatchdogCheck 执行一轮检查：余额阈值 / 模型熔断 / 错误率三项告警巡检。
// 该方法在后台 goroutine 中周期调用，无参数无返回；检查结果直接写入告警表。
func (s *Server) runWatchdogCheck() {
	// 1. 余额阈值告警：遍历所有启用租户，余额为 0 或低于阈值则创建告警
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
				// 仅巡检启用状态的租户（禁用租户不产生余额告警）
				if t.Status != tenant.StatusActive {
					continue
				}
				bal, err := s.Store.GetBalance(t.ID)
				if err != nil {
					continue
				}
				// 余额耗尽 → critical 级告警；低于阈值 → warning 级告警
				if bal.Balance <= 0 {
					msg := "租户余额已耗尽，翻译服务将被暂停"
					if s.Store.CreateAlert(t.ID, "critical", "balance", msg) == nil {
						s.notifyAlert("余额耗尽告警（租户 #"+strconv.FormatInt(t.ID, 10)+"）", msg)
					}
				} else if bal.Balance < threshold {
					_ = s.Store.CreateAlert(t.ID, "warning", "balance", "租户余额低于阈值")
				}
			}
		}
	}

	// 2. 模型可用性告警：主模型熔断状态检查
	if s.Engine != nil && s.Engine.BreakerOpen() {
		// 熔断中：创建 critical 级模型告警
		msg := "主翻译模型已熔断，正在使用备用模型"
		if s.Store.CreateAlert(0, "critical", "model", msg) == nil {
			s.notifyAlert("翻译模型熔断告警", msg)
		}
	} else {
		// 熔断已恢复 → 自动关闭历史 model 告警（避免重复堆积）
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
			// 错误率超阈值：创建 warning 级告警
			_ = s.Store.CreateAlert(0, "warning", "error_rate", fmt.Sprintf("LLM 调用错误率 %.0f%% 超出阈值", rate*100))
		} else {
			// 错误率恢复正常：自动关闭历史 error_rate 告警
			alerts, _ := s.Store.ListAlerts(0, "open", 100)
			for _, a := range alerts {
				if a.Kind == "error_rate" {
					_ = s.Store.ResolveAlert(a.ID)
				}
			}
		}
	}
}

// parseInt 解析字符串为 int 整数。
// 参数 s: 待解析字符串；返回: (解析结果, 错误)。非法输入返回错误。
func parseInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	return n, err
}

// parseInt64 解析字符串为 int64 整数。
// 参数 s: 待解析字符串；返回: (解析结果, 错误)。非法输入返回错误。
func parseInt64(s string) (int64, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err
}

// notifyAlert 关键告警邮件通知：向配置的收件人（系统配置 alert_email）发送告警邮件。
// 收件人未配置时跳过（告警仍写入 alerts 表，后台可见）；邮件走 mail.Sender（未配置
// SMTP 时为 Noop 打印日志，配置后发送真实邮件）。
// 参数：subject=邮件主题，body=告警内容。
func (s *Server) notifyAlert(subject, body string) {
	recipients := ""
	if v, _ := s.Store.GetConfig("alert_email"); v != "" {
		recipients = v
	}
	if recipients == "" {
		return // 未配置告警收件人，不发送
	}
	for _, to := range strings.Split(recipients, ",") {
		to = strings.TrimSpace(to)
		if to == "" {
			continue
		}
		_ = s.mailer().Send(&mail.Message{To: to, Subject: subject, Body: body})
	}
}
