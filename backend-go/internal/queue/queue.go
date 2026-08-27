// ============ 本文件职责中文说明 ============
// 异步任务队列接缝：业务代码只接触 Enqueue（入队）与 worker 循环两个口子。
// 当前唯一实现为 direct（进程内执行器，jobs 表持久化+租约超时回收+重试+死信）；
// 未来触发条件成立（编排器独立集群/万级日单量/多消费组）时，
// 新增 kafka driver 文件实现同一接口即可接入，业务逻辑零改动。
// =============================================
// Package queue 提供异步任务队列接缝：定义 Queue 接口与 Job 结构，当前由 direct
// 实现（进程内 SQLite 持久化+租约超时回收+重试+死信），未来可新增 kafka 驱动零改业务。
package queue

import (
	"context"
	"encoding/json"
	"time"
)

// Job 异步任务（jobs 表一行）。
type Job struct {
	ID          int64           `json:"id"`           // 任务主键
	Type        string          `json:"type"`         // 任务类型（如 ticket_run）
	Payload     json.RawMessage `json:"payload"`      // JSON 载荷（如 {"ticket_id":1}）
	Status      string          `json:"status"`       // queued/running/done/failed/dead
	Attempts    int             `json:"attempts"`     // 已尝试次数
	MaxAttempts int             `json:"max_attempts"` // 最大尝试次数
	Error       string          `json:"error"`        // 最近失败原因
}

// Queue 异步任务队列接缝。direct 与未来的 kafka 驱动实现同一接口。
type Queue interface {
	// Enqueue 入队一个任务（落账本 status=queued）。
	Enqueue(ctx context.Context, jobType string, payload []byte, maxAttempts int) (int64, error)
	// Reserve 领取下一个可执行任务（原子：queued 或 租约过期的 running → running）；
	// 无可执行任务返回 (nil, nil)。leaseSec=租约时长，超时未 Ack 将被其他 worker 回收。
	Reserve(ctx context.Context, workerID string, leaseSec int) (*Job, error)
	// MarkDone 标记完成。
	MarkDone(ctx context.Context, jobID int64) error
	// MarkFailed 标记失败：attempts<max 时回 queued 延迟重试，否则置 dead（死信）。
	MarkFailed(ctx context.Context, jobID int64, errMsg string) error
	// RecoverStale 启动/巡检时回收中断任务：running 且租约过期 → queued。返回回收条数。
	RecoverStale(ctx context.Context) (int64, error)
}

// TicketPayload 工单翻译任务的载荷约定。
type TicketPayload struct {
	TicketID int64 `json:"ticket_id"`
}

// NewTicketPayload 构造工单任务载荷。
func NewTicketPayload(ticketID int64) []byte {
	b, _ := json.Marshal(TicketPayload{TicketID: ticketID})
	return b
}

// ParseTicketPayload 解析工单任务载荷。
func ParseTicketPayload(b []byte) (int64, error) {
	var p TicketPayload
	if err := json.Unmarshal(b, &p); err != nil {
		return 0, err
	}
	return p.TicketID, nil
}

// 默认参数。
const (
	DefaultMaxAttempts = 3
	DefaultLeaseSec    = 1800 // 30 分钟租约：大文件翻译的可见性超时上限
)

// Now 时间源（测试可替换）。
var Now = time.Now
