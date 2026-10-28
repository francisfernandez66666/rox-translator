package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"translator/internal/fileproc"
	"translator/internal/textscript"
)

// ocrTestDoc 构造一份最小 OcrDoc（画像/列不需要真实数据时留空）。
func ocrTestDoc(lines ...fileproc.OcrLine) *fileproc.OcrDoc {
	return &fileproc.OcrDoc{Version: fileproc.OcrDocVersion, Lines: lines,
		Profile: textscript.OfLines(nil)}
}

// TestOcrFixEnabledSwitch kill-switch：off/0/false 关闭，空/其它开启。
func TestOcrFixEnabledSwitch(t *testing.T) {
	e := &Engine{}
	cases := map[string]bool{"": true, "on": true, "1": true, "off": false, "0": false, "FALSE": false}
	for v, want := range cases {
		t.Setenv("OCR_FIX", v)
		if got := e.OcrFixEnabled(); got != want {
			t.Errorf("OCR_FIX=%q 得 %v，期望 %v", v, got, want)
		}
	}
}

// TestReconcileIncludesCleanLines clean 行也进 ③（★ 2026-10-29 按新口径**翻转**）。
//
// 旧期望是「clean 行一条都不送」——那正是 `manua` 这类**高置信**错字永远修不掉的根因：
// 模型认得很自信，只是少了个字符，而旧射程只收隔离行。现口径＝所有含字母/汉字的行都送，
// 采纳仍由 `ocrFixDocSupported`（改一字 + 新词在本文件别处出现过）把着，不会把正文改坏。
// 保留测试名会让下一个人按旧口径改回去，故连名带注释一起翻。
func TestReconcileIncludesCleanLines(t *testing.T) {
	e := &Engine{} // 无 LLM：走 no_model 分支，但候选数必须如实反映"该送的都送了"
	doc := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "manual", Final: "manual", Verdict: fileproc.OcrVerdictClean, Conf: 0.99},
		fileproc.OcrLine{Index: 1, Text: "entry", Final: "entry", Verdict: fileproc.OcrVerdictClean, Conf: 0.98},
		// 纯数字/符号行不送（没有可修内容，送进去只会诱模型改写）。
		fileproc.OcrLine{Index: 2, Text: "0", Final: "0", Verdict: fileproc.OcrVerdictClean, Conf: 0.99},
	)
	st := e.ReconcileOcrDoc(context.Background(), doc)
	if st.Candidates != 2 {
		t.Fatalf("含字母的行都要送审（数字/符号行除外），期望 2 实得 %d：%+v", st.Candidates, st)
	}
	if st.Skipped != 1 {
		t.Fatalf("纯数字行应计入 skipped，实得 %d", st.Skipped)
	}
}

// TestOcrFixDocSupported 高置信行的文档内佐证闸：只认「改一字 + 新词在本文件别处出现过」。
func TestOcrFixDocSupported(t *testing.T) {
	doc := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Verdict: fileproc.OcrVerdictClean, Conf: 0.97},
		fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Verdict: fileproc.OcrVerdictClean, Conf: 0.98},
		fileproc.OcrLine{Index: 2, Text: "cat", Final: "cat", Verdict: fileproc.OcrVerdictClean, Conf: 0.97},
	)
	// 现网形态：manua → manual（本文件另有一处 manual 佐证）。
	if !ocrFixDocSupported(doc, "manual", doc.Lines[0]) {
		t.Fatal("manua→manual 有本文件佐证，应当采纳")
	}
	// 无佐证的单字符替换（cat→car）必须拒绝——否则"改一字"就成了改写正文的口子。
	if ocrFixDocSupported(doc, "car", doc.Lines[2]) {
		t.Fatal("cat→car 没有任何本文件佐证，必须拒绝")
	}
	// 改两个词（超出"恰好一个词变了"）必须拒绝。
	doc2 := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "global time", Final: "global time", Verdict: fileproc.OcrVerdictClean, Conf: 0.97},
		fileproc.OcrLine{Index: 1, Text: "global time", Final: "global time", Verdict: fileproc.OcrVerdictClean, Conf: 0.98},
	)
	if ocrFixDocSupported(doc2, "globel tome", doc2.Lines[0]) {
		t.Fatal("一次改两个词必须拒绝")
	}
	// 数字序列变了必须拒绝（0↔O、1↔l 这类）。
	doc3 := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "v1.0", Final: "v1.0", Verdict: fileproc.OcrVerdictClean, Conf: 0.97},
		fileproc.OcrLine{Index: 1, Text: "v1.5", Final: "v1.5", Verdict: fileproc.OcrVerdictClean, Conf: 0.98},
	)
	if ocrFixDocSupported(doc3, "v1.5", doc3.Lines[0]) {
		t.Fatal("数字序列变化必须拒绝")
	}
}

