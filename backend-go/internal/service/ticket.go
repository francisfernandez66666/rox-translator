// ============ 本文件职责中文说明 ============
// 工单服务层：连接 API 层与编排器的桥梁。
// 职责：
//   - EnqueueTicketRun：工单入队（API 层调用，立即返回 ticket_no）
//   - StartWorkers：goroutine 工作池，循环 Reserve → 分发执行 → Ack/Fail
//   - safeProcessJob/processJob：按任务类型分发；捕获 panic 转为任务失败，避免击穿进程
//   - runTicket：按工单类型执行——纯文本走五步编排流水线；文件走提取→翻译→原格式回写
//   - chargeTokens/dispatchCompletedWebhook：完成时按真实 token 计费并推送 webhook
//   - StartStallSweep/BootResume：卡死巡检重置 + 启动断点续跑
//   - 文件翻译硬闸结束后对漏翻段落追加 warning 轨迹与站内通知
//
// 队列接缝：仅依赖 queue.Queue 接口（当前 direct 实现；未来 kafka driver 单文件接入）。
// =============================================
// Package service 提供服务层：连接 API 与编排器的桥梁，负责工单入队、worker 工作池、
// 文本/文件翻译执行、Token 实费计费、webhook 回调、卡死巡检与启动断点续跑。
package service

import (
	"archive/zip"
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"translator/internal/billing"
	"translator/internal/engine"
	"translator/internal/kb"
	"translator/internal/orchestrator"
	"translator/internal/queue"
	"translator/internal/store"
	"translator/internal/tenant"
)

// TicketService 工单服务。
type TicketService struct {
	Store  *store.Store
	Engine *engine.Engine
	Ten    *tenant.Store
	DB     *kb.KBDatabase
	Queue  queue.Queue
	Bill   *billing.Service // 计费服务（用量流水；可 nil）

	stopCh  chan struct{}
	mu      sync.Mutex
	started bool
}

// NewTicketService 创建工单服务。参数 q=队列实现（direct）；bill=计费服务（可 nil）。
func NewTicketService(st *store.Store, eng *engine.Engine, ts *tenant.Store, db *kb.KBDatabase, q queue.Queue, bill *billing.Service) *TicketService {
	return &TicketService{Store: st, Engine: eng, Ten: ts, DB: db, Queue: q, Bill: bill, stopCh: make(chan struct{})}
}

// EnqueueTicketRun 将工单翻译任务入队（立即返回，不阻塞 HTTP）。
func (s *TicketService) EnqueueTicketRun(ctx context.Context, ticketID int64) (int64, error) {
	return s.Queue.Enqueue(ctx, "ticket_run", queue.NewTicketPayload(ticketID), queue.DefaultMaxAttempts)
}

// StartWorkers 启动 n 个 worker goroutine（幂等；重复调用忽略）。
func (s *TicketService) StartWorkers(n int) {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	s.mu.Unlock()
	if n <= 0 {
		n = 2
	}
	workerID := fmt.Sprintf("worker-%d", time.Now().UnixNano())
	for i := 0; i < n; i++ {
		go s.workerLoop(fmt.Sprintf("%s-%d", workerID, i))
	}
}

