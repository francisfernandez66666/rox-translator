// ============ 本文件职责中文说明 ============
// Redis 队列唤醒信号器实现：基于 Redis 列表的跨实例信号分发。
// Signal 通过 RPUSH 写入列表；Wait 通过 BLPOP 阻塞读取，超时返回。
// =============================================
package redis

import (
	"context"
	"time"

	"translator/internal/queue"
)

// NewQueueNotifier 创建基于 Redis 的队列唤醒信号器。
func NewQueueNotifier(r *Client) queue.Notifier {
	return &redisNotifier{client: r}
}

type redisNotifier struct {
	client *Client
}

// Signal 发布一个唤醒事件（RPUSH 到通知列表）。
func (n *redisNotifier) Signal(ctx context.Context) error {
	return n.client.RPush(ctx, "wake", "1")
}

// Wait 阻塞等待下一个唤醒事件（BLPOP 超时返回）。
func (n *redisNotifier) Wait(ctx context.Context, d time.Duration) error {
	_, err := n.client.BLPop(ctx, "wake", d)
	return err
}