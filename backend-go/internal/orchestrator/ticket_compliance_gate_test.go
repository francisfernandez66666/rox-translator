// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 合规闸工单接入批的**接线锁**（施工方案 §1.8 / §8 第 1、2 条）。
//
// 为什么单独一个文件：`ticket_compliance_test.go` 与 `ticket_compliance_record_test.go` 测的是
// **逐行腿**（判定、交付形态、留痕条数）。而本批最容易被下一批改坏的，是另外两条**整篇型腿**：
//
//	整篇腿（单段工单，或应急档 text_ticket_per_line=0 的多行工单）
//	驳回重翻腿（reject_source=human 且有可重翻译文）
//
// 这两条腿的形态与逐行腿**结构性不同**：它们一次调用就把整段源文送上去，
// 因此输入侧只能整语种跳过（排不掉那一行），输出侧也拿不到"行↔行"的保证。
// 上一轮复核把这一块判成"只有输入侧跳过、模型自产违禁译文直达交付"，本文件就是那条缺口的锁。
//
// 五条判据（编号与方案稿逐条对应）：
//
//	C1 整篇腿输入侧：源文命中 ⇒ **整语种零模型调用**、交付位全是原文、来源落第六档、
//	   点名素材只有那一行且带词面（用户令「写明跳过的禁词」）。
//	C2 整篇腿输出侧（行数对得齐）：模型回违禁英文 ⇒ 只撤命中那一行、另一行保持模型译文、
//	   撤下的那行点名不带词面（回显＝把违禁译文写进成品）。
//	C3 整篇腿输出侧（行数不齐）：整篇按原文交付、`Line=0` 表示整篇、来源**不许**仍写 model
//	   （AGENTS §19：回填过原文就不许再写 model）。另有一条源码顺序锁：撤下必须排在
//	   模型调用**之后**（排在之前撤的是空串＝这条腿一次都不会生效）。
//	C4 驳回重翻腿输入侧：整语种跳过、一次重拨都不打（这条腿是命中内容最常走的路）。
//	C5 驳回重翻腿输出侧：模型自产违禁译文同样被撤下，来源档跟着改。
//
// 反证口径：本仓不在锁文件里造假"变异体"，反证由 `scripts/gate_mutants.tsv` 登记的行为变异承担
// （摘掉 `sweepComplianceOnOutputWhole` 的调用 ⇒ C2/C3/C5 一起红；把它挪到模型调用之前 ⇒ C3 顺序腿红）。
//
// ============================================
package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"

	"translator/internal/store"
)

// setTicketPerLine 把逐行档按下去（0＝回退整篇旧行为），让 `runAIInitial` 走整篇腿。
// 必须走 `ConfigIntAllowZero` 那一档才读得到（ConfigInt 把 0 读成"没配置"）。
func setTicketPerLine(t *testing.T, h *pwHarness, v string) {
	t.Helper()
	if err := h.St.SetConfig("text_ticket_per_line", v); err != nil {
		t.Fatalf("写 text_ticket_per_line=%s 失败: %v", v, err)
	}
	if got := h.St.ConfigIntAllowZero("text_ticket_per_line", 1); got != 0 {
		t.Fatalf("夹具失效：text_ticket_per_line 按不下去（读回 %d）⇒ 下面测的仍是逐行腿", got)
	}
}

// complianceItemsOf 取某一成因档的点名素材（调用方自己按 side 分派，避免两族混看）。
func complianceItemsOf(p ticketPayload, side string) []ComplianceItem {
	var out []ComplianceItem
	for _, it := range p.ComplianceItems {
		if it.Side == side {
			out = append(out, it)
		}
	}
	return out
}

// mustRunAIInitial 跑初翻步骤并把载荷读回来（读的是**落库形态**，不是函数内那份指针）。
func mustRunAIInitial(t *testing.T, h *pwHarness, tk *store.Ticket) ticketPayload {
	t.Helper()
	if err := h.W.runAIInitial(context.Background(), tk); err != nil {
		t.Fatalf("runAIInitial 出错: %v", err)
	}
	return pwPayload(t, h, tk.ID)
}