// Stop 停止工作池（优雅停机时调用；在途任务由租约超时机制回收）。
func (s *TicketService) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// workerLoop 单个 worker 的主循环：领取→执行→Ack/Fail；空闲时 1s 轮询。
func (s *TicketService) workerLoop(workerID string) {
	for {
		select {
		case <-s.stopCh:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		job, err := s.Queue.Reserve(ctx, workerID, queue.DefaultLeaseSec)
		cancel()
		if err != nil || job == nil {
			time.Sleep(1 * time.Second)
			continue
		}
		jctx, jcancel := context.WithTimeout(context.Background(), 25*time.Minute)
		perr := s.safeProcessJob(jctx, job)
		jcancel()
		if perr != nil {
			_ = s.Queue.MarkFailed(context.Background(), job.ID, perr.Error())
		} else {
			_ = s.Queue.MarkDone(context.Background(), job.ID)
		}
	}
}

// safeProcessJob panic 防护（整改 D4）：worker goroutine 不在 net/http 的连接级
// recover 保护内——引擎/KB/文件深处一次 panic 即击穿整个进程，任务滞留至租约超期。
// 此处兜底把 panic 转为任务失败，交由 MarkFailed 的重试/死信机制收场。
func (s *TicketService) safeProcessJob(ctx context.Context, job *queue.Job) (perr error) {
	defer func() {
		if r := recover(); r != nil {
			perr = fmt.Errorf("worker panic: %v", r)
			log.Printf("[worker] panic recovered job=%d type=%s: %v\n%s", job.ID, job.Type, r, debug.Stack())
		}
	}()
	return s.processJob(ctx, job)
}

// processJob 按任务类型分发执行。
func (s *TicketService) processJob(ctx context.Context, job *queue.Job) error {
	switch job.Type {
	case "ticket_run":
		ticketID, err := queue.ParseTicketPayload(job.Payload)
		if err != nil {
			return nil // 载荷损坏：标记完成避免毒丸死循环
		}
		return s.runTicket(ctx, ticketID)
	default:
		return nil // 未知类型直接完成（防毒丸）
	}
}

// 工单失败错误码前缀：余额不足（OpenAPI 状态接口据此返回独立出参 error_code）
const errInsufficientCode = "insufficient_balance"

// runTicket 执行单个工单的翻译流程并投递通知。
func (s *TicketService) runTicket(ctx context.Context, ticketID int64) error {
	t, err := s.Store.GetTicketGlobal(ticketID)
	if err != nil {
		return fmt.Errorf("工单不存在: %w", err)
	}
	// 已完成/已取消的工单不重跑
	if t.Status == store.TicketCompleted {
		return nil
	}
	// ★ 注入创建人组织（2026-08-26 KB继承链）：异步工单按「发起用户当前所在部门」
	//   的祖先链决定部门包可见范围；OpenAPI 任务（CreatedBy=0）回退其归属用户 APIUserID。
	creatorUID := t.CreatedBy
	if creatorUID <= 0 && t.APIUserID > 0 {
		creatorUID = t.APIUserID
	}
	if creatorUID > 0 {
		if cu, uerr := s.Store.GetUser(creatorUID, t.TenantID); uerr == nil && cu != nil && cu.OrgID > 0 {
			ctx = engine.WithUserOrg(ctx, cu.OrgID)
		}
	}
	// ★ 余额预检（强制计费时）：可用额度 = 未过期台账 + 永久余额（双桶口径，评审整改 A1），
	//   与 gateUsage/CheckBalance/DeductWithGrants 保持一致；不足快速失败，单独错误码提示充值/升级套餐
	if s.Bill != nil && s.Bill.Enabled() {
		if grants, perm, berr := s.Store.TenantRemainTotal(t.TenantID); berr == nil && grants+perm <= 0 {
			t.Status = store.TicketRejected
			t.RejectReason = errInsufficientCode + ": 余额不足，请充值或升级套餐"
			_ = s.Store.UpdateTicket(t)
			return fmt.Errorf("%s: 余额不足，请充值或升级套餐", errInsufficientCode)
		}
	}
	// ★ 认领防覆盖（UAT 修复②）：仅当仍为 queued 才翻 in_progress——
	//   用户在认领窗口内已取消时尊重取消态，静默退出（job 由 MarkDone 收尾），
	//   消除「cancel 先写 cancelled、认领后写 in_progress 覆盖之」的时序竞态。
	if cur, ge := s.Store.GetTicketGlobal(t.ID); ge == nil && cur != nil && cur.Status == store.TicketCancelled {
		return nil
	}
	t.Status = store.TicketInProgress
	_ = s.Store.UpdateTicket(t)

	// ★ 心跳保活（评审整改 R3）：长翻译阶段内业务状态不变化，60s 触碰一次 updated_at，
	//   防止卡死巡检把仍在运行的工单误判重排（重复执行/双扣费的根源）。
	// ★ 取消传播（2026-08-26 UAT 缺陷修复②）：此前用户取消仅改库内状态，
	//   执行中的 goroutine 无感知——继续消耗 LLM token 直到自然结束；
	//   且与「认领方回写 in_progress」存在覆盖竞态。现以 3s 轮询监视取消态，
	//   命中即取消执行 ctx，LLM 调用链随 ctx 立即中断。
	hbStop := make(chan struct{})
	defer close(hbStop)
	runCtx, runCancel := context.WithCancel(ctx)
	defer runCancel()
	go func() {
		tk := time.NewTicker(60 * time.Second)
		wt := time.NewTicker(3 * time.Second)
		defer tk.Stop()
		defer wt.Stop()
		for {
			select {
			case <-tk.C:
				_ = s.Store.TouchTicket(t.ID)
			case <-wt.C:
				if cur, gerr := s.Store.GetTicketGlobal(t.ID); gerr == nil && cur != nil &&
					(cur.Status == "cancelled") {
					runCancel()
					return
				}
			case <-hbStop:
				return
			}
		}
	}()
	ctx = runCtx

	// ★ 注入用量收集器：本工单全链路（初翻/校对/Judge/文化闸门/embedding）
	// 的真实 token 用量自动归集，完成后按实费计费。
	ctx = s.Engine.WithUsageRecorder(ctx)

	var runErr error
	if t.FilePath != "" {
		runErr = s.runFileTicket(ctx, t)
	} else {
		runErr = s.runTextTicket(ctx, t)
	}

	if runErr != nil {
		// ★ 取消语义（UAT 修复②配套）：ctx 被取消监视器触发时，状态已是 cancelled——
		//   保持之，不再覆盖为 rejected、不投失败通知。
		if ctx.Err() != nil {
			if cur, ge := s.Store.GetTicketGlobal(t.ID); ge == nil && cur != nil && cur.Status == "cancelled" {
				return nil
			}
		}
		t.Status = store.TicketRejected
		t.RejectReason = runErr.Error()
		_ = s.Store.UpdateTicket(t)
		_ = s.Store.CreateNotification(t.CreatedBy,
			fmt.Sprintf("翻译工单失败：%s", t.Title),
			fmt.Sprintf("工单号 %s 失败原因：%s", t.TicketNo, runErr.Error()),
			"ticket", t.ID)
		return runErr
	}
	// ★ 收尾守卫（评审整改 R3）：复查当前状态——
	//   ① cancelled：用户已取消，放弃计费/完成态/通知（产物留档不下载）；
	//   ② queued：被卡死巡检重排（说明另一副本已接管本工单），本副本退出，
	//      杜绝双份 chargeTokens 扣费与重复通知。
	//   注意不可扩大化：pending_approval/approved/completed 是流水线自身步骤写入的合法中间态。
	if cur, ge := s.Store.GetTicketGlobal(t.ID); ge == nil && cur != nil &&
		(cur.Status == "cancelled" || cur.Status == store.TicketQueued) {
		if cur.Status == store.TicketQueued {
			s.Store.LogAudit(t.TenantID, 0, "ticket_dup_exit", "tickets",
				fmt.Sprintf("工单 %s 检测到已被巡检重排，本副本放弃收尾（防双扣费）", t.TicketNo))
		}
		return nil
	}
	// ★ Token 实费计费：聚合本工单全链路真实 token × 均摊系数（强制计费时扣余额；
	// 扣减失败仅告警不回滚——翻译成果已产出，欠费由告警跟进）
	billed := s.chargeTokens(ctx, t)
	t.TokensBilled = billed
	t.Status = store.TicketCompleted
	_ = s.Store.UpdateTicket(t)
	// 产物保留期打点：ticket_retention_days（默认 14 天；0=永久）。到期由后台每日扫描清理文件，
	// 核心译文不受影响——文本工单存 final_result、文件工单回写 tm_segments 长期沉淀。
	retentionDays := 14
	if v, _ := s.Store.GetConfig("ticket_retention_days"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			retentionDays = n
		}
	}
	expireHint := "结果文件长期保留"
	if retentionDays > 0 {
		exp := time.Now().AddDate(0, 0, retentionDays)
		_ = s.Store.SetTicketExpiry(t.ID, exp.Format(time.RFC3339))
		expireHint = fmt.Sprintf("结果文件保留 %d 天（至 %s），请尽快下载", retentionDays, exp.Format("2006-01-02"))
	}
	_ = s.Store.CreateNotification(t.CreatedBy,
		fmt.Sprintf("翻译工单完成：%s", t.Title),
		fmt.Sprintf("工单号 %s 翻译完成。%s；译文已沉淀至翻译记忆长期有效。", t.TicketNo, expireHint),
		"ticket", t.ID)
	// ★ Webhook 完成回调（OpenAPI 轮询之外的推送通道）：带 task_id 与 token 消耗
	s.dispatchCompletedWebhook(ctx, t)
	return nil
}

