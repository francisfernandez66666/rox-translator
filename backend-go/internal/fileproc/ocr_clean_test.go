package fileproc

import (
	"strings"
	"testing"

	"translator/internal/textscript"
)

// mkLine 构造一行测试数据：box 为 [x0,y0,x1,y1]。
func mkLine(text string, conf float64, box ...float64) OcrLine {
	return OcrLine{Text: text, Conf: conf, Box: box, Final: text, Block: -1, Column: -1}
}

// TestLowConfSplit 置信度三档：≥0.9 clean；[0.6,0.9) soft；<0.6 hard；读数缺失按 soft。
func TestLowConfSplit(t *testing.T) {
	cases := []struct {
		conf    float64
		missing bool
		wantV   string
		wantR   string
	}{
		{0.95, false, OcrVerdictClean, ""},
		{0.90, false, OcrVerdictClean, ""},
		{0.75, false, OcrVerdictUncertain, OcrReasonLowConfSoft},
		{0.60, false, OcrVerdictUncertain, OcrReasonLowConfSoft},
		{0.59, false, OcrVerdictQuarantine, OcrReasonLowConfHard},
		{0, true, OcrVerdictUncertain, OcrReasonLowConfSoft},
	}
	for _, c := range cases {
		v, r := lowConfVerdict(OcrLine{Conf: c.conf, ConfMissing: c.missing})
		if v != c.wantV || r != c.wantR {
			t.Errorf("conf=%v missing=%v：得 (%s,%s)，期望 (%s,%s)", c.conf, c.missing, v, r, c.wantV, c.wantR)
		}
	}
}

// TestColumnModeFixesMinority 同列 20 个 manual + 1 个 manua（低置信）⇒ manua 判 column_mode 并给出候选。
func TestColumnModeFixesMinority(t *testing.T) {
	lines := make([]OcrLine, 0, 21)
	for i := 0; i < 20; i++ {
		y := float64(100 + i*30)
		lines = append(lines, mkLine("manual", 0.97, 100, y, 180, y+20))
	}
	lines = append(lines, mkLine("manua", 0.41, 100, 100+20*30, 180, 100+20*30+20))
	out, _, cols := CleanOcrDoc(lines)
	if len(cols) == 0 {
		t.Fatalf("应聚出列结构：%+v", cols)
	}
	last := out[len(out)-1]
	if last.Verdict != OcrVerdictUncertain || last.Reason != OcrReasonColumnMode {
		t.Fatalf("manua 应判 uncertain/column_mode，得 %s/%s", last.Verdict, last.Reason)
	}
	if len(last.Alts) != 1 || last.Alts[0] != "manual" {
		t.Fatalf("候选应为 [manual]，得 %v", last.Alts)
	}
}

// TestColumnModeGuards 六条护栏：含数字/大小写差异/长度差 2/置信度不低 ⇒ 都不触发。
func TestColumnModeGuards(t *testing.T) {
	base := func() []OcrLine {
		lines := make([]OcrLine, 0, 21)
		for i := 0; i < 20; i++ {
			y := float64(100 + i*30)
			lines = append(lines, mkLine("manual", 0.97, 100, y, 180, y+20))
		}
		return lines
	}
	cases := []struct {
		name string
		text string
		conf float64
	}{
		{"含数字", "manua1", 0.4},
		{"大小写差异", "Manual", 0.4},
		{"长度差 2", "man", 0.4},
		{"置信度不低", "manua", 0.95},
	}
	for _, c := range cases {
		lines := append(base(), mkLine(c.text, c.conf, 100, 100, 180, 120))
		out, _, _ := CleanOcrDoc(lines)
		last := out[len(out)-1]
		if last.Reason == OcrReasonColumnMode {
			t.Errorf("%s：不该判 column_mode（得 %s/%s）", c.name, last.Verdict, last.Reason)
		}
	}
}

