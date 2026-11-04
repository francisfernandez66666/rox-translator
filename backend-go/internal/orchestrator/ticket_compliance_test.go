// ============ 本文件职责中文说明 ============
// ★★ 2026-11-03 用户裁决①/②的行为锁：文本工单命中禁词 ⇒ **跳过该处**，并逐条点名到交付面。
//
// 这一族锁钉的六件事，每一件都对应一种「看着做了其实没落地」的历史形态：
//
//	B1 命中行**一次模型都不拨**（上游零暴露）。判据问的是「这行打没打电话」，不是产物长什么样——
//	   只数产物的话，「把违禁原文当译文交付」也能过（AGENTS §一·3 那条「读数为失败背书」）。
//	B2 交付位**不留白、不丢行、不是占位符**（等于该行原文）。留空会让行数比源文少，
//	   而 qa 的 structure/数字规则按行数与数字差判整单驳回（BUG-01/06 的病文本体）。
//	B3 计数与点名**同源等值**，且不进既有三档（unresolved/dead/truncated）。
//	   混进去＝把「平台决定不翻」记成「没翻出来」，还会抬起「质检存疑」徽标（§19 族二同判）。
//	B4 无命中 ⇒ 两个键**整条缺席**（不是 0、不是 null）；显式关闸（sensitive_gate_enabled=0）
//	   必须与「没有这个特性」**逐字等价**（§22 表头上下文那条不变式）。
//	B5 整篇型调用（单段工单，或应急档 text_ticket_per_line=0）遇到命中 ⇒ **整语种跳过**：
//	   宁可少翻也不把违禁原文送出去。这条同时钉住「合规优先于应急档」这个取舍。
//	B6 含合规跳过计数的语种**一条都不许沉淀进正式翻译记忆**（裁决①的直接副产品）。
//
// 反证在文件末尾：每条都用「改回坏形态」的源码喂同一个判据函数，断的是「变异后真判红」，
// 不是「落地计数」（空替换会伪装成锁抓住了破坏）。
// ============================================
package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"translator/internal/engine"
	"translator/internal/sensitive"
	"translator/internal/store"
)

// pwSensitive 给 harness 装一份临时词包并打开闸。
// 词包只放本用例需要的词。输入侧那几条刻意用 CJK 词（走子串口径）——拉丁词的词边界口径
// 由 internal/sensitive 自己的锁（TestLatinWordsUseWordBoundary）管，这里不重复测：
// 两把尺子各守各的射程，重复测只会让下一位不知道该改哪一条。
//
// ⚠️ **输出侧那一族必须用拉丁词**（TestComplianceRecordBothSides 传 "guns"）：
// 那条腿判的是"模型自产的译文里有没有违禁内容"，而 `en` 目标语下**中文译文会先被
// TextTranslationUsable 判成不可用**、走补译梯、最终回落原文——撤下腿压根走不到
// （夹具给中文坏译文时，实测读数就是"输入侧 1 处、输出侧 0 处"）。
// 所以那一族的假上游必须回**可用的英文**且里面真带违禁词，否则测的是"回落原文"而不是"撤下译文"。
func pwSensitive(t *testing.T, h *pwHarness, words ...string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sensitive_words.txt")
	if err := os.WriteFile(p, []byte(strings.Join(words, "\n")+"\n"), 0o644); err != nil {
		t.Fatalf("写临时词包失败: %v", err)
	}
	h.W.Engine.Sensitive = sensitive.New(p)
	if !h.W.Engine.TicketSensitiveEnabled() {
		t.Fatal("夹具失效：词包装好了但工单合规闸没开 ⇒ 下面所有断言都会是假绿")
	}
}

const (
	cmpClean1 = "本产品提供标准接口服务。"
	cmpBad    = "本产品随货附赠枪支配件一套。" // 命中词：枪支
	cmpClean2 = "接口文档另行提供。"
)

