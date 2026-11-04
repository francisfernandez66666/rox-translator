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
//
// ★ 2026-11-03 用户裁决三：告警正文**不再写词包路径**（旧形态把 `e.Sensitive.Path()` 打进
//
//	站内信/群机器人——那是本仓 §一·12 对 /api/health 立的同一条纪律「只出状态词与计数，
//	禁出绝对路径」，告警面此前漏在外头，而告警是被截图转发的那一面）。
//
// 一条留痕只由**一条通道的一个咽喉**调用一次；调用方若在语种循环或逐批循环里调它，
// 一次命中就会刷出 N 条审计（本仓「读数为失败背书」的姊妹形态：读数翻倍），
// 等值判据见 sensitive_record_once_test.go。
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
		fmt.Sprintf("[%s] 敏感词命中 %d 项：%s（处置见对应通道的交付面与审计留痕）",
			channel, len(hits), strings.Join(hits, ","))); err != nil {
		log.Printf("[sensitive] 告警写入失败: %v", err)
	}
}

// ============ 工单通道（★ 2026-11-03 用户裁决 1） ============
// 这三条导出口是工单链（internal/orchestrator）唯一能碰到合规闸的口——
// 本包其余判据都是包内私有，orchestrator 调不到，**禁止**在调用方另写一份命中判定
// （那会长出第二把词边界尺子，§18 那条「拉丁词必须走词边界」的现网事故就是这么来的）。
//
// 与即时/文件两通道的交付语义**刻意不同**（用户裁决原文：命中后跳过该处翻译，
// 并在交付面写明跳过的是哪个词）：
//   - 即时通道＝整段拒译（一段话一个成品，跳过无处可跳）；
//   - 文件通道＝命中段写占位符（§21 交付图上不许出现占位符是另一条腿）；
//   - 工单通道＝命中行**不进模型、不交付译文、按原文出货**，并把命中词点名到交付面。

// TicketSensitiveEnabled 工单通道的闸是否生效：与另外两条通道**同一个开关**
// （词包装载非空 且 sensitive_gate_enabled != "0"），不新增第二个开关。
func (e *Engine) TicketSensitiveEnabled() bool { return e.sensitiveOn() }

// TicketSensitiveHits 返回该段命中的词面（零命中回 nil）。
// 刻意**不留痕**：留痕由调用方在「每一单一次」的位置统一调 TicketSensitiveRecord，
// 逐语种各记一次会把一处命中刷成语种数条审计。
func (e *Engine) TicketSensitiveHits(text string) []string {
	if !e.sensitiveOn() {
		return nil
	}
	return e.Sensitive.Hits(text)
}

// TicketSensitiveRecord 工单通道的命中留痕（channel 由调用方给 ticket_input / ticket_output）。
// 只在**真跳过了内容**时调用——没有跳过就没有这条留痕（照 §21 那条「只有真拨过才记账」，
// 否则一次读不到词包的 fail-open 也会留下一条"拦住了什么"的假读数）。
func (e *Engine) TicketSensitiveRecord(ctx context.Context, channel string, hits []string, snippet string) {
	e.sensitiveRecord(ctx, channel, hits, snippet)
}

// SensitiveSkippedLead 交付面点名跳过项时用的引导语（唯一事实源）。
// 之所以收在引擎侧：交付面有两个渲染点（服务端 .md 尾节、前端详情区），
// 两处各写一份措辞迟早打架（同 §一·5「那两处面是同一句话」那条的裁决手法——
// 等值由 sensitive_skipped_wording_gate_test.go 钉住，前端那份锁跨语言比对）。
//
// ⚠️ 这一句只描述**工单通道**的交付形态（跳过位=原文按原样保留）。
// 文件通道的交付位是占位符，用的是 SensitiveBlockedLeadForFile——
// 两通道共用一句会写出"原文已按原样保留"这种与成品不符的话（读数与事实相反，§一·3 那一族）。
const SensitiveSkippedLead = "内容合规审核：该处未翻译，原文已按原样保留"

