// ============ bus.go · 职责说明 ============
// 二期 mq 核心层：进程内事件总线（照外仓 LogCenter 的 fan-out 语义，不引 kafka）。
// ★ 迁入即换：① 无全局单例、无 Init() 自动启动、无启动即跑，订阅者一律由组合根显式注册；
// ② 日志全走 observability（零裸日志）；③ handler panic 逐个隔离并出声，不许炸发布方；
// ④ 外仓原「异步 goroutine＋重试」在本层不做（组合根按 runExclusive 槽承担周期与多实例互斥），
// 进程内总线只保证「同步分发、错误可辨」，避免把可靠性语义藏进第二套调度里。
// 多实例语义：本总线只在单进程内有效，跨实例去重由 inbox_events 幂等键与调度层互斥锁承担。
// ================================================
package mq

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"translator/internal/observability"
)

// Bus 进程内事件总线：主题 → 订阅者列表，读写由 RWMutex 保护。
type Bus struct {
	mu       sync.RWMutex
	handlers map[string][]EventHandler
}

// NewBus 构造空总线；不启动任何协程、不注册任何消费者。
// 参数：无；返回：可注入的总线实例。
func NewBus() *Bus {
	return &Bus{handlers: make(map[string][]EventHandler)}
}

// Subscribe 登记某主题的一个订阅者（同主题可多方 fan-out）。
// 参数：topic=主题；h=回调；返回：入参不合法的错误（fail-closed，不许静默丢订阅）。
func (b *Bus) Subscribe(topic string, h EventHandler) error {
	if b == nil {
		return errors.New("总线实例为空，无法订阅")
	}
	if strings.TrimSpace(topic) == "" {
		return errors.New("订阅缺少主题")
	}
	if h == nil {
		return errors.New("订阅缺少回调")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = make(map[string][]EventHandler)
	}
	b.handlers[topic] = append(b.handlers[topic], h)
	return nil
}

// SubscriberCount 返回某主题当前订阅者数量（供门禁与组合根自证射程）。
// 参数：topic=主题；返回：订阅者个数。
func (b *Bus) SubscriberCount(topic string) int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.handlers[topic])
}

// validateEnvelope 校验信封的最小必要字段；租户不明一律拒收。
// 参数：env=待投递信封；返回：校验错误。
func validateEnvelope(env Envelope) error {
	if env.Header.TenantID <= 0 {
		return errors.New("事件缺少有效租户（tid<=0 拒收）")
	}
	if strings.TrimSpace(env.Header.EventID) == "" {
		return errors.New("事件缺少幂等键")
	}
	if strings.TrimSpace(env.Topic) == "" {
		return errors.New("事件缺少主题")
	}
	return nil
}

// dispatchOne 在 recover 保护下调用单个订阅者，panic 转成可辨错误并出声。
// 参数：ctx=上下文；h=回调；env=信封副本；返回：回调错误或 panic 转换的错误。
func dispatchOne(ctx context.Context, h EventHandler, env Envelope) (err error) {
	defer func() {
		if r := recover(); r != nil {
			observability.Error(ctx, "消息总线订阅者 panic 已隔离", "event_id", env.Header.EventID, "topic", env.Topic, "panic", fmt.Sprint(r))
			err = fmt.Errorf("订阅者 panic: %v", r)
		}
	}()
	return h(ctx, env)
}

// Publish 同步分发一条信封到该主题全部订阅者。
// 无订阅者不报错但必须 Warn 出声（防止「腿没接上却看起来成功」）；
// 任一订阅者出错/panic 都汇总回错误，绝不静默吞掉。
// 参数：ctx=上下文；env=已构造好的信封；返回：分发错误。
func (b *Bus) Publish(ctx context.Context, env Envelope) error {
	if b == nil {
		return errors.New("总线实例为空，无法发布")
	}
	if err := validateEnvelope(env); err != nil {
		return err
	}
	b.mu.RLock()
	hs := append([]EventHandler(nil), b.handlers[env.Topic]...)
	b.mu.RUnlock()
	if len(hs) == 0 {
		observability.Warn(ctx, "消息总线无订阅者，事件仅在进程内出声不投递", "topic", env.Topic, "event_id", env.Header.EventID, "tenant_id", env.Header.TenantID)
		return nil
	}
	var failed []string
	for i, h := range hs {
		if err := dispatchOne(ctx, h, env); err != nil {
			observability.Error(ctx, "消息总线订阅者处理失败", "topic", env.Topic, "event_id", env.Header.EventID, "handler_index", i, "error", err.Error())
			failed = append(failed, fmt.Sprintf("#%d: %v", i, err))
		}
	}
	if len(failed) > 0 {
		return fmt.Errorf("消息总线分发有 %d 个订阅者失败: %s", len(failed), strings.Join(failed, "；"))
	}
	observability.Info(ctx, "消息总线分发完成", "topic", env.Topic, "event_id", env.Header.EventID, "handlers", len(hs))
	return nil
}
