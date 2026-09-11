// ============ metrics.go · 职责说明 ============
// fileproc 包子进程监控指标收集。
// 提供子进程启动次数、成功/失败次数、超时次数、SIGKILL 次数、平均耗时等指标。
// 供 /metrics 接口导出 Prometheus 文本格式。
package fileproc

import (
	"sync"
	"sync/atomic"
	"time"
)

// ProcMetrics 子进程监控指标（原子递增，供 /metrics 导出）
type ProcMetrics struct {
	// 启动次数
	starts int64
	// 成功完成次数
	success int64
	// 失败次数（非超时）
	failures int64
	// 超时次数（被 context 取消）
	timeouts int64
	// SIGKILL 次数（强制杀进程组）
	sigkills int64
	// 累计执行时间（纳秒，用于计算平均耗时）
	totalDurationNs int64
	// 当前运行中的子进程数
	running int64
	// 队列等待次数（超过 2s）
	queueWaits int64
	// 队列等待总时间（纳秒）
	queueWaitTotalNs int64
}

var globalProcMetrics ProcMetrics

// RecordStart 记录子进程启动
func RecordStart() {
	atomic.AddInt64(&globalProcMetrics.starts, 1)
	atomic.AddInt64(&globalProcMetrics.running, 1)
}

// RecordSuccess 记录子进程成功完成
func RecordSuccess(duration time.Duration) {
	atomic.AddInt64(&globalProcMetrics.success, 1)
	atomic.AddInt64(&globalProcMetrics.running, -1)
	atomic.AddInt64(&globalProcMetrics.totalDurationNs, int64(duration))
}

// RecordFailure 记录子进程失败（非超时）
func RecordFailure(duration time.Duration) {
	atomic.AddInt64(&globalProcMetrics.failures, 1)
	atomic.AddInt64(&globalProcMetrics.running, -1)
	atomic.AddInt64(&globalProcMetrics.totalDurationNs, int64(duration))
}

// RecordTimeout 记录子进程超时
func RecordTimeout(duration time.Duration) {
	atomic.AddInt64(&globalProcMetrics.timeouts, 1)
	atomic.AddInt64(&globalProcMetrics.running, -1)
	atomic.AddInt64(&globalProcMetrics.totalDurationNs, int64(duration))
}

// RecordSigkill 记录 SIGKILL 强制杀进程
func RecordSigkill() {
	atomic.AddInt64(&globalProcMetrics.sigkills, 1)
}

// RecordQueueWait 记录队列等待（超过 2s）
func RecordQueueWait(waitTime time.Duration) {
	atomic.AddInt64(&globalProcMetrics.queueWaits, 1)
	atomic.AddInt64(&globalProcMetrics.queueWaitTotalNs, int64(waitTime))
}

// Snapshot 指标快照（供 /metrics 导出）
type ProcMetricsSnapshot struct {
	Starts          int64
	Success         int64
	Failures        int64
	Timeouts        int64
	Sigkills        int64
	TotalDurationNs int64
	Running         int64
	QueueWaits      int64
	QueueWaitTotalNs int64
	AvgDurationMs   float64
	AvgQueueWaitMs  float64
}

// Snapshot 获取指标快照（原子读取，保证一致性）
func (m *ProcMetrics) Snapshot() ProcMetricsSnapshot {
	s := ProcMetricsSnapshot{
		Starts:          atomic.LoadInt64(&m.starts),
		Success:         atomic.LoadInt64(&m.success),
		Failures:        atomic.LoadInt64(&m.failures),
		Timeouts:        atomic.LoadInt64(&m.timeouts),
		Sigkills:        atomic.LoadInt64(&m.sigkills),
		TotalDurationNs: atomic.LoadInt64(&m.totalDurationNs),
		Running:         atomic.LoadInt64(&m.running),
		QueueWaits:      atomic.LoadInt64(&m.queueWaits),
		QueueWaitTotalNs: atomic.LoadInt64(&m.queueWaitTotalNs),
	}
	total := s.Success + s.Failures + s.Timeouts
	if total > 0 {
		s.AvgDurationMs = float64(s.TotalDurationNs) / float64(total) / float64(time.Millisecond)
	}
	if s.QueueWaits > 0 {
		s.AvgQueueWaitMs = float64(s.QueueWaitTotalNs) / float64(s.QueueWaits) / float64(time.Millisecond)
	}
	return s
}

// GetProcMetrics 获取全局子进程指标
func GetProcMetrics() *ProcMetrics {
	return &globalProcMetrics
}

// globalMetricsOnce 保证全局指标只初始化一次
var globalMetricsOnce sync.Once

// InitProcMetrics 初始化子进程监控指标（可选调用，不调用也不影响功能）
func InitProcMetrics() {
	globalMetricsOnce.Do(func() {
		// 指标已在包级变量初始化，这里仅确保 Once 执行
	})
}
