// ============ ticket.go · 职责说明 ============
// service 包工单服务层实现。
// 连接 API 层与编排器的桥梁。
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
	"encoding/json"
	"errors" // ★ F-42-a：errors.Is 识别余额耗尽哨兵（store.ErrInsufficientBalance）
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
	"translator/internal/db"
	"translator/internal/engine"
	"translator/internal/infra/distlock"
	"translator/internal/infra/redis"
	"translator/internal/kb"
	"translator/internal/mail"
	"translator/internal/observability"
	"translator/internal/orchestrator"
	"translator/internal/queue"
	"translator/internal/store"
	"translator/internal/tenant"
)

// TicketService 工单服务。
type TicketService struct {
	Store      *store.Store
	Engine     *engine.Engine
	Ten        *tenant.Store
	DB         *kb.KBDatabase
	Queue      queue.Queue
	Bill       *billing.Service // 计费服务（用量流水；可 nil）
	Mailer     mail.Sender      // 默认邮件发送器（注册验证码/重置码等）；可 nil
	InfoMailer mail.Sender      // 专用邮箱发送器（产品手册等）；可 nil
	Notifier   queue.Notifier   // 多实例唤醒信号器（Redis 启用时注入；nil=轮询）

	stopCh  chan struct{}
	mu      sync.Mutex
	started bool
}

// NewTicketService 创建工单服务。参数 q=队列实现（direct）；bill=计费服务（可 nil）。
func NewTicketService(st *store.Store, eng *engine.Engine, ts *tenant.Store, db *kb.KBDatabase, q queue.Queue, bill *billing.Service) *TicketService {
	return &TicketService{Store: st, Engine: eng, Ten: ts, DB: db, Queue: q, Bill: bill, stopCh: make(chan struct{})}
}

// EnqueueTicketRun 将工单翻译任务入队（立即返回，不阻塞 HTTP）。
// 入队成功后敲一次 Notifier：唤醒正阻塞在 Wait 上的 worker，省掉最长 1s 的轮询空等；
// Notifier 为 nil（未启用 Redis 的标准单实例形态）时不敲信号也会被轮询捞起，功能不降级。
func (s *TicketService) EnqueueTicketRun(ctx context.Context, ticketID int64) (int64, error) {
	id, err := s.Queue.Enqueue(ctx, "ticket_run", queue.NewTicketPayload(ticketID), queue.DefaultMaxAttempts)
	if err == nil && s.Notifier != nil {
		_ = s.Notifier.Signal(ctx)
	}
	return id, err
}

// StartWorkers 启动 n 个 worker goroutine（幂等；重复调用忽略）。
// started 标志由 mu 保护：多实例/多处重复调用只会起一份协程，避免同进程内自己抢自己的租约。
// workerID 带进程启动纳秒戳，保证多实例部署下互相不重名（租约按 leased_by 判定归属）。
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
// select+default 的写法让重复调用安全：已关闭再 close 会 panic。
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
		// 领取动作本身给 5 分钟上限（含 Notifier 等待）：这是个短操作，超时只代表「本轮没活」，
		// 与任务执行时长无关，所以绝不能把这条 ctx 传给执行逻辑。
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		// 多实例唤醒：有信号器时阻塞等待（最长 1s），否则直接领取（等价于原轮询节奏）
		if s.Notifier != nil {
			s.Notifier.Wait(ctx, time.Second)
		}
		job, err := s.Queue.Reserve(ctx, workerID, queue.DefaultLeaseSec)
		cancel()
		if err != nil || job == nil {
			if err != nil {
				log.Printf("[worker] 领取任务出错 worker=%s err=%v", workerID, err)
			}
			select { // ★ D9：关停信号立即退出（ctx 已被 cancel 不可复用，以 stopCh 为准）
			case <-time.After(1 * time.Second):
			case <-s.stopCh:
				return
			}
			continue
		}
		// 执行 ctx 与领取 ctx 完全分离：25 分钟是「单个任务最长执行时间」的硬上限，
		// 刻意小于默认租约窗口（30min），保证任务正常超时收尾时租约还在，
		// 不会被 RecoverStale/其他实例判成死任务而双跑。
		jctx, jcancel := context.WithTimeout(context.Background(), 25*time.Minute)
		// ★ 2026-09-04 加固：租约续期心跳——长任务处理期间定期刷新 jobs.leased_at，
		//   防止处理时长逼近/超过租约窗口（默认 30m）时被 RecoverStale 或其他实例
		//   误回收（running→queued 双跑）。任务结束/取消时停止。
		hbStop := make(chan struct{})
		go func() {
			t := time.NewTicker(60 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					if herr := s.Queue.Heartbeat(context.Background(), job.ID, workerID); herr != nil {
						log.Printf("[worker] 租约续期失败 job=%d: %v", job.ID, herr)
					}
				case <-hbStop:
					return
				}
			}
		}()
		perr := s.safeProcessJob(jctx, job)
		close(hbStop)
		jcancel()
		if perr != nil {
			_ = s.Queue.MarkFailed(context.Background(), job.ID, perr.Error())
			// ★ F-04（2026-09-25 UAT 修复批）死信告警：此前「重试耗尽置 dead」只写
			// jobs.error 字段，零告警零指标——SMTP 故障期验证码/欢迎邮件成片死在队列里
			// 无人知晓（入队即回 success:true 的三层「假话」最后一环）。
			// 判据：Reserve 已把 attempts 自增为「本次是第 N 次尝试」，与 MarkFailed 的
			// dead 条件（attempts>=max_attempts）同式。告警走普通 CreateAlert（同 kind open
			// 去重）：故障期只刷一条「有邮件死信」，细节留在 jobs 表，避免 SMTP 全挂时刷屏。
			if job.Type == "mail_send" && job.Attempts >= job.MaxAttempts {
				var p queue.MailPayload
				_ = json.Unmarshal(job.Payload, &p)
				_ = s.Store.CreateAlert(0, "critical", "mail_dead",
					fmt.Sprintf("邮件任务 %d 重试 %d 次耗尽转死信 to=%s subject=%q err=%s",
						job.ID, job.Attempts, p.To, p.Subject, perr.Error()))
			}
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
	case "mail_send":
		return s.processMailJob(job)
	default:
		return nil // 未知类型直接完成（防毒丸）
	}
}