// TestComplianceGate08C1_WholeTextLegSkipsWholeLang 整篇腿遇到源文命中 ⇒ 整语种跳过。
func TestComplianceGate08C1_WholeTextLegSkipsWholeLang(t *testing.T) {
	h := pwSetup(t, func(string) string { return "" })
	pwSensitive(t, h, "枪支")
	setTicketPerLine(t, h, "0")

	src := strings.Join([]string{cmpClean1, cmpBad, cmpClean2}, "\n")
	tk := pwTicket(t, h, src, "en")
	p := mustRunAIInitial(t, h, tk)

	// —— 输入侧收益：违禁原文一次都没有进上游（整篇形态物理上排不掉那一行 ⇒ 整语种跳过）
	if n := h.Stub.asked(cmpBad); n != 0 {
		t.Errorf("命中禁词的行不许进模型，实得 %d 次", n)
	}
	// 负向对照的另一半：**正常行也不许拨**。只看 cmpBad 的话，"模型压根没被调用"与
	// "跳过逻辑正确"两种现场长得一样，这条锁就会在整条链断掉时继续绿。
	if n := h.Stub.asked(cmpClean1); n != 0 {
		t.Errorf("整篇腿遇命中应整语种跳过（连正常行一起不拨），实得正常行被拨 %d 次", n)
	}

	if p.Sources["en"] != SourceComplianceSkipped {
		t.Errorf("整语种零交付模型译文 ⇒ 来源必须是 %q（写 model 是给失败背书），实得 %q",
			SourceComplianceSkipped, p.Sources["en"])
	}
	if p.Translations["en"] != src {
		t.Errorf("交付位必须是整段原文（不留白、不丢行、不出现占位符），实得 %q", p.Translations["en"])
	}
	if p.ComplianceSkipped["en"] != 1 {
		t.Errorf("跳过处数应为 1（只有中间那一行命中），实得 %d", p.ComplianceSkipped["en"])
	}
	items := complianceItemsOf(p, ComplianceSideInput)
	if len(items) != 1 || items[0].Line != 2 {
		t.Fatalf("输入侧点名应是恰好一条、行号 2（按源文行序 1 起），实得 %+v", items)
	}
	if len(items[0].Terms) == 0 || items[0].Terms[0] != "枪支" {
		t.Errorf("用户令「写明跳过的禁词」：词面必须落进点名素材，实得 %+v", items[0])
	}
	if strings.TrimSpace(items[0].Lead) == "" {
		t.Error("点名的处置说明不许为空（文本工单不产 .md，前端详情区读的就是这一格）")
	}
}

