// ============ 本文件职责中文说明 ============
// 图片原位回填两条「画/不画」与「改/不改」的机械锁（★ 2026-10-29 用户第二次报障批）。
//
// 两条都是现网实证、且都属于「有降级链的静默失败」那一族（产物能打开、零报错，客户拿到的是坏图）：
//
//	① 开关被画成 `0`：OCR 把开/关控件读成 `0`/`O`/`√`/`2`（实测置信度 0.06~0.39），
//	   而回填层对「没有译文」这一支的修法本来就是**把识别出的原文画回去**——
//	   上一批只做了「不翻译」（engine.RenderSafeTranslations 里摘掉那个键），
//	   键一缺席，这里立刻按"保留原文"把 `0` 盖到开关上。⇒ 跳过翻译 ≠ 跳过绘制。
//	② 修正被丢掉：CommitOcrDoc 的"不同源保护"拿调用方的 texts 与 ③ 改完的 Final 比，
//	   而 ③ 的每一次采纳都必然让两者不等 ⇒ 判 Stale ⇒ 返回旧文本、不重建映射。
//	   现网表现就是「manua 换几版都修不掉」。基准必须是 ② 完成时的快照 doc.Base。
//
// 五条腿（判据函数两侧各喂一次真件与变异件，证明它不是空转，AGENTS §三 那条纪律）：
//
//	L1 判据行为：单字符非汉字 ⇒ 不画；单字中文/多位数字 ⇒ 照旧。
//	L2 接线形态：绘制循环里"整框跳过 + Dropped 计数"必须排在"保留原文 + Residue"之前，
//	   且脚本自报的 dropped 必须**累加**不许覆盖（覆盖＝把整框跳过那一档抹掉）。
//	L3 修正落地：③ 改过的 Final 必须进 texts 与映射（旧形态在这一步整份退回）。
//	L4 反向对照：别的链路真换过文本时仍必须 Stale、不重建映射（只测 L3 会诱使人删掉保护）。
//	L5 射程自证：把 L2 的跳过腿从**临时副本**里摘掉必须判红——不碰仓库真件。
//
// ============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
)

// TestIdentityOnlyPredicate L1：判据本体（单字符且非汉字 ⇒ 不画）。
//
// 现网实测的开关误读全在这一档：`0` `O` `2` `4` `√` `。` `D` `1` `の`；
// 而单字中文（`买` `是`）是可译正文，必须照常回填——这一支放错方向就是把正文摘掉。
func TestIdentityOnlyPredicate(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"0", true}, {"O", true}, {"2", true}, {"√", true}, {"。", true}, {"の", true}, {"D", true}, {" 1 ", true},
		{"买", false}, {"是", false}, // 单字中文＝正文，照常回填
		{"", false}, {"ab", false}, {"0.00", false}, {"manual", false},
		{"1003", false}, // 多位数字是表格正文（成交笔数那一列），不在"图形误读"这一档
	}
	for _, c := range cases {
		if got := IsIdentityOnlyText(c.in); got != c.want {
			t.Errorf("IsIdentityOnlyText(%q)=%v，期望 %v", c.in, got, c.want)
		}
	}
}

// identityPaintGateRed 问"绘制循环的接线形态对不对"，返回一句红话（空串＝绿）。
//
// 提成纯函数是为了 L5：同一把尺子喂真件与变异件，才能证明红灯来自腿被摘、而不是判据写死。
func identityPaintGateRed(src string) string {
	skip := strings.Index(src, "IsIdentityOnlyText(b.Text)")
	drop := strings.Index(src, "stats.Dropped++")
	residue := strings.Index(src, "stats.Residue++")
	fallback := strings.Index(src, "t = b.Text")
	if skip < 0 {
		return "绘制循环里没有整框跳过腿（开关会被识别值直接画回图上）"
	}
	if drop < 0 {
		return "跳过腿没有计入 Dropped（客户会收到一条指向不存在问题的『未译出』质量提示）"
	}
	if residue < 0 || fallback < 0 {
		return "绘制循环里『未译出保留原文』那一支不见了（真正漏翻的行会被抹成空洞）"
	}
	if !(skip < drop && drop < residue) {
		return "跳过/Dropped 必须排在保留原文之前，否则符号框会先被算成未译出"
	}
	if !strings.Contains(src, "stats.Dropped += parseImageDropped") {
		return "脚本自报的 dropped 必须累加而不是覆盖（覆盖＝整框跳过那一档被抹掉）"
	}
	return ""
}

// TestRenderSkipsIdentityBoxesBeforeResidue L2：真件必须绿。
func TestRenderSkipsIdentityBoxesBeforeResidue(t *testing.T) {
	src := readCodeLines(t, "ocr_image.go")
	if msg := identityPaintGateRed(src); msg != "" {
		t.Fatal(msg)
	}
}