// EnqueueMail 将邮件投递异步化：入队由 worker 发送，失败自动重试直至死信。
// 参数 useInfo=true 时使用专用邮箱（产品手册等）；否则默认邮箱。
func (s *TicketService) EnqueueMail(ctx context.Context, msg *mail.Message, useInfo bool) (int64, error) {
	id, err := s.Queue.Enqueue(ctx, "mail_send", queue.NewMailPayload(msg, useInfo), 5)
	if err == nil && s.Notifier != nil {
		_ = s.Notifier.Signal(ctx)
	}
	return id, err
}

// processMailJob 消费邮件任务：还原 mail.Message 并经对应 sender 发送。
func (s *TicketService) processMailJob(job *queue.Job) error {
	var p queue.MailPayload
	if err := json.Unmarshal(job.Payload, &p); err != nil {
		return nil // 载荷损坏：标记完成避免毒丸
	}
	m := p.ToMailMessage()
	var sender mail.Sender
	if p.UseInfo && s.InfoMailer != nil {
		sender = s.InfoMailer
	} else if s.Mailer != nil {
		sender = s.Mailer
	} else {
		log.Printf("[mail-worker] 无可用 sender（use_info=%v），丢弃任务 %d", p.UseInfo, job.ID)
		return nil
	}
	if err := sender.Send(m); err != nil {
		log.Printf("[mail-worker] 发送失败 to=%s 任务 %d: %v", m.To, job.ID, err)
		return err // 触发重试/死信
	}
	log.Printf("[mail-worker] 已发送 to=%s 任务 %d", m.To, job.ID)
	return nil
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
	// ★ 注入租户到 ctx：异步工单 ctx 源自 context.Background()，而实时计费钩子
	// （eng.LLM.OnUsage）靠 tenant.FromContext 取租户扣费，必须显式注入，
	// 否则 tid=0 不计费（白嫖）。t.TenantID 同时覆盖 OpenAPI 任务归属租户。
	ctx = tenant.WithTenant(ctx, t.TenantID)
	// ★ 性能优化 B1：注入发起用户到 ctx，修正实时计量 user_id 恒为 0 的缺陷
	//   （个人/组织用量看板此前失真）。OpenAPI 任务回退其归属用户 APIUserID。
	if t.CreatedBy > 0 {
		ctx = tenant.WithUser(ctx, t.CreatedBy)
	} else if t.APIUserID > 0 {
		ctx = tenant.WithUser(ctx, t.APIUserID)
	}
	// ★ 注入创建人组织（2026-08-26 KB继承链）：异步工单按「发起用户当前所在部门」
	//   的祖先链决定部门包可见范围；OpenAPI 任务（CreatedBy=0）回退其归属用户 APIUserID。
	creatorUID := t.CreatedBy
	if creatorUID <= 0 && t.APIUserID > 0 {
		creatorUID = t.APIUserID
	}
	if creatorUID > 0 {
		if cu, uerr := s.Store.GetUser(creatorUID, t.TenantID); uerr == nil && cu != nil {
			if cu.OrgID > 0 {
				ctx = engine.WithUserOrg(ctx, cu.OrgID)
			}
			if cu.JobRole != "" { // ★ 角色功能（2026-09-19）：工单按创建人职业角色装配角色包
				ctx = engine.WithUserJobRole(ctx, cu.JobRole)
			}
		}
	}
	// ★ 余额预检（强制计费时）：可用额度 = 未过期台账 + 永久余额（双桶口径，评审整改 A1），
	//   与 gateUsage/CheckBalance/DeductWithGrants 保持一致；不足快速失败，单独错误码提示充值/升级套餐
	// ★ 计费豁免（2026-09-02 需求6）：超管建单的工单 tenant_id=0（平台上下文）跳过余额预检，
	//   与 gateUsage 建单豁免保持一致——避免超管平台视角工单异步执行被误判「余额不足」拒绝。
	if s.Bill != nil && s.Bill.Enabled() && t.TenantID > 0 {
		if grants, perm, berr := s.Store.TenantRemainTotal(t.TenantID); berr == nil && grants+perm <= 0 {
			t.Status = store.TicketRejected
			t.RejectReason = errInsufficientCode + ": 余额不足，请充值或升级套餐"
			// ★ F-42-b：建单/入队预检属系统写入，来源标 'system'（不得进人工重翻分支）
			t.RejectSource = store.RejectSourceSystem
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
	// ★ P1-5 修复（2026-09-14）：并发认领互斥——CAS 把 draft/queued/rejected 原子推进到
	//   in_progress，抢不到（已被其他 worker 认领或进入终态）即静默退出。
	//   旧实现仅挡 completed，in_progress 工单可被第二个 job 再次执行（双跑双扣费）。
	claimed, cerr := s.Store.ClaimTicketForRun(t.ID)
	if cerr != nil {
		return fmt.Errorf("工单认领失败: %w", cerr)
	}
	if claimed == 0 {
		log.Printf("[ticket-run] 工单 %d 已被其他 worker 认领或处于不可执行态，本 job 跳过", t.ID)
		return nil
	}
	// 库里的状态已被 ClaimTicketForRun 原子推进，这里只同步内存对象，
	// 免得后续按 t 落库（失败收尾等）时把 in_progress 又覆盖回 queued。
	t.Status = store.TicketInProgress

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
	// 两个 ticker 故意不合并：心跳只要比 20min 卡死阈值密若干倍即可（60s），
	// 取消监视则越早掐断越少烧 LLM 钱（3s），共用一个周期必然迁就其中一方。
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
	// ★ 缩翻（任务7）：工单最长字符限制注入 ctx（0=未启用；>0=译文总长不得超过该值）
	if t.MaxLength > 0 {
		ctx = engine.WithMaxLength(ctx, int(t.MaxLength))
	}

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
		// ★ F-42-a（2026-09-25 UAT 修复批）：翻译途中余额烧穿的中止原因要从 cause 链
		//   里认出来。此前只写 runErr.Error()（旧值 'context canceled'，见 orchestrator/flow.go
		//   :121 的 context.Cause 修复），既无错误码可取，又会被重翻分支当成人工驳回意见。
		//   命中 ErrInsufficientBalance 时改写为与 :293 预检逐字一致的文案，88/90 两型合流：
		//   预检拦下的与烧穿中止的在库内是同一条可读理由，OpenAPI 侧按前缀取 insufficient_balance 码。
		if errors.Is(runErr, store.ErrInsufficientBalance) {
			t.RejectReason = errInsufficientCode + ": 余额不足，请充值或升级套餐"
		} else {
			t.RejectReason = runErr.Error()
		}
		// ★ F-42-b：本处是系统失败收尾，来源标 'system'
		t.RejectSource = store.RejectSourceSystem
		// ★ #40②（2026-09-21）：状态 + 失败通知同事务落库（旧实现两条独立写且忽略错误，
		//   会出现「工单已判失败但无人收到通知」）；写失败必须大声记录，不再 `_ =` 吞掉。
		notify := &store.Notification{
			UserID: t.CreatedBy,
			Title:  fmt.Sprintf("翻译工单失败：%s", t.Title),
			// ★ F-42-a：正文改取 t.RejectReason（欠费型已由上面归一为用户可读文案，
			//   旧写法直贴 runErr.Error() 会在欠费单上显示裸「余额不足」/「context canceled」）
			Body:    fmt.Sprintf("工单号 %s 失败原因：%s", t.TicketNo, t.RejectReason),
			RefType: "ticket",
			RefID:   t.ID,
		}
		if applied, ferr := s.Store.FinishTicket(&store.TicketFinishInput{
			Ticket: t, Notify: notify, ExcludeStatuses: []string{"cancelled"},
		}); ferr != nil {
			observability.Error(ctx, "工单失败收尾写库出错（状态/通知未落库，需人工核对）", "ticket_no", t.TicketNo, "err", ferr.Error())
		} else if !applied {
			observability.Warn(ctx, "工单失败收尾守卫命中（已取消），放弃失败态与通知写入", "ticket_no", t.TicketNo)
		}
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
	// 产物保留期打点：ticket_retention_days（默认 14 天；0=永久）。到期由后台每日扫描清理文件，
	// 核心译文不受影响——文本工单存 final_result、文件工单回写 tm_segments 长期沉淀。
	retentionDays := 14
	if v, _ := s.Store.GetConfig("ticket_retention_days"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			retentionDays = n
		}
	}
	expireHint := "结果文件长期保留"
	expiresAt := ""
	if retentionDays > 0 {
		exp := time.Now().AddDate(0, 0, retentionDays)
		expiresAt = exp.Format(time.RFC3339)
		expireHint = fmt.Sprintf("结果文件保留 %d 天（至 %s），请尽快下载", retentionDays, exp.Format("2006-01-02"))
	}
	// ★ #40②（2026-09-21）：完成态 + 到期打点 + 站内信收进一个事务（旧实现三条独立写且 `_ =`
	//   吞错，会留下「已完成却无 result_expires_at」——留存扫描永远漏掉这类工单，产物无限堆积）。
	//   守卫把上面「先查后改」的 R3 判断下沉为条件 UPDATE，堵住 TOCTOU 窗口。
	applied, ferr := s.Store.FinishTicket(&store.TicketFinishInput{
		Ticket:    t,
		ExpiresAt: expiresAt,
		Notify: &store.Notification{
			UserID:  t.CreatedBy,
			Title:   fmt.Sprintf("翻译工单完成：%s", t.Title),
			Body:    fmt.Sprintf("工单号 %s 翻译完成。%s；译文已沉淀至翻译记忆长期有效。", t.TicketNo, expireHint),
			RefType: "ticket",
			RefID:   t.ID,
		},
		ExcludeStatuses: []string{"cancelled", store.TicketQueued},
	})
	if ferr != nil {
		observability.Error(ctx, "工单完成收尾写库出错（状态/到期/通知未落库，需人工核对）", "ticket_no", t.TicketNo, "err", ferr.Error())
		return ferr
	}
	if !applied {
		observability.Warn(ctx, "工单完成收尾守卫命中（已取消或被巡检重排），放弃收尾与 webhook", "ticket_no", t.TicketNo)
		return nil
	}
	// ★ Webhook 完成回调（OpenAPI 轮询之外的推送通道）：带 task_id 与 token 消耗
	s.dispatchCompletedWebhook(ctx, t)
	return nil
}

// chargeTokens 工单级 Token 实费展示回填：读取 ctx 收集器累计的真实 token。
// ★ 性能优化 B1（修双重计费资损）：实时计量钩子（eng.LLM.OnUsage → ChargeUsageRealtime）
//
//	已是唯一扣费来源，每次 LLM 调用即扣减并支持余额不足中止；本函数**不再二次扣费**，
//	仅回填展示用的真实 token 数（TokensBilled）。强制计费开关仅影响实时路径是否落账，
//	与这里无关。
func (s *TicketService) chargeTokens(ctx context.Context, t *store.Ticket) int64 {
	if s.Engine == nil {
		return 0
	}
	prompt, completion := s.Engine.UsageTokens(ctx)
	total := prompt + completion
	if total <= 0 {
		return 0 // 无 LLM 调用（纯 KB 命中等）
	}
	return total
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
		"points_used": s.Store.PointsFromTokens(prompt + completion), // 积分口径（token 裸值不外发，2026-09-19）
		"time":        time.Now().Format(time.RFC3339),
	})
}