// chargeTokens 工单级 Token 实费扣费：读取 ctx 收集器累计的真实 token × 均摊系数。
// 强制计费关闭时仅留痕。扣减失败写 critical 告警（欠费跟进），不阻断完成态。
func (s *TicketService) chargeTokens(ctx context.Context, t *store.Ticket) int64 {
	if s.Bill == nil || s.Engine == nil || s.Store == nil {
		return 0
	}
	prompt, completion := s.Engine.UsageTokens(ctx)
	total := prompt + completion
	if total <= 0 {
		return 0 // 无 LLM 调用（纯 KB 命中等）
	}
	m := 1.5 // 默认均摊系数
	if v, _ := s.Store.GetConfig("billing_markup_multiplier"); v != "" {
		if f, perr := strconv.ParseFloat(v, 64); perr == nil && f >= 1.0 {
			m = f
		}
	}
	billed := int64(float64(total) * m)
	if billed < total {
		billed = total
	}
	provider, model := s.Engine.UsageModel(ctx)
	// ★ 用量看板标注（2026-08-26 需求）：文件工单=file、文本工单=text；模式随工单落库值
	bizKind := "text"
	if t.FilePath != "" {
		bizKind = "file"
	}
	if err := s.Bill.MeterDeferred(t.TenantID, t.CreatedBy, "translate", provider, model, billed, bizKind, normalizeMode(t.Mode)); err != nil {
		_ = s.Store.CreateAlert(t.TenantID, "critical", "billing",
			fmt.Sprintf("工单 %s 计费失败（余额不足）：应扣 %d token，请充值或升级套餐", t.TicketNo, billed))
	}
	return billed
}

