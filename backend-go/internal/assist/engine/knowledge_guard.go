// ============ knowledge_guard.go · 职责说明 ============
// 一期《确定的多语言能力》§7.1 出站咽喉上的知识层答案守卫。
//
// ★ 判据**不在这里实现**：唯一真值在 internal/engine/knowledge_guard.go 的
//
//	GuardKnowledgeAnswer（导出纯函数），api 的 /api/product/answer 与这里的出站链共用它。
//	本文件只是 ctx → Reply 的薄适配，禁止在本包再写一份数字/认证词词表。
//
// ★ 默认零改动：ctx 未注入 WithKnowledgeFacts 时原样返回 rep。
//
//	所有既有客服/翻译链（含全租户术语命中）行为一字不变；
//	一期知识层问答由 api 侧在调用前显式注入素材。
//
// ★ 挂点（文档 §7.1 全篇统一）：在 guardReplyBrand 之后、applyRechargeIntent 之前——
//
//	答案必须先过反编造/报价/品牌三道，再谈充值意图与按钮本地化；
//	且不许跨过 observeVisitorAsides（它只读不改，位置仍在 localizeActionNames 之后）。
//
// ================================================
package engine

import (
	"context"

	coreengine "translator/internal/engine"
	"translator/internal/observability"
)

// guardKnowledgeAnswer 校验知识层答案的无来源数字／认证词，整句剥除并回退已备物料原文。
// 参数 ctx=请求上下文（可携带校验素材）；answerLang=本轮作答语言；rep=出站回复；返回处理后的回复。
func (e *Engine) guardKnowledgeAnswer(ctx context.Context, answerLang string, rep *Reply) *Reply {
	if rep == nil || rep.Content == "" {
		return rep
	}
	facts, fallback, ok := coreengine.KnowledgeFactsFromContext(ctx)
	if !ok {
		return rep // 非知识层答案：零改动放行
	}
	out, reason := coreengine.GuardKnowledgeAnswer(rep.Content, facts, fallback)
	if reason == coreengine.KnowReasonOK {
		return rep
	}
	observability.Warn(ctx, "assist.engine 知识层答案已按事实来源剥句",
		"lang", canonicalLang(answerLang), "reason", reason, "source", rep.Source,
		"before", firstRunes(rep.Content, 200))
	rep.Content = out
	return rep
}
