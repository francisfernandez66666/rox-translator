// ============ 本文件职责中文说明 ============
// ★ 2026-11-03 立。`sensitive.go` 的 `SensitiveSkippedLead` 注释上写着
// 「措辞是唯一事实源，前端那份锁跨语言比对，等值由 sensitive_skipped_wording_gate_test.go 钉住」，
// 本文件就是那条判据——**注释点名的锚点必须存在**（AGENTS §一·14）。
//
// 这条锁防的两族形态：
//
//	G1 **承诺本仓不存在的环节**。`sensitiveReplyBlocked` 曾逐字写着
//	   「已拒绝翻译并转人工复核通道」，而 AGENTS §19 用户明令「本仓无人工复核环节，
//	   任何交付面都不许出现『等待审核／请联系客服／转人工』」。它一直静默活着，
//	   是因为唯一那条禁词锁（`noManualPromise.lock.test.ts`）只扫前端 12 份词典，
//	   **结构性够不到后端硬编码常量**——本批除了改话术，还给那条锁补了后端射程腿。
//	G2 **两个通道共用一句与成品不符的话**。工单通道的跳过位交付的是**原文**，
//	   文件通道交付的是**占位符**（`[已拦截·REDACTED]`）。写成同一句，
//	   总有一个通道的客户会照着句子去找一样不存在的东西（读数与事实相反，§一·3 那一族）。
//
// ⚠️ 这条判据**刻意只扫三个具名常量**，不做全仓中文话术横扫：
// 「待审核」是真实存在的审核队列功能名（`tmr.pending`），横扫式禁词锁会把它一起杀掉，
// 而那种锁的归宿是被下一个人摘掉（比没有锁更坏）。W4 因此专门配了正反两枚自证。
// ============================================
package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// manualPromisePhrases 客户可见文案里**不许出现**的承诺话术。
// 名单来源＝AGENTS §19 用户明令那一句（"已交人工复核／请联系客服／等待审核"）＋
// 复核批方案 §1.4 点名的「转人工复核通道」原病文本体。
//
// ⚠️ 刻意**不收**「待审核」：那是真实功能名（审核队列 `tmr.pending`）。
// 收进去就会把功能名一起判红，而那种锁最终会被人为绿灯而摘掉。
var manualPromisePhrases = []string{
	"转人工", "人工复核", "人工处理", "已交人工", "请联系客服", "等待审核", "待人工",
}

// manualPromiseViolations 返回一句话里命中的承诺话术（空 ⇒ 合规）。
//
// 抽成纯函数是为了 W4 的**自证**能喂真实语料：判据自己也要被验一次
// （只测三个常量时，把函数改成恒返回 nil 也能全绿）。
func manualPromiseViolations(s string) []string {
	var out []string
	for _, p := range manualPromisePhrases {
		if strings.Contains(s, p) {
			out = append(out, p)
		}
	}
	return out
}

// TestSensitiveWordingHasNoManualPromise W1：三条客户可见常量逐条不许出现承诺话术。
//
// 三条一起判的理由：它们都是「命中之后客户看到的那一句话」，
// 任一条漏掉＝某一条通道的客户仍被告知"有人会来处理"。
func TestSensitiveWordingHasNoManualPromise(t *testing.T) {
	cases := []struct {
		name string
		text string
	}{
		{"SensitiveSkippedLead（工单通道）", SensitiveSkippedLead},
		{"SensitiveBlockedLeadForFile（文件通道）", SensitiveBlockedLeadForFile},
		{"sensitiveReplyBlocked（即时/文本通道拒译）", sensitiveReplyBlocked},
	}
	for _, c := range cases {
		if strings.TrimSpace(c.text) == "" {
			t.Errorf("%s 是空的 ⇒ 客户会看到一句空气；这条常量必须带实质内容", c.name)
			continue
		}
		if v := manualPromiseViolations(c.text); len(v) > 0 {
			t.Errorf("%s 承诺了本仓不存在的环节（命中 %v）：%q\n"+
				"→ AGENTS §19 用户明令：本仓无人工复核环节，交付面不许出现等待审核/请联系客服/转人工",
				c.name, v, c.text)
		}
	}
}