// runTextTicket 纯文本工单：编排流水线（pro=全步骤；fast=初翻+校对，见 orchestrator 模式覆盖）。
func (s *TicketService) runTextTicket(ctx context.Context, t *store.Ticket) error {
	// ★ F-14 后端半（2026-09-25 UAT 修复批）：实时计量钩子（billing_api.meterUsage）取
	// usage_ledger.biz_mode 的来源是 ctx 注入的 mode——文件路径（runFileTicket :585）有注入，
	// 文本工单 worker 路径从不注入 ⇒ biz_mode='' ⇒ 台账「模式」列显「-」。
	// 空 mode 按库口径归一为 pro（fast|pro，空=专业校对），与计费文案侧一致。
	textMode := t.Mode
	if textMode == "" {
		textMode = "pro"
	}
	ctx = tenant.WithMode(ctx, textMode)
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

// lowBalanceThreshold 低额告警绝对阈值：读 system_config low_balance_alert_tokens。
// ★ 缺陷核实修复（2026-09-16 D2）：旧实现硬编码 100000 与注释不符——超管改配置不生效。
// 缺失/非法/非正数回退默认 100000（与 store 种子值口径一致）。
func (s *TicketService) lowBalanceThreshold() int64 {
	if s.Store != nil {
		if v, _ := s.Store.GetConfig("low_balance_alert_tokens"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
				return n
			}
		}
	}
	return 100000
}