// dispatchCompletedWebhook 投递工单完成 webhook 事件（OpenAPI 任务完成推送）。
func (s *TicketService) dispatchCompletedWebhook(ctx context.Context, t *store.Ticket) {
	if s.Store == nil {
		return
	}
	prompt, completion := s.Engine.UsageTokens(ctx)
	s.Store.DispatchWebhook(t.TenantID, "translation.completed", map[string]interface{}{
		"event":       "translation.completed",
		"tenant_id":   t.TenantID,
		"task_id":     t.ID,
		"ticket_no":   t.TicketNo,
		"type":        map[bool]string{true: "files", false: "text"}[t.FilePath != ""],
		"title":       t.Title,
		"tokens_used": prompt + completion,
		"time":        time.Now().Format(time.RFC3339),
	})
}

// runTextTicket 纯文本工单：编排流水线（pro=全步骤；fast=初翻+校对，见 orchestrator 模式覆盖）。
func (s *TicketService) runTextTicket(ctx context.Context, t *store.Ticket) error {
	wf := orchestrator.NewWorkflow(s.Store, s.Engine, s.Ten, s.DB)
	if wf == nil {
		return fmt.Errorf("编排器未初始化")
	}
	err := wf.Executor.Execute(ctx, t, func(step string, ok bool, errMsg string) {})
	if err != nil {
		return err
	}
	// 计费统一在 runTicket 完成态按真实 token 聚合扣减（chargeTokens）
	return nil
}

// lowBalanceThreshold 低额告警绝对阈值（system_config low_balance_alert_tokens，默认100000）。
func lowBalanceThreshold() int64 {
	return 100000
}

