// ============ sales_lead_inbox_bridge.go · 职责说明 ============
// 四期 W2（施工图 §4.2.2 第 3 条）：把**公开页留资**补进销售收件箱。
//
// 缺陷形态：/api/lead/public 落库只写 feedbacks（handleLeadCreatePublic 的 CreateFeedback），
// 而销售收件箱读口（sales_inbox_api.go → Store.GetInboxEvent / sales_crm_api.go →
// Store.ListInboxEvents）只读 inbox_events。两表分离 ⇒ 线索落了库，卖家在收件箱
// 却永远看不到这条，界面零症状（AGENTS §三「有脚本无调度」的同族）。
//
// 修法（W4 选 lead.captured 作首个消费者的真实需求，不是演示）：留资落库成功后，
// 以 topic="lead.captured" 往 inbox_events 幂等补一条事件；event_id 带
// "lead-product:" 前缀（来自公开页 <form> 的 hidden source），one_id 用刚落库的
// feedbacks.id。收件箱按 tenant_id 隔离，故 tid 必须是产品所属租户（>0），
// 绝不能把匿名 feedbacks 行的 TenantID=0 透传进来。
//
// ★ 三条硬口径：
//
//	① 幂等：复用 Store.TryProcessInboxEvent（INSERT … WHERE NOT EXISTS + 唯一索引），
//	   不在本文件再造第二把查重尺子；重复投递/并发败方自然落空，不报错。
//	② 租户隔离：tid 由调用点按 tenant_code 解析出真实租户传入；写错/摘掉 tenant_id
//	   ⇒ 卖家收件箱查不到，R4 门禁（sales_inbox_lead_gate_test.go）当场判红。
//	③ fail-open：收件箱是旁路能力，写失败只留 observability 告警，绝不阻断已落库的
//	   留资主链（与 lead_public.go 蜜罐/升级信号「不拿可用性交押金」同口径）。
//
// ★ 挂点不在本文件：见 /tmp/w2_wiring.md（handleLeadCreatePublic 落库成功之后）。
// ================================================
package api

import (
	"context"
	"fmt"
	"strings"

	"translator/internal/observability"
)

const (
	// leadCapturedTopic W4 首个消费者事件：公开页留资（对外排障契约，消费方按它订阅）。
	leadCapturedTopic = "lead.captured"
	// leadSourceProductPrefix 公开页 <form> hidden source 的前缀（ProductPublicPage.tsx 的 lead-product:{tc}:{pc}）。
	leadSourceProductPrefix = "lead-product:"
)

// LeadCapturedInboxEventID 由留资来源串与反馈行 id 拼出收件箱幂等键。
// 前缀让收件箱读口看得出「这条来自产品页留资」；反馈行 id 保证每条留资一个键、可重放。
// 参数：leadSource=公开页 hidden source（含 lead-product: 前缀）；feedbackID=刚落库的 feedbacks.id；返回幂等键。
func LeadCapturedInboxEventID(leadSource string, feedbackID int64) string {
	return fmt.Sprintf("%s#%d", strings.TrimSpace(leadSource), feedbackID)
}

// bridgeLeadCapturedToInbox 公开页留资落库成功后，把同一条线索以 lead.captured 事件补进
// 幂等收件箱（inbox_events），否则卖家在收件箱读口永远看不到这条留资。
// 参数：ctx=请求上下文；tid=产品所属租户（>0，收件箱按它隔离）；
// eventID=含 lead_source 前缀的幂等键（LeadCapturedInboxEventID 产物）；oneID=线索主键（feedbacks.id 字符串）。
// 返回：写库错误（本函数已记 observability，不阻断留资主链；供调用方决定是否再告警）。
func (s *Server) bridgeLeadCapturedToInbox(ctx context.Context, tid int64, eventID, oneID string) error {
	if s == nil || s.Store == nil {
		return nil // 无库：不阻断留资主链（fail-open）
	}
	if _, err := s.Store.TryProcessInboxEvent(tid, eventID, oneID, leadCapturedTopic); err != nil {
		observability.Warn(ctx, "公开页留资补进销售收件箱失败（不阻断留资主链）",
			"event_id", eventID, "tenant_id", tid, "error", err)
		return err
	}
	return nil
}