// SensitiveBlockedLeadForFile 文件通道「合规跳过」那一节的处置说明。
// 该通道命中段的交付位是 SensitivePlaceholderText（**不是**原文），所以这句必须说清交付位是什么，
// 客户才不会去文档里找那句"按原样保留的原文"而找不到（§19 族三那条"点名必须点得准"的同型要求）。
const SensitiveBlockedLeadForFile = "内容合规审核：该处未翻译，交付位已标记为已拦截"

// sensitiveBlockedSegments 文件通道输入侧：挑出命中段（不送模型），
// 并为全部目标语言预填占位译文；有命中则留痕。
//
// 返回 (命中段集合, 每段命中的词面)。第二个返回值**必须在这里一次判完带出来**：
// 词包装载、归一化口径、拉丁词边界这三件事只有这一处判据，
// 调用方若再对同一批段跑一次 `Hits` 就是"两处各判一次同一件事"——
// 除了白烧 CPU，更要紧的是它会让 `blockedSeg` 多出一个引用点，
// 而那个引用数正是 file_writepoints_guard_test.go 用来抓"数字段重新进上游"的判据本体。
func (e *Engine) sensitiveBlockedSegments(ctx context.Context, texts []string, langs []string, addTrans func(lc, orig, tr string)) (map[string]bool, map[string][]string) {
	blocked := map[string]bool{}
	terms := map[string][]string{}
	if !e.sensitiveOn() {
		return blocked, terms
	}
	var firstHits []string
	for _, t := range texts {
		if blocked[t] {
			continue
		}
		if hits := e.Sensitive.Hits(t); len(hits) > 0 {
			blocked[t] = true
			terms[t] = hits // 交付面「合规跳过」那一节读它（★ 2026-11-03 用户裁决②）
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
	return blocked, terms
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
//
// ★★ 2026-11-03 复核批改写：旧文案写着「已拒绝翻译并转人工复核通道」，而本仓**没有人工复核环节**
//
//	（AGENTS §19 用户明令：任何客户可见面都不许出现"已交人工复核／请联系客服／等待审核"）。
//	这句一直静默活着，是因为唯一那条禁词锁（noManualPromise.lock.test.ts）只扫前端 12 份词典，
//	**够不到后端硬编码常量**——本批除了改话术，还给那条锁补了后端射程腿。
//
// 只陈述事实＋给一条**真实存在**的出路（误判的出路是运营改词包，词包是数据不用发版；
// "重新发起工单"是客户手里真有的动作）：不许承诺一个系统里不存在的环节。
const sensitiveReplyBlocked = "⚠️ 内容合规审核：本次请求包含平台不予受理的内容，本次未翻译（内容未送出处置上游）。" +
	"如认为属误判，可调整后重新提交。"

// CodeSensitiveBlocked 敏感词拒译的**稳定错误码**（★ 2026-09-26 〇-U 批 I-8 · F-53）。
// 值沿用历史字面量 "sensitive_blocked" 一个字符都没改：对话面（SSE error 帧的 error_code）
// 与前端 useChat 的分支按它走，改名即失联。
// ★ 口径校准（2026-10-04 〇-AR 第 2 波）：**OpenAPI 同步面不再往外发这一内部码**——
// 旧形态把码当 message 发（T36/T37 当时 grep 的就是这个泄漏点），现在按修法 F 归到
// 对外既有档 rejected/403（与异步面 gateErrorCode 的内容类拒绝同档），见
// internal/api/api_openapi_tasks.go 的 openAPIEngineFailure。
// 内部各面（引擎结果、试用面 trial.go 的分支）仍按这一常量比较，**不许改成别的字面量**。
// ★ 为什么要从「text.go 里两处裸字面量」升格成具名导出：消费方（api/stream.go 的 SSE error 帧）
// 需要**按码分支**，而不是把码当文案发给用户。旧形态下文本通道命中敏感词时，
// res.Error="sensitive_blocked"（机器码）被直接写进 error 字段，而真正给人看的
// sensitiveReplyBlocked 在 res.Reply 里被丢掉 ⇒ 客户气泡里是一串裸键名（本轮 UAT 实测所见）。
const CodeSensitiveBlocked = "sensitive_blocked"
