// ============ watchdog.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现监控看门狗（Watchdog）：
//   - startWatchdog：服务启动时在后台 goroutine 按周期（alert_interval_sec，默认 300 秒）轮询
//   - runWatchdogCheck：每轮执行三类检查并写入告警表（alerts）：
//       1) 余额阈值告警：遍历所有启用租户，余额为 0 或低于阈值（alert_balance_threshold，默认 1000 token）
//       2) 模型可用性告警：主翻译模型熔断时告警，熔断恢复后自动关闭历史 model 告警
//       3) 错误率告警：LLM 调用窗口错误率 > 40% 时告警，恢复后自动关闭
//       4) 自检探活（R3）：周期性请求本机 /status，连续超时判定「请求挂起不自愈」——
//          先写 critical 告警触达管理员；开启 watchdog_selfcheck_restart=1 后自动退出进程，
//          由 systemd（Restart=always）拉起恢复，根治整机受压时的锁饥饿卡死。
// 告警数据由管理后台「系统告警」页面展示。

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"os/exec"
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
	// 检查周期（默认 300 秒；自检探活也引用此值，故提升到函数作用域）
	interval := 300
	if v, _ := s.Store.GetConfig("alert_interval_sec"); v != "" {
		if n, err := parseInt(v); err == nil && n > 0 {
			interval = n
		}
	}
	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		// 定时触发一轮检查
		for range ticker.C {
			s.runWatchdogCheck()
		}
	}()
	// ★ R3 自检探活：本机 /status 连续超时 → critical 告警；可选自动退出由 systemd 拉起。
	// 背景：2026-08-23 生产事故——整机内存受压导致 25 个 goroutine 阻塞在互斥锁上，
	// 所有 HTTP 请求永久挂起且无法自愈，只能人工重启。此检查把该场景的恢复时间从
	// 「人工发现」缩短到一个周期内。开关：watchdog_selfcheck_restart=1 启用自动重启。
	go func() {
		checkInterval := time.Duration(interval) * time.Second
		if interval > 60 {
			checkInterval = 60 * time.Second // 探活频率上限 1 分钟，比主检查更勤
		}
		failStreak := 0
		const failThreshold = 3 // 连续 3 次超时才判定挂起（防单次抖动误杀）
		for {
			time.Sleep(checkInterval)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get("http://127.0.0.1:8787/status")
			if err == nil {
				resp.Body.Close()
				if failStreak >= failThreshold && s.Store != nil {
					_ = s.Store.CreateAlert(0, "info", "selfcheck", "服务探活已恢复正常")
				}
				failStreak = 0
				continue
			}
			failStreak++
			log.Printf("[watchdog-selfcheck] 本机探活失败 %d/%d: %v", failStreak, failThreshold, err)
			if failStreak == failThreshold && s.Store != nil {
				_ = s.Store.CreateAlert(0, "critical", "selfcheck",
					fmt.Sprintf("服务连续 %d 次探活超时，疑似请求挂起锁死", failThreshold))
			}
			if failStreak >= failThreshold {
				if v, _ := s.Store.GetConfig("watchdog_selfcheck_restart"); v == "1" {
					log.Printf("[watchdog-selfcheck] 自动重启触发（watchdog_selfcheck_restart=1）")
					_ = s.Store.CreateAlert(0, "critical", "selfcheck", "服务无响应，自动重启以恢复")
					// os.Exit 触发 systemd Restart=always 拉起新进程
					os.Exit(1)
				}
			}
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
	// 订阅到期扫描（每日一轮：启动即扫一次；到期摘除 + 7/1 天前提醒）
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		s.runSubscriptionScan()
		for range ticker.C {
			s.runSubscriptionScan()
		}
	}()
	// 工单产物留存扫描（每日一轮：剩余 7/3/1 天提醒下载；到期清理文件，译文已入 TM 不受影响）
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		s.runTicketRetentionScan()
		for range ticker.C {
			s.runTicketRetentionScan()
		}
	}()
	// 泄漏日志（每日一轮：RSS/堆/goroutine 采样 + heap 快照留存；启动即采一次）
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		s.runMemLeakCapture()
		for range ticker.C {
			s.runMemLeakCapture()
		}
	}()
	log.Println("监控看门狗已启动")
}