// TestComplianceSkipPerLine B1＋B2＋B3：三行源文、中间一行命中 ⇒
// 该行不拨模型、按原文出货，其余两行照常翻译；计数与点名等值，且不进既有三档。
func TestComplianceSkipPerLine(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		// 假上游只对**没被跳过**的行作答；若命中行被误送进模型，
		// asked(cmpBad) 会现形（B1）。一批里全是命中行时回空串＝坏形态。
		var sb strings.Builder
		n := 0
		for _, s := range []string{cmpClean1, cmpClean2} {
			if strings.Contains(prompt, s) {
				n++
				sb.WriteString("<s" + strconv.Itoa(n) + ">OK" + strconv.Itoa(n) + "</s" + strconv.Itoa(n) + ">\n")
			}
		}
		return sb.String()
	})
	pwSensitive(t, h, "枪支")

	p := &ticketPayload{
		SourceText:   strings.Join([]string{cmpClean1, cmpBad, cmpClean2}, "\n"),
		TargetLangs:  []string{"en"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行腿未接管（开关或行数判据不对，本条锁失明）")
	}

	// —— B1 命中行一次模型都不拨
	if n := h.Stub.asked(cmpBad); n != 0 {
		t.Errorf("命中禁词的行不该进模型（上游零暴露是这条闸的全部收益），实得 %d 次", n)
	}
	// —— B1 正向对照：没被跳过的行必须真被拨到过；否则上面那条恒真、整段在真空里跑
	if n := h.Stub.asked(cmpClean1); n == 0 {
		t.Error("正常行一次都没送模型 ⇒ 整条链没跑到，B1 的零命中断言属空转锁")
	}

	// —— B2 交付形态：不留白、不丢行、命中位是原文本身、不是占位符
	lines := strings.Split(p.Translations["en"], "\n")
	if len(lines) != 3 {
		t.Fatalf("交付行数必须与源文一致（少了会触发 qa 结构驳回），实得 %d: %q", len(lines), p.Translations["en"])
	}
	if strings.TrimSpace(lines[1]) == "" {
		t.Error("命中行交付位是空的 ⇒ 违反「不留白」，客户会拿到一份缺行的成品")
	}
	if lines[1] != cmpBad {
		t.Errorf("命中行交付位必须是该行原文（跳过＝不翻译），实得 %q", lines[1])
	}
	if strings.Contains(p.Translations["en"], "已拦截") {
		t.Errorf("工单通道的交付位不该出现占位符（那是文件通道的形态），实得 %q", p.Translations["en"])
	}

	// —— B3 计数与点名等值
	if got := p.ComplianceSkipped["en"]; got != 1 {
		t.Errorf("compliance_skipped.en 应为 1，实得 %d", got)
	}
	var named []ComplianceItem
	for _, it := range p.ComplianceItems {
		if it.Lang == "en" {
			named = append(named, it)
		}
	}
	if len(named) != p.ComplianceSkipped["en"] {
		t.Errorf("点名条数(%d)必须等于处数(%d)，否则交付面那一节与对账读数各说各的", len(named), p.ComplianceSkipped["en"])
	}
	if len(named) != 1 || named[0].Line != 2 {
		t.Fatalf("点名的行号必须是 2（按源文行序 1 起），实得 %+v", named)
	}
	if named[0].Side != ComplianceSideInput {
		t.Errorf("源文命中的成因档应是 %q，实得 %q", ComplianceSideInput, named[0].Side)
	}
	if len(named[0].Terms) == 0 || named[0].Terms[0] != "枪支" {
		t.Errorf("用户令「写明跳过的禁词」：词面必须落进点名素材，实得 %+v", named[0].Terms)
	}
	// ★ 措辞必须随载荷出栈（唯一事实源＝engine.SensitiveSkippedLead）：
	// 文本工单不产 .md，前端详情区读的就是这一格；空着前端只能自己再写一句，
	// 两处各写一份＝「改一处留一处」（本仓页脚/手册入口反复漂移过的那一族）。
	// 而这里一旦写死中文期望，就变成第二份措辞——所以判的是**等值于那条常量**，不是某个字面量。
	for _, it := range p.ComplianceItems {
		if it.Lead != engine.SensitiveSkippedLead {
			t.Errorf("点名素材必须带处置说明且逐字等于 engine.SensitiveSkippedLead（唯一事实源），实得 %q（%+v）", it.Lead, it)
		}
	}
	if p.UnresolvedLines["en"] != 0 {
		t.Errorf("合规跳过不许记进 unresolved_lines（那一档答「想翻没翻出来」），实得 %d", p.UnresolvedLines["en"])
	}
	if p.UpstreamDeadLines["en"] != 0 || p.TruncatedLines["en"] != 0 {
		t.Errorf("合规跳过不许记进上游断/截断两档，实得 dead=%d trunc=%d", p.UpstreamDeadLines["en"], p.TruncatedLines["en"])
	}
	// 混合形态（两行真翻了）来源列不许谎称整语种合规
	if p.Sources["en"] != SourceModel && p.Sources["en"] != SourceModelWithFallback {
		t.Errorf("混合形态的来源档不该是合规档，实得 %q", p.Sources["en"])
	}
}

