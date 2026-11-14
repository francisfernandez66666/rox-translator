// ============ relay.go · 职责说明 ============
// W4 relay：由 watchdog 槽 runExclusive('outbox-relay', 5s, ...) 每 5 秒拨一轮（周期与多实例互斥
// 归组合根/watchdog，本包不启协程、不注册全局单例）。
//
// ★ 硬口径 4「relay 只做投递、不做业务」：RelayOnce 只做三件传输动作——
//
//	① 扫 event_outbox 的 pending 行搬运进 queue 账本（Enqueue）；
//	② 用 queue.Reserve 领一条投递任务，把信封交给 Deliverer（默认 mq.Bus.Publish）；
//	③ 成功 queue.MarkDone / 失败 queue.MarkFailed。
//	relay **绝不**写 event_inbox、绝不写读模型、绝不发通知——那全是 consumer 的副作用。
//
// ★ 硬口径 4「失败重试与死信复用 queue 的 Reserve/MarkFailed，不新造第二套调度」：
//
//	本文件零 attempts 计数、零死信表、零自写重试循环；重试与死信 100% 由
//	queue.MarkFailed 的 CASE WHEN 语义承担（见 internal/queue/direct.go:MarkFailed）。
//
// ★ E6「关 relay 后 outbox 只积压不丢」：enabled=false 时 RelayOnce 立即返回，
//
//	pending 行既不 Enqueue、也不标记、更不删除，原地积压。
//
// ================================================
package outbox

import (
	"context"
	"database/sql"
	"fmt"
	"sync/atomic"

	"translator/internal/db"
	"translator/internal/mq"
	"translator/internal/observability"
)

// Relay outbox 投递器。conn=outbox 表所在库；q=投递账本（真实 queue.Queue 直接满足 DeliveryQueue）；
// deliver=投递动作；enabled=开关（atomic，SetEnabled 可在运行期拨）。
type Relay struct {
	conn    *sql.DB
	d       db.Dialect
	q       DeliveryQueue
	deliver Deliverer
	enabled atomic.Bool
}

// NewRelay 构造投递器；enabled 传 false 即「关闭态」（只积压不消费，E6）。
// 参数：conn=连接；d=方言；q=投递账本；deliver=投递动作；enabled=初始开关；返回：投递器。
func NewRelay(conn *sql.DB, d db.Dialect, q DeliveryQueue, deliver Deliverer, enabled bool) *Relay {
	r := &Relay{conn: conn, d: d, q: q, deliver: deliver}
	r.enabled.Store(enabled)
	return r
}

// SetEnabled 运行期拨开关（组合根按 kill-switch 调；测试用它验「关闭→重开」）。
func (r *Relay) SetEnabled(v bool) { r.enabled.Store(v) }

// Enabled 读当前开关。
func (r *Relay) Enabled() bool { return r.enabled.Load() }

// RelayOnce 单轮投递（watchdog 槽的 fn 调它）。关闭态立即返回，pending 行原样积压。
// 参数：ctx=上下文；返回：本轮错误（搬运/领取/投递失败均上抛，不静默吞）。
func (r *Relay) RelayOnce(ctx context.Context) error {
	if !r.enabled.Load() {
		// ★ E6 / M0297 靶：关闭态「只积压不丢」。换成 DELETE FROM event_outbox ⇒ 行数归零 ⇒ 对应断言红。
		observability.Info(ctx, "outbox-relay 关闭，本轮跳过（只积压不消费）", "worker", relayWorkerID)
		return nil
	}
	if err := r.enqueuePending(ctx); err != nil {
		observability.Error(ctx, "outbox-relay 搬运待投递事件失败", "worker", relayWorkerID, "error", err.Error())
		return err
	}
	return r.deliverOne(ctx)
}

