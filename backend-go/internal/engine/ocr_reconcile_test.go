package engine

import (
	"context"
	"fmt"
	"os"
	"regexp"
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

// TestOcrFixGroupsBudget 每份文件 ≤ocrFixMaxCalls 次、每次 ≤ocrFixMaxLinesPerCall 行，超出部分跳过。
// ★ 2026-10-29：期望值改成**从常量派生**（旧版写死 2 组/60 行，改了批量就整条红而读数指不到哪变）。
func TestOcrFixGroupsBudget(t *testing.T) {
	mk := func(n int) []int {
		p := make([]int, 0, n)
		for i := 0; i < n; i++ {
			p = append(p, i)
		}
		return p
	}
	// 远超上限：组数封顶、每组不超上限、其余记 skipped。
	pending := mk(ocrFixMaxCalls*ocrFixMaxLinesPerCall + 50)
	groups, skipped := ocrFixGroups(pending)
	if len(groups) != ocrFixMaxCalls {
		t.Fatalf("应分 %d 组，得 %d", ocrFixMaxCalls, len(groups))
	}
	for i, g := range groups {
		if len(g) > ocrFixMaxLinesPerCall {
			t.Fatalf("第 %d 组 %d 行，超过单批上限 %d", i, len(g), ocrFixMaxLinesPerCall)
		}
	}
	if skipped != 50 {
		t.Fatalf("超出上限应跳过 50 行，得 %d", skipped)
	}
	// 恰好 2.5 批：3 组、最后一组不满、不跳过。
	partial, s2 := ocrFixGroups(mk(ocrFixMaxLinesPerCall * 5 / 2))
	if len(partial) != 3 || s2 != 0 {
		t.Fatalf("2.5 批应切 3 组且不跳过：%d 组 skipped=%d", len(partial), s2)
	}
	if len(partial[2]) != ocrFixMaxLinesPerCall/2 {
		t.Fatalf("末组应只有半批，得 %d", len(partial[2]))
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
		// ★ 2026-10-29：总预算改为三次单调用（20 行/次 × 3 批的等待封顶）。
		if got := ocrFixTotalBudget(); got != 3*want {
			t.Errorf("总预算应为单次三倍：%v vs %v", got, 3*want)
		}
	}
}

// TestFixDocTruncations 确定性「文档内佐证」修正：治 `manua` 那一族（丢尾字符）。
//
// ★ 为什么必须有不依赖模型的这条路：现网六次运行里 ③ 的模型腿全是 `tokens:0 / timeout`
// （全局模型是翻译模型，答不了这份判定题），而 `manua` 恰是客户最容易看见的那一类错字。
// 本判据只吃"同一个词在文件里写对过"这一份证据，可机械复算。
func TestFixDocTruncations(t *testing.T) {
	cases := []struct {
		name string
		doc  *fileproc.OcrDoc
		want string // 修正后的 Final；空串表示必须保持原样
	}{
		{
			name: "丢尾字母：manua → manual（本文件出现两次）",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Conf: 0.914},
				fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "manual 0.00", Final: "manual 0.00", Conf: 0.99},
			),
			want: "manual",
		},
		{
			name: "丢首字母：anual → manual",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "anual", Final: "anual", Conf: 0.80},
				fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "manual", Final: "manual", Conf: 0.98},
			),
			want: "manual",
		},
		{
			name: "单复数不动：order 有 orders×2 佐证也不改",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "order", Final: "order", Conf: 0.80},
				fileproc.OcrLine{Index: 1, Text: "orders", Final: "orders", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "orders", Final: "orders", Conf: 0.98},
			),
			want: "",
		},
		{
			name: "两个不同佐证词 ⇒ 不猜",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "appleq", Final: "appleq", Conf: 0.70},
				fileproc.OcrLine{Index: 1, Text: "appler", Final: "appler", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "appler", Final: "appler", Conf: 0.98},
				fileproc.OcrLine{Index: 3, Text: "apple", Final: "apple", Conf: 0.98},
				fileproc.OcrLine{Index: 4, Text: "apple", Final: "apple", Conf: 0.98},
			),
			want: "",
		},
		{
			name: "高置信文本不动（conf ≥0.95）",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Conf: 0.99},
				fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "manual", Final: "manual", Conf: 0.98},
			),
			want: "",
		},
		{
			name: "待修词本身出现两次 ⇒ 不是孤立错字，不动",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Conf: 0.80},
				fileproc.OcrLine{Index: 1, Text: "manua", Final: "manua", Conf: 0.80},
				fileproc.OcrLine{Index: 2, Text: "manual", Final: "manual", Conf: 0.98},
				fileproc.OcrLine{Index: 3, Text: "manual", Final: "manual", Conf: 0.98},
			),
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &OcrReconcileStats{Reasons: map[string]int{}}
			rest := fixDocTruncations(c.doc, []int{0}, st)
			got := c.doc.Lines[0].Final
			if c.want == "" {
				if got != "manua" && got != "anual" && got != "order" && got != "appleq" {
					t.Fatalf("不该改，却改成了 %q", got)
				}
				if st.DocFixed != 0 {
					t.Fatalf("DocFixed 应为 0，实得 %d", st.DocFixed)
				}
				if len(rest) != 1 {
					t.Fatalf("未修正的行必须原样送回模型腿，实得 %v", rest)
				}
				return
			}
			if got != c.want {
				t.Fatalf("修正结果 %q，期望 %q", got, c.want)
			}
			if st.DocFixed != 1 || st.Fixed != 1 {
				t.Fatalf("DocFixed/Fixed 应各为 1，实得 %d/%d", st.DocFixed, st.Fixed)
			}
			if len(rest) != 0 {
				t.Fatalf("修好的行不该再送模型，实得 %v", rest)
			}
		})
	}
}