// TestComplianceGate08C2_WholeTextLegSweepsAlignedOutput 行数对得齐时，输出侧只撤命中那一行。
func TestComplianceGate08C2_WholeTextLegSweepsAlignedOutput(t *testing.T) {
	const cleanEN = "Interface docs are provided separately."
	h := pwSetup(t, func(prompt string) string {
		if !strings.Contains(prompt, cmpClean1) {
			return ""
		}
		// 两行对两行：第一行带违禁词、第二行干净 ⇒ 只该撤第一行。
		return "This product ships with guns included.\n" + cleanEN
	})
	pwSensitive(t, h, "guns")
	setTicketPerLine(t, h, "0")

	src := strings.Join([]string{cmpClean1, cmpClean2}, "\n")
	tk := pwTicket(t, h, src, "en")
	p := mustRunAIInitial(t, h, tk)

	if strings.Contains(p.Translations["en"], "guns") {
		t.Fatalf("模型自产的违禁译文必须被撤下，交付物里还留着：%q", p.Translations["en"])
	}
	lines := splitLines(p.Translations["en"])
	if len(lines) != 2 {
		t.Fatalf("撤下不许改行数（改行数会被 qa 的 structure 规则判整单驳回），实得 %d 行: %q", len(lines), p.Translations["en"])
	}
	if lines[0] != cmpClean1 {
		t.Errorf("被撤下的那一行必须换回**该行原文**，实得 %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != cleanEN {
		t.Errorf("没命中的那一行必须保持模型译文（整篇误撤＝客户白扣一次模型调用），实得 %q", lines[1])
	}
	items := complianceItemsOf(p, ComplianceSideOutput)
	if len(items) != 1 || items[0].Line != 1 {
		t.Fatalf("输出侧点名应是恰好一条、行号 1，实得 %+v", items)
	}
	if len(items[0].Terms) != 0 {
		t.Errorf("输出侧点名**不许带词面**（那是模型产的违禁内容，写进成品比不点名更坏），实得 %+v", items[0])
	}
	if p.Sources["en"] != SourceModelWithFallback {
		t.Errorf("部分行被撤下 ⇒ 来源必须落 %q（不许写 model），实得 %q",
			SourceModelWithFallback, p.Sources["en"])
	}
}

// TestComplianceGate08C3_WholeTextLegRevertsWhenLinesMisaligned 行数不齐 ⇒ 整篇按原文交付。
func TestComplianceGate08C3_WholeTextLegRevertsWhenLinesMisaligned(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		if !strings.Contains(prompt, cmpClean1) {
			return ""
		}
		// 两行源文、一行译文：逐行撤下按行号对齐不可能 ⇒ 只能整篇撤。
		return "This product ships with guns included and interface docs are provided separately."
	})
	pwSensitive(t, h, "guns")
	setTicketPerLine(t, h, "0")

	src := strings.Join([]string{cmpClean1, cmpClean2}, "\n")
	tk := pwTicket(t, h, src, "en")
	p := mustRunAIInitial(t, h, tk)

	if p.Translations["en"] != src {
		t.Fatalf("行数不齐时整篇型腿必须整篇按原文交付，实得 %q", p.Translations["en"])
	}
	items := complianceItemsOf(p, ComplianceSideOutput)
	if len(items) != 1 {
		t.Fatalf("整篇撤下只出一条点名素材，实得 %+v", items)
	}
	if items[0].Line != 0 {
		t.Errorf("整篇撤下的行号必须是 0（=整篇）；写具体行号等于指着一个对不上的位置，实得 %d", items[0].Line)
	}
	if p.ComplianceSkipped["en"] != 2 {
		t.Errorf("整篇撤下的处数应等于可译行数 2（空格子不算一处），实得 %d", p.ComplianceSkipped["en"])
	}
	if p.Sources["en"] != SourceComplianceSkipped {
		t.Errorf("整语种交付位全是原文 ⇒ 来源必须是 %q，实得 %q", SourceComplianceSkipped, p.Sources["en"])
	}
}

// TestComplianceGate08C3b_WholeLegSweepIsWiredAfterModelCall 源码顺序锁：撤下排在模型调用之后。
//
// 为什么需要这一条（行为锁测不到的那一档）：把 `sweepComplianceOnOutputWhole` 挪到
// `TranslateLangsInto` **之前**，它读到的是空串、一次都不生效；而"输入侧跳过"那几条断言
// 仍然全绿（命中源文的用例在 skip 那一支就 return，压根走不到输出侧）。
// 这条锁按**调用序**判：模型调用 < 撤下 < 落库。
func TestComplianceGate08C3b_WholeLegSweepIsWiredAfterModelCall(t *testing.T) {
	b, err := os.ReadFile("workflow.go")
	if err != nil {
		t.Fatalf("读 workflow.go 失败: %v", err)
	}
	src := string(b)
	for _, anchor := range []string{"w.Engine.TranslateLangsInto(", "w.Engine.TranslateWithFeedback("} {
		i := strings.Index(src, anchor)
		if i < 0 {
			t.Fatalf("锚点 %q 不在了 ⇒ 本锁失明，先把它指到新的调用形态，别直接删", anchor)
		}
		j := strings.Index(src[i:], "sweepComplianceOnOutputWhole(")
		if j < 0 {
			t.Errorf("%q 之后找不到输出侧撤下调用 ⇒ 这条腿的模型产物无人把关", anchor)
			continue
		}
		save := strings.Index(src[i:], "w.savePayload(t, p)")
		if save < 0 {
			t.Errorf("%q 之后找不到 savePayload ⇒ 锚点漂了，本锁要跟着改", anchor)
			continue
		}
		if j > save {
			t.Errorf("%q 之后：撤下(%d) 必须排在落库(%d) 之前，否则撤的是已经交付出去的成品", anchor, j, save)
		}
	}
}

