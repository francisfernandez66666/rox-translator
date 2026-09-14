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
//          先写 critical 告警触达管理员；自动退出进程（watchdog_selfcheck_restart
//          默认开启，显式 "0" 关闭），由 systemd（Restart=always）拉起恢复，根治整机受压时的锁饥饿卡死。
//          探活目标可用 SELFCHECK_URL 覆盖（默认 127.0.0.1:8787/status）。
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

	"translator/internal/auth"
	"translator/internal/store"
	"translator/internal/tenant"
)

// handleWatchdogSubscriptionScan ★ G5（2026-09-12）：手动立即执行订阅到期扫描（仅超管）。
// 生产由 watchdog「启动即扫 + 24h 周期」自动执行；本端点为 UAT/发布矩阵提供
// 「注入到期 → 触发 → 断言 permissions 实际摘除」的可验证入口，操作幂等。
func (s *Server) handleWatchdogSubscriptionScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]interface{}{"success": false, "message": "仅支持 POST"})
		return
	}
	u, err := s.requireTenantAdmin(r)
	if err != nil || !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "仅超管可触发订阅到期扫描"})
		return
	}
	s.runSubscriptionScan()
	writeJSON(w, 200, map[string]interface{}{"success": true, "message": "订阅到期扫描已执行"})
}

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
	// 启动后台协程：按配置周期定时执行告警检查（如续费到期、余额耗尽等）
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
		// ★ 探活目标：默认 127.0.0.1:8787（与生产 systemd 监听一致）；
		//   非标准端口部署可经 SELFCHECK_URL 覆盖，防「探活打错地址→误重启」
		selfcheckURL := os.Getenv("SELFCHECK_URL")
		if selfcheckURL == "" {
			selfcheckURL = "http://127.0.0.1:8787/status"
		}
		for {
			time.Sleep(checkInterval)
			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(selfcheckURL)
			// ★ H11：探活结果即可用性 SLI 样本，每轮驱动 SLO 燃烧率评估
			s.sloSampleTick(err == nil)
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
				// ★ 2026-09-04 加固：默认开启自动重启（仅显式 "0" 关闭），根治锁饥饿不自愈；
				//   探活地址允许经 SELFCHECK_URL 覆盖（默认 127.0.0.1:8787，与生产监听一致）
				if v, _ := s.Store.GetConfig("watchdog_selfcheck_restart"); v != "0" {
					// ★ B9（2026-09-12）：重启风暴熔断。若故障根因不在进程内（如依赖挂起、
					//   /status 自身逻辑死锁），每次「拉起→3 分钟判死→再退出」会无限循环，
					//   比带着告警硬扛更糟。用文件时间戳跨进程计数：1 小时内自动重启
					//   ≥3 次则拒绝再退，升级人工介入告警（systemd 侧 StartLimitBurst 双保险）。
					n, allowed := selfRestartBudget(time.Hour, 3)
					if !allowed {
						_ = s.Store.CreateAlert(0, "critical", "selfcheck",
							fmt.Sprintf("自动重启熔断：%d 分钟内已自愈 %d 次仍无法恢复，跳过本次重启，需人工介入（排查后调 watchdog_selfcheck_restart=0 或清理标记文件）", int(time.Hour.Minutes()), n))
						failStreak = failThreshold - 1 // 下个周期仍评估，便于恢复后自动清零告警
						continue
					}
					log.Printf("[watchdog-selfcheck] 自动重启触发（watchdog_selfcheck_restart 默认开启，本小时第 %d 次）", n+1)
					_ = s.Store.CreateAlert(0, "critical", "selfcheck", "服务无响应，自动重启以恢复")
					markSelfRestart()
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
	// ★ 任务2.5：同周期顺带扫描体验台账到期前 3 天提醒
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		s.runSubscriptionScan()
		s.runTrialScan()
		s.runGrowthScan()
		for range ticker.C {
			s.runSubscriptionScan()
			s.runTrialScan()
			s.runGrowthScan()
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
	// ★ 行业/语言文化包自动采集（2026-09-01）：低占用驱动 + 断点续传 + 首日铺底
	s.startPackScraper()
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
				s.Store.S7MarkLapsed(t.ID, exp) // ★ S7：登记到期时刻，驱动 T+3 老客回访
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
		if daysLeft <= 3 && !perms.NotifiedRenew3 {
			// ★ S7 续费三封之 T-3：邮件+站内（升级/续订引导），独立去重档
			_ = s.Store.SetNotifiedExpFlag(t.ID, "notified_renew3")
			s.notifyTenantAdmins(t.ID, "续费窗口：剩 "+strconv.Itoa(daysLeft+1)+" 天",
				"您的订阅「"+perms.PackageCode+"」将于 "+expDate+" 到期。到期前续订额度无缝衔接；"+
					"老客升级至更高档可抵扣旧包剩余价值（订阅页一键升级）。续订入口：管理后台 → 套餐与账单。")
		}
		if daysLeft < 1 && !perms.NotifiedExp1 {
			_ = s.Store.SetNotifiedExpFlag(t.ID, "notified_exp1") // ★ B1
			s.notifyTenantAdmins(t.ID, "订阅今日到期",
				"商业包「"+perms.PackageCode+"」将于今日到期，续订请前往管理后台订阅页。")
		}
	}
}

// runGrowthScan S7 增长触达日扫（★ 2026-09-14）：
//
//	① 每轮刷新各租户「余额清零起点」；清零满 48h 且从未付费 → 发放一次性挽回礼包
//	  （5 万内部 token≈167 积分 / 7 天台账），站内+邮件+运营群留痕；
//	② 订阅到期 ≥72h（T+3）仍未续订 → 老客回归触达（每轮到期只发一次）。
func (s *Server) runGrowthScan() {
	if s.Store == nil || s.Ten == nil {
		return
	}
	tenants, err := s.Ten.List()
	if err != nil {
		return
	}
	for _, t := range tenants {
		if t.ID <= 1 {
			continue
		}
		grants, permanent, e := s.Store.TenantRemainTotal(t.ID)
		if e != nil {
			continue
		}
		s.Store.S7MarkZero(t.ID, grants+permanent <= 0)
	}
	// ① 挽回礼包（每租户终身一次）
	for _, tid := range s.Store.S7RescueCandidates(48 * time.Hour) {
		_ = s.Store.CreateQuotaGrant(tid, "trial", 50000, time.Now().UTC().Add(7*24*time.Hour), "rescue_pack", 0)
		s.Store.S7MarkRescued(tid)
		s.notifyTenantAdmins(tid, "专属挽回礼包已到账",
			"检测到一个免费体验周期用尽，送您 167 积分挽回礼包（7 天有效），够再跑一批真实内容验证效果。"+
				"满意可随时订阅/充值，入口：管理后台 → 套餐与账单。")
		s.Store.LogAudit(tid, 0, "s7_rescue_pack", "tenant", "rescue 50000tok/7d")
		s.notifyBots("S7 挽回礼包发放", "租户 #"+strconv.FormatInt(tid, 10)+" 耗尽满 48h 未付费，已发放一次性挽回礼包（167 积分/7 天）。")
	}
	// ② T+3 老客回访（到期后仍未续订）
	for _, tid := range s.Store.S7LapsedFollowups(72 * time.Hour) {
		if t, e := s.Ten.GetByID(tid); e == nil {
			p := tenant.ParsePerms(t.Permissions)
			if p.PackageCode != "" && p.PackageCode != "trial" {
				continue // 已续订（新一期生效）：不打扰
			}
		}
		s.notifyTenantAdmins(tid, "好久不见——续订即可无缝恢复",
			"您的订阅到期已 3 天。这期间产生的术语沉淀与翻译记忆都还在，续订后立即生效、无需重来。"+
				"如需按量波动更大的方案，可选购永久积分充值包。入口：管理后台 → 套餐与账单。")
		s.Store.S7MarkLapsed3Sent(tid)
	}
}

// runTrialScan 体验台账到期提醒扫描（每日执行，任务2.5）：
//   - 遍历租户，取其最早到期的未过期体验台账（quota_grants kind='trial'）
//   - 剩余 ≤3 天 → 通知租户管理员并置 NotifiedExp3 去重标记（新发放 trial 由 CreateQuotaGrant 自动复位）
func (s *Server) runTrialScan() {
	if s.Store == nil || s.Ten == nil {
		return
	}
	tenants, err := s.Ten.List()
	if err != nil {
		return
	}
	for _, t := range tenants {
		if t.ID <= 1 {
			continue
		}
		perms := tenant.ParsePerms(t.Permissions)
		if perms.NotifiedExp3 {
			continue // 本档提醒已发送过（新发放 trial 会复位）
		}
		expStr, found := s.Store.EarliestActiveTrialExpiry(t.ID)
		if !found {
			continue // 无未过期体验台账
		}
		exp, perr := time.Parse(time.RFC3339, expStr)
		if perr != nil {
			continue
		}
		daysLeft := int(time.Until(exp).Hours() / 24)
		if daysLeft > 3 {
			continue
		}
		if err := s.Store.SetNotifiedExpFlag(t.ID, "notified_exp3"); err == nil {
			s.notifyTenantAdmins(t.ID, "体验额度即将到期",
				"体验额度将于 "+exp.Format("2006-01-02")+" 到期（剩 "+strconv.Itoa(daysLeft+1)+" 天）。到期后可购买月租套餐或充值永久 token 继续使用。")
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
				// ★ C13（2026-09-12）：口径统一 TenantRemainTotal（未过期台账 + 永久余额），
				//   旧实现只看 balance_accounts 永久桶，与预检/SettleExhausted 已整改的
				//   口径分裂：租户明明有 30 万体验 token 却被告"余额耗尽"。
				g, perm, err := s.Store.TenantRemainTotal(t.ID)
				if err != nil {
					continue
				}
				remain := g + perm
				// 余额耗尽 → critical 级告警；低于阈值 → warning 级告警
				if remain <= 0 {
					msg := "租户余额已耗尽，翻译服务将被暂停"
					existed := s.hasOpenAlert(t.ID, "balance") // 邮件触达去重：仅新告警时发信
					_ = s.Store.CreateAlertEx(t.ID, "critical", "balance", msg, 0,
						fmt.Sprintf("租户 #%d（%s）翻译额度余额已耗尽，翻译服务将被暂停", t.ID, t.Name))
					if !existed {
						s.notifyAlert("余额耗尽告警（租户 #"+strconv.FormatInt(t.ID, 10)+"）", msg)
						s.notifyTenantAdmins(t.ID, "余额已耗尽",
							"您的企业翻译额度余额已耗尽，翻译服务即将暂停。请前往管理后台订阅套餐或联系管理员充值。")
						s.notifyBots("租户余额耗尽",
							"租户 #"+strconv.FormatInt(t.ID, 10)+"（"+t.Name+"）翻译额度余额已耗尽，服务暂停中。")
					}
				} else if remain < threshold {
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

// notifyAlert 关键告警邮件通知：向配置的收件人（系统配置 alert_email）发送告警邮件，
// 可配置 alert_email_cc 作为抄送（两者均为逗号分隔多地址）。
// 收件人未配置时跳过（告警仍写入 alerts 表，后台可见）；邮件走 mail.Sender（未配置
// SMTP 时为 Noop 打印日志，配置后发送真实邮件）。
// 参数：subject=邮件主题，body=告警内容。
func (s *Server) notifyAlert(subject, body string) {
	recipients := ""
	if v, _ := s.Store.GetConfig("alert_email"); v != "" {
		recipients = v
	}
	cc := ""
	if v, _ := s.Store.GetConfig("alert_email_cc"); v != "" {
		cc = v
	}
	if recipients == "" {
		return // 未配置告警收件人，不发送
	}
	for _, to := range strings.Split(recipients, ",") {
		to = strings.TrimSpace(to)
		if to == "" {
			continue
		}
		// 渲染 alert 模板后补 To/Cc，入队异步发送（Message.CC 由 SMTPSender 写入 Cc 头）
		msg := renderMailTpl(s.getMailTpl("alert"), map[string]string{
			"title":   subject,
			"content": body,
			"level":   "warning",
		})
		msg.To = to
		msg.CC = cc
		_ = s.enqueueMail(msg, false)
	}
}

// selfRestartFile 自动重启历史文件（一行一个 unix 秒；跨进程可见）。
func selfRestartFile() string {
	return filepath.Join(os.TempDir(), "translator-selfcheck-restarts")
}

// selfRestartBudget 统计窗口内已发生的自动重启次数；达到上限 cap 返回 (n,false)（禁止本次重启），
// 否则返回 (n,true)。仅读取，不记账（记账由 markSelfRestart 在确定退出前执行）。
func selfRestartBudget(window time.Duration, cap int) (int, bool) {
	b, err := os.ReadFile(selfRestartFile())
	if err != nil {
		return 0, true
	}
	cutoff := time.Now().Add(-window).Unix()
	n := 0
	for _, ln := range strings.Split(string(b), "\n") {
		if ts, err := strconv.ParseInt(strings.TrimSpace(ln), 10, 64); err == nil && ts >= cutoff {
			n++
		}
	}
	return n, n < cap
}

// markSelfRestart 追加一条本次重启记录，并裁剪窗口外旧行。
func markSelfRestart() {
	path := selfRestartFile()
	b, _ := os.ReadFile(path)
	cutoff := time.Now().Add(-time.Hour).Unix()
	var keep []string
	for _, ln := range strings.Split(string(b), "\n") {
		if ts, err := strconv.ParseInt(strings.TrimSpace(ln), 10, 64); err == nil && ts >= cutoff {
			keep = append(keep, ln)
		}
	}
	keep = append(keep, strconv.FormatInt(time.Now().Unix(), 10))
	_ = os.WriteFile(path, []byte(strings.Join(keep, "\n")+"\n"), 0644)
}
