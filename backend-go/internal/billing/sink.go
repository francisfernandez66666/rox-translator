// ============ sink.go · 职责说明 ============
// 实时计量批量落库（性能优化 B2/B3，不换库）：
// 此前每次 LLM 调用都经 ChargeUsageRealtime → RecordUsage 开一条 BEGIN IMMEDIATE 写事务
// （双桶扣减 + ledger INSERT）。大文件翻译上千段 × 多语言 = 上千个串行写事务，
// 多租户并发时全部排队撞 busy_timeout 报 SQLITE_BUSY。
//
// 本文件把实时计量改为「内存累积 + 周期批量落库」：
//   - Record() 仅追加到内存缓冲，并维护每租户的内存影子余额（seed 自 DB），余额不足立即
//     触发 abort（保留「余额耗尽即停」语义，延迟 ≤ 刷新周期）；
//   - 后台 flusher 每 flushInterval（默认 2s）或缓冲达 maxBatch（默认 200）触发一次，
//     按租户分组、每租户单事务（RecordUsageBatch / LogUsageBatch）批量扣减+多行落账，
//     写事务从「每秒 N 个」降到「每周期每租户 1 个」。
//
// 强制计费开关由 Service.Enabled() 决定：开启走扣减批次，关闭走留痕批次（仅 INSERT ledger）。
// =============================================
package billing

import (
	"errors"
	"log"
	"sync"
	"time"

	"translator/internal/store"
)

// usageRecord 单条待落库计量（携带 abort 回调以支持余额不足即时中止）。
type usageRecord struct {
	Tid      int64
	UID      int64
	TaskType string
	Provider string
	Model    string
	Quantity int64 // 已含对外计费系数（markup）后的计费量
	BizKind  string
	BizMode  string
	Abort    func()
}

// UsageSink 实时计量批量缓冲（进程级单例）。
type UsageSink struct {
	mu       sync.Mutex
	svc      *Service
	buf      []usageRecord
	shadow   map[int64]int64 // 每租户内存影子余额（token 量，已含 markup）
	shadowOk map[int64]bool  // 影子是否已 seed

	flushInterval time.Duration
	maxBatch      int

	wake chan struct{}
	stop chan struct{}
}

// RecordUsage 进程级入口：把一条实时计量追加到批量缓冲（供 llm.OnUsage 钩子调用）。
// abort 在余额不足时用于中止整次翻译；可为 nil。
func RecordUsage(tid, uid int64, taskType, provider, model string, quantity int64, bizKind, bizMode string, abort func()) {
	DefaultSink.Record(usageRecord{
		Tid: tid, UID: uid, TaskType: taskType, Provider: provider, Model: model,
		Quantity: quantity, BizKind: bizKind, BizMode: bizMode, Abort: abort,
	})
}

// DefaultSink 进程级单例（由 InitGlobalSink 绑定 Store 与 Service）。
var DefaultSink = &UsageSink{
	shadow:        map[int64]int64{},
	shadowOk:      map[int64]bool{},
	flushInterval: 2 * time.Second,
	maxBatch:      200,
	wake:          make(chan struct{}, 1),
	stop:          make(chan struct{}),
}

// InitGlobalSink 绑定 Store 与 Service 并启动 flusher（main 启动时调用一次）。
// svc 可为 nil（billing 未启用时仅做安全兜底，不落库）。
func InitGlobalSink(svc *Service) {
	DefaultSink.mu.Lock()
	DefaultSink.svc = svc
	DefaultSink.mu.Unlock()
	go DefaultSink.run()
}

// Invalidate 使指定租户的影子余额失效，下次 Record 时重新从数据库 seed。
// 用途：发放试用/充值/退款等余额变动后立即调用，避免内存影子余额仍停留在旧值
// （此前发放试用后若影子在「余额为 0」时被 seed，会一直负化并误触发 abort，
// 表现为「已发放试用仍提示组织 token 已耗尽」）。
func (s *UsageSink) Invalidate(tid int64) {
	s.mu.Lock()
	delete(s.shadowOk, tid)
	delete(s.shadow, tid)
	s.mu.Unlock()
}

// InvalidateShadow 进程级入口：使某租户影子余额失效（发放/充值后调用）。
func InvalidateShadow(tid int64) {
	DefaultSink.Invalidate(tid)
}