// StartStallSweep 卡死工单巡检：每 5 分钟扫描 in_progress 且 updated_at 超过 20 分钟的工单，
// 重置为 queued 触发断点续传（worker 收尾前已有取消复查，重排安全）。防信号量饿死类静默卡死。
// ★ 同周期顺带执行商业化巡检：订单15min超时自动关闭（CloseStalePendingOrders）+ 低额提醒（24h去重）。
func (s *TicketService) StartStallSweep() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			s.Store.CloseStalePendingOrders()                     // ★ 订单15min超时自动关闭
			s.Store.TenantLowBalanceAlerts(lowBalanceThreshold()) // ★ 低额提醒(24h去重)
			n, err := s.Store.RequeueStalledTickets(20 * time.Minute)
			if err != nil {
				continue
			}
			if n > 0 {
				s.Store.CreateAlert(0, "warning", "stall",
					fmt.Sprintf("检测到 %d 个翻译卡死工单（>20min 无进展），已自动重新排队续跑", n))
			}
		}
	}()
}

// runFileTicket 文件翻译工单执行主流程：解析目标语言→按模式(fast/pro)驱动引擎管线→
// 阶段进度回调（提取20/初翻40/校对60/回写80）→产物落盘与工单状态推进。
// 参数：ctx=取消/超时上下文（暂停与硬闸重试依赖）；t=工单对象；返回错误（含回显重试语义）。
func (s *TicketService) runFileTicket(ctx context.Context, t *store.Ticket) error {
	langs := parseLangs(t.TargetLangs)
	mode := t.Mode // fast | pro（空=pro）
	// ★ 归属登记用创建者 ID（评审整改 C1）：OpenAPI 任务回退其归属用户
	ownerUID := t.CreatedBy
	if ownerUID <= 0 && t.APIUserID > 0 {
		ownerUID = t.APIUserID
	}
	// ★ 进度阶梯回调：把引擎内部阶段映射为步骤状态（前端锚点：提取20/初翻40/校对60/回写80）
	progFn := func(step string, done, total int) {
		switch {
		case strings.Contains(step, "第1步"):
			s.Store.SetTicketState(t.ID, "file_extract", "running", "")
		case strings.Contains(step, "第2步"):
			s.Store.SetTicketState(t.ID, "file_translate", "running", "")
		case strings.Contains(step, "第3步"):
			s.Store.SetTicketState(t.ID, "file_writeback", "running", "")
		}
	}
	// 多文件模式：并行处理（信号量限制同时 3 个，避免打爆 LLM API）
	files, _ := s.Store.TicketFiles(t.ID)
	if len(files) > 0 {
		var mu sync.Mutex
		var okCount, failCount, unTotal int64
		var firstErr string
		s.Store.SetTicketState(t.ID, "file_extract", "running",
			fmt.Sprintf("total=%d mode=%s", len(files), normalizeMode(mode)))
		sem := make(chan struct{}, 3) // 同时最多 3 个文件在翻译
		var wg sync.WaitGroup
		for _, f := range files {
			wg.Add(1)
			go func(tf *store.TicketFile) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				res := s.Engine.HandleFile(ctx, tf.FilePath,
					map[string]interface{}{"target_langs": langs, "mode": mode}, progFn)
				mu.Lock()
				defer mu.Unlock()
				if res.Error != "" || len(res.Files) == 0 {
					_ = s.Store.SetTicketFileError(tf.ID, res.Error)
					failCount++
					if firstErr == "" {
						firstErr = tf.FileName + ": " + res.Error
					}
					return
				}
				// ★ 多语言产物打包 zip 存入 result_path
				zipPath := ""
				if len(res.Files) > 1 {
					zp, zerr := zipOutputs(res.Files, strings.TrimSuffix(filepath.Base(tf.FilePath), filepath.Ext(tf.FilePath))+"_translated.zip")
					if zerr == nil {
						zipPath = zp
					}
				}
				storePath := zipPath
				if storePath == "" && len(res.Files) > 0 {
					storePath = res.Files[0]
				}
				_ = s.Store.SetTicketFileResult(tf.ID, storePath)
				s.Store.RegisterArtifact(storePath, t.TenantID, ownerUID, t.ID) // ★ 归属登记（C1）
				okCount++
				doneN := okCount + failedCount(s.Store, t.ID)
				mu.Unlock()
				s.Store.SetTicketState(t.ID, "file_translate", "running",
					fmt.Sprintf("progress=%d/%d", doneN, len(files)))
				mu.Lock()
				s.bumpTmHitsFromTranslations(t.TenantID, res.Data.Translations) // ★ 自闭环计数（不自动入库）
				// ★ 漏翻可见性：聚合各文件未译出段数，收尾统一落轨迹+通知
				unTotal += int64(untranslatedTotal(res.Data.Untranslated))
			}(f)
		}
		wg.Wait()
		s.Store.SetTicketState(t.ID, "file_extract", "success", "")
		if okCount > 0 {
			// 校对（pro 流水线内含 QA，此处为阶梯标记）与回写完成
			s.Store.SetTicketState(t.ID, "file_qa", "success",
				fmt.Sprintf("ok=%d fail=%d", okCount, failCount))
			s.Store.SetTicketState(t.ID, "file_writeback", "success", "")
		}
		// ★ 漏翻可见性（2026-08-26）：硬闸结束后仍有缺失时，追加 warning 轨迹 + 通知创建人
		if unTotal > 0 {
			s.Store.SetTicketState(t.ID, "file_qa", "warning",
				fmt.Sprintf("untranslated=%d（模型未能译出已保留原文，建议人工检查产物）", unTotal))
			s.Store.CreateNotification(t.CreatedBy,
				fmt.Sprintf("文件翻译完成但有 %d 段未译出：%s", unTotal, t.Title),
				"部分段落模型未能译出已保留原文，请打开产物人工检查；必要时可重新发起工单。",
				"ticket", t.ID)
		}
		if okCount == 0 && failCount == int64(len(files)) && firstErr != "" {
			return fmt.Errorf("%s", firstErr)
		}
		return nil
	}
	// 旧单文件路径
	s.Store.SetTicketState(t.ID, "file_translate", "running", "single")
	res := s.Engine.HandleFile(ctx, t.FilePath, map[string]interface{}{"target_langs": langs, "mode": mode}, progFn)
	if res.Error != "" {
		return fmt.Errorf("%s", res.Error)
	}
	// 翻译完成 → 校对标记 → 进入回写阶段
	s.Store.SetTicketState(t.ID, "file_qa", "success", "")
	s.Store.SetTicketState(t.ID, "file_writeback", "running", "")
	// ★ 漏翻可见性（2026-08-26）：硬闸结束后仍有缺失 → warning 轨迹 + 站内通知
	if un := untranslatedTotal(res.Data.Untranslated); un > 0 {
		s.Store.SetTicketState(t.ID, "file_qa", "warning",
			fmt.Sprintf("untranslated=%d（模型未能译出已保留原文，建议人工检查产物）", un))
		s.Store.CreateNotification(t.CreatedBy,
			fmt.Sprintf("文件翻译完成但有 %d 段未译出：%s", un, t.Title),
			"部分段落模型未能译出已保留原文，请打开产物人工检查；必要时可重新发起工单。",
			"ticket", t.ID)
	}
	// ★ 多语言产物打包 zip
	zipPath := ""
	if len(res.Files) > 1 {
		zp, zerr := zipOutputs(res.Files, t.TicketNo+"_translated.zip")
		if zerr == nil {
			zipPath = zp
		}
	}
	if zipPath != "" {
		_ = s.Store.SetTicketResultPath(t.ID, zipPath)
		s.Store.RegisterArtifact(zipPath, t.TenantID, ownerUID, t.ID) // ★ 归属登记（C1）
	} else if len(res.Files) > 0 {
		_ = s.Store.SetTicketResultPath(t.ID, res.Files[0])
	}
	for _, fp := range res.Files { // ★ 逐产物归属登记（C1）
		s.Store.RegisterArtifact(fp, t.TenantID, ownerUID, t.ID)
	}
	s.Store.SetTicketState(t.ID, "file_writeback", "success", "")
	s.bumpTmHitsFromTranslations(t.TenantID, res.Data.Translations) // ★ 自闭环计数（不自动入库）
	return nil
}