// runTicketRetentionScan 工单产物保留期扫描（每日执行）：
//   - 剩余 ≤7/3/1 天 → 通知中心提醒创建者尽快下载（expire_notify 档位标记去重）
//   - 已过期 → 删除产物文件（主产物+多文件表各产物）、清空路径字段并通知；
//     核心译文已沉淀 tm_segments/final_result，不受清理影响
func (s *Server) runTicketRetentionScan() {
	if s.Store == nil {
		return
	}
	rows, err := s.Store.ListTicketsForRetention()
	if err != nil {
		return
	}
	for _, r := range rows {
		exp, perr := time.Parse(time.RFC3339, r.ResultExpiresAt)
		if perr != nil {
			continue
		}
		daysLeft := int(math.Ceil(time.Until(exp).Hours() / 24))
		// 已过期：清理产物
		if daysLeft <= 0 {
			paths, cerr := s.Store.CleanupTicketResults(r.ID)
			if cerr == nil {
				for _, p := range paths {
					_ = os.Remove(p)
				}
				_ = s.Store.CreateNotification(r.CreatedBy, "工单产物已过保留期",
					fmt.Sprintf("工单号 %s 的结果文件超过 %s 保留期已被清理；译文仍保留在翻译记忆中，可重新发起工单复用。", r.TicketNo, exp.Format("2006-01-02")),
					"ticket", r.ID)
				s.Store.LogAudit(r.TenantID, 0, "ticket_result_cleanup", "tickets", r.TicketNo)
			}
			continue
		}
		// 分档提醒：7/3/1 天（每档一次）
		for _, tier := range []int{7, 3, 1} {
			tierStr := strconv.Itoa(tier)
			if daysLeft <= tier && !s.Store.TicketExpireMarked(r.ID, tierStr) {
				if err := s.Store.MarkTicketExpireNotify(r.ID, tierStr); err == nil {
					_ = s.Store.CreateNotification(r.CreatedBy, "工单结果即将过期",
						fmt.Sprintf("工单号 %s 的结果文件将在 %d 天后（%s）清理，请尽快下载；译文长期有效。",
							r.TicketNo, daysLeft, exp.Format("2006-01-02")),
						"ticket", r.ID)
				}
			}
		}
	}
}

