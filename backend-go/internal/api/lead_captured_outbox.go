// ============ lead_captured_outbox.go · 职责说明 ============
// 四期 W4 · lead.captured 的生产者适配层（只新建，不改任何现有 lead 文件）。
//
// 留资写点在**业务事务内**调 EmitLeadCapturedInTx(tx, ...) 或 WriteLeadCaptured(...)，
// 即可把「INSERT 业务行 + INSERT event_outbox(topic='lead.captured')」绑进同一事务（硬口径 1）。
// 本文件是 W2 留资落库点与 outbox 包之间的唯一适配缝：W2 agent 正在做「留资进收件箱」，
// 可能与本生产者同区域 —— 故刻意不 import 也不修改任何现有 lead 文件，挂点统一写
// /tmp/w4_wiring.md 由主代理落。
//
// 信封必带 tenant_id（硬口径 3）：构造走 mq.NewEnvelope（tid<=0 拒收），落库再经
// outbox.InsertOutboxEvent 的 mq.UnmarshalEnvelope 复核，两道 fail-closed。
// ================================================
package api

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"translator/internal/db"
	"translator/internal/mq"
	"translator/internal/outbox"
)

// LeadCapturedParams 是一次留资落库要带入事件的派生字段（只承载读模型/通知，不碰钱与接待主链）。
type LeadCapturedParams struct {
	TenantID int64  // 必带，tid<=0 拒收
	OneID    string // 用户主键（匿名可空）
	EventID  string // 幂等键（建议与业务行主键绑定，如 "lead_<feedbackID>"；空则由 mq.NewEventID 生成）
	TraceID  string // 链路号，贯穿 outbox → consumer（E5）
	Company  string
	Email    string
	Langs    string
	Source   string
}

// newLeadCapturedEvent 把留资派生字段封装成 outbox.StoredEvent（Payload 为完整 mq.Envelope JSON）。
// 参数：p=留资派生字段；返回：事件行与错误。
func newLeadCapturedEvent(p LeadCapturedParams) (outbox.StoredEvent, error) {
	eid := strings.TrimSpace(p.EventID)
	if eid == "" {
		eid = mq.NewEventID()
	}
	payload := map[string]any{
		"company": p.Company,
		"email":   p.Email,
		"langs":   p.Langs,
		"source":  p.Source,
		"one_id":  p.OneID,
	}
	env, err := mq.NewEnvelope(outbox.TopicLeadCaptured, p.TenantID, p.OneID, eid, p.TraceID, payload)
	if err != nil {
		return outbox.StoredEvent{}, fmt.Errorf("构造 lead.captured 信封失败: %w", err)
	}
	raw, err := env.Marshal()
	if err != nil {
		return outbox.StoredEvent{}, fmt.Errorf("序列化 lead.captured 信封失败: %w", err)
	}
	return outbox.StoredEvent{
		Topic:    outbox.TopicLeadCaptured,
		EventID:  eid,
		TenantID: p.TenantID,
		OneID:    p.OneID,
		TraceID:  p.TraceID,
		Payload:  raw,
	}, nil
}

// EmitLeadCapturedInTx 在**调用方已开启的业务事务**内写一条 lead.captured 到 event_outbox。
// 业务行 INSERT 与本函数必须共用同一个 tx（同事务原子，E1）；本函数不 Commit/Rollback。
// 参数：tx=业务事务（*sql.Tx 满足 db.Execer）；d=方言；p=留资派生字段；返回：写入错误。
func EmitLeadCapturedInTx(tx db.Execer, d db.Dialect, p LeadCapturedParams) error {
	ev, err := newLeadCapturedEvent(p)
	if err != nil {
		return err
	}
	return outbox.InsertOutboxEvent(tx, d, ev)
}

// WriteLeadCaptured 给尚无事务的写点用：开一个事务，先跑 business 业务写（收 *sql.Tx），
// 再在同一事务写 lead.captured，最后 Commit。任一环节失败 ⇒ 业务行与事件行都不落库。
// 参数：ctx=上下文；conn=连接；d=方言；business=业务写回调；p=留资派生字段；返回：错误。
func WriteLeadCaptured(ctx context.Context, conn *sql.DB, d db.Dialect, business func(*sql.Tx) error, p LeadCapturedParams) error {
	ev, err := newLeadCapturedEvent(p)
	if err != nil {
		return err
	}
	return outbox.WritePoint(ctx, conn, d, business, ev)
}
