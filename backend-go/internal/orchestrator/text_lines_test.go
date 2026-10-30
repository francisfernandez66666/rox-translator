// ============ 本文件职责中文说明 ============
// DEF-02（2026-10-24）：文本工单逐行翻译/结构重发的最小单测。
// 真实模型链路由 UAT 覆盖；这里锁「行数保持」「只补缺失/错误行」「重发次数上限」三条纯逻辑。
//
// ★ 2026-10-25 对账补腿（TestTranslateTextPerLineReusesGoodLines）：
//
//	方案里那句「重发时只重译缺失/错误行」以前只有**静态**断言，没有任何一条锁问过上游
//	「这一行到底打没打电话」。而这条链的成本口径是**按真实 token 聚合扣减**
//	（见 internal/service/ticket.go runTicket 的 chargeTokens），所以「正确行复用了没有」
//	不是清洁度问题，而是**客户为一遍已经翻对的内容重复付钱**的问题。
//	本文件用 pwSetup 的假上游（httptest + pwStub.asked 数 prompt 命中条数）把它钉成行为锁：
//	正确的行零模型调用、缺失的行恰好一次、拼接后行数与源文一致。
//
// ============================================
package orchestrator

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestComposeLineOutputPreservesLines(t *testing.T) {
	out := composeLineOutput([]string{"line one", "line two\nmerged", ""})
	if got := strings.Count(out, "\n"); got != 2 {
		t.Fatalf("行数应保持 3 行（2 个换行），got %d: %q", got, out)
	}
	if strings.Contains(out, "\nmerged") {
		t.Fatalf("行内换行应压成空格，不能让一段变多段: %q", out)
	}
}

func TestUsefulTermSource(t *testing.T) {
	bad := []string{"中", "中文", "验收", "翻译", "ab"}
	for _, s := range bad {
		if usefulTermSource(s) {
			t.Errorf("泛词/短词 %q 不应进入 examples", s)
		}
	}
	good := []string{"销售线索", "极石汽车", "API integration", "Bluetooth"}
	for _, s := range good {
		if !usefulTermSource(s) {
			t.Errorf("术语 %q 被误过滤", s)
		}
	}
}

func TestStructureRetryNeededAndMax(t *testing.T) {
	h := pwSetup(t, nil)
	tk := pwTicket(t, h, "第一段。\n第二段。", "en")
	// 两段源文只回一段 → 结构失败
	tk.FinalResult = `{"source_text":"第一段。\n第二段。","translations":{"en":"only first"}}`
	if err := h.St.UpdateTicket(tk); err != nil {
		t.Fatalf("写工单失败: %v", err)
	}
	if !h.W.StructureRetryNeeded(tk) {
		t.Fatal("结构失败应触发整单重发")
	}
	// ★ 2026-10-25 对账（§10.1.2）：计数从 final_result 里的 JSON 字段迁到 tickets.repair_attempts 列。
	//   判据跟着换成现读那一列——载荷字段那条形态会被任何一次整段重写载荷抹掉，上限随之复位。
	n, err := h.St.TicketRepairAttempts(tk.ID)
	if err != nil {
		t.Fatalf("读 repair_attempts: %v", err)
	}
	if n != 1 {
		t.Fatalf("重发次数应写回 1，实得 %d", n)
	}
	// 上限 3：再如实重发两次（把列推到 3），下一次不应再重发
	for i := 2; i <= 3; i++ {
		if !h.W.StructureRetryNeeded(tk) {
			t.Fatalf("第 %d 次结构失败应仍重发（默认上限 3），实得 false", i)
		}
	}
	if h.W.StructureRetryNeeded(tk) {
		t.Fatal("达到上限 3 次后不应再重发")
	}
}