// StartStallSweep 卡死工单巡检：每 5 分钟扫描 in_progress 且 updated_at 超过 20 分钟的工单，
// 重置为 queued 触发断点续传（worker 收尾前已有取消复查，重排安全）。防信号量饿死类静默卡死。
// ★ 同周期顺带执行商业化巡检：订单15min超时自动关闭（CloseStalePendingOrders）
//   - 超时单的券额度回收（ReleaseStaleCouponRedemptions，#41）+ 低额提醒（24h去重）。
//
// 阶段二：多实例部署下用分布式锁保证巡检同一时刻仅一个实例执行（避免重复告警/重复下单关闭）。
func (s *TicketService) StartStallSweep() {
	// 分布式锁（Redis 启用时跨实例互斥；未启用则进程内锁，单实例行为不变）。
	lock := distlock.New("lock:stallsweep", redis.Get())
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			// 非阻塞获取：拿不到说明其他实例正在巡检，本实例直接跳过本轮（尽力而为）。
			// TTL 取 6 分钟（略大于 5 分钟轮询周期）：正常路径靠 release 主动释放，
			// TTL 只兜「持锁实例崩溃」的残留，最多让集群多等一轮，不会长期锁死巡检。
			got, release, err := lock.TryLock(context.Background(), 6*time.Minute)
			// ★ P1-4（2026-09-18）：Redis 异常与「他实例持锁」语义不同——异常时原写法
			//   `err != nil || !got → continue` 会让全集群巡检静默停摆；改为保守降级
			//   本进程执行（关单/低额提醒/重排均幂等，宁可偶发重复不可停摆）。
			if err != nil {
				observability.Warn(context.Background(), "stall 巡检分布式锁异常，本轮保守降级本进程执行", "err", err.Error())
				release = func() {}
			} else if !got {
				continue
			}
			// 用一层匿名函数包住本轮巡检：defer release() 的作用域因此是「本轮」而不是
			// 整个 goroutine ——写在 for 里会等 goroutine 退出才释放（即永远持锁）。
			func() {
				defer release()
				s.Store.CloseStalePendingOrders() // ★ 订单15min超时自动关闭
				// ★ 优惠券（#41）：上一步把超时单置 cancelled 后，这里释放其占用的券核销额度，
				//   否则「下单没用券成功、单又超时」的失败尝试会把总配额吃干净（活动还没开始就显示已抢完）。
				s.Store.ReleaseStaleCouponRedemptions()
				s.Store.TenantLowBalanceAlerts(s.lowBalanceThreshold()) // ★ 低额提醒(24h去重)
				n, rerr := s.Store.RequeueStalledTickets(20 * time.Minute)
				if rerr != nil {
					return
				}
				if n > 0 {
					s.Store.CreateAlert(0, "warning", "stall",
						fmt.Sprintf("检测到 %d 个翻译卡死工单（>20min 无进展），已自动重新排队续跑", n))
				}
			}()
		}
	}()
}