// runSubscriptionScan 订阅到期扫描（每日执行）：
//   - 已到期（now ≥ package_expires_at）：摘除订阅身份（ExpirePackage），句数余额保留，
//     写审计（actor=system）并通知租户管理员
//   - 剩余 ≤7 天 / ≤1 天：分别发送一次通知中心提醒（NotifiedExp7/NotifiedExp1 标记去重）
func (s *Server) runSubscriptionScan() {
	if s.Store == nil || s.Ten == nil {
		return
	}
	tenants, err := s.Ten.List()
	if err != nil {
		return
	}
	now := time.Now()
	for _, t := range tenants {
		// 跳过平台宿主租户（租户 1）与无效 ID
		if t.ID <= 1 {
			continue
		}
		perms := tenant.ParsePerms(t.Permissions)
		if perms.PackageCode == "" || perms.PackageCode == "trial" || perms.PackageExpires == "" {
			continue // 无订阅 / 试用包 / 不限期：不参与到期扫描
		}
		exp, perr := time.Parse(time.RFC3339, perms.PackageExpires)
		if perr != nil {
			continue
		}
		// 已到期：摘除订阅身份并通知
		if !now.Before(exp) {
			if code, err := s.Store.ExpirePackage(t.ID); err == nil {
				s.Store.LogAuditDiff(t.ID, 0, "package_expire", "tenant", strconv.FormatInt(t.ID, 10),
					`{"package_code":"`+code+`"}`, `{"package_code":""}`)
				s.notifyTenantAdmins(t.ID, "订阅已到期",
					"商业包「"+code+"」已到期，订阅身份已移除；剩余句数仍可正常使用，续订后即时生效。")
				s.notifyBots("订阅到期摘除",
					"租户 #"+strconv.FormatInt(t.ID, 10)+"（"+t.Name+"）商业包「"+code+"」已到期，订阅身份已摘除。")
			}
			continue
		}
		// 未到期：按剩余天数分档提醒（每档只发一次）
		daysLeft := int(time.Until(exp).Hours() / 24)
		expDate := exp.Format("2006-01-02")
		if daysLeft <= 7 && !perms.NotifiedExp7 {
			_ = s.Store.SetNotifiedExpFlag(t.ID, "notified_exp7") // ★ B1：单字段原子置位，不再整体覆盖
			s.notifyTenantAdmins(t.ID, "订阅即将到期",
				"商业包「"+perms.PackageCode+"」将于 "+expDate+" 到期（剩 "+strconv.Itoa(daysLeft+1)+" 天），请及时续订。")
		}
		if daysLeft < 1 && !perms.NotifiedExp1 {
			_ = s.Store.SetNotifiedExpFlag(t.ID, "notified_exp1") // ★ B1
			s.notifyTenantAdmins(t.ID, "订阅今日到期",
				"商业包「"+perms.PackageCode+"」将于今日到期，续订请前往管理后台订阅页。")
		}
	}
}

// runBackup 执行一次数据库备份并清理旧备份；成功后按 backup_remote_cmd 推送异地（容灾）。
func (s *Server) runBackup(backupDir string, keep int) {
	dest, err := s.Store.Backup(backupDir, s.Cfg.DBPath)
	if err != nil {
		log.Printf("数据库备份失败: %v", err)
		return
	}
	// 清理旧备份，仅保留最近 keep 份
	store.PruneBackups(backupDir, strings.TrimSuffix(filepath.Base(s.Cfg.DBPath), filepath.Ext(s.Cfg.DBPath)), keep)
	log.Printf("数据库已备份: %s（保留最近 %d 份）", dest, keep)
	// 异地推送钩子：backup_remote_cmd 配置 shell 命令，{path} 替换为本份备份路径
	// （示例：rclone copy {path} remote:translator-backups）；失败仅告警不阻断主流程
	if cmdStr, _ := s.Store.GetConfig("backup_remote_cmd"); cmdStr != "" {
		full := strings.ReplaceAll(cmdStr, "{path}", dest)
		out, cerr := exec.Command("/bin/sh", "-c", full).CombinedOutput()
		if cerr != nil {
			log.Printf("异地备份推送失败: %v, 输出: %s", cerr, string(out))
			_ = s.Store.CreateAlert(0, "warning", "backup", "异地备份推送失败: "+cerr.Error())
		} else {
			log.Printf("异地备份已推送: %s", dest)
		}
	}
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
					existed := s.hasOpenAlert(t.ID, "balance") // 邮件触达去重：仅新告警时发信
					_ = s.Store.CreateAlert(t.ID, "critical", "balance", msg)
					if !existed {
						s.notifyAlert("余额耗尽告警（租户 #"+strconv.FormatInt(t.ID, 10)+"）", msg)
						s.notifyTenantAdmins(t.ID, "余额已耗尽",
							"您的企业翻译额度余额已耗尽，翻译服务即将暂停。请前往管理后台订阅套餐或联系管理员充值。")
						s.notifyBots("租户余额耗尽",
							"租户 #"+strconv.FormatInt(t.ID, 10)+"（"+t.Name+"）翻译额度余额已耗尽，服务暂停中。")
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
		existed := s.hasOpenAlert(0, "model")
		_ = s.Store.CreateAlert(0, "critical", "model", msg)
		if !existed {
			s.notifyAlert("翻译模型熔断告警", msg)
			s.notifyBots("翻译模型熔断", msg)
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
