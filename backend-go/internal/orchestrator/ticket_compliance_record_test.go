// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 用户裁决①的**留痕条数**锁。
//
// 这条锁存在的具体理由（不是"清洁度"）：上一轮复核批要给报告的修法
// 是"把合规闸下沉到引擎底层翻译原语"，而即时链与文件链的**内层也走同一批原语**
// （engine.HandleText → handleTextCore → … ；engine/file.go 先挑命中段、之后照样落到原语）。
// 那样做完之后，一次命中会写**两条审计、两条告警**——告警中心、群机器人、站内信三个面一起灌脏，
// 而"这次拦了多少"的读数翻倍（AGENTS §一·3 那条「读数为失败背书」的姊妹形态：读数给规模背书）。
//
// 判据吃**库侧事实**（audit_logs 里 resource=通道名的条数），不吃日志行数：
// 同 §一·8「接口说成功不算验收，库侧事实＋新路径留痕才算」的取证口径。
//
// 三个取值都要钉：
//
//	R1 多行 × 多语种命中 ⇒ 输入侧**恰 1 条**（这是本缺陷的主形态）
//	R2 零命中 ⇒ **0 条**（负向对照：没有这一条，"每次都只写一条"会因为"压根不写"而恒真）
//	R3 输出侧撤下与输入侧同时发生 ⇒ 两族各 1 条、各不吞对方
//
// ============================================
package orchestrator

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// pwAuditCount 数某一通道（resource 字段）的敏感留痕条数。
// 取不到 ⇒ 直接判死并说明「这一腿没跑到」，不许把它当成 0（0 与「没跑到」在两处断言里含义相反）。
func pwAuditCount(t *testing.T, h *pwHarness, channel string) int {
	t.Helper()
	logs, err := h.St.ListAuditFilter(1, "sensitive_block", channel, 0, "", "", 50)
	if err != nil {
		t.Fatalf("数审计留痕失败: %v", err)
	}
	return len(logs)
}

