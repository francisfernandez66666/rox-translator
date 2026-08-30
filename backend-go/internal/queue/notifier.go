// ============ 本文件职责中文说明 ============
// 队列唤醒信号器接口：多 AZ worker 跨实例唤醒。
// Signal 发布一个唤醒事件；Wait 阻塞等待下一个唤醒事件（超时返回）。
// Redis 启用时由 redis.NewQueueNotifier 实现；nil=轮询降级。
// =============================================
package queue

import (
	"context"
	"time"
)

// Notifier 队列唤醒信号器。Signal 发布唤醒，Wait 阻塞等待下一个唤醒事件。
type Notifier interface {
	// Signal 发布一个唤醒事件，唤醒一个等待中的 Wait 调用。
	Signal(ctx context.Context) error
	// Wait 阻塞等待下一个唤醒事件，最长等待 d 时间后返回。
	Wait(ctx context.Context, d time.Duration) error
}