// untranslatedTotal 汇总各语言未译出段数（引擎硬闸收尾统计，2026-08-26 漏翻可见性）。
func untranslatedTotal(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// failedCount 统计工单内处理失败的文件数。
func failedCount(s *store.Store, ticketID int64) int64 {
	files, _ := s.TicketFiles(ticketID)
	var n int64
	for _, f := range files {
		if f.Error != "" {
			n++
		}
	}
	return n
}

// zipOutputs 将多个产物文件打包为一个 zip（供下载一次获取全部语言版本）。
func zipOutputs(paths []string, zipName string) (string, error) {
	outDir := filepath.Dir(paths[0])
	zipPath := filepath.Join(outDir, zipName)
	f, err := os.Create(zipPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	w := zip.NewWriter(f)
	defer w.Close()
	for _, p := range paths {
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			continue
		}
		fe, _ := w.Create(filepath.Base(p))
		_, _ = fe.Write(data)
	}
	return zipPath, w.Close()
}

// normalizeMode 模式归一化（轨迹展示用）。
func normalizeMode(m string) string {
	if m == "fast" {
		return "fast"
	}
	return "pro"
}

// bumpTmHitsFromTranslations TM 自闭环计数：模型最终译文按 (原文,语言,译文) 累计；
// 达到 tm_review_threshold（默认100）自动生成待审候选并告警提醒超管。绝不直接写入正式 TM。
func (s *TicketService) bumpTmHitsFromTranslations(tid int64, translations map[string]map[string]string) {
	if s.DB == nil || s.Store == nil || len(translations) == 0 || tid <= 0 {
		return
	}
	th := int64(100)
	if v, _ := s.Store.GetConfig("tm_review_threshold"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			th = x
		}
	}
	for lc, m := range translations {
		for src, tgt := range m {
			n, created, err := s.Store.BumpTmHit(tid, src, lc, tgt, th)
			if err != nil {
				continue
			}
			if created {
				preview := src
				if len([]rune(preview)) > 50 {
					preview = string([]rune(preview)[:50]) + "…"
				}
				s.Store.CreateAlert(tid, "warning", "tm_review",
					fmt.Sprintf("相同翻译累计达 %d 次，已生成待审候选：%s", n, preview))
			}
		}
	}
}