// TestSensitiveLeadsDescribeTheirOwnDeliveryForm W2＋W3：
// 两通道那句话必须**不同**，且各自点名自己真实的交付形态。
//
// 这条是 G2 的机械形态——只判"没有禁词"时，把两句话写成同一句照样全绿，
// 而"文件通道说原文已保留、实际交付的是占位符"是比多一句废话严重得多的事：
// 客户会照着句子去文档里找那句原文。
func TestSensitiveLeadsDescribeTheirOwnDeliveryForm(t *testing.T) {
	if SensitiveSkippedLead == SensitiveBlockedLeadForFile {
		t.Fatalf("两通道的处置说明不许逐字相同：工单通道交付原文、文件通道交付占位符，共用一句必有一边与成品不符")
	}
	// 工单通道：交付位＝原文。必须说清"原文还在"，否则客户以为这一行被删了。
	if !strings.Contains(SensitiveSkippedLead, "原文") {
		t.Errorf("工单通道的说明必须点名交付形态是原文，实得 %q", SensitiveSkippedLead)
	}
	// 文件通道：交付位＝占位符。**不许**说"原文按原样保留"（那是工单通道的事实）。
	if strings.Contains(SensitiveBlockedLeadForFile, "按原样保留") {
		t.Errorf("文件通道的交付位是占位符，说明里不许写「按原样保留」（会与成品不符）：%q", SensitiveBlockedLeadForFile)
	}
	if !strings.Contains(SensitiveBlockedLeadForFile, "拦截") {
		t.Errorf("文件通道的说明必须点名交付位已被标记（占位符），实得 %q", SensitiveBlockedLeadForFile)
	}
	// 三条都该带"未翻译"这层意思：它们是**处置说明**，不是错误码。
	for name, s := range map[string]string{
		"SensitiveSkippedLead":        SensitiveSkippedLead,
		"SensitiveBlockedLeadForFile": SensitiveBlockedLeadForFile,
	} {
		if !strings.Contains(s, "未翻译") {
			t.Errorf("%s 必须说明「该处未翻译」，实得 %q", name, s)
		}
	}
}

// TestManualPromiseJudgementSelfCheck W4：判据自身的正反两枚自证。
//
// 正：一段真的承诺话术必须被抓到（≥3 项），否则 W1 是空转锁。
// 反：真实功能名「待审核」必须**不被**误伤——横扫式禁词锁杀功能名，
// 那种锁迟早被人为绿灯而摘掉（AGENTS §一·5 那条「禁词锁必须配正向对照」）。
func TestManualPromiseJudgementSelfCheck(t *testing.T) {
	bad := "已交人工复核，请等待审核，或请联系客服"
	if v := manualPromiseViolations(bad); len(v) < 3 {
		t.Fatalf("自证失败：明显违规的一句只抓到 %v ⇒ 判据本身没射程", v)
	}
	legit := "译文记忆待审核队列（tmr.pending）：运营在管理台逐条确认"
	if v := manualPromiseViolations(legit); len(v) != 0 {
		t.Fatalf("自证失败：真实功能名被误伤（命中 %v）⇒ 这条锁会被人为绿灯摘掉", v)
	}
}

// TestSkippedLeadHasProductionConsumer W5：`SensitiveSkippedLead` 必须有生产消费者。
//
// 这一条是"注释点名了、实现没人用"那一族的机械防线：
// 常量写好了、测过了，但工单链没把它带进载荷 ⇒ 前端只能自己再写一句，
// 于是措辞又长成两份（改一处留一处）。首轮复核时它确实零消费方（
// `ComplianceItem` 当初压根没有 Lead 字段），本批补上字段后才有这条腿。
//
// 判据按**文件**点名（text_lines.go 逐行链 + ticket_compliance.go 整篇/输出侧），
// 不数总数：只判总数时，删掉其中一处仍可能过线。
func TestSkippedLeadHasProductionConsumer(t *testing.T) {
	dir := filepath.Join("..", "orchestrator")
	for _, f := range []string{"text_lines.go", "ticket_compliance.go"} {
		b, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatalf("读 %s 失败（这一腿没跑到，不许当通过）: %v", f, err)
		}
		if n := strings.Count(string(b), "engine.SensitiveSkippedLead"); n < 1 {
			t.Errorf("%s 里没有出现 engine.SensitiveSkippedLead ⇒ 工单链没有把措辞带进载荷，"+
				"前端只能自己再写一句（两处各写一份就是「改一处留一处」）", f)
		}
	}
}
