// ============ 本文件职责中文说明 ============
// 工单服务层：连接 API 层与编排器的桥梁。
// 职责：
//   - EnqueueTicketRun：工单入队（API 层调用，立即返回 ticket_no）
//   - StartWorkers：goroutine 工作池，循环 Reserve → 分发执行 → Ack/Fail
//   - runTicket：按工单类型执行——纯文本走五步编排流水线；文件走提取→翻译→原格式回写
//   - 完成后投递站内信（通知中心）
// 队列接缝：仅依赖 queue.Queue 接口（当前 direct 实现；未来 kafka driver 单文件接入）。
// =============================================
package service

import (
	"context"
	"fmt"
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

	stopCh chan struct{}
	mu     sync.Mutex
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
	t.Status = store.TicketInProgress
	_ = s.Store.UpdateTicket(t)

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
	t.Status = store.TicketCompleted
	_ = s.Store.UpdateTicket(t)
	_ = s.Store.CreateNotification(t.CreatedBy,
		fmt.Sprintf("翻译工单完成：%s", t.Title),
		fmt.Sprintf("工单号 %s 翻译完成，请前往工单页下载结果。", t.TicketNo),
		"ticket", t.ID)
	return nil
}

// runTextTicket 纯文本工单：五步编排流水线（kb_match→初翻→评估→审校→gate）。
func (s *TicketService) runTextTicket(ctx context.Context, t *store.Ticket) error {
	wf := orchestrator.NewWorkflow(s.Store, s.Engine, s.Ten, s.DB)
	if wf == nil {
		return fmt.Errorf("编排器未初始化")
	}
	err := wf.Executor.Execute(ctx, t, func(step string, ok bool, errMsg string) {})
	if err != nil {
		return err
	}
	s.meterUsage(t.TenantID, t.CreatedBy, int64(len([]rune(t.SourceText))))
	s.meterSentences(t.TenantID, t.SourceText, 0, parseLangs(t.TargetLangs))
	return nil
}

// runFileTicket 文件工单：提取→逐段翻译→原格式回写（docx/xlsx/pptx）；pdf 降级 xlsx 对照表。
func (s *TicketService) runFileTicket(ctx context.Context, t *store.Ticket) error {
	langs := parseLangs(t.TargetLangs)
	res := s.Engine.HandleFile(ctx, t.FilePath, map[string]interface{}{"target_langs": langs}, func(step string, done, total int) {})
	if res.Error != "" {
		return fmt.Errorf("%s", res.Error)
	}
	if len(res.Files) > 0 {
		_ = s.Store.SetTicketResultPath(t.ID, res.Files[0])
	}
	seg := int64(res.Data.TotalTexts)
	nl := int64(len(res.Data.TargetLangs))
	s.meterUsage(t.TenantID, t.CreatedBy, seg*nl)
	s.meterSentences(t.TenantID, "", seg, res.Data.TargetLangs)
	return nil
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

// meterUsage 写入用量流水（字符/段数口径；Bill 未装配时跳过）。
func (s *TicketService) meterUsage(tid, userID int64, quantity int64) {
	if s.Bill == nil || quantity <= 0 {
		return
	}
	_ = s.Bill.MeterDeferred(tid, userID, "translate", "", "", quantity)
}

// countSentences 统计源句数（与 api 层同规则：换行+句末标点切分，至少 1）。
func countSentencesSvc(text string) int64 {
	repl := strings.NewReplacer("\n", "。", "\r", "。", "。", "。", "！", "。", "？", "。", ".", "。", "!", "。", "?", "。", ";", "。", "；", "。")
	segs := strings.Split(repl.Replace(text), "。")
	n := int64(0)
	for _, sg := range segs {
		if strings.TrimSpace(sg) != "" {
			n++
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// meterSentences 句数扣减（受强制计费门控；sourceText 为空时用 segments 段数）。
func (s *TicketService) meterSentences(tid int64, sourceText string, segments int64, langs []string) {
	if s.Store == nil || tid <= 0 {
		return
	}
	if v, _ := s.Store.GetConfig("sentence_enforced"); v != "1" {
		return
	}
	n := int64(len(langs))
	if n <= 0 {
		n = 1
	}
	if sourceText != "" {
		n = countSentencesSvc(sourceText) * n
	} else if segments > 0 {
		n = segments * n
	} else {
		return
	}
	_, _ = s.Store.DeductSentences(tid, n)
}

var _ = kb.KBDatabase{} // 保持 DB 字段类型引用