// parseLangs 解析逗号分隔语言串。
func parseLangs(s string) []string {
	var out []string
	for _, p := range splitComma(s) {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		out = []string{"en"}
	}
	return out
}

// splitComma 逗号分隔（兼容中英文逗号）。
func splitComma(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ',' || r == '，' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	out = append(out, cur)
	return out
}

// 编译期引用占位：保持 KBDatabase 字段类型引用（跨构建标签保留导入）。
var _ = kb.KBDatabase{} // 保持 DB 字段类型引用

// BootResume 启动断点续跑：把上次进程退出时遗留的 in_progress 工单重置为 queued，
// worker 立即接管（配合各步骤幂等：提取/翻译/回写均可安全重来）。
func (s *TicketService) BootResume() {
	// ★ 启动即强制释放所有在途 ticket 任务：上一进程必然已死，剩余租约无意义。
	//   不释放则新 worker 需等满租约（默认 30min）才能接管，表现为「卡死」。
	// ★ 修复（2026-08-26 P1-c）：入队类型是 ticket_run，旧 SQL 的 type='ticket' 永远匹配 0 行，
	//   断点续跑的租约释放形同虚设——统一更正为 ticket_run。
	if s.DB != nil {
		s.DB.RawDB().Exec("UPDATE jobs SET status='queued', leased_by='', leased_at='' WHERE type='ticket_run' AND status='running'")
	}
	if n, err := s.Store.RequeueStalledTickets(0); err == nil && n > 0 {
		log.Printf("[boot-resume] 已重新排队 %d 个中断工单", n)
	}
}