// TestIdentityPaintGateHasTeeth L5：射程自证——摘掉跳过腿必须让同一条判据判红。
//
// 反证走的是**临时副本**（内存里替换字符串），不改仓库文件：这条链最容易的假绿是
// "判据只问字符串在不在"，而那串字面在说明注释里也出现过，所以判据先剥注释、再按代码行点位比顺序。
func TestIdentityPaintGateHasTeeth(t *testing.T) {
	src := readCodeLines(t, "ocr_image.go")
	// 变异 1：整框跳过被摘掉（回到"没译文就画原文"那一支）。
	m1 := strings.Replace(src, "if IsIdentityOnlyText(b.Text) {", "if false {", 1)
	if m1 == src {
		t.Fatal("夹具空转：找不到可摘的跳过腿那一行")
	}
	if msg := identityPaintGateRed(m1); msg == "" {
		t.Fatal("摘掉跳过腿后判据仍判绿＝闸门失明")
	}
	// 变异 2：dropped 改回覆盖写法（整框跳过那一档被脚本读数抹掉）。
	m2 := strings.Replace(src, "stats.Dropped += parseImageDropped", "stats.Dropped = parseImageDropped", 1)
	if m2 == src {
		t.Fatal("夹具空转：找不到累加那一行")
	}
	if msg := identityPaintGateRed(m2); msg == "" {
		t.Fatal("把累加改回覆盖后判据仍判绿＝闸门失明")
	}
	// 正向对照：真件必须仍然绿（否则上面两条红是判据坏，不是变异有效）。
	if msg := identityPaintGateRed(src); msg != "" {
		t.Fatalf("真件判红＝判据写错：%s", msg)
	}
}

// TestCommitOcrDocAppliesFixesOnBaseline L3：③ 采纳的修正必须真正落地。
//
// 旧形态拿 currentTexts 与 finalTexts 比 ⇒ 任何一次采纳都自触发 Stale ⇒ 修正被整份退回。
func TestCommitOcrDocAppliesFixesOnBaseline(t *testing.T) {
	base := []string{"manua", "manual"}
	doc := &OcrDoc{Version: OcrDocVersion, Base: base, Lines: []OcrLine{
		{Index: 0, Text: "manua", Final: "manual", Box: []float64{0, 0, 40, 12}},
		{Index: 1, Text: "manual", Final: "manual", Box: []float64{0, 30, 40, 42}},
	}}
	got, st := CommitOcrDoc("/tmp/fp-identity-commit.png", doc, base)
	if st.Stale {
		t.Fatalf("③ 的修正被判成不同源（现网 manua 修不掉的机理）：%+v", st)
	}
	if len(got) != 1 || got[0] != "manual" {
		t.Fatalf("送翻文本应是修正后的 [manual]，实得 %v", got)
	}
	if !st.Mapping {
		t.Fatal("行↔框映射必须按修正后的 Final 重建")
	}
	boxes := ImageBoxes("/tmp/fp-identity-commit.png")
	if len(boxes) != 2 || boxes[0].Text != "manual" {
		t.Fatalf("映射里的键应是修正后的文本（否则译文找不到框），实得 %+v", boxes)
	}
}

// TestCommitOcrDocStaleWhenTextsReplacedByOtherChain L4：反向对照——别的链路真换过文本时必须 Stale。
//
// 这条是 L3 的存在理由的另一半：只测"修正要落地"会诱使下一个人把不同源保护整个删掉。
// 判据问的是「currentTexts 与 ② 的快照等不等」，不是「与 Final 等不等」。
func TestCommitOcrDocStaleWhenTextsReplacedByOtherChain(t *testing.T) {
	base := []string{"manua", "manual"}
	doc := &OcrDoc{Version: OcrDocVersion, Base: base, Lines: []OcrLine{
		{Index: 0, Text: "manua", Final: "manual", Box: []float64{0, 0, 40, 12}},
		{Index: 1, Text: "manual", Final: "manual", Box: []float64{0, 30, 40, 42}},
	}}
	replaced := []string{"一整段别的链路换来的文本"}
	got, st := CommitOcrDoc("/tmp/fp-identity-stale.png", doc, replaced)
	if !st.Stale {
		t.Fatal("文本被别的链路换掉时必须判 Stale（否则回填会按旧坐标盖错位置）")
	}
	if len(got) != 1 || got[0] != replaced[0] {
		t.Fatalf("Stale 时必须原样退回调用方的 texts，实得 %v", got)
	}
	if st.Mapping {
		t.Fatal("Stale 时不许重建映射")
	}
}

// TestBuildOcrDocSnapshotsBaseline 基线快照本身必须在 ② 之后落一次（没有它 L3 的判据无从谈起）。
func TestBuildOcrDocSnapshotsBaseline(t *testing.T) {
	res := ocrResult{Text: "manua\nmanual\nmanual", Boxes: [][]float64{
		{0, 0, 40, 12}, {0, 30, 40, 42}, {0, 60, 40, 72},
	}, Confs: []float64{0.914, 0.98, 0.98}}
	doc := BuildOcrDoc("/tmp/fp-identity-build.png", res)
	if doc.Base == nil {
		t.Fatal("BuildOcrDoc 没有落下 ② 之后的文本快照（Commit 会退回拿 Final 当基准的旧缺陷）")
	}
	if strings.Join(doc.Base, "|") != "manua|manual" {
		t.Fatalf("快照应是去重后的交付文本，实得 %v", doc.Base)
	}
}

// readCodeLines 读一个随包文件并剥掉 Go 注释（整行注释与行内 `// ` 尾巴）。
// 判负锁必须先剥注释：说明注释里会写下被禁止的形态（"开关被画成 0"这类），
// 不剥就是把自己在解释缺陷的那段话判成缺陷（AGENTS §一·5 那族「注释冒充实现」）。
func readCodeLines(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败: %v", name, err)
	}
	out := make([]string, 0, 256)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		if i := strings.Index(line, "// "); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