// TestReconcileFailOpenWhenOff 关掉时必须全档 keep，且 low_conf_hard 记存疑读数。
func TestReconcileFailOpenWhenOff(t *testing.T) {
	t.Setenv("OCR_FIX", "off")
	e := &Engine{}
	doc := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Verdict: fileproc.OcrVerdictQuarantine,
			Reason: fileproc.OcrReasonLowConfHard, Conf: 0.4},
		fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Verdict: fileproc.OcrVerdictQuarantine,
			Reason: fileproc.OcrReasonColumnMode, Conf: 0.5, Alts: []string{"manual"}},
	)
	st := e.ReconcileOcrDoc(context.Background(), doc)
	if st.Calls != 0 || st.Reasons["off"] != 2 {
		t.Fatalf("关闭档读数不对：%+v", st)
	}
	for _, ln := range doc.Lines {
		if ln.Final == "" {
			t.Fatalf("fail-open 不许丢行：%+v", ln)
		}
	}
	if st.Unclear != 1 {
		t.Fatalf("low_conf_hard 未修复应记 1 条存疑，得 %d", st.Unclear)
	}
}

// TestApplyOcrFixGates 采纳闸六条：候选命中/改一字符/超距/数字变动/字符集变动/not_text 限定。
func TestApplyOcrFixGates(t *testing.T) {
	e := &Engine{}
	prof := textscript.OfLines([]string{strings.Repeat("manualmanual", 50), "の"})

	// 候选集命中。
	doc := ocrTestDoc(fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua",
		Verdict: fileproc.OcrVerdictUncertain, Reason: fileproc.OcrReasonColumnMode,
		Conf: 0.41, Alts: []string{"manual"}})
	st := OcrReconcileStats{Reasons: map[string]int{}}
	if !e.applyOcrFix(doc, 0, ocrFixResult{Index: 0, Verdict: "fix", Value: "manual"}, &st) {
		t.Fatal("候选集命中应被采纳")
	}
	if doc.Lines[0].Final != "manual" || st.Fixed != 1 {
		t.Fatalf("采纳结果不对：final=%q stats=%+v", doc.Lines[0].Final, st)
	}

	// 新候选：conf<0.6 + 改一个字符。
	doc2 := ocrTestDoc(fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua",
		Verdict: fileproc.OcrVerdictQuarantine, Reason: fileproc.OcrReasonLowConfHard, Conf: 0.4})
	st2 := OcrReconcileStats{Reasons: map[string]int{}}
	if !e.applyOcrFix(doc2, 0, ocrFixResult{Index: 0, Verdict: "fix", Value: "manual"}, &st2) {
		t.Fatal("conf<0.6 且改一个字符应被采纳")
	}

	// 超距（改两个字符）：必须拒绝。
	doc3 := ocrTestDoc(fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua",
		Verdict: fileproc.OcrVerdictQuarantine, Reason: fileproc.OcrReasonLowConfHard, Conf: 0.4})
	st3 := OcrReconcileStats{Reasons: map[string]int{}}
	if e.applyOcrFix(doc3, 0, ocrFixResult{Index: 0, Verdict: "fix", Value: "mxnux"}, &st3) {
		t.Fatal("改两个字符必须拒绝")
	}
	if doc3.Lines[0].Final != "manua" {
		t.Fatalf("拒绝后必须保持原识别：%q", doc3.Lines[0].Final)
	}

	// 数字变动：必须拒绝。
	doc4 := ocrTestDoc(fileproc.OcrLine{Index: 0, Text: "ab1c", Final: "ab1c",
		Verdict: fileproc.OcrVerdictQuarantine, Reason: fileproc.OcrReasonLowConfHard, Conf: 0.4})
	st4 := OcrReconcileStats{Reasons: map[string]int{}}
	if e.applyOcrFix(doc4, 0, ocrFixResult{Index: 0, Verdict: "fix", Value: "ab2c"}, &st4) {
		t.Fatal("数字变动必须拒绝")
	}

	// not_text：只许 glyph_set/geom_anomaly 且长度 ≤2。
	doc5 := ocrTestDoc(fileproc.OcrLine{Index: 0, Text: "の", Final: "の",
		Verdict: fileproc.OcrVerdictQuarantine, Reason: fileproc.OcrReasonGlyphSet, Conf: 0.4})
	doc5.Profile = prof
	st5 := OcrReconcileStats{Reasons: map[string]int{}}
	if !e.applyOcrFix(doc5, 0, ocrFixResult{Index: 0, Verdict: "not_text"}, &st5) {
		t.Fatal("glyph_set 的短行应允许 not_text")
	}
	if doc5.Lines[0].Final != "" || st5.NotText != 1 {
		t.Fatalf("not_text 应清空 Final：%+v", doc5.Lines[0])
	}

	// low_conf_hard 的 not_text 必须拒绝（它不是"判非文字"的证据）。
	doc6 := ocrTestDoc(fileproc.OcrLine{Index: 0, Text: "の", Final: "の",
		Verdict: fileproc.OcrVerdictQuarantine, Reason: fileproc.OcrReasonLowConfHard, Conf: 0.4})
	st6 := OcrReconcileStats{Reasons: map[string]int{}}
	if e.applyOcrFix(doc6, 0, ocrFixResult{Index: 0, Verdict: "not_text"}, &st6) {
		t.Fatal("low_conf_hard 不许直接 not_text")
	}
}

