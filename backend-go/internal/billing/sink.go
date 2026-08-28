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
	s.mu.Unlock()

	groups := map[int64][]usageRecord{}
	for _, r := range batch {
		groups[r.Tid] = append(groups[r.Tid], r)
	}
	enforced := s.svc.Enabled()
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
			// 余额不足：对批次内各条触发中止（兜底；影子已提前中止过）
			if errors.Is(err, store.ErrInsufficientBalance) {
				for _, r := range recs {
					if r.Abort != nil {
						r.Abort()
					}
				}
			}
			log.Printf("[usagesink] flush tenant=%d failed: %v", tid, err)
		}
	}
}