// runFileTicket 文件翻译工单执行主流程：解析目标语言→按模式(fast/pro)驱动引擎管线→
// 阶段进度回调（提取20/初翻40/校对60/回写80）→产物落盘与工单状态推进。
// 参数：ctx=取消/超时上下文（暂停与硬闸重试依赖）；t=工单对象；返回错误（含回显重试语义）。
func (s *TicketService) runFileTicket(ctx context.Context, t *store.Ticket) error {
	// ★ 性能优化（不换库 Phase A3）：文件翻译（尤其 PDF 转换）期间 Go 侧会累积大量
	//   段落/译文缓冲与 KB 向量检索结果；转换子进程退出后其峰值已释放，但 Go 的
	//   GC 默认保留 RSS 不立即归还 OS。本 defer 在工单收尾时主动归还，给 1G 机器上的
	//   后续转换子进程（pdf2docx/LibreOffice）留出内存空间，规避累积式 OOM。
	defer debug.FreeOSMemory()
	langs := parseLangs(t.TargetLangs)
	mode := t.Mode // fast | pro（空=pro）
	// ★ 工单双模式（2026-09-13）：交付方式 restore 还原文件（默认）/ text 纯文案（anydoc 提取，交付 .md）
	delivery := t.Delivery
	if delivery == "" {
		delivery = "restore"
	}
	// ★ 运营策略引擎（2026-09-05）：注入模式到 ctx，异步工单实时计量按 fast/pro 区分免费/扣费
	ctx = tenant.WithLang(tenant.WithMode(ctx, mode), firstLangOf(langs)) // ★ C4：模式+主目标语种注入
	// ★ 归属登记用创建者 ID（评审整改 C1）：OpenAPI 任务回退其归属用户
	ownerUID := t.CreatedBy
	if ownerUID <= 0 && t.APIUserID > 0 {
		ownerUID = t.APIUserID
	}
	// ★ 进度阶梯回调：把引擎内部阶段映射为步骤状态（前端锚点：提取20/初翻40/校对60/回写80）。
	// 同时归集细粒度「初翻/校对」逐段进度：引擎以 "file_translate|初翻|en" / "file_translate|校对|en"
	// 上报（done/total 为该语言段数），此处按语言累加，落库为单条 file_translate 轨迹的 JSON。
	// 按语言各记一份 done/total：多语言并发时每个协程只覆盖自己那一格（不是累加器，
	// 引擎按语言各自上报 0..N 的游标），汇总时才求和，写竞争面最小。
	type segProg struct {
		mu      sync.Mutex
		init    map[string]int64
		initT   map[string]int64
		review  map[string]int64
		reviewT map[string]int64
	}
	sp := &segProg{init: map[string]int64{}, initT: map[string]int64{}, review: map[string]int64{}, reviewT: map[string]int64{}}
	// ★ 性能优化 B4：逐段进度落库节流——每 1s 或每 100 段才写一次 ticket_state，
	//   避免上千段 × 多语言把 SQLite 写事务堆成瓶颈（与计量批量落库协同缓解 SQLITE_BUSY）。
	var progLastWrite time.Time
	progWriteCount := 0
	finalizeFileTranslate := func() {
		sp.mu.Lock()
		// 四个 map 求和成一条全局进度（锁内算完即解锁，落库在锁外）
		var id, it, rd, rt int64
		for _, v := range sp.init {
			id += v
		}
		for _, v := range sp.initT {
			it += v
		}
		for _, v := range sp.review {
			rd += v
		}
		for _, v := range sp.reviewT {
			rt += v
		}
		// 手工拼 JSON：字段全是整数、无用户文本，不存在需要转义的内容（含文本的 payload 不可照此写）
		payload := fmt.Sprintf(`{"init_done":%d,"init_total":%d,"review_done":%d,"review_total":%d}`, id, it, rd, rt)
		sp.mu.Unlock()
		s.Store.SetTicketState(t.ID, "file_translate", "success", payload)
	}
	progFn := func(step string, done, total int) {
		if strings.Contains(step, "|") {
			parts := strings.Split(step, "|")
			stage, lang := parts[1], parts[2]
			sp.mu.Lock()
			if stage == "初翻" {
				sp.init[lang] = int64(done)
				sp.initT[lang] = int64(total)
			} else if stage == "校对" {
				sp.review[lang] = int64(done)
				sp.reviewT[lang] = int64(total)
			}
			var id, it, rd, rt int64
			for _, v := range sp.init {
				id += v
			}
			for _, v := range sp.initT {
				it += v
			}
			for _, v := range sp.review {
				rd += v
			}
			for _, v := range sp.reviewT {
				rt += v
			}
			payload := fmt.Sprintf(`{"init_done":%d,"init_total":%d,"review_done":%d,"review_total":%d}`, id, it, rd, rt)
			sp.mu.Unlock()
			// ★ 性能优化 B4：节流落库（每 1s 或每 100 段一次；最终态由 finalizeFileTranslate 落）
			progWriteCount++
			if time.Since(progLastWrite) >= time.Second || progWriteCount%100 == 0 {
				progLastWrite = time.Now()
				s.Store.SetTicketState(t.ID, "file_translate", "running", payload)
			}
			return
		}
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
		var okCount, failCount, unTotal, degradedFiles int64
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
					// ★B3：工单队列通道不接逐段事件（emit=nil，方案 A2 前端改动点 4：段落中间态
					//   需落库+轮询暴露，放二期）；体验口径见 API 文档「SSE 通道有实时段落、任务通道完成后可得」。
					// ★ #65：source_name 传 DB 里的原件展示名（tf.FileName），引擎据此取产物名——
					//   落盘名 tf.FilePath 带内部纳秒前缀，只能当唯一性标识，绝不能进交付物文件名。
					map[string]interface{}{"target_langs": langs, "mode": mode, "delivery": delivery, "source_name": tf.FileName}, progFn, nil)
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
					zp, zerr := zipOutputs(res.Files, zipDeliveryName(res.Files, langs, tf.FilePath))
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
				// ★ 双模式（2026-09-13）：纯文案旁路产物登记（还原模式兜底附加物）
				if p := s.persistTextOutputs(t, ownerUID, res.TextFiles); p != "" {
					_ = s.Store.SetTicketFileTextResult(tf.ID, p)
				}
				// ★ 双模式：版式还原失败已降级纯文案——文件级通知（工单仍成功），汇总轨迹在循环后
				if len(res.Data.DegradedLangs) > 0 {
					degradedFiles++
					s.Store.CreateNotification(t.CreatedBy,
						fmt.Sprintf("文件版式还原失败已交付纯文案：%s", tf.FileName),
						fmt.Sprintf("%s 翻译已完成，但版式还原失败（%s），已以纯文案 .md 交付；如需还原版式可重新发起还原模式工单。",
							tf.FileName, strings.Join(res.Data.DegradedLangs, "/")),
						"ticket", t.ID)
				}
				okCount++
				// doneN = 本轮成功数 + 库里已标 error 的文件数（重跑同一工单时进度才不会倒退）
				doneN := okCount + failedCount(s.Store, t.ID)
				// 进度落库前主动放锁、写完再抢回来：持锁做写事务会把同批其他文件的协程
				// 全部串到最慢的一次 DB 写后面。重新加锁是为了与本函数入口的 defer mu.Unlock()
				// 配对——这一区间内绝不能 return，否则 deferred Unlock 解的是未加的锁（panic）。
				mu.Unlock()
				s.Store.SetTicketState(t.ID, "file_translate", "running",
					fmt.Sprintf("progress=%d/%d", doneN, len(files)))
				mu.Lock()
				// ★ 逐段对照真值落库（2026-09-18）：按文件维度（ticket_segments 唯一键含 file_path），
				//   多文件工单各文件的段不会互相覆盖。
				s.persistTicketSegments(t, tf.FilePath, res)
				s.bumpTmHitsFromTranslations(t.TenantID, res.Data.Translations) // ★ 自闭环计数（不自动入库）
				// ★ 漏翻可见性：聚合各文件未译出段数，收尾统一落轨迹+通知
				unTotal += int64(untranslatedTotal(res.Data.Untranslated))
			}(f)
		}
		wg.Wait()
		finalizeFileTranslate()
		s.Store.SetTicketState(t.ID, "file_extract", "success", "")
		if okCount > 0 {
			// 校对（pro 流水线内含 QA，此处为阶梯标记）与回写完成
			s.Store.SetTicketState(t.ID, "file_qa", "success",
				fmt.Sprintf("ok=%d fail=%d", okCount, failCount))
			s.Store.SetTicketState(t.ID, "file_writeback", "success", "")
			// ★ 双模式（2026-09-13）：任一文件版式还原失败降级 → 回写步骤置 warning（阶梯语义仍完成）
			if degradedFiles > 0 {
				s.Store.SetTicketState(t.ID, "file_writeback", "warning",
					fmt.Sprintf("degraded_files=%d（版式还原失败，已以纯文案 .md 交付，详见通知）", degradedFiles))
			}
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
	// 旧单文件路径（★B3：emit=nil 同队列通道口径，不接逐段事件）
	// ★ #65：不传 source_name——tickets 表这一路径没有「原件展示名」列（Title 用户可改，
	//   不能当文件名用），由引擎回落「落盘名剥内部时间戳标记」，同样不会把纳秒前缀交付出去。
	s.Store.SetTicketState(t.ID, "file_translate", "running", "single")
	res := s.Engine.HandleFile(ctx, t.FilePath, map[string]interface{}{"target_langs": langs, "mode": mode, "delivery": delivery}, progFn, nil)
	if res.Error != "" {
		return fmt.Errorf("%s", res.Error)
	}
	finalizeFileTranslate()
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
	// ★ 双模式（2026-09-13）：纯文案旁路产物登记；降级语言另发通知并回置 warning 轨迹
	if p := s.persistTextOutputs(t, ownerUID, res.TextFiles); p != "" {
		_ = s.Store.SetTicketTextResultPath(t.ID, p)
	}
	if len(res.Data.DegradedLangs) > 0 {
		s.Store.SetTicketState(t.ID, "file_writeback", "warning",
			fmt.Sprintf("degraded=%s（版式还原失败，已以纯文案 .md 交付）", strings.Join(res.Data.DegradedLangs, "/")))
		s.Store.CreateNotification(t.CreatedBy,
			fmt.Sprintf("文件版式还原失败已交付纯文案：%s", t.Title),
			fmt.Sprintf("翻译已完成，但 %s 版式还原失败，已以纯文案 .md 交付；如需还原版式可重新发起还原模式工单。",
				strings.Join(res.Data.DegradedLangs, "/")),
			"ticket", t.ID)
	}
	// ★ 逐段对照真值落库（2026-09-18，独立表 ticket_segments，前端无感知）：
	//   必须在 HandleFile 之后、结果还在手上时落——SourceSegments/Translations 都是不序列化的
	//   字段，落库晚了就拿不到「提取顺序 ↔ 译文」的精确配对（对照编辑器会退回按下标硬对齐）。
	s.persistTicketSegments(t, t.FilePath, res)
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
// 口径以库里的 ticket_files.error 为准（由 SetTicketFileError 写入），不看内存累加器：
// 因此本次运行之外（断点续跑/巡检重排前）已标错的文件也会计入，进度与统计不会因重跑而偏小。
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

// zipDeliveryName 给多语言产物压缩包起名（工单 T20260921…：英文交付包顶着中文原件名
// `产品方案书_translated.zip` 是体验缺陷——RC-4 已把**产物文件**名翻成目标语，压缩包名漏改了）。
// 口径：取第一个产物的 base（已是目标语名字）并剥掉其 `_<语言码>[_text]` 尾巴，
// 因为包里含多种语言、不该只挂一种语言的标记；剥不到就用整个 base，
// 无产物时回落原件名。**只做字符串整理，不碰文件系统**，落盘仍由 zipOutputs 负责。
func zipDeliveryName(files []string, langs []string, srcPath string) string {
	name := strings.TrimSuffix(filepath.Base(srcPath), filepath.Ext(srcPath))
	if len(files) > 0 {
		if base := strings.TrimSuffix(filepath.Base(files[0]), filepath.Ext(files[0])); base != "" {
			name = base
			for _, lc := range langs { // 先 `_lang_text`（纯文案旁路）再 `_lang`（主件）
				for _, sfx := range []string{"_" + lc + "_text", "_" + lc} {
					if strings.HasSuffix(name, sfx) {
						name = strings.TrimSuffix(name, sfx)
						break
					}
				}
			}
			if name == "" {
				name = base // 整名就是一个语言标记（极端命名），用回原 base 不出空文件名
			}
		}
	}
	return name + "_translated.zip"
}

// zipOutputs 将多个产物文件打包为一个 zip（供下载一次获取全部语言版本）。
// zip 与产物同目录（即 translated/<落盘名>/），下载侧的目录白名单因此天然覆盖。
// 单个文件读不到时跳过而非整体失败——已生成的语言不该被一个坏文件连累。
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
		// 只存 basename：包内不带任何目录成分，解压方不会因绝对/相对路径写到目录外
		fe, _ := w.Create(filepath.Base(p))
		_, _ = fe.Write(data)
	}
	// Close 才真正落中央目录，其错误才是打包是否可用的结论；deferred Close 仅兜底（重复调用返回值被丢）
	return zipPath, w.Close()
}