// TestGlyphSetQuarantinesLoneKana 英文文档里的孤立「の」⇒ quarantine/glyph_set。
func TestGlyphSetQuarantinesLoneKana(t *testing.T) {
	lines := make([]OcrLine, 0, 21)
	for i := 0; i < 20; i++ {
		y := float64(100 + i*30)
		lines = append(lines, mkLine("manual entry", 0.97, 100, y, 260, y+20))
	}
	lines = append(lines, mkLine("の", 0.41, 300, 100, 320, 120))
	out, prof, _ := CleanOcrDoc(lines)
	if prof.Tier != "single" {
		t.Fatalf("画像应为 single，得 %s", prof.Tier)
	}
	last := out[len(out)-1]
	if last.Verdict != OcrVerdictQuarantine || last.Reason != OcrReasonGlyphSet {
		t.Fatalf("の 应判 quarantine/glyph_set，得 %s/%s", last.Verdict, last.Reason)
	}
}

// TestGlyphSetDisabledInMultiTier 真·多语种档整条关闭（日文混排里的「の」不是异常）。
func TestGlyphSetDisabledInMultiTier(t *testing.T) {
	lines := []OcrLine{
		mkLine("これは日本語の文です", 0.97, 100, 100, 400, 120),
		mkLine("設定を変更します", 0.97, 100, 140, 400, 160),
		mkLine("の", 0.41, 100, 180, 120, 200),
	}
	out, prof, _ := CleanOcrDoc(lines)
	if prof.Tier != "multi" {
		t.Fatalf("画像应为 multi，得 %s（share=%v）", prof.Tier, prof.Share)
	}
	last := out[len(out)-1]
	if last.Reason == OcrReasonGlyphSet {
		t.Fatal("multi 档里不该判 glyph_set")
	}
	if last.Verdict != OcrVerdictQuarantine || last.Reason != OcrReasonLowConfHard {
		t.Fatalf("应回落 low_conf_hard，得 %s/%s", last.Verdict, last.Reason)
	}
}

// TestGeomAnomalyDetectsSwitchLikeBox 开关状小框（宽高比≈1 + 偏离行基线）⇒ geom_anomaly。
// 对照：同基线的正常单字符列不许命中。
func TestGeomAnomalyDetectsSwitchLikeBox(t *testing.T) {
	// 真实表格形态：两个文字列（各 5 行）＋ 夹在两列之间的一枚"开关"小框。
	// 开关：宽高比≈1（命中①）且 x0 对不上任何一列的列缘（命中③）。
	lines := []OcrLine{}
	for i := 0; i < 5; i++ {
		y := float64(100 + i*40)
		lines = append(lines, mkLine("double_bump", 0.97, 100, y, 200, y+20))
		lines = append(lines, mkLine("value", 0.97, 400, y, 500, y+20))
	}
	lines = append(lines, mkLine("の", 0.95, 300, 100, 320, 120))
	out, _, _ := CleanOcrDoc(lines)
	var switchLine OcrLine
	for _, ln := range out {
		if ln.Text == "の" {
			switchLine = ln
		}
	}
	if switchLine.Reason != OcrReasonGeomAnomaly {
		t.Fatalf("开关状小框应判 geom_anomaly，得 %s/%s", switchLine.Verdict, switchLine.Reason)
	}

	// 正常单字符列：同基线、同列缘 ⇒ 只命中"宽高比"一条，不判。
	normal := []OcrLine{
		mkLine("1", 0.97, 100, 100, 120, 120),
		mkLine("2", 0.97, 100, 140, 120, 160),
		mkLine("3", 0.97, 100, 180, 120, 200),
	}
	out2, _, _ := CleanOcrDoc(normal)
	for _, ln := range out2 {
		if ln.Reason == OcrReasonGeomAnomaly {
			t.Fatalf("正常单字符列不该判 geom_anomaly：%+v", ln)
		}
	}
}

