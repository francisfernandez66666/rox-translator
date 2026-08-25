// ============ 本文件职责中文说明 ============
// 工单服务层：连接 API 层与编排器的桥梁。
// 职责：
//   - EnqueueTicketRun：工单入队（API 层调用，立即返回 ticket_no）
//   - StartWorkers：goroutine 工作池，循环 Reserve → 分发执行 → Ack/Fail
//   - runTicket：按工单类型执行——纯文本走五步编排流水线；文件走提取→翻译→原格式回写
//   - 完成后投递站内信（通知中心）
//
// 队列接缝：仅依赖 queue.Queue 接口（当前 direct 实现；未来 kafka driver 单文件接入）。
// =============================================
package service

import (
	"log"
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		perr := s.processJob(jctx, job)
		jcancel()
		if perr != nil {
			_ = s.Queue.MarkFailed(context.Background(), job.ID, perr.Error())
		} else {
			_ = s.Queue.MarkDone(context.Background(), job.ID)
		}
	}
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
	// ★ 余额预检（强制计费时）：余额不足快速失败，单独错误码提示充值/升级套餐
	if s.Bill != nil && s.Bill.Enabled() {
		if b, berr := s.Store.GetBalance(t.TenantID); berr == nil && b != nil && b.Balance <= 0 {
			t.Status = store.TicketRejected
			t.RejectReason = errInsufficientCode + ": 余额不足，请充值或升级套餐"
			_ = s.Store.UpdateTicket(t)
			return fmt.Errorf("%s: 余额不足，请充值或升级套餐", errInsufficientCode)
		}
	}
	t.Status = store.TicketInProgress
	_ = s.Store.UpdateTicket(t)

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
		t.Status = store.TicketRejected
		t.RejectReason = runErr.Error()
		_ = s.Store.UpdateTicket(t)
		_ = s.Store.CreateNotification(t.CreatedBy,
			fmt.Sprintf("翻译工单失败：%s", t.Title),
			fmt.Sprintf("工单号 %s 失败原因：%s", t.TicketNo, runErr.Error()),
			"ticket", t.ID)
		return runErr
	}
	// ★ 用户取消：收尾前复查状态——已 cancelled 则放弃计费/完成态/通知（产物留档不下载）
	if cur, ge := s.Store.GetTicketGlobal(t.ID); ge == nil && cur != nil && cur.Status == "cancelled" {
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
	if err := s.Bill.MeterDeferred(t.TenantID, t.CreatedBy, "translate", provider, model, billed); err != nil {
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

// runFileTicket 文件工单：提取→逐段翻译→原格式回写（docx/xlsx/pptx/pdf）。
// 多文件工单（ticket_files 表有行）：逐文件处理，各自记录产物/失败原因；
// 全部失败才置工单失败。单文件旧工单走 tickets.file_path 历史路径。
// mode 透传：fast 模式跳过 KB 匹配（纯模型直翻），pro 保持知识库链路。

// StartStallSweep 卡死工单巡检：每 5 分钟扫描 in_progress 且 updated_at 超过 20 分钟的工单，
// 重置为 queued 触发断点续传（worker 收尾前已有取消复查，重排安全）。防信号量饿死类静默卡死。

// lowBalanceThreshold 低额告警绝对阈值（system_config low_balance_alert_tokens，默认100000）。
func lowBalanceThreshold() int64 {
	return 100000
}
func (s *TicketService) StartStallSweep() {
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			s.Store.CloseStalePendingOrders()  // ★ 订单15min超时自动关闭
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

func (s *TicketService) runFileTicket(ctx context.Context, t *store.Ticket) error {
	langs := parseLangs(t.TargetLangs)
	mode := t.Mode // fast | pro（空=pro）
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
		var okCount, failCount int64
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
				okCount++
				doneN := okCount + failedCount(s.Store, t.ID)
				mu.Unlock()
				s.Store.SetTicketState(t.ID, "file_translate", "running",
					fmt.Sprintf("progress=%d/%d", doneN, len(files)))
				mu.Lock()
				s.bumpTmHitsFromTranslations(t.TenantID, res.Data.Translations) // ★ 自闭环计数（不自动入库）
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
	} else if len(res.Files) > 0 {
		_ = s.Store.SetTicketResultPath(t.ID, res.Files[0])
	}
	s.Store.SetTicketState(t.ID, "file_writeback", "success", "")
	s.bumpTmHitsFromTranslations(t.TenantID, res.Data.Translations) // ★ 自闭环计数（不自动入库）
	return nil
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

var _ = kb.KBDatabase{} // 保持 DB 字段类型引用

// BootResume 启动断点续跑：把上次进程退出时遗留的 in_progress 工单重置为 queued，
// worker 立即接管（配合各步骤幂等：提取/翻译/回写均可安全重来）。
func (s *TicketService) BootResume() {
	// ★ 启动即强制释放所有在途 ticket 任务：上一进程必然已死，剩余租约无意义。
	//   不释放则新 worker 需等满租约（默认 30min）才能接管，表现为「卡死」。
	if s.DB != nil {
		s.DB.RawDB().Exec("UPDATE jobs SET status='queued', leased_by='', leased_at=0 WHERE type='ticket' AND status='running'")
	}
	if n, err := s.Store.RequeueStalledTickets(0); err == nil && n > 0 {
		log.Printf("[boot-resume] 已重新排队 %d 个中断工单", n)
	}
}
