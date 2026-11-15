// ============ sales_lead_inbox_bridge.go · 职责说明 ============
// 四期 W2 / W4 收口：把**公开页留资**补进销售收件箱，并在同一事务写 event_outbox。
//
// 缺陷形态（W2）：/api/lead/public 落库只写 feedbacks，而销售收件箱读口只读
// inbox_events ⇒ 线索落了库，卖家在收件箱永远看不到（界面零症状）。
//
// ★ W4 收口（2026-11-15，审计 §3.4）：旧形态 bridge 直接写 inbox_events，
//
//	  `WriteLeadCaptured` / `EmitLeadCapturedInTx` **零调用者** ⇒ event_outbox 恒空，
//	  而 E1–E6 全在测框架台架。现在改成：
//
//		一个业务事务内 = INSERT inbox_events（原旁路）+ INSERT event_outbox（新事件行），
//		成功路径严格同事务（反证：把 outbox 写挪到事务外 ⇒ 回滚后多一行 ⇒ 闸门红）。
//
// ★ 三条硬口径（一条都不能丢）：
//
//	① 幂等：复用 Store.TryProcessInboxEventTx（与 TryProcessInboxEvent 同一条 SQL），
//	   重复投递/并发败方返回 sentinel，本函数既不写 outbox 也不告警（静默跳过是正确语义）。
//	② 租户隔离：tid 由调用点按 tenant_code 解析出真实租户传入（绝不用匿名 feedbacks 的 0）。
//	③ fail-open：收件箱是旁路能力。双写失败 ⇒ 事务整体回滚，此时**必须补偿一次
//	   单写 inbox**，否则新加的事件行反而把「卖家看得见线索」这条旧能力打没了
//	   （AGENTS §一·10「新增防护不拿可用性交押金」）。
//
// 挂点：product_public_api.go 的 handleProductLead（留资落库成功之后）。
// ================================================
package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"translator/internal/db"
	"translator/internal/observability"
)

const (
	// leadCapturedTopic W4 首个消费者事件：公开页留资（对外排障契约，消费方按它订阅）。
	leadCapturedTopic = "lead.captured"
	// leadSourceProductPrefix 公开页 <form> hidden source 的前缀（ProductPublicPage.tsx 的 lead-product:{tc}:{pc}）。
	leadSourceProductPrefix = "lead-product:"
)

// errLeadAlreadyProcessed 幂等重放哨兵：business 回调抢账 0 行时返回，
// 让 WriteLeadCaptured 不写 outbox 并回滚空事务，由本函数识别为「正常跳过」。
var errLeadAlreadyProcessed = errors.New("lead.captured 已被处理（幂等跳过）")

// LeadCapturedInboxEventID 由留资来源串与反馈行 id 拼出收件箱幂等键。
// 前缀让收件箱读口看得出「这条来自产品页留资」；反馈行 id 保证每条留资一个键、可重放。
// 参数：leadSource=公开页 hidden source（含 lead-product: 前缀）；feedbackID=刚落库的 feedbacks.id；返回幂等键。
func LeadCapturedInboxEventID(leadSource string, feedbackID int64) string {
	return fmt.Sprintf("%s#%d", strings.TrimSpace(leadSource), feedbackID)
}

// bridgeLeadCapturedToInbox 公开页留资落库成功后，把同一条线索**同事务**写进
// 幂等收件箱（inbox_events）与事件表（event_outbox）。
// 参数：ctx=请求上下文；tid=产品所属租户（>0，收件箱按它隔离）；
// eventID=含 lead_source 前缀的幂等键（LeadCapturedInboxEventID 产物）；oneID=线索主键（feedbacks.id 字符串）。
// 返回：写库错误（本函数已记 observability，不阻断留资主链；供调用方决定是否再告警）。
func (s *Server) bridgeLeadCapturedToInbox(ctx context.Context, tid int64, eventID, oneID string) error {
	if s == nil || s.Store == nil {
		return nil // 无库：不阻断留资主链（fail-open）
	}
	if tid <= 0 {
		// 租户号无效时信封会被 mq.NewEnvelope 拒收；此处直接跳过并留痕，
		// 绝不让「事件化」这条旁路把已落库的留资主链打成错误。
		observability.Warn(ctx, "公开页留资事件化跳过：租户号无效",
			"event_id", eventID, "tenant_id", tid)
		return nil
	}
	params := LeadCapturedParams{
		TenantID: tid,
		OneID:    oneID,
		EventID:  eventID,
		// 请求级 trace 未在本层暴露；用幂等键派生稳定 trace，
		// 保证 outbox → relay → consumer 四环能按同一串追溯（E5）。
		TraceID: "lead:" + eventID,
		Source:  leadSourceProductPrefix,
	}
	err := WriteLeadCaptured(ctx, s.Store.DB(), db.CurrentDialect(), func(tx *sql.Tx) error {
		won, e := s.Store.TryProcessInboxEventTx(tx, tid, eventID, oneID, leadCapturedTopic)
		if e != nil {
			return e
		}
		if !won {
			return errLeadAlreadyProcessed
		}
		return nil
	}, params)
	if errors.Is(err, errLeadAlreadyProcessed) {
		return nil // 幂等重放：两表都不该多一行
	}
	if err == nil {
		return nil
	}
	// ★ fail-open 补偿：双写事务已整体回滚，必须把「原实现唯一做的那件事」补回来。
	observability.Warn(ctx, "公开页留资双写失败，降级为仅写收件箱（不阻断留资主链）",
		"event_id", eventID, "tenant_id", tid, "error", err)
	if _, e2 := s.Store.TryProcessInboxEvent(tid, eventID, oneID, leadCapturedTopic); e2 != nil {
		observability.Warn(ctx, "公开页留资补进销售收件箱失败（补偿写也失败）",
			"event_id", eventID, "tenant_id", tid, "error", e2)
	}
	return err
}