// TestComplianceRecordOnceAcrossLangs R1：两行命中 × 两个语种 ⇒ ticket_input 恰 1 条。
func TestComplianceRecordOnceAcrossLangs(t *testing.T) {
	src := strings.Join([]string{
		cmpClean1,
		"本合同随附枪支一支。",
		"另有弹药盒两个。",
		cmpClean2,
	}, "\n")
	h := pwSetup(t, func(prompt string) string {
		// 命中行不该进模型；这里对任何提示词都回一版可用译文，
		// 好让"命中行被送进模型"这种破坏只由 asked() 与留痕条数现形，而不是由译文缺行现形。
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
	pwSensitive(t, h, "枪支", "弹药")

	p := &ticketPayload{
		SourceText:   src,
		TargetLangs:  []string{"en", "ja"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行腿未接管")
	}
	// 前置：本例确实跳过了 2 行 × 2 语种＝4 处（不然条数断言是在真空里跑）
	total := 0
	for _, n := range p.ComplianceSkipped {
		total += n
	}
	if total != 4 {
		t.Fatalf("夹具不对：应跳过 4 处（2 行 × 2 语种），实得 %d（%+v）⇒ 留痕条数断言会空转", total, p.ComplianceSkipped)
	}
	if n := pwAuditCount(t, h, "ticket_input"); n != 1 {
		t.Errorf("一次执行内多行多语种命中必须**只留一条**审计，实得 %d 条（翻倍即「告警灌脏」那族事故）", n)
	}
	// 输出侧这一腿本例不该触发：模型没被喂违禁内容，也不该产出违禁译文
	if n := pwAuditCount(t, h, "ticket_output"); n != 0 {
		t.Errorf("本例没有输出侧撤下，ticket_output 必须 0 条，实得 %d", n)
	}
}

// TestComplianceRecordAbsentWhenNoHit R2：零命中 ⇒ 两族都 0 条。
// 这条是上一条的**正向对照**——按记忆「负向锁必须配正向对照」，
// 只测"命中时恰一条"会在"整条留痕腿被摘掉"时照样绿。
func TestComplianceRecordAbsentWhenNoHit(t *testing.T) {
	h := pwSetup(t, func(prompt string) string {
		var sb strings.Builder
		n := 0
		for _, s := range []string{cmpClean1, cmpBad, cmpClean2} {
			if strings.Contains(prompt, s) {
				n++
				sb.WriteString("<s" + strconv.Itoa(n) + ">OK" + strconv.Itoa(n) + "</s" + strconv.Itoa(n) + ">\n")
			}
		}
		return sb.String()
	})
	pwSensitive(t, h, "枪支") // 词包装着，但本例源文里没有它 ⇒ 闸在、零命中
	p := &ticketPayload{
		SourceText:   strings.Join([]string{cmpClean1, cmpClean2, "第三段正常内容。"}, "\n"),
		TargetLangs:  []string{"en"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行腿未接管")
	}
	if len(p.ComplianceSkipped) != 0 || len(p.ComplianceItems) != 0 {
		t.Errorf("零命中时两个键必须都不出现，实得 skipped=%+v items=%d", p.ComplianceSkipped, len(p.ComplianceItems))
	}
	if n := pwAuditCount(t, h, "ticket_input"); n != 0 {
		t.Errorf("零命中不许留痕，实得 %d 条", n)
	}
	// 且零命中 ⇒ 三行都该真送模型（正向对照：证明"没留痕"不是因为"整条链没跑"）
	if n := h.Stub.asked(cmpClean1); n == 0 {
		t.Error("零命中组一次模型都没拨 ⇒ 上面的 0 条断言属空转锁")
	}
}

// TestComplianceRecordBothSides R3：输入侧跳过与输出侧撤下同时发生 ⇒ **各 1 条**。
// 两族成因的处置动作不同（前者改源文措辞/核词包，后者查模型换档），
// 混成一条＝把其中一族藏起来（§19 族一"三档混成一味，读的人无从对账"同判）。
func TestComplianceRecordBothSides(t *testing.T) {
	// ★ 假上游必须按 **prompt 里实际出现的段数**产 markup（第 100 条教训的修法）：
	// 逐行链的补译腿是一行一枪，固定回两段会让槽位错位、译文判不可用、回落原文 ⇒
	// 输出侧根本没东西可撤，于是"产品没接线"的假红。
	// 这里刻意先 `strings.Count` prompt 里出现的目标句数，再按那个数产同样多段。
	//
	// ★★ 译文必须是**可用英文**且里面真带违禁词（`guns`）：给中文坏译文时，
	// `en` 目标语下它先被 TextTranslationUsable 判不可用、走补译梯、最终回落原文，
	// 于是 sweepComplianceOnOutput 看到的是干净源文 —— 输出侧那 2 处永远到不了。
	// 夹具首跑就是这么红的（实得 map[en:1]，只有输入侧那一条），属夹具缺陷而非产品腿。
	reply := func(prompt string) string {
		segs := 0
		for _, s := range []string{cmpClean1, cmpClean2} {
			if strings.Contains(prompt, s) {
				segs++
			}
		}
		if segs == 0 {
			return ""
		}
		var sb strings.Builder
		for i := 1; i <= segs; i++ {
			sb.WriteString("<s" + strconv.Itoa(i) + ">This product ships with guns included (" + strconv.Itoa(i) + ").</s" + strconv.Itoa(i) + ">\n")
		}
		return sb.String()
	}
	h := pwSetup(t, reply)
	pwSensitive(t, h, "枪支", "guns")
	p := &ticketPayload{
		SourceText:   strings.Join([]string{cmpClean1, cmpBad, cmpClean2}, "\n"),
		TargetLangs:  []string{"en"},
		Translations: map[string]string{},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行腿未接管")
	}
	// 输入侧：那一行被跳；输出侧：模型自产的那两行含「枪支」⇒ 被撤下换成原文
	if p.ComplianceSkipped["en"] < 3 {
		t.Fatalf("夹具不对：应同时出现输入侧 1 处与输出侧 2 处，实得 %+v（items=%+v）", p.ComplianceSkipped, p.ComplianceItems)
	}
	var in, out int
	sides := map[string]int{}
	for _, it := range p.ComplianceItems {
		sides[it.Side]++
	}
	in, out = sides[ComplianceSideInput], sides[ComplianceSideOutput]
	if in != 1 || out < 1 {
		t.Fatalf("两族都得有素材：input=%d output=%d（items=%+v）", in, out, p.ComplianceItems)
	}
	if n := pwAuditCount(t, h, "ticket_input"); n != 1 {
		t.Errorf("输入侧留痕必须恰 1 条，实得 %d", n)
	}
	if n := pwAuditCount(t, h, "ticket_output"); n != 1 {
		t.Errorf("输出侧留痕必须恰 1 条（不许被输入侧那条吞掉），实得 %d", n)
	}
	// 真空自证由上面两条承担：`out >= 1` 要求点名素材里真有 output 档，
	// 而那条素材只会在"译文里真命中"时追加 ⇒ 若假上游压根没产出坏译文，这里就先红。
	// 输出侧撤下的那一行**不许把违禁译文留在交付位上**，且不许回显词面
	for _, it := range p.ComplianceItems {
		if it.Side == ComplianceSideOutput && len(it.Terms) > 0 {
			t.Errorf("输出侧点名不许带词面（那是模型产的违禁内容，写进成品比不点名更坏），实得 %+v", it)
		}
	}
	if strings.Contains(p.Translations["en"], "guns") {
		t.Errorf("模型自产的违禁译文必须撤下，交付物里还留着：%q", p.Translations["en"])
	}
}