// Stop 停止 flusher（优雅停机时调用，执行一次最终 flush）。
func (s *UsageSink) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	s.flush()
}

// Record 追加一条计量；必要时触发即时中止与唤醒 flusher。
func (s *UsageSink) Record(r usageRecord) {
	s.mu.Lock()
	if s.svc == nil || s.svc.Store == nil {
		s.mu.Unlock()
		return // billing 未初始化：安全丢弃（与「未启用不计费」一致）
	}
	// seed 影子余额（每租户一次 DB 读）
	if !s.shadowOk[r.Tid] {
		if g, p, err := s.svc.Store.TenantRemainTotal(r.Tid); err == nil {
			s.shadow[r.Tid] = g + p // 双桶口径（未过期台账 + 永久余额）
		}
		s.shadowOk[r.Tid] = true
	}
	s.shadow[r.Tid] -= r.Quantity
	// ★ 余额不足立即中止（保留实时中止语义）：仅强制计费时生效
	if s.svc.Enabled() && s.shadow[r.Tid] < 0 && r.Abort != nil {
		r.Abort()
	}
	s.buf = append(s.buf, r)
	over := len(s.buf) >= s.maxBatch
	s.mu.Unlock()
	if over {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

// run flusher 主循环。
func (s *UsageSink) run() {
	ticker := time.NewTicker(s.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-s.wake:
			s.flush()
		case <-ticker.C:
			s.flush()
		}
	}
}

// flush 取出当前缓冲并按租户分组批量落库（调用方需自行保证并发安全由 mu 保护）。
func (s *UsageSink) flush() {
	s.mu.Lock()
	if len(s.buf) == 0 {
		s.mu.Unlock()
		return
	}
	batch := s.buf
	s.buf = nil
	// ★ 影子账本自愈（修复 P0-2）：按租户回读真实双桶余额并重算影子。
	// 每周期每租户一次 DB 读，符合「每周期每租户一次写事务」的批处理目标，不破坏性能优化；
	// 同时保证外部充值/退款或上次扣减失败后，影子不会永久负化导致租户被冻结。
	if s.svc != nil && s.svc.Store != nil {
		byTid := map[int64]int64{}
		for _, r := range batch {
			byTid[r.Tid] += r.Quantity
		}
		for tid, pending := range byTid {
			if g, p, err := s.svc.Store.TenantRemainTotal(tid); err == nil {
				s.shadow[tid] = g + p - pending // 真实余额 - 本批待落库量，保持乐观准确
				s.shadowOk[tid] = true
			}
		}
	}
	s.mu.Unlock()

	groups := map[int64][]usageRecord{}
	for _, r := range batch {
		groups[r.Tid] = append(groups[r.Tid], r)
	}
	enforced := s.svc.Enabled()
	var failed []usageRecord
	for tid, recs := range groups {
		rows := make([]store.UsageBatchRow, 0, len(recs))
		for _, r := range recs {
			rows = append(rows, store.UsageBatchRow{
				UserID: r.UID, TaskType: r.TaskType, Provider: r.Provider,
				Model: r.Model, Quantity: r.Quantity, BizKind: r.BizKind, BizMode: r.BizMode,
			})
		}
		var err error
		if enforced {
			_, err = s.svc.Store.RecordUsageBatch(tid, rows)
		} else {
			err = s.svc.Store.LogUsageBatch(tid, rows)
		}
		if err != nil {
			if errors.Is(err, store.ErrInsufficientBalance) {
				// 余额不足：中止本批且不再重试（避免无限回放）
				for _, r := range recs {
					if r.Abort != nil {
						r.Abort()
					}
				}
				log.Printf("[usagesink] flush tenant=%d 余额不足，丢弃计费: %v", tid, err)
				continue
			}
			// 其余错误（如 SQLITE_BUSY/磁盘抖动）：回插缓冲，下一周期重试，避免 fail-open 少计费
			log.Printf("[usagesink] flush tenant=%d 失败，回插缓冲重试: %v", tid, err)
			failed = append(failed, recs...)
		}
	}
	if len(failed) > 0 {
		s.mu.Lock()
		// 内存护栏：缓冲超过 5 万条时丢弃最旧部分，防止持续故障下无限膨胀
		s.buf = append(failed, s.buf...)
		if len(s.buf) > 50000 {
			s.buf = s.buf[len(s.buf)-50000:]
		}
		s.mu.Unlock()
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}