// TestTranslateTextPerLineReusesGoodLines DEF-02「只重译缺失/错误行」的行为锁（2026-10-25 对账补腿）。
//
// 判据本体是**上游被打了几次**，不是产物长什么样：计费按真实 token 聚合扣减
// （internal/service/ticket.go runTicket 的 chargeTokens），正确行若被再送一遍
// ＝客户为已经翻对的内容重复付钱，而产物依然完全正确、界面一行异常都没有。
//
// 三档缺一不可：
//  1. 正确行 ⇒ asked(该行)==0（不重复扣费的模型侧保证）；
//  2. 缺失行 ⇒ asked(该行)==1（正向对照——没有这一条，第 1 条会因为"整批压根没发出去"而恒真）；
//  3. 复用行逐字保留 + 行数与源文一致（重发不许把段落结构改坏，否则又是 DEF-02 的母缺陷）。
func TestTranslateTextPerLineReusesGoodLines(t *testing.T) {
	const line1 = "第一段内容在这里。"
	const line2 = "第二段内容在这里。"
	const reused = "The first paragraph content is here."
	const fresh = "The second paragraph content is here."

	h := pwSetup(t, func(prompt string) string {
		// 假上游只在被问到缺失行时作答；任何一次"把正确行又送去翻"都会退空串，
		// 于是第 3 档的逐字等值断言同时能抓到"复用了又被覆盖"这种形态。
		if strings.Contains(prompt, line2) {
			return "<s1>" + fresh + "</s1>"
		}
		return ""
	})
	ctx := context.Background()
	p := &ticketPayload{
		SourceText:   line1 + "\n" + line2,
		TargetLangs:  []string{"en"},
		Translations: map[string]string{"en": reused + "\n"},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(ctx, p) {
		t.Fatal("逐行翻译分支未接管（text_ticket_per_line 开关或行数判据不对，本条锁会失明）")
	}
	if n := h.Stub.asked(line1); n != 0 {
		t.Errorf("第一行译文本来就对，不该再打模型（重复扣费），实得 %d 次", n)
	}
	if n := h.Stub.asked(line2); n != 1 {
		t.Errorf("第二行缺失，应恰好打一次模型，实得 %d 次", n)
	}
	got := strings.Split(p.Translations["en"], "\n")
	if len(got) != 2 {
		t.Fatalf("行数应与源文一致（2 行），实得 %d 行: %q", len(got), p.Translations["en"])
	}
	if got[0] != reused {
		t.Errorf("正确行应逐字复用，实得 %q（期望 %q）", got[0], reused)
	}
	if got[1] != fresh {
		t.Errorf("缺失行应补上新译文，实得 %q（期望 %q）", got[1], fresh)
	}
}

// TestTranslateTextPerLineRetranslatesBadLine DEF-02 第二档：错的行（这里喂中文回显）必须重译。
//
// 与上一条互为对照：上一条锁"对的行不发"，这一条锁"错的行一定发"。
// 两条都在，"只重译缺失/错误行"这句话才有射程；只有前一条时，
// 把 BatchTranslate 整调用摘掉也不会红。
func TestTranslateTextPerLineRetranslatesBadLine(t *testing.T) {
	const line1 = "第一段内容在这里。"
	const line2 = "第二段内容在这里。"
	const reused = "The first paragraph content is here."
	const bad = "第二段内容在这里。" // 原样回显＝必过不了 TextTranslationUsable
	const fixed = "The second paragraph content is here."

	h := pwSetup(t, func(prompt string) string {
		if strings.Contains(prompt, line2) {
			return "<s1>" + fixed + "</s1>"
		}
		return ""
	})
	p := &ticketPayload{
		SourceText:   line1 + "\n" + line2,
		TargetLangs:  []string{"en"},
		Translations: map[string]string{"en": reused + "\n" + bad},
		Sources:      map[string]string{},
	}
	if !h.W.translateTextPerLine(context.Background(), p) {
		t.Fatal("逐行翻译分支未接管，本条锁失明")
	}
	if n := h.Stub.asked(line2); n < 1 {
		t.Errorf("回显的错行必须重译，实得上游被问 %d 次", n)
	}
	got := strings.Split(p.Translations["en"], "\n")
	if len(got) != 2 {
		t.Fatalf("行数应保持 2，实得 %d 行: %q", len(got), p.Translations["en"])
	}
	if got[1] != fixed {
		t.Errorf("错行应被替换成合规译文，实得 %q", got[1])
	}
	if got[0] != reused {
		t.Errorf("同批里的正确行不该被连带覆盖，实得 %q（期望 %q）", got[0], reused)
	}
}

// TestTextTicketWireGlossary 文本工单链必须真把领域词表接进来，且**两条腿都要吃到**。
//
// ★ 2026-10-30 之前这条链**完全不读词表**：同一次现网探针里即时链有 domain_glossary、文本工单腿 0 行。
// 这条链有两条腿，**任何一条漏了都表现为"这个特性对文本客户不存在"**：
//
//	· 多行 ⇒ translateTextPerLine（逐行、走批量层）；
//	· 单段落 / text_ticket_per_line=0 ⇒ TranslateLangsInto（整篇、走 singleLangRaw）。
//
// 所以判定必须落在**分叉之前**、挂进 ctx（整篇腿本来就读 ctx；批量层在 opts 为空时回落 ctx）。
//
// 四腿判据：
//
//	① 判定在 workflow.go 里、参数是本单源文与**全部**目标语种；
//	② 判定结果挂进 ctx（`engine.WithGlossary`）——只算不挂等于没接；
//	③ 顺序：判定必须排在 `translateTextPerLine` 之前（排在后面就只覆盖整篇腿）；
//	④ 负向：不许在 text_lines.go 里再出现"只给批量腿传 opts"那半截——两套机制并存，
//	   下次有人改一处就会漏一处（本仓「多把尺子」的老形态）。
func TestTextTicketWireGlossary(t *testing.T) {
	raw, err := os.ReadFile("workflow.go")
	if err != nil {
		t.Fatalf("读 workflow.go 失败：%v", err)
	}
	wf := string(raw)
	for _, want := range []string{
		"PrepareTicketGlossary(ctx, p.SourceText, p.TargetLangs)",
		"engine.WithGlossary(ctx, prof, maxTerms)",
	} {
		if !strings.Contains(wf, want) {
			t.Fatalf("文本工单链没接上领域词表：workflow.go 缺 %q", want)
		}
	}
	callAt := strings.Index(wf, "PrepareTicketGlossary(")
	lineAt := strings.Index(wf, "w.translateTextPerLine(ctx, p)")
	if callAt < 0 || lineAt < 0 || callAt > lineAt {
		t.Fatalf("判定必须放在两条腿分叉之前（call=%d line=%d）：放后面就只覆盖整篇腿，逐行腿永远拿不到", callAt, lineAt)
	}

	tlRaw, err := os.ReadFile("text_lines.go")
	if err != nil {
		t.Fatalf("读 text_lines.go 失败：%v", err)
	}
	if strings.Contains(string(tlRaw), "bopts.Glossary") {
		t.Fatalf("text_lines.go 里又出现了自带的词表传递：词表的唯一载体是 ctx（批量层回落），两套并存迟早漏一处")
	}
}