// TestComplianceWholeTextSingleLine B5：整篇只有一行且命中 ⇒
// 零模型、交付＝原文、来源落第六档。这一档最容易被漏测：
// nonEmptyLines<2 时逐行腿不接管，走的是整篇腿，而现网文本工单大量就是单段。
func TestComplianceWholeTextSingleLine(t *testing.T) {
	h := pwSetup(t, func(prompt string) string { return "<s1>SHOULD NOT BE CALLED</s1>" })
	pwSensitive(t, h, "枪支")
	tk := pwTicket(t, h, cmpBad, "en")

	called := 0
	h.Stub.reply = func(prompt string) string { called++; return "<s1>x</s1>" }

	if err := h.W.runAIInitial(context.Background(), tk); err != nil {
		t.Fatalf("初翻失败: %v", err)
	}
	if called != 0 {
		t.Errorf("单段命中工单一次模型都不该拨（违禁原文不得进上游），实得 %d 次", called)
	}
	p := pwPayload(t, h, tk.ID)
	if strings.TrimSpace(p.Translations["en"]) != cmpBad {
		t.Errorf("交付位应为原文（跳过＝不翻译、不留白），实得 %q", p.Translations["en"])
	}
	if p.Sources["en"] != SourceComplianceSkipped {
		t.Errorf("整语种零模型时来源必须是第六档 %q，实得 %q", SourceComplianceSkipped, p.Sources["en"])
	}
	if p.ComplianceSkipped["en"] != 1 || len(p.ComplianceItems) != 1 {
		t.Errorf("处数与点名必须各为 1，实得 skipped=%d items=%d", p.ComplianceSkipped["en"], len(p.ComplianceItems))
	}
	if p.UnresolvedLines["en"] != 0 {
		t.Errorf("把合规跳过算成「没译出来」＝把设计报成缺陷，实得 %d", p.UnresolvedLines["en"])
	}
}

// TestComplianceWholeTextUnderPerLineOff B5 的第二形态：多行源文但应急档把逐行腿关了
// （text_ticket_per_line=0）⇒ 仍必须合规优先（整语种跳过），
// 不许因为"运营关了逐行"就把含违禁行的整篇源文送去上游。
func TestComplianceWholeTextUnderPerLineOff(t *testing.T) {
	h := pwSetup(t, func(prompt string) string { return "<s1>x</s1>" })
	pwSensitive(t, h, "枪支")
	if err := h.St.SetConfig("text_ticket_per_line", "0"); err != nil {
		t.Fatalf("关逐行档失败: %v", err)
	}
	tk := pwTicket(t, h, strings.Join([]string{cmpClean1, cmpBad, cmpClean2}, "\n"), "en")

	called := 0
	h.Stub.reply = func(prompt string) string { called++; return "<s1>x</s1>" }
	if err := h.W.runAIInitial(context.Background(), tk); err != nil {
		t.Fatalf("初翻失败: %v", err)
	}
	if called != 0 {
		t.Errorf("应急档下命中工单仍一次模型都不该拨，实得 %d 次（合规优先于应急档）", called)
	}
	p := pwPayload(t, h, tk.ID)
	if p.ComplianceSkipped["en"] != 1 {
		t.Errorf("处数应为 1，实得 %+v", p.ComplianceSkipped)
	}
	if p.Sources["en"] != SourceComplianceSkipped {
		t.Errorf("整语种零模型时来源必须是第六档，实得 %q", p.Sources["en"])
	}
	// 整篇腿的交付是原文整段（不留白）
	if strings.TrimSpace(p.Translations["en"]) == "" {
		t.Error("整篇腿不许留白：交付位应是源文原样")
	}
}

