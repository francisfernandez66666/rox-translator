// ============ 本文件职责中文说明 ============
// S8 敏感词兑底闸 · engine 挂接层（词包/检测器见 internal/sensitive）：
//   - 输入侧：文本通道整段拒译；文件通道**命中段不进模型**（上游零暴露），
//     交付物该段位置写占位符，其余段照常翻译；
//   - 输出侧：模型产出再检一遍（兑底），命中同样替换占位符；
//   - 留痕：LogAudit(sensitive_block/sensitive_output) + CreateAlert（去重由告警侧）。
//
// 开关：engine.Sensitive 装载词包 且 system_config sensitive_gate_enabled != "0"。
package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// SensitivePlaceholderText 命中段交付占位符（各语言统一，审计话术见下）。
const SensitivePlaceholderText = "[已拦截·REDACTED]"

// sensitiveOn 闸门是否生效：装载了非空词包 且 运营开关未显式关闭。
func (e *Engine) sensitiveOn() bool {
	if e.Sensitive == nil || e.Sensitive.Count() == 0 {
		return false
	}
	if e.St != nil {
		if v, _ := e.St.GetConfig("sensitive_gate_enabled"); v == "0" {
			return false
		}
	}
	return true
}

// sensitiveRecord 命中留痕：审计（含租户/通道/命中词/上下文摘要）+ 告警。
func (e *Engine) sensitiveRecord(ctx context.Context, channel string, hits []string, snippet string) {
	if e.St == nil {
		return
	}
	tid := e.tenantID(ctx)
	snip := snippet
	if len([]rune(snip)) > 80 {
		snip = string([]rune(snip)[:80])
	}
	e.St.LogAudit(tid, 0, "sensitive_block", channel,
		fmt.Sprintf("命中=%s 上下文=%q", strings.Join(hits, ","), snip))
	if err := e.St.CreateAlert(tid, "warn", "sensitive_block",
		fmt.Sprintf("[%s] 敏感词命中：%s（人工复核通道：审计详情/词包 %s）", channel, strings.Join(hits, ","), e.Sensitive.Path())); err != nil {
		log.Printf("[sensitive] 告警写入失败: %v", err)
	}
}

// sensitiveBlockedSegments 文件通道输入侧：挑出命中段（不送模型），
// 并为全部目标语言预填占位译文；有命中则留痕。返回命中段集合（可能为空）。
func (e *Engine) sensitiveBlockedSegments(ctx context.Context, texts []string, langs []string, addTrans func(lc, orig, tr string)) map[string]bool {
	blocked := map[string]bool{}
	if !e.sensitiveOn() {
		return blocked
	}
	var firstHits []string
	for _, t := range texts {
		if blocked[t] {
			continue
		}
		if hits := e.Sensitive.Hits(t); len(hits) > 0 {
			blocked[t] = true
			if firstHits == nil {
				firstHits = hits
			}
			for _, lc := range langs {
				addTrans(lc, t, SensitivePlaceholderText)
			}
		}
	}
	if len(blocked) > 0 {
		log.Printf("[sensitive] 文件通道拦截 %d 段（示例命中 %v）", len(blocked), firstHits)
		e.sensitiveRecord(ctx, "file_input", firstHits, "命中 "+fmt.Sprint(len(blocked))+" 段")
	}
	return blocked
}

// sensitiveSweepOutput 输出侧兑底：扫描各语言译文，命中段替换占位符并留痕。
// 返回被替换的段数。
func (e *Engine) sensitiveSweepOutput(ctx context.Context, langTranslations map[string]map[string]string) int {
	if !e.sensitiveOn() {
		return 0
	}
	n := 0
	var sample []string
	for lc, segs := range langTranslations {
		for orig, tr := range segs {
			if tr == SensitivePlaceholderText {
				continue
			}
			if hits := e.Sensitive.Hits(tr); len(hits) > 0 {
				segs[orig] = SensitivePlaceholderText
				if sample == nil {
					sample = hits
				}
				n++
			}
		}
		_ = lc
	}
	if n > 0 {
		log.Printf("[sensitive] 输出侧兑底替换 %d 段（示例命中 %v）", n, sample)
		e.sensitiveRecord(ctx, "output", sample, fmt.Sprintf("%d 段译文命中（模型自产内容）", n))
	}
	return n
}

// sensitiveTextGuard 文本/对话通道双向闸：
// guard 返回非空话术=整单拒绝（输入侧命中，不应再送模型）；
// post 对产出译文复核，命中则整体替换为拒付话术并留痕。
func (e *Engine) sensitiveTextGuardInput(ctx context.Context, text string) string {
	if !e.sensitiveOn() {
		return ""
	}
	hits := e.Sensitive.Hits(text)
	if len(hits) == 0 {
		return ""
	}
	e.sensitiveRecord(ctx, "text_input", hits, text)
	return sensitiveReplyBlocked
}

// sensitiveTextGuardOutput 文本通道产出复核：任一语言译文命中 → 整体拒付。
func (e *Engine) sensitiveTextGuardOutput(ctx context.Context, translations map[string]string) string {
	if !e.sensitiveOn() {
		return ""
	}
	for _, tr := range translations {
		if hits := e.Sensitive.Hits(tr); len(hits) > 0 {
			e.sensitiveRecord(ctx, "text_output", hits, tr)
			return sensitiveReplyBlocked
		}
	}
	return ""
}

// sensitiveReplyBlocked 对客户统一话术（不暴露命中词与规则细节）。
const sensitiveReplyBlocked = "⚠️ 内容合规审核：本次请求包含平台不予受理的内容，已拒绝翻译并转人工复核通道。" +
	"如属误判，请通过工单/客服提交原文复核（Reference: sensitive_review）。"

// CodeSensitiveBlocked 敏感词拒译的**稳定错误码**（★ 2026-09-26 〇-U 批 I-8 · F-53）。
// 值沿用历史字面量 "sensitive_blocked" 一个字符都没改——OpenAPI 同步通道的 T36/T37 断言、
// 以及 SDK 侧按 error 串分支的消费方都吃这个值，改值即契约破坏。
// ★ 为什么要从「text.go 里两处裸字面量」升格成具名导出：消费方（api/stream.go 的 SSE error 帧）
// 需要**按码分支**，而不是把码当文案发给用户。旧形态下文本通道命中敏感词时，
// res.Error="sensitive_blocked"（机器码）被直接写进 error 字段，而真正给人看的
// sensitiveReplyBlocked 在 res.Reply 里被丢掉 ⇒ 客户气泡里是一串裸键名（本轮 UAT 实测所见）。
const CodeSensitiveBlocked = "sensitive_blocked"