// persistTicketSegments 把「源文段 → 译文段」的**精确配对**落进 ticket_segments 真值表。
//
// 为什么需要：PDF 文件工单的源文与译本是**两次独立**的 pdf2docx 转换产物，段落切分粒度
// 必然不同（实测同一工单源 504 段 / 译本 578 段）。对照编辑器旧实现按下标 min() 硬对齐，
// 抽查 20 对全部错位（源「新车上市当天官网多语齐发」↔ 译「Method B: Integrate into Skills
// for calling via Lark」），前端双栏编辑器显示成「大量块不匹配」。而翻译这一段当时手上本就有
// SourceSegments[i] ↔ Translations[lang][SourceSegments[i]] 的真值，直接落库即可。
//
// 独立性：写的是**新增的独立表**，不改 translation_edits / tickets 的任何字段；对照接口
// （EditorSegment）形状不变，故前端无感知。写失败只记日志——附加数据，缺了自动回退旧口径。
// 未译出的段也写入（target 留空），保证段序号与源文一侧严格对齐，不产生「跳号」。
// 参数：t=工单；filePath=本次处理的源文件；res=引擎文件翻译结果（含不序列化的真值字段）。
func (s *TicketService) persistTicketSegments(t *store.Ticket, filePath string, res *engine.FileTranslateResult) {
	// 防御性早退：失败结果（只带 Error 的 FileTranslateResult）与测试/历史构造的 res
	// 都没有有序源文段。此时**不能**改用 Translations 的 map 键补写——map 无顺序，
	// 写出来的 seg_index 是随机序，宁可什么都不写让读取侧回退旧口径。
	if t == nil || res == nil || filePath == "" || len(res.Data.SourceSegments) == 0 {
		return
	}
	src := res.Data.SourceSegments
	for lang, tr := range res.Data.Translations {
		if len(tr) == 0 {
			// 该语言一条「原文→译文」都没有：写一张全空 target 的表没有信息量，
			// 还会让 segmentsFromStore 命中并把空段喂给编辑器，不如留空走回退。
			continue
		}
		rows := make([]store.TicketSegment, 0, len(src))
		for i, s0 := range src {
			rows = append(rows, store.TicketSegment{
				SegIndex: i,
				Source:   s0,
				Target:   strings.TrimSpace(tr[s0]), // 未命中=未译出，留空保持段序对齐
			})
		}
		if err := s.Store.SaveTicketSegments(t.TenantID, t.ID, filePath, lang, rows); err != nil {
			// 失败**不返回 error**：主交付物此时已落盘，真值表只是对照编辑器的附加数据，
			// 缺了只是回退到旧的按下标对齐口径，没理由把整单打回重跑（重跑还要再吃一遍积分）。
			// 日志口径沿用本文件既有的标准库 log（observability 需要 ctx，本函数没有 ctx 形参）。
			log.Printf("[segments] 逐段对照真值落库失败（工单 %d / %s，不影响交付）: %v", t.ID, lang, err)
		}
	}
}