// TestComplianceClosedEqualsNoFeature B4：显式关闸 ⇒ 与「没有这个特性」逐字等价。
// 判据两侧各拨一次（开闸/关闸），**只测关闸那一边等于什么都没测**
// （摘掉整个特性也会让"关档无变化"恒真——AGENTS §三 那条反证自伤的写法）。
func TestComplianceClosedEqualsNoFeature(t *testing.T) {
	src := strings.Join([]string{cmpClean1, cmpBad, cmpClean2}, "\n")
	reply := func(prompt string) string {
		var sb strings.Builder
		n := 0
		for _, s := range []string{cmpClean1, cmpBad, cmpClean2} {
			if strings.Contains(prompt, s) {
				n++
				sb.WriteString("<s" + strconv.Itoa(n) + ">OK" + strconv.Itoa(n) + "</s" + strconv.Itoa(n) + ">\n")
			}
		}
		return sb.String()
	}

	// 开闸：命中行被跳（前一条锁已细验，这里只取"两键在位"这一个形状）
	on := pwSetup(t, reply)
	pwSensitive(t, on, "枪支")
	pOn := &ticketPayload{SourceText: src, TargetLangs: []string{"en"}, Translations: map[string]string{}, Sources: map[string]string{}}
	if !on.W.translateTextPerLine(context.Background(), pOn) {
		t.Fatal("开闸组未接管")
	}
	if len(pOn.ComplianceSkipped) == 0 || len(pOn.ComplianceItems) == 0 {
		t.Error("开闸组必须有两个键（对照组不成立 ⇒ 下面的等价断言会假绿）")
	}
	if on.Stub.asked(cmpBad) != 0 {
		t.Error("开闸组命中行仍进了模型，本文件全部断言的前提塌了")
	}

	// 关闸：两键**整条缺席**、命中行照常被送模型（＝改造前的形态，逐字等价）
	off := pwSetup(t, reply)
	pwSensitive(t, off, "枪支")
	if err := off.St.SetConfig("sensitive_gate_enabled", "0"); err != nil {
		t.Fatalf("关闸失败: %v", err)
	}
	if off.W.Engine.TicketSensitiveEnabled() {
		t.Fatal("夹具失效：写了 0 但闸还开着 ⇒ 等价断言会假绿")
	}
	pOff := &ticketPayload{SourceText: src, TargetLangs: []string{"en"}, Translations: map[string]string{}, Sources: map[string]string{}}
	if !off.W.translateTextPerLine(context.Background(), pOff) {
		t.Fatal("关闸组未接管")
	}
	if _, ok := pOff.ComplianceSkipped["en"]; ok {
		t.Errorf("关档时 compliance_skipped 必须整条缺席（不是 0、不是 null），实得 %+v", pOff.ComplianceSkipped)
	}
	if len(pOff.ComplianceItems) != 0 {
		t.Errorf("关档时不许留下任何点名素材（那是「有该特性」的形状），实得 %d 条", len(pOff.ComplianceItems))
	}
	if off.Stub.asked(cmpBad) == 0 {
		t.Error("关档却把命中行挡在模型外 ⇒ 关档不等于「没有这个特性」，运维按 0 关掉后仍会拦客户内容")
	}
}

// TestComplianceSkippedExcludedFromTM B6：交付位混着原文的单，一条都不许沉淀进正式 TM。
// 判据吃**审计那行读数**（自动入库句数＝0、跳过语种数≥1），不吃"没报错"：
// 沉淀的代价是复发（以后每次命中都直出原文冒充译文），比少沉淀严重得多。
func TestComplianceSkippedExcludedFromTM(t *testing.T) {
	h := pwSetup(t, func(prompt string) string { return "<s1>ok</s1>" })
	pwSensitive(t, h, "枪支")
	tk := pwTicket(t, h, cmpBad, "en")
	// 反馈步只在**批准**后回写（runFeedback 第一道闸就是这一判），夹具必须照生产形态摆
	tk.Status = store.TicketApproved
	tk.FinalResult = `{"source_text":"` + cmpBad + `","target_langs":["en"],` +
		`"translations":{"en":"` + cmpBad + `"},"sources":{"en":"compliance_skipped"},` +
		`"compliance_skipped":{"en":1},"eval_scores":{"en":99}}`
	if err := h.St.UpdateTicket(tk); err != nil {
		t.Fatalf("写工单失败: %v", err)
	}
	if err := h.W.runFeedback(context.Background(), tk); err != nil {
		t.Fatalf("反馈步失败: %v", err)
	}
	detail := pwAuditDetail(t, h, "tm_feedback_screen")
	if !strings.Contains(detail, "跳过1语种") {
		t.Errorf("含合规跳过计数的语种必须整语种跳过 TM 回写，审计读数： %q", detail)
	}
	if strings.Contains(detail, "自动入库1") {
		t.Errorf("审计说「自动入库1句」⇒ 原文冒充译文被沉淀进正式 TM 了：%q", detail)
	}
}

// pwAuditDetail 取某动作最近一条审计的 detail（判据吃库侧事实，不吃日志行数——
// 同 AGENTS §一·8 那条「库侧事实＋新路径留痕才算」的取证口径）。
func pwAuditDetail(t *testing.T, h *pwHarness, action string) string {
	t.Helper()
	logs, err := h.St.ListAuditFilter(1, action, "", 0, "", "", 5)
	if err != nil || len(logs) == 0 {
		t.Fatalf("没读到动作 %s 的审计留痕（err=%v, n=%d）⇒ 该步没跑到，断言会空转", action, err, len(logs))
	}
	return logs[0].Detail
}