// TestComplianceGate08C4_ReworkLegSkipsBeforeModel 驳回重翻腿：源文命中 ⇒ 一次重拨都不打。
func TestComplianceGate08C4_ReworkLegSkipsBeforeModel(t *testing.T) {
	h := pwSetup(t, func(string) string { return "REWORK translation" })
	pwSensitive(t, h, "枪支")

	src := strings.Join([]string{cmpClean1, cmpBad, cmpClean2}, "\n")
	tk := pwTicket(t, h, src, "en")
	// 载荷里确有可重翻译文 + 人工驳回意见 ⇒ 才会进驳回重翻腿（f42 那三条前置）。
	tk.FinalResult = f42PayloadJSON(t, src, map[string]string{"en": "Previous translation."})
	tk.RejectReason = "术语不一致，请统一"
	tk.RejectSource = store.RejectSourceHuman

	p := mustRunAIInitial(t, h, tk)

	if n := h.Stub.asked("驳回意见"); n != 0 {
		t.Errorf("命中源文时驳回重翻腿不许拨模型（整篇调用必然带出那一行），实得 %d 次", n)
	}
	if p.Translations["en"] != src {
		t.Errorf("整语种跳过 ⇒ 交付位是整段原文，实得 %q", p.Translations["en"])
	}
	if p.Sources["en"] != SourceComplianceSkipped {
		t.Errorf("来源必须是 %q，实得 %q", SourceComplianceSkipped, p.Sources["en"])
	}
	if items := complianceItemsOf(p, ComplianceSideInput); len(items) != 1 || items[0].Line != 2 {
		t.Errorf("输入侧点名应恰一条、行号 2，实得 %+v", items)
	}
}

// TestComplianceGate08C5_ReworkLegSweepsModelOutput 驳回重翻腿的输出侧同样要撤。
func TestComplianceGate08C5_ReworkLegSweepsModelOutput(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		if !strings.Contains(prompt, "驳回意见") {
			return ""
		}
		return "This product ships with guns included and interface docs are provided separately."
	})
	pwSensitive(t, h, "guns")

	src := strings.Join([]string{cmpClean1, cmpClean2}, "\n")
	tk := pwTicket(t, h, src, "en")
	tk.FinalResult = f42PayloadJSON(t, src, map[string]string{"en": "Previous translation."})
	tk.RejectReason = "术语不一致，请统一"
	tk.RejectSource = store.RejectSourceHuman

	p := mustRunAIInitial(t, h, tk)

	if n := h.Stub.asked("驳回意见"); n == 0 {
		t.Fatal("驳回重翻腿一次都没被拨到 ⇒ 下面的输出侧断言属空转锁")
	}
	if strings.Contains(p.Translations["en"], "guns") {
		t.Fatalf("重翻产物里的违禁译文必须被撤下，实得 %q", p.Translations["en"])
	}
	items := complianceItemsOf(p, ComplianceSideOutput)
	if len(items) != 1 || items[0].Line != 0 {
		t.Fatalf("行数不齐（1 行 vs 2 行）⇒ 整篇撤下、行号 0，实得 %+v", items)
	}
	if len(items[0].Terms) != 0 {
		t.Errorf("输出侧点名不许带词面，实得 %+v", items[0])
	}
	if p.Sources["en"] == SourceModel {
		t.Error("撤下之后来源仍写 model ⇒ 拿读数为失败背书（AGENTS §19）")
	}
}
