package engine

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/llm"
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
	// ★ 2026-10-29 傍晚补的第二态（现网实证）：`OCR_FIX=off` 只关**模型腿**。
	// 上面那份夹具刻意让佐证词只出现一次（确定性腿的判据要 ≥2），所以它测不到这一态；
	// 这里补一份"文档自带证据"的：关掉开关后 `manua` 仍必须被修成 `manual`——
	// 旧顺序把这条零 token 的腿排在开关之后，现网 `manua` 就是这么复发的，而屏幕上一条读数都指不到开关。
	e2 := &Engine{}
	doc2 := ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Verdict: fileproc.OcrVerdictQuarantine,
			Reason: fileproc.OcrReasonLowConfHard, Conf: 0.4},
		fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Conf: 0.9},
		fileproc.OcrLine{Index: 2, Text: "manual", Final: "manual", Conf: 0.9},
	)
	st2 := e2.ReconcileOcrDoc(context.Background(), doc2)
	if st2.DocFixed != 1 || doc2.Lines[0].Final != "manual" {
		t.Fatalf("off 档把确定性佐证腿一起关了：doc_fixed=%d 第0行=%q（应为 1 / manual）", st2.DocFixed, doc2.Lines[0].Final)
	}
	if st2.Calls != 0 {
		t.Fatalf("off 档一次模型都不许拨，实得 calls=%d", st2.Calls)
	}
	if st2.Reasons["off"] != 2 {
		t.Fatalf("off 读数只该记**剩下**的待判行（佐证腿已修掉的那行不该再算），实得 %+v", st2.Reasons)
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
		why  string // 失败信息里要能读出病因（变异体按它判红）
	}{
		{
			name: "丢尾字母：manua → manual（本文件出现两次）",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Conf: 0.914},
				fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "manual 0.00", Final: "manual 0.00", Conf: 0.99},
			),
			want: "manual",
			why:  "低置信丢尾字母必须修",
		},
		{
			name: "丢首字母：anual → manual",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "anual", Final: "anual", Conf: 0.80},
				fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "manual", Final: "manual", Conf: 0.98},
			),
			want: "manual",
			why:  "丢首字母必须修",
		},
		{
			name: "单复数不动：order 有 orders×2 佐证也不改",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "order", Final: "order", Conf: 0.80},
				fileproc.OcrLine{Index: 1, Text: "orders", Final: "orders", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "orders", Final: "orders", Conf: 0.98},
			),
			want: "",
			why:  "单复数不许被 +1 字符启发式改写",
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
			why:  "两个不同佐证不许猜",
		},
		{
			// ★ 2026-10-29 第十轮翻转：旧口径「conf ≥0.95 不动」正是 `manua` 修不掉的入口。
			//   翻转依据＝`truncationFix` 的结构护栏与置信度无关；高置信错字恰恰是现网主流形态。
			name: "高置信也修（conf ≥0.95，但有文档内佐证）",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Conf: 0.99},
				fileproc.OcrLine{Index: 1, Text: "manual", Final: "manual", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "manual", Final: "manual", Conf: 0.98},
			),
			want: "manual",
			why:  "高置信错字必须进确定性腿",
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
			why:  "待修词出现两次不许改",
		},
		{
			name: "数字插入不动（高置信）",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "nshape1", Final: "nshape1", Conf: 0.99},
				fileproc.OcrLine{Index: 1, Text: "nshape12", Final: "nshape12", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "nshape12", Final: "nshape12", Conf: 0.98},
			),
			want: "",
			why:  "数字插入不许当错字修",
		},
		{
			name: "高置信但大写标签不动（Close 有 Closed×2 也不改）",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "Close", Final: "Close", Conf: 0.99},
				fileproc.OcrLine{Index: 1, Text: "Closed", Final: "Closed", Conf: 0.98},
				fileproc.OcrLine{Index: 2, Text: "Closed", Final: "Closed", Conf: 0.98},
			),
			want: "",
			why:  "大写标签不许被 +1 字符启发式改写",
		},
		{
			name: "高置信但无佐证不动",
			doc: ocrTestDoc(
				fileproc.OcrLine{Index: 0, Text: "manua", Final: "manua", Conf: 0.99},
				fileproc.OcrLine{Index: 1, Text: "other", Final: "other", Conf: 0.98},
			),
			want: "",
			why:  "无佐证不许改",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := &OcrReconcileStats{Reasons: map[string]int{}}
			rest := fixDocTruncations(c.doc, []int{0}, st)
			got := c.doc.Lines[0].Final
			// ★ 反向断言（方案 §3.5）：未命中档位的行必须与源文**逐字相等**——
			//   防 truncationFix 返回整行重写时顺手改动别的词。
			for i := 1; i < len(c.doc.Lines); i++ {
				if c.doc.Lines[i].Final != c.doc.Lines[i].Text {
					t.Fatalf("%s：未命中档位的行被顺带改写（line %d: %q → %q）",
						c.why, i, c.doc.Lines[i].Text, c.doc.Lines[i].Final)
				}
			}
			if c.want == "" {
				// ★ 2026-10-29 第十一轮：期望值＝与源文**逐字相等**（旧写法是四个字面量的白名单，
				//   新增档位会被它判成"改了"＝判据自伤）。
				if got != c.doc.Lines[0].Text {
					t.Fatalf("%s（不该改的档：%q → %q）", c.why, c.doc.Lines[0].Text, got)
				}
				if st.DocFixed != 0 {
					t.Fatalf("%s：DocFixed 应为 0，实得 %d", c.why, st.DocFixed)
				}
				if len(rest) != 1 {
					t.Fatalf("%s：未修正的行必须原样送回模型腿，实得 %v", c.why, rest)
				}
				return
			}
			if got != c.want {
				t.Fatalf("%s（修正结果 %q，期望 %q）", c.why, got, c.want)
			}
			if st.DocFixed != 1 || st.Fixed != 1 {
				t.Fatalf("%s：DocFixed/Fixed 应各为 1，实得 %d/%d", c.why, st.DocFixed, st.Fixed)
			}
			if len(rest) != 0 {
				t.Fatalf("%s：修好的行不该再送模型，实得 %v", c.why, rest)
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
	want := map[string]bool{"off": true, "no_model": true, "no_stage_model": true, "budget": true, "schema": true, "truncated": true, "backoff": true}
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

// TestOcrFixModelLegBreakerEnv 熔断两档 env 的默认与钳位（档位本身就是判据的一部分）。
func TestOcrFixModelLegBreakerEnv(t *testing.T) {
	if got := ocrFixFailStreak(); got != 3 {
		t.Fatalf("默认连续零产出轮数应为 3，实得 %d", got)
	}
	if got := ocrFixBackoff(); got != 600*time.Second {
		t.Fatalf("默认冷却应为 600s，实得 %v", got)
	}
	cases := map[string][2]int{ // env → {streak 读数, backoff 秒读数}
		"0":   {1, 600},
		"99":  {10, 600},
		"abc": {3, 600},
		"2":   {2, 600},
	}
	for v, want := range cases {
		t.Setenv("OCR_FIX_FAIL_STREAK", v)
		if got := ocrFixFailStreak(); got != want[0] {
			t.Errorf("OCR_FIX_FAIL_STREAK=%q 得 %d，期望 %d", v, got, want[0])
		}
	}
	for v, want := range map[string]int{"": 600, "1": 1, "0": 1, "99999": 3600, "xyz": 600} {
		t.Setenv("OCR_FIX_BACKOFF_SEC", v)
		if got := ocrFixBackoff(); got != time.Duration(want)*time.Second {
			t.Errorf("OCR_FIX_BACKOFF_SEC=%q 得 %v，期望 %ds", v, got, want)
		}
	}
}

// TestOcrFixModelLegBreakerTripsAndRecovers 熔断的**进与出**都要跑一次。
//
// 只测"进"的那批锁是本仓反复栽过的形态：一条只有进没有出的熔断＝把一次抖动钉成永久关闭
// （AGENTS §一·5 那条「失败不写状态，不许把一次网络抖动钉成这个语种永远翻不了」同一族）。
// 冷却下界刻意放到 1s，所以"到期自己再试"这一条能被真实跑到，而不是靠 sleep 或靠读注释。
func TestOcrFixModelLegBreakerTripsAndRecovers(t *testing.T) {
	t.Setenv("OCR_FIX_FAIL_STREAK", "2")
	t.Setenv("OCR_FIX_BACKOFF_SEC", "1")
	e := &Engine{}
	e.ocrFixNoteOutcome(context.Background(), 0)
	if e.ocrFixBreaker().IsOpen() {
		t.Fatal("一轮零产出就熔断＝把偶发抖动当故障")
	}
	e.ocrFixNoteOutcome(context.Background(), 0)
	if !e.ocrFixBreaker().IsOpen() {
		t.Fatal("连续两轮零产出必须熔断（否则每张图继续白等满 45s 预算）")
	}
	// 成功一次即清零并关窗（判据只看 tokens：模型真答了才会有 usage 读数）。
	e.ocrFixNoteOutcome(context.Background(), 512)
	if e.ocrFixBreaker().IsOpen() {
		t.Fatal("有产出后仍处熔断＝把修好的模型继续押在冷却里")
	}
	// 到期自动半开：再走两轮零产出、等过冷却窗口，必须自己放开。
	e2 := &Engine{}
	e2.ocrFixNoteOutcome(context.Background(), 0)
	e2.ocrFixNoteOutcome(context.Background(), 0)
	if !e2.ocrFixBreaker().IsOpen() {
		t.Fatal("夹具没进熔断，下面那条'到期'判据无从谈起")
	}
	time.Sleep(1100 * time.Millisecond)
	if e2.ocrFixBreaker().IsOpen() {
		t.Fatal("冷却到期必须放开再试一次（只有进没有出的熔断是第二起事故）")
	}
}

// TestOcrFixHealthWords 健康面四个状态词各自对应的真形态，逐档点名。
//
// `backoff`（等）与 `degraded`（去配）必须分得开——两者动作不同：
// 混成一个词，读的人会对着"该去配模型"的现场抬超时，或对着"模型暂时不答"的现场去改配置。
func TestOcrFixHealthWords(t *testing.T) {
	e := &Engine{Cfg: &config.Config{}, LLM: &llm.Client{}}
	t.Setenv("OCR_FIX", "off")
	if got := e.OcrFixHealth(context.Background()); got != "off" {
		t.Fatalf("kill-switch 关着必须报 off，实得 %q", got)
	}
	t.Setenv("OCR_FIX", "")
	t.Setenv("OCR_FIX_FAIL_STREAK", "1")
	e2 := &Engine{Cfg: &config.Config{}, LLM: &llm.Client{}}
	e2.ocrFixNoteOutcome(context.Background(), 0)
	if got := e2.OcrFixHealth(context.Background()); got != "backoff" {
		t.Fatalf("熔断窗口内必须报 backoff（动作是等），实得 %q", got)
	}
	// 没配 ocr_fix 阶段卡片（含 St==nil）＝degraded（动作是去配），**不许**报 ok。
	e3 := &Engine{Cfg: &config.Config{}, LLM: &llm.Client{}}
	if got := e3.OcrFixHealth(context.Background()); got != "degraded" {
		t.Fatalf("没配 ocr_fix 阶段模型必须报 degraded，实得 %q", got)
	}
}

// TestOcrFixBreakerWiredAtGuard 熔断判据必须在**守卫入口**被真调用（判据级代替不了守卫级，AGENTS §三）。
//
// 只测 `ocrFixBreaker()`/`ocrFixNoteOutcome()` 本身的语义，把这两行从 ReconcileOcrDoc 里摘掉它完全无感——
// 表现就是"熔断单测全绿、现网照旧每张图白等 45 秒"。⇒ 按代码行现扫调用点。
func TestOcrFixBreakerWiredAtGuard(t *testing.T) {
	code := ocrReconcileCodeLines(t)
	for _, must := range []string{
		"if cooling := e.ocrFixCooling(); cooling {",      // 拨之前先问窗口（唯一读点）
		"e.ocrFixNoteOutcome(ctx, st.Tokens)",             // 拨过之后必须记这一笔
		`st.Reasons["backoff"]`,                           // 窗口内如实出声，不许静默
		"b, k, m, stageActive := e.ocrFixStageModel(ctx)", // 只认这一档自己配的模型（两处共用一个助手）
	} {
		if !strings.Contains(code, must) {
			t.Errorf("③ 模型腿守卫入口缺少 %q（熔断/档位判据被摘掉时不会有任何症状）", must)
		}
	}
	// 两处读点必须共用那两个助手，不许在任一侧再造一次直调
	// （"health 说 ok、实际根本没拨"就是两处各判一次的产物）。
	for _, must := range []string{
		"if e.ocrFixCooling() {", // OcrFixHealth 那一侧
		"func (e *Engine) ocrFixCooling() bool",
		"func (e *Engine) ocrFixStageModel(",
	} {
		if !strings.Contains(code, must) {
			t.Errorf("③ 的单一读点缺失 %q", must)
		}
	}
	// 反向：不许再走 resolveModelForStage 那条回落链（它会把 ai_initial／全局翻译模型算成"配好了"）。
	if strings.Contains(code, "resolveModelForStage(ctx, config.StageOcrFix)") {
		t.Error("③ 又吃回落链了：`ai_initial`／全局模型被算成 ocr_fix 配好了（现网 tokens=0 全 timeout 而 health 报 ok 的机理）")
	}
}

// ocrReconcileCodeLines 读本文件并剥掉 Go 注释（判据只许落在代码行上，
// 否则说明注释里那句"这里不许再走回落链"会自己把负向锁喂饱）。
func ocrReconcileCodeLines(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("ocr_reconcile.go")
	if err != nil {
		t.Fatalf("读 ocr_reconcile.go 失败: %v", err)
	}
	out := make([]string, 0, 512)
	for _, ln := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

// TestOcrFixSkipsModelLegWhileCooling 冷却窗口内**行为级**不拨，并同刻跑一条反向对照。
//
// 为什么必须两条一起：只测"进冷却就不拨"，判据会被一条根本不生效的守卫喂绿
// （摘掉 `IsOpen()` 那一档，测试看到的仍是 calls=0——因为夹具压根没配阶段模型）。
// 这里用**真配了 ocr_fix 阶段卡片**的库，指到一个必然秒退的连接（127.0.0.1:1），
// 于是"没拨"与"拨了但失败"两个读数能分开：冷却里 calls=0，对照里 calls>0 且记 upstream。
func TestOcrSkipsModelLegWhileCoolingBehaviour(t *testing.T) {
	st := newTestStore(t)
	setStageModels(t, st, config.StageModels{
		config.StageOcrFix: {Provider: "ocr_fix", APIBase: "http://127.0.0.1:1/v1", APIKey: "sk-cooling-probe", Model: "cooling/Probe"},
	})
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	t.Setenv("OCR_FIX", "")
	t.Setenv("OCR_FIX_FAIL_STREAK", "1")
	t.Setenv("OCR_FIX_BACKOFF_SEC", "60")

	cooling := &Engine{St: st, Cfg: cfg, LLM: llm.NewClient(cfg)}
	cooling.ocrFixNoteOutcome(context.Background(), 0) // 先制造一轮零产出 ⇒ 进冷却
	if !cooling.ocrFixCooling() {
		t.Fatal("夹具没能进入冷却窗口，下面的判据无从谈起")
	}
	got := cooling.ReconcileOcrDoc(context.Background(), ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "zzzq", Final: "zzzq", Verdict: fileproc.OcrVerdictClean, Conf: 0.90},
	))
	if got.Calls != 0 {
		t.Fatalf("冷却窗口内一次都不该拨，实得 calls=%d reasons=%v", got.Calls, got.Reasons)
	}
	if got.Reasons["backoff"] != 1 {
		t.Fatalf("必须记 backoff＝1（静默不拨＝把冷却写成没毛病），实得 %v", got.Reasons)
	}
	if got.Fallback != 1 {
		t.Fatalf("冷却里的行必须按 fail-open keep 交付，实得 fallback=%d", got.Fallback)
	}
	// 反向对照：同一份配置、没进冷却 ⇒ 必须真拨一次（否则上面那条绿灯是空转）。
	probe := &Engine{St: st, Cfg: cfg, LLM: llm.NewClient(cfg)}
	got2 := probe.ReconcileOcrDoc(context.Background(), ocrTestDoc(
		fileproc.OcrLine{Index: 0, Text: "zzzq", Final: "zzzq", Verdict: fileproc.OcrVerdictClean, Conf: 0.90},
	))
	if got2.Calls == 0 {
		t.Fatal("对照失败：没进冷却时这条腿一次都没被拨 ⇒ 上面『calls=0』不是熔断的功劳，判据射程是空的")
	}
	if got2.Reasons["backoff"] != 0 {
		t.Fatalf("对照组不该出现 backoff，实得 %v", got2.Reasons)
	}
}

// TestOcrFixNoThinkingWired ③「写死不推理」的形状、接线，以及**不许长出开关**这一条。
//
// 用户明令：这一腿不留"换了模型再复开推理"的空间。所以除了正向判形状与接线，
// 还要有一条负向锁钉住"这里没有 env 开关"——否则下一位遇到某家供应商回 400 时，
// 最省事的动作就是顺手加一个 `OCR_FIX_THINKING`，那条腿就会悄悄退回思考模式，
// 而症状还是老样子（tokens=0 全 timeout），没人会想到是开关被谁打开了。
func TestOcrFixNoThinkingWired(t *testing.T) {
	code := ocrReconcileCodeLines(t)
	// 形状：只发一个键，值就是 disabled。
	got := ocrFixNoThinking()
	if len(got) != 1 {
		t.Fatalf("③ 的私有字段只该有 thinking 一个键，实得 %v", got)
	}
	th, ok := got["thinking"].(map[string]interface{})
	if !ok || th["type"] != "disabled" {
		t.Fatalf("thinking 必须是 {\"type\":\"disabled\"}，实得 %v", got["thinking"])
	}
	// 接线：必须挂在这次调用的 ctx 上，且 CallChat 用的是**挂过的** ctx。
	for _, must := range []string{
		"callCtx := llm.WithChatExtraParams(llm.WithInteractive(llm.WithoutFileMode(cctx)), ocrFixNoThinking())",
		"e.LLM.CallChat(callCtx, b, k, m,",
	} {
		if !strings.Contains(code, must) {
			t.Errorf("③ 的调用点缺少 %q（注入了但没挂到这次调用的 ctx 上＝死码，现网零症状）", must)
		}
	}
	// 负向：这一腿不许出现"用 env 决定要不要关思考"的形态。
	for _, banned := range []string{`Getenv("OCR_FIX_THINKING")`, `Getenv("OCR_FIX_DISABLE_THINKING")`, `Getenv("OCR_FIX_REASONING")`} {
		if strings.Contains(code, banned) {
			t.Errorf("③ 长出了复开推理的开关 %s ⇒ 用户明令不留这一档（关了它这条腿就退回白等 45 秒的老形态）", banned)
		}
	}
	// ③ 必须**不进文件池**（★ 2026-10-29 第三次归因）：排在 fileChatSem(6) 后面时，
	// 图片件自己那批翻译把池占满（实测 waited=18.17s/inflight=6）⇒ 15s 预算在队列里就耗光，
	// 请求根本没到模型。这一条与"关思维链"是两件事，缺哪一件这条腿都不动。
	for _, must := range []string{"llm.WithoutFileMode(cctx)", "llm.WithInteractive("} {
		if !strings.Contains(code, must) {
			t.Errorf("③ 又回到文件池里排队了（缺 %q）⇒ 每枪 15s 预算会在队列里耗光，模型压根收不到请求", must)
		}
	}
	// 只有 ③ 这一腿关思考：审校/补译那两腿是真需要推理的，不许顺手一起关。
	// 数的是**调用形态**而不是裸函数名——函数定义那一行也含函数名，按裸词数会把定义算成调用点（本仓老坑）。
	if n := strings.Count(code, "llm.WithChatExtraParams("); n != 1 {
		t.Errorf("关思考的注入点应为 1 处（只有 ③），实得 %d 处 ⇒ 别的腿被顺手关了，那是拿质量换延迟", n)
	}
}