// TestDuplicateBoxesKeepHigherConf 重叠框保留高置信度那条，另一条不交付。
func TestDuplicateBoxesKeepHigherConf(t *testing.T) {
	lines := []OcrLine{
		mkLine("manual", 0.95, 100, 100, 200, 130),
		mkLine("manua", 0.40, 102, 101, 201, 131),
	}
	out, _, _ := CleanOcrDoc(lines)
	if out[1].Final != "" || out[1].Reason != OcrReasonDup {
		t.Fatalf("低置信度重叠框应标 dup 且不交付：%+v", out[1])
	}
	if out[0].Final != "manual" {
		t.Fatalf("高置信度那条必须保留：%+v", out[0])
	}
}

// TestCommitOcrDocRebuildsMapping Final 重建映射：空 Final 不进 texts/映射；stale 时不覆盖。
func TestCommitOcrDocRebuildsMapping(t *testing.T) {
	path := "/tmp/ocr-plan-test-commit.png"
	doc := &OcrDoc{Version: OcrDocVersion, Lines: []OcrLine{
		{Text: "manual", Final: "manual", Box: []float64{0, 0, 10, 10}},
		{Text: "の", Final: "", Box: []float64{20, 0, 30, 10}},
		{Text: "total", Final: "total", Box: []float64{0, 20, 10, 30}},
	}}
	texts, st := CommitOcrDoc(path, doc, nil)
	if len(texts) != 2 || texts[0] != "manual" || texts[1] != "total" {
		t.Fatalf("texts 应去掉空 Final：%v", texts)
	}
	if !st.Mapping || st.Dropped != 1 || st.Delivered != 2 {
		t.Fatalf("读数不对：%+v", st)
	}
	boxes := ImageBoxes(path)
	if len(boxes) != 2 {
		t.Fatalf("映射应有 2 条：%+v", boxes)
	}
	// stale：传入不同的 currentTexts ⇒ 不重建、原样返回。
	texts2, st2 := CommitOcrDoc(path, doc, []string{"other"})
	if !st2.Stale || len(texts2) != 1 || texts2[0] != "other" {
		t.Fatalf("stale 保护失效：texts=%v stat=%+v", texts2, st2)
	}
	ReleaseImageBoxes(path)
}

// TestBuildOcrDocMissingConfs 老脚本无 confs ⇒ 全行 ConfMissing，但文本照常交付。
func TestBuildOcrDocMissingConfs(t *testing.T) {
	res := ocrResult{Text: "manual\nentry", Boxes: [][]float64{{0, 0, 10, 10}, {0, 20, 10, 30}}}
	doc := BuildOcrDoc("/tmp/ocr-plan-test-noconf.png", res)
	if len(doc.Lines) != 2 {
		t.Fatalf("行数不对：%d", len(doc.Lines))
	}
	for _, ln := range doc.Lines {
		if !ln.ConfMissing {
			t.Fatalf("无 confs 时必须标 ConfMissing：%+v", ln)
		}
	}
	if got := doc.Texts(); len(got) != 2 || got[0] != "manual" {
		t.Fatalf("Texts 不对：%v", got)
	}
}

// TestOcrDocTextsDedup 去重语义与 Extractor.add 一致（trim、跳过空串、首次出现保留）。
func TestOcrDocTextsDedup(t *testing.T) {
	doc := &OcrDoc{Lines: []OcrLine{
		{Final: " manual "}, {Final: "manual"}, {Final: ""}, {Final: "total"},
	}}
	got := doc.Texts()
	want := []string{"manual", "total"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("去重结果=%v，期望 %v", got, want)
	}
}

// TestTextscriptProfileIntegration 画像与清洗层联通：单语种英文文档的 kana 占比为 0。
func TestTextscriptProfileIntegration(t *testing.T) {
	out, prof, _ := CleanOcrDoc([]OcrLine{
		mkLine("manual", 0.97, 0, 0, 10, 10),
		mkLine("entry", 0.97, 0, 20, 10, 30),
	})
	if len(out) != 2 {
		t.Fatalf("行数不对：%d", len(out))
	}
	if prof.ShareOf(textscript.Katakana) != 0 || prof.ShareOf(textscript.Hiragana) != 0 {
		t.Fatalf("英文文档不该有假名占比：%v", prof.Share)
	}
}