// TestReconcileNoStageModelSkipsModelLeg 没配 `ocr_fix` 阶段模型时，模型腿必须**立刻收手**。
//
// 现网实证：全局主模型是翻译模型（tencent/Hunyuan-MT-7B），它答不了这份判定题——
// 六次运行全是 `calls:3, tokens:0, latency_ms:44998`，每张图白等 45 秒且零收益。
// 新口径是"没配阶段模型就不拨"，并如实记账 `no_stage_model`（不是 timeout，也不是 no_model）。
func TestReconcileNoStageModelSkipsModelLeg(t *testing.T) {
	e := &Engine{} // 无 St/无 LLM：resolveModelForStage 必然回落，stageActive=false
	// 一号夹具：一行确定性腿修不掉的字母行 ⇒ 必须走到模型腿那里并记 no_stage_model。
	doc := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "zzzq", Final: "zzzq", Verdict: fileproc.OcrVerdictClean, Conf: 0.914},
	)
	st := e.ReconcileOcrDoc(context.Background(), doc)
	if st.Calls != 0 {
		t.Fatalf("没有阶段模型时一次都不该拨，实得 calls=%d", st.Calls)
	}
	if st.Reasons["no_stage_model"] == 0 {
		t.Fatalf("必须记 no_stage_model，实得 %v", st.Reasons)
	}
	if st.Reasons["timeout"] != 0 || st.Reasons["no_model"] != 0 {
		t.Fatalf("不许把「没配阶段模型」记成 timeout/no_model，实得 %v", st.Reasons)
	}
	// 二号夹具：确定性腿能修掉的那一行先落地，剩下 2 行仍按 fail-open 记账（不是 timeout）。
	doc2 := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Verdict: fileproc.OcrVerdictClean, Conf: 0.914},
		fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Verdict: fileproc.OcrVerdictClean, Conf: 0.98},
		fileproc.OcrLine{Index: 2, Text: "manual", Final: "manual", Verdict: fileproc.OcrVerdictClean, Conf: 0.98},
	)
	st2 := e.ReconcileOcrDoc(context.Background(), doc2)
	if doc2.Lines[0].Final != "manual" {
		t.Fatalf("确定性腿应把 manua 修成 manual，实得 %q", doc2.Lines[0].Final)
	}
	if st2.DocFixed != 1 {
		t.Fatalf("DocFixed 应为 1，实得 %d", st2.DocFixed)
	}
	if st2.Reasons["no_stage_model"] != 2 || st2.Reasons["timeout"] != 0 {
		t.Fatalf("剩下 2 行应按 no_stage_model 记账（且不得记成 timeout），实得 %v", st2.Reasons)
	}
}

// TestOcrFixCallReasonWords ③ 的失败原因档位名是**对外排障契约**，逐字钉住（AGENTS §13 同族纪律）。
//
// ★ 2026-10-29 新增 `cancelled` 一档并与 `timeout` 分开：现网三次调用只花两千多毫秒却全记 timeout
// （那是上层取消了这一单的 ctx），混档会把人引去"换模型/抬超时"，而真凶在上一层。
func TestOcrFixCallReasonWords(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{{nil, ""},
		{context.DeadlineExceeded, "timeout"},
		{context.Canceled, "cancelled"},
		{fmt.Errorf("wrap: %w", context.DeadlineExceeded), "timeout"},
		{fmt.Errorf("wrap: %w", context.Canceled), "cancelled"},
		{fmt.Errorf("上游 500"), "upstream"},
	}
	for _, c := range cases {
		if got := ocrFixCallReason(c.err); got != c.want {
			t.Errorf("ocrFixCallReason(%v)=%q，期望 %q", c.err, got, c.want)
		}
	}
	// 名单锁：本文件实际写进 reasons 的档位名，必须**恰好**是登记的那几支
	// （多一个＝没登记，少一个＝写了个永远不会出现的档位名）。判据按代码行现扫，不靠注释里那句声明。
	raw, err := os.ReadFile("ocr_reconcile.go")
	if err != nil {
		t.Fatalf("读 ocr_reconcile.go 失败: %v", err)
	}
	got := map[string]bool{}
	for _, m := range regexp.MustCompile(`st\.Reasons\[("([a-z_]+)")\]`).FindAllStringSubmatch(string(raw), -1) {
		got[m[2]] = true
	}
	// dup_index 由 parseOcrFixReply 的 error 文案出（不是下标字面量），所以不在这一份直接名单里。
	want := map[string]bool{"off": true, "no_model": true, "no_stage_model": true, "budget": true, "schema": true, "truncated": true}
	for k := range got {
		if !want[k] {
			t.Errorf("reasons 里长出未登记的直接档位 %q", k)
		}
	}
	for k := range want {
		if !got[k] {
			t.Errorf("登记的直接档位 %q 在代码里找不到了（档位名单与实现漂移）", k)
		}
	}
}