// TestParseOcrFixReply 回执解析：版本/重复 index/围栏/未知 verdict。
func TestParseOcrFixReply(t *testing.T) {
	ok := "```json\n{\"version\":1,\"results\":[{\"index\":0,\"verdict\":\"keep\"}]}\n```"
	if m, err := parseOcrFixReply(ok); err != nil || len(m) != 1 {
		t.Fatalf("应解析成功：m=%v err=%v", m, err)
	}
	if _, err := parseOcrFixReply(`{"version":2,"results":[]}`); err == nil {
		t.Fatal("版本不符必须整份作废")
	}
	if _, err := parseOcrFixReply(`{"version":1,"results":[{"index":0,"verdict":"keep"},{"index":0,"verdict":"fix","value":"x"}]}`); err == nil || err.Error() != "dup_index" {
		t.Fatalf("重复 index 必须整份作废，得 %v", err)
	}
	m, err := parseOcrFixReply(`{"version":1,"results":[{"index":0,"verdict":"bogus"},{"index":1,"verdict":"keep"}]}`)
	if err != nil {
		t.Fatalf("单行非法不应整份作废：%v", err)
	}
	if _, has := m[0]; has {
		t.Fatal("非法 verdict 的行必须作废")
	}
	if _, has := m[1]; !has {
		t.Fatal("合法行必须保留")
	}
}

// TestOcrFixGroupsBudget 每份文件 ≤2 次、每次 ≤60 行，超出部分跳过。
func TestOcrFixGroupsBudget(t *testing.T) {
	pending := make([]int, 0, 130)
	for i := 0; i < 130; i++ {
		pending = append(pending, i)
	}
	groups, skipped := ocrFixGroups(pending)
	if len(groups) != 2 {
		t.Fatalf("130 行应分 2 组，得 %d", len(groups))
	}
	if len(groups[0]) != 60 || len(groups[1]) != 60 || skipped != 10 {
		t.Fatalf("分组/跳过读数不对：%d/%d skipped=%d", len(groups[0]), len(groups[1]), skipped)
	}
	g1, s1 := ocrFixGroups(pending[:50])
	if len(g1) != 1 || s1 != 0 {
		t.Fatalf("50 行应 1 组：%d skipped=%d", len(g1), s1)
	}
}