// persistTextOutputs 登记纯文案旁路产物（还原模式）。
// 逐个 Stat 过滤，只登记磁盘上确实存在的文件；多份打成一个 zip 返回（zip 自身也要登记归属），
// 打包失败则退回第一份路径——至少保证有一个可下载产物，而不是整体留空。
func (s *TicketService) persistTextOutputs(t *store.Ticket, ownerUID int64, paths []string) string {
	var exist []string
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			exist = append(exist, p)
			s.Store.RegisterArtifact(p, t.TenantID, ownerUID, t.ID)
		}
	}
	if len(exist) == 0 {
		return ""
	}
	if len(exist) == 1 {
		return exist[0]
	}
	zp, err := zipOutputs(exist, t.TicketNo+"_texts.zip")
	if err != nil {
		return exist[0]
	}
	s.Store.RegisterArtifact(zp, t.TenantID, ownerUID, t.ID)
	return zp
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
// ★ 性能优化 B5：改为单次批量 upsert（单事务），避免大文件逐条 INSERT+SELECT 产生数千次 DB 往返。
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
	var pairs []store.TmHitPair
	for lc, m := range translations {
		for src, tgt := range m {
			pairs = append(pairs, store.TmHitPair{Zh: src, Lang: lc, Trans: tgt})
		}
	}
	created := s.Store.BumpTmHitsBatch(tid, pairs, th)
	for _, preview := range created {
		s.Store.CreateAlert(tid, "warning", "tm_review",
			fmt.Sprintf("相同翻译累计达 %d 次，已生成待审候选：%s", th, preview))
	}
}

