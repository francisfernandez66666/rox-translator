// ============ chat_seg_plan.go · 职责说明 ============
// ★ DEF-P1-05 B2（2026-11-01）：即时翻译「走不走分段」这一决定的**唯一挂载口**。
//
// 为什么判定要放在 api 侧算、再挂进 ctx，而不是让引擎自己读配置：
//
//	分段成本上限吃的是**客户实付积分**，而 token→积分要过模式倍率（modeMarkup，租户策略＋模式因子），
//	那个解析口只在 api 存在。引擎要是自己再算一遍倍率，就是本仓反复登记的「多把尺子」
//	（AGENTS §一·5④／§一·11／§23）——最坏的形态不是报错，而是**横幅按分段报价、执行按整篇跑**，
//	客户按预估备的钱不够花。
//	⇒ 所以：预估侧与执行侧调的是**同一个** engine.DecideChatSegments，输入也一样（同一个 markup 口）。
//
// ⚠️ 没有这个标记 ⇒ 引擎按整篇旧路径走（不分段）。这是刻意的 fail-closed：
//
//	漏挂的后果是"能力没生效"，而不是"按错误的价格跑了分段"。
//	哪一条腿该挂、哪一条腿点名不挂，由 chat_seg_plan_gate_test.go 按**调用点全集**机械判。
//
// =============================================
package api

import (
	"context"

	"translator/internal/engine"
	"translator/internal/tenant"
)

// chatEstimateAndPlan 预估与分段判定的**唯一现场**。
// 三个消费者必须都走这里（少一个就是"两把尺子"的起点）：
//
//	① 预估横幅 handleTranslationEstimate／estimateTokens（客户看到的区间）；
//	② 免费档计量 instantDecide／gateUsageInstant（平台按预估给匿名与免费档记账）；
//	③ 进引擎的 ctx 标记 chatSegPlanCtx（真正决定这一单跑不跑分段）。
//
// 返回 (计划, 预估下限 token, 预估上限 token)——token 都是**未乘模式倍率**的真实口径，
// 与 EstimateChatTokens/EstimateParagraphTokens 同一把尺子；调用方要出"实收"自己过 ApplyMarkup。
func (s *Server) chatEstimateAndPlan(tid int64, text string, langs []string, mode, srcLang, uiLang string) (engine.ChatSegPlan, int64, int64) {
	segs := engine.SplitParagraphs(text)
	wMin, wMax := engine.EstimateChatTokens(text, langs, mode, srcLang, uiLang)
	enabled, maxPoints := s.chatSegTiers()
	plan := engine.ChatSegPlan{Paragraphs: segs, MinTokens: wMin, MaxTokens: wMax, MaxPoints: maxPoints}
	if !enabled {
		plan.Reason = "off"
		return plan, wMin, wMax
	}
	if len(segs) < engine.ChatSegmentMinParagraphs {
		plan.Reason = "single_paragraph"
		return plan, wMin, wMax
	}
	pMin, pMax := engine.EstimateParagraphTokens(text, langs, mode, srcLang, uiLang)
	markup := modeMarkup(s.effectivePolicyCached(tid), mode)
	if maxPoints > 0 {
		// 换算只走 store 那一个口（积分↔token 不许在别处再写一遍除法式）
		pts := s.Store.PointsFromTokens(engine.ApplyMarkup(pMax, markup))
		if pts > maxPoints {
			// 回落是**成本保护**，不是故障：不报错、不加钱、照常按整篇交付（口径 B）。
			plan.Reason = "whole_text_fallback:points_cap"
			return plan, wMin, wMax
		}
	}
	plan.Use = true
	plan.Reason = "paragraph"
	plan.MinTokens, plan.MaxTokens = pMin, pMax
	return plan, pMin, pMax
}

// chatSegTiers 两档运行期配置（唯一读点）。
// ⚠️ 开关必须走 ConfigIntAllowZero：ConfigInt 的「≤0 视为未配置」会把"关"这一档读回默认 1，
//
//	那条回退路径就按不下去了（工单侧 text_ticket_per_line 同款坑，注释写在那里）。
func (s *Server) chatSegTiers() (enabled bool, maxPoints int64) {
	// 无库形态（工具/挂件实例）也按**同一组常量**起步，不在这个分支里另写一遍 1/60
	enabled, maxPoints = engine.ChatPerParagraphDefault != 0, int64(engine.ChatSegmentMaxPtsDefault)
	if s.Store == nil {
		return enabled, maxPoints
	}
	enabled = s.Store.ConfigIntAllowZero(engine.CfgKeyChatPerParagraph, engine.ChatPerParagraphDefault) != 0
	// ⚠️ 上限这一档**也必须**走 ConfigIntAllowZero：ConfigInt 把 ≤0 一律读成「库里没表态」而回默认值，
	//   于是运营写 -5 想表达"不设上限"会被静默成默认 60——**同一把钳位尺子不许一个键走 AllowZero、
	//   另一个键走 ConfigInt**（0 与"没配"在库里是两种形态，这条与 §19 九-4 那条同源）。
	maxPoints = int64(s.Store.ConfigIntAllowZero(engine.CfgKeyChatSegmentMaxPts, engine.ChatSegmentMaxPtsDefault))
	if maxPoints < engine.ChatSegmentMaxPtsLo {
		maxPoints = engine.ChatSegmentMaxPtsLo
	}
	if maxPoints > engine.ChatSegmentMaxPtsHi {
		maxPoints = engine.ChatSegmentMaxPtsHi
	}
	return enabled, maxPoints
}

// chatSegPlanCtx 算好分段计划并挂进 ctx（进引擎前唯一挂载口）。
func (s *Server) chatSegPlanCtx(ctx context.Context, text string, options map[string]interface{}) context.Context {
	if s.Store == nil {
		return ctx // 无库形态（工具/挂件实例）：不猜档位，按整篇旧路径
	}
	plan, _, _ := s.chatEstimateAndPlan(tenant.FromContext(ctx), text,
		engine.TargetLangsFromOptions(options), normalizeTaskMode(engine.ModeFromOptions(options)),
		func() string { sl, _ := options["source_lang"].(string); return sl }(),
		tenant.LangFromOptions(options))
	return engine.WithChatSegPlan(ctx, plan)
}