// TestOcrFixPayloadContract 载荷只带待判行 + 同列统计 + 书写体系占比，不携带图片/整篇原文。
func TestOcrFixPayloadContract(t *testing.T) {
	doc := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "manual", Final: "manual", Verdict: fileproc.OcrVerdictClean, Conf: 0.97,
			Box: []float64{0, 0, 10, 10}, Column: 0},
		fileproc.OcrLine{Index: 1, Text: "manua", Final: "manua", Verdict: fileproc.OcrVerdictUncertain,
			Reason: fileproc.OcrReasonColumnMode, Conf: 0.4, Box: []float64{0, 20, 10, 30}, Column: 0,
			Alts: []string{"manual"}},
	)
	doc.Profile = textscript.OfLines([]string{"manual", "manua"})
	doc.Columns = []fileproc.OcrColumn{{Index: 0, Lines: []int{0, 1}, ModalText: "manual", ModalCount: 1, Valid: true, MedianConf: 0.97}}
	items, err := ocrFixPayload(doc, []int{1})
	if err != nil {
		t.Fatalf("构造载荷失败：%v", err)
	}
	if items.Version != fileproc.OcrDocVersion || len(items.Items) != 1 {
		t.Fatalf("载荷形态不对：%+v", items)
	}
	it := items.Items[0]
	if it.Index != 1 || it.Text != "manua" || it.Reason != fileproc.OcrReasonColumnMode {
		t.Fatalf("待判行字段不对：%+v", it)
	}
	if len(it.Candidates) != 1 || it.Candidates[0] != "manual" {
		t.Fatalf("候选集应透传：%+v", it.Candidates)
	}
	if len(it.Column) == 0 || it.Column[0].Text != "manual" {
		t.Fatalf("同列统计应带出众数：%+v", it.Column)
	}
}

// TestOcrFixAllowanceDerivesFromRealPrompt allowance 必须从真提示词现量（改模板即改预估）。
func TestOcrFixAllowanceDerivesFromRealPrompt(t *testing.T) {
	if strings.TrimSpace(ocrFixPrompt) == "" {
		t.Fatal("提示词常量为空")
	}
	for _, must := range []string{"不翻译", "不许改", "只输出一行 JSON"} {
		if !strings.Contains(ocrFixPrompt, must) {
			t.Errorf("提示词缺少硬约束 %q（改模板必须同步预估 allowance）", must)
		}
	}
	one := OcrFixAllowanceTokens(1)
	two := OcrFixAllowanceTokens(2)
	if one <= 0 || two != 2*one {
		t.Fatalf("allowance 应随文件数线性：1=%d 2=%d", one, two)
	}
	if OcrFixAllowanceTokens(0) != 0 {
		t.Fatal("无图片文件时 allowance 必须为 0（老客户端等价）")
	}
}

// TestOcrFixTimeoutEnv 单次预算的 env 档位：默认 15s，钳 2~60s，非法值回落默认。
func TestOcrFixTimeoutEnv(t *testing.T) {
	cases := map[string]time.Duration{
		"":    15 * time.Second,
		"3":   3 * time.Second,
		"1":   2 * time.Second,
		"99":  60 * time.Second,
		"abc": 15 * time.Second,
	}
	for v, want := range cases {
		t.Setenv("OCR_FIX_TIMEOUT_SEC", v)
		if got := ocrFixCallTimeout(); got != want {
			t.Errorf("OCR_FIX_TIMEOUT_SEC=%q 得 %v，期望 %v", v, got, want)
		}
		if got := ocrFixTotalBudget(); got != 2*want {
			t.Errorf("总预算应为单次两倍：%v vs %v", got, 2*want)
		}
	}
}