// parseLangs 解析逗号分隔语言串。
// firstLangOf ★ C4：主目标语种（target_langs 首个；空=通配）。
func firstLangOf(langs []string) string {
	if len(langs) > 0 {
		return langs[0]
	}
	return ""
}

// parseLangs 解析逗号分隔的语言列表，忽略空项。
// 全空时兜底单一 en（工单执行必须有一个目标语言，缺省不能把空列表传给引擎）。
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
		// ★ P1-4 修复（2026-09-14）：只回收「无租约」或「租约已陈旧」（120s 未心跳）的
		//   running 任务——旧实现无条件重置全部 running，多实例部署下任一实例重启会把
		//   其他实例正在执行的任务重置回队（注释假设「上一进程必然已死」仅单实例成立），
		//   造成同一工单双跑双扣费。健康 worker 心跳间隔 60s，120s 宽限可稳定区分死活；
		//   本实例崩溃遗留任务的租约同样在 120s 后被回收，「卡死」顾虑不受损。
		grace := time.Now().Add(-120 * time.Second).Format(time.RFC3339)
		// ★ P1-5（2026-09-18）：旧写法 RawDB().Exec 裸用 `?` 占位符——lib/pq（PG 生产）
		//   下直接报错且错误被丢弃，崩溃租约回收在 PG 上从未生效；改走 db.Exec 方言包装。
		if _, rerr := db.Exec(s.DB.RawDB(), db.CurrentDialect(), "UPDATE jobs SET status='queued', leased_by='', leased_at='' WHERE type='ticket_run' AND status='running' AND (leased_by='' OR leased_at='' OR leased_at<?)", grace); rerr != nil {
			log.Printf("[boot-resume] 租约回收 SQL 执行失败: %v", rerr)
		}
	}
	if n, err := s.Store.RequeueStalledTickets(0); err == nil && n > 0 {
		log.Printf("[boot-resume] 已重新排队 %d 个中断工单", n)
	}
}