// enqueuePending 把 event_outbox 的 pending 行搬进 queue 账本，成功后仅把状态置 relayed（不删行）。
// 每行 Enqueue 成功才标记；崩溃在两步之间 ⇒ 下轮最多重复 Enqueue 一次，由 consumer 的
// (topic,event_id) 唯一键兜住「至少一次投递 + 幂等消费」，不产生重复副作用。
func (r *Relay) enqueuePending(ctx context.Context) error {
	rows, err := db.Query(r.conn, r.d,
		"SELECT topic,event_id,tenant_id,one_id,trace_id,payload FROM event_outbox WHERE status=? ORDER BY id",
		StatusPending)
	if err != nil {
		return fmt.Errorf("扫描 event_outbox 待投递行失败: %w", err)
	}
	type pending struct {
		topic, eventID, oneID, traceID, payload string
		tenantID                                int64
	}
	var list []pending
	for rows.Next() {
		var p pending
		if err := rows.Scan(&p.topic, &p.eventID, &p.tenantID, &p.oneID, &p.traceID, &p.payload); err != nil {
			rows.Close()
			return fmt.Errorf("读 event_outbox 行失败: %w", err)
		}
		list = append(list, p)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close() // 单连接 SQLite 下必须先收完结果集再 UPDATE，避免撞写锁
	for _, p := range list {
		// Payload 即完整 mq.Envelope JSON，直接作为投递任务载荷（trace/tenant/event_id 都在头里）。
		if _, err := r.q.Enqueue(ctx, JobTypeOutboxDelivery, []byte(p.payload), DeliveryMaxAttempts); err != nil {
			return fmt.Errorf("搬运事件 %s/%s 入队失败: %w", p.topic, p.eventID, err)
		}
		// 不带尾随 LIMIT（AGENTS §一·4）：本 UPDATE 无需分批，直接按唯一键精确定位。
		if _, err := db.Exec(r.conn, r.d,
			"UPDATE event_outbox SET status=? WHERE topic=? AND event_id=? AND status=?",
			StatusRelayed, p.topic, p.eventID, StatusPending); err != nil {
			return fmt.Errorf("标记事件 %s/%s 已搬运失败: %w", p.topic, p.eventID, err)
		}
		observability.Info(ctx, "outbox-relay 已搬运事件", "topic", p.topic, "event_id", p.eventID, "trace_id", p.traceID)
	}
	return nil
}

// deliverOne 领取一条投递任务并交给 Deliverer；成功/失败全交给 queue 的 MarkDone/MarkFailed。
func (r *Relay) deliverOne(ctx context.Context) error {
	job, err := r.q.Reserve(ctx, relayWorkerID, DeliveryLeaseSec) // 复用 queue.Reserve，不自造领取
	if err != nil {
		return fmt.Errorf("领取投递任务失败: %w", err)
	}
	if job == nil {
		return nil // 空轮：无可执行任务
	}
	env, uerr := mq.UnmarshalEnvelope(job.Payload)
	if uerr != nil {
		// 坏载荷：交回 queue 判死信（防毒丸），绝不静默吞。
		_ = r.q.MarkFailed(ctx, job.ID, "投递载荷反序列化失败: "+uerr.Error())
		observability.Error(ctx, "outbox-relay 坏载荷转死信", "job", job.ID, "error", uerr.Error())
		return uerr
	}
	if derr := r.deliver(ctx, env); derr != nil {
		// ★ E2 / M0293 靶：失败必须交回 queue 重试/死信，不能 MarkDone。
		_ = r.q.MarkFailed(ctx, job.ID, derr.Error())
		observability.Warn(ctx, "outbox-relay 投递失败（交回 queue 重试/死信）",
			"job", job.ID, "event_id", env.Header.EventID, "error", derr.Error())
		return derr
	}
	if err := r.q.MarkDone(ctx, job.ID); err != nil {
		return fmt.Errorf("投递任务标记完成失败: %w", err)
	}
	observability.Info(ctx, "outbox-relay 投递完成", "job", job.ID, "event_id", env.Header.EventID, "topic", env.Topic)
	return nil
}
