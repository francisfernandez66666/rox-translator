// ============ 本文件职责中文说明 ============
// P5 表头上下文抽取的闸门锁 —— ★ 2026-10-28 计价与成本优化批（方案 §6.2(f)）。
//
// 为什么这一族必须有机械锁：表头上下文的坏形态**全都不报错**——
//
//	抽错了行（把正文当表头）⇒ 模型拿到一段无关术语，译文用词更歪，界面没有任何异常；
//	抽不到却返回 true ⇒ 每次批量调用都多付几百 token 的固定指令开销，白花钱；
//	截断切在半个 rune 上 ⇒ prompt 里出现非法 UTF-8，模型那边只表现为"这段有乱码"；
//	八值多边形框没归一 ⇒ 这一行归不到任何单元格，表头只剩一半。
//
// 七条腿：首行+首列 / 合并单元格 / 八值框 / 多表只取首张 / 无结构即 false / 按 rune 截断 / env 档位。
// 每条都配一条**反向读数**（多返一个字节、少返一段、切坏一个 rune 都会红），
// 因为这一族的缺陷全在"看起来正常"的那一侧。
// ============================================
package fileproc

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// hdrLine 造一条 OCR 行（Final 即终稿，抽取只读它；Text 是原始识别）。
func hdrLine(text string, box ...float64) OcrLine {
	return OcrLine{Text: text, Final: text, Box: box}
}

// hdrTable 2x3 网格的表头夹具：
//
//	项目 | 金额 | 说明
//	甲方 | 100  | 备注
//
// 坐标刻意让行框（100..120）比单元格（100..120）等高，且列宽不等（90/100/90）：
// 等宽等高的夹具测不出"按交集面积最大归属"这条判据（任何归属方式都会得到同一答案）。
func hdrGrid() StructureTable {
	cell := func(row, col int, x0, y0, x1, y1 float64) StructureCell {
		return StructureCell{Row: row, Col: col, RowSpan: 1, ColSpan: 1,
			Box: [4]float64{x0, y0, x1, y1}}
	}
	return StructureTable{
		Box:  [4]float64{10, 100, 300, 160},
		Rows: 2,
		Cols: 3,
		Cells: []StructureCell{
			cell(0, 0, 10, 100, 90, 120), cell(0, 1, 100, 100, 200, 120), cell(0, 2, 210, 100, 300, 120),
			cell(1, 0, 10, 130, 90, 160), cell(1, 1, 100, 130, 200, 160), cell(1, 2, 210, 130, 300, 160),
		},
	}
}

// hdrGridLines 与 hdrGrid 对应的六条 OCR 行。
func hdrGridLines() []OcrLine {
	return []OcrLine{
		hdrLine("项目", 10, 101, 90, 119),
		hdrLine("金额", 102, 101, 198, 119),
		hdrLine("说明", 212, 101, 298, 119),
		hdrLine("甲方", 10, 131, 90, 159),
		hdrLine("100", 102, 131, 198, 159),
		hdrLine("备注", 212, 131, 298, 159),
	}
}

// ============ L1：首行 + 首列 ============

// TestTableHeaderRowAndColumn 首行按列从左到右、" | " 连接；再拼首列其余行（"；"）。
// 反向读数：正文行（(1,1)、(1,2)）**一个字都不许进**——它们不是表头，
// 混进来等于把客户的数据塞进语境（模型会照着数据猜术语）。
func TestTableHeaderRowAndColumn(t *testing.T) {
	doc := StructureDoc{Tables: []StructureTable{hdrGrid()}}
	got, ok := TableHeaderContext(hdrGridLines(), doc, 400)
	if !ok {
		t.Fatal("完整网格 + 齐备行文本必须抽得出表头，实际 false ⇒ 表头上下文整条腿没生效")
	}
	want := "项目 | 金额 | 说明；甲方"
	if got != want {
		t.Fatalf("表头应为 %q，实际 %q ⇒ 首行/首列口径或分隔符变了", want, got)
	}
	for _, banned := range []string{"100", "备注"} {
		if strings.Contains(got, banned) {
			t.Fatalf("正文单元格 %q 混进了表头上下文（%q）⇒ 模型会照着客户数据猜术语", banned, got)
		}
	}
	// 顺序反向读数：把结构数组整体倒序，输出必须逐字不变（排序不能依赖数组顺序）。
	doc2 := StructureDoc{Tables: []StructureTable{hdrGrid()}}
	cells := doc2.Tables[0].Cells
	for i, j := 0, len(cells)-1; i < j; i, j = i+1, j-1 {
		cells[i], cells[j] = cells[j], cells[i]
	}
	if got2, _ := TableHeaderContext(hdrGridLines(), doc2, 400); got2 != want {
		t.Fatalf("结构数组顺序变了输出就变（%q vs %q）⇒ 排序依赖了数组顺序，真实推理返回序不稳定", got2, want)
	}
}

// ============ L2：合并单元格 ============

// TestTableHeaderMergedCells 跨列合并的首格仍只贡献一段文本（不许按 colspan 重复拼）。
// 重复拼的后果是表头被同一段文本占满，真术语被挤到截断线外面。
func TestTableHeaderMergedCells(t *testing.T) {
	doc := StructureDoc{Tables: []StructureTable{{
		Box:  [4]float64{10, 100, 300, 160},
		Rows: 2, Cols: 3,
		Cells: []StructureCell{
			{Row: 0, Col: 0, RowSpan: 1, ColSpan: 2, Box: [4]float64{10, 100, 200, 120}}, // 合并跨两列
			{Row: 0, Col: 2, RowSpan: 1, ColSpan: 1, Box: [4]float64{210, 100, 300, 120}},
			{Row: 1, Col: 0, RowSpan: 1, ColSpan: 1, Box: [4]float64{10, 130, 200, 160}},
		},
	}}}
	lines := []OcrLine{
		hdrLine("合并表头", 12, 101, 198, 119),
		hdrLine("备注", 212, 101, 298, 119),
		hdrLine("甲方", 12, 131, 198, 159),
	}
	got, ok := TableHeaderContext(lines, doc, 400)
	if !ok {
		t.Fatal("合并单元格形态必须抽得出表头")
	}
	want := "合并表头 | 备注；甲方"
	if got != want {
		t.Fatalf("合并单元格应为 %q，实际 %q ⇒ colspan 被当成两格重复拼了（或首行排序错）", want, got)
	}
	if n := strings.Count(got, "合并表头"); n != 1 {
		t.Fatalf("合并格文本出现 %d 次（应为 1）⇒ 按 colspan 重复拼，真术语会被挤出截断线", n)
	}
}

// ============ L3：八值多边形框 ============

// TestTableHeaderPolygonBox 八值多边形框必须先归一到包围盒才参与归属。
// 不归一的后果：这一行归不到任何单元格 ⇒ 表头少一格，而返回值仍然 ok=true（静默缺一半）。
func TestTableHeaderPolygonBox(t *testing.T) {
	doc := StructureDoc{Tables: []StructureTable{hdrGrid()}}
	lines := hdrGridLines()
	// 把首行首格那条换成逆时针八值多边形（顺序刻意不规整：右下→左上→右上→左下）。
	lines[0].Box = []float64{90, 119, 10, 101, 90, 101, 10, 119}
	got, ok := TableHeaderContext(lines, doc, 400)
	if !ok {
		t.Fatal("八值框形态必须抽得出表头")
	}
	if want := "项目 | 金额 | 说明；甲方"; got != want {
		t.Fatalf("八值多边形框应归一到包围盒后与四值框同值 %q，实际 %q ⇒ 多边形没归一（或按 min/max 之外的假定取点）", want, got)
	}
}

// ============ L4：多表只取首张 ============

// TestTableHeaderOnlyFirstTable 两张表 ⇒ 只取第一张，第二张的文本一个字都不许出现。
// 理由（table_header.go 头注第 3 条）：上下文越长越容易被模型当待译内容，且多表术语本来不共享。
func TestTableHeaderOnlyFirstTable(t *testing.T) {
	second := hdrGrid()
	// 第二张表整体下移 200px，文本换成明显不同的词。
	for i := range second.Cells {
		second.Cells[i].Box[1] += 200
		second.Cells[i].Box[3] += 200
	}
	doc := StructureDoc{Tables: []StructureTable{hdrGrid(), second}}
	lines := hdrGridLines()
	lines = append(lines,
		hdrLine("第二表甲", 10, 301, 90, 319),
		hdrLine("第二表乙", 102, 301, 198, 319),
	)
	got, ok := TableHeaderContext(lines, doc, 400)
	if !ok {
		t.Fatal("多表形态必须抽得出表头（只取第一张，不是放弃）")
	}
	if strings.Contains(got, "第二表") {
		t.Fatalf("第二张表的文本进了上下文（%q）⇒ 只取首张那条没生效", got)
	}
	if want := "项目 | 金额 | 说明；甲方"; got != want {
		t.Fatalf("多表必须只取首张 %q，实际 %q", want, got)
	}
}

// ============ L5：无结构 / 抽不出 ⇒ false ============

// TestTableHeaderAbsentIsFalse 三种"没有上下文"的形态都必须回 ("", false)：
// 没有 Tables、没有 Cells、单元格里没有任何行文本。
//
// 为什么 false 必须与"空串 + true"分开：调用方据 ok 决定要不要注入提示句；
// 空串进提示句会变成"表头为：”"这种**看起来有上下文**的指令，模型会去猜（比没有更坏）。
func TestTableHeaderAbsentIsFalse(t *testing.T) {
	lines := hdrGridLines()
	cases := []struct {
		name string
		doc  StructureDoc
	}{
		{"无结构数据", StructureDoc{}},
		{"有表无单元格", StructureDoc{Tables: []StructureTable{{Rows: 2, Cols: 3}}}},
		{"单元格里没有文字", StructureDoc{Tables: []StructureTable{hdrGrid()}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := lines
			if c.name == "单元格里没有文字" {
				in = []OcrLine{hdrLine("", 10, 101, 90, 119)} // 空文本 ⇒ 抽不出
			}
			got, ok := TableHeaderContext(in, c.doc, 400)
			if ok || got != "" {
				t.Fatalf("%s：应回 (\"\", false)，实际 (%q, %v) ⇒ 调用方会给每次批量调用白付固定开销", c.name, got, ok)
			}
		})
	}
	// 反向对照：同一份 lines 配完整结构必须抽得出来（证明上面三例的 false 不是"整条腿坏了"）。
	if _, ok := TableHeaderContext(lines, StructureDoc{Tables: []StructureTable{hdrGrid()}}, 400); !ok {
		t.Fatal("反向对照失败：完整形态也抽不出 ⇒ 本条锁失去射程")
	}
}

// ============ L6：按 rune 截断 ============

// TestTableHeaderTruncatesByRune 硬截断按**字符**、且不许切出半个 rune（非法 UTF-8 会进 prompt）。
func TestTableHeaderTruncatesByRune(t *testing.T) {
	doc := StructureDoc{Tables: []StructureTable{hdrGrid()}}
	lines := hdrGridLines()
	// 首格换成 10 个汉字：字节数 30，字符数 10 ⇒ 5 字符刚好切在汉字中间（若按字节切必然坏）。
	lines[0] = hdrLine("一二三四五六七八九十", 10, 101, 90, 119)

	got, ok := TableHeaderContext(lines, doc, 5)
	if !ok {
		t.Fatal("截断到 5 字符后仍应有内容（不该整段丢）")
	}
	if !utf8.ValidString(got) {
		t.Fatalf("截断产物不是合法 UTF-8（%q）⇒ 按字节切在汉字中间了，prompt 里会出现乱码", got)
	}
	if n := utf8.RuneCountInString(got); n != 5 {
		t.Fatalf("截断到 5 字符，实际 %d 字符（%q）⇒ maxChars 口径不是 rune 数", n, got)
	}
	if want := "一二三四五"; got != want {
		t.Fatalf("截断应为前缀 %q，实际 %q ⇒ 截断方向/位置错了", want, got)
	}
	// maxChars<=0 视为不截断（上游已按 FILE_HEADER_CONTEXT_MAX_CHARS 钳过，这里只是"没有上限"）。
	if full, _ := TableHeaderContext(lines, doc, 0); !strings.HasPrefix(full, "一二三四五六七八九十") {
		t.Fatalf("maxChars=0 应视为不截断，实际 %q", full)
	}
}

// ============ L7：env 档位 ============

// TestFileHeaderContextEnv 开关只认 on/1/true；上限默认 400、钳 80..2000。
// 反向读数：`yes`/`true `（带空格）之外的任何写法都不许开——"看起来像开"的值会让
// 一次发版在没人察觉的情况下多付几百 token/批。
func TestFileHeaderContextEnv(t *testing.T) {
	on := []string{"on", "1", "true", "TRUE", " on "}
	for _, v := range on {
		t.Setenv("FILE_HEADER_CONTEXT", v)
		if !FileHeaderContextEnabled() {
			t.Fatalf("FILE_HEADER_CONTEXT=%q 应判开启", v)
		}
	}
	for _, v := range []string{"", "off", "0", "false", "no", "yes", "enable"} {
		t.Setenv("FILE_HEADER_CONTEXT", v)
		if FileHeaderContextEnabled() {
			t.Fatalf("FILE_HEADER_CONTEXT=%q 不许判开启（默认关，只认 on/1/true）", v)
		}
	}

	clamp := []struct {
		raw  string
		want int
	}{
		{"", 400},
		{"0", 80},      // 低于下限 ⇒ 下界（"截到只剩一个词"比没有更误导）
		{"79", 80},     // 下界边界
		{"80", 80},     // 下界本身
		{"400", 400},   // 默认值本身
		{"2000", 2000}, // 上界本身
		{"2001", 2000}, // 上界边界
		{"99999", 2000},
		{"abc", 400}, // 非数字 ⇒ 默认（不许静默变 80）
	}
	for _, c := range clamp {
		t.Setenv("FILE_HEADER_CONTEXT_MAX_CHARS", c.raw)
		if got := FileHeaderContextMaxChars(); got != c.want {
			t.Fatalf("FILE_HEADER_CONTEXT_MAX_CHARS=%q 应为 %d，实际 %d", c.raw, c.want, got)
		}
	}
}

// ============ L8：几何尺子与 AssignBlocks 同源 ============

// TestTableHeaderToleranceAbsorbsTallerLineBox 行框比单元格高一点点（行距）时仍要归得到。
//
// 这是实机上的常态：PP-StructureV3 的单元格框是**框线内沿**，而 OCR 行框含行距与字形外沿，
// 于是"面积最大"这一判据在边缘上会算不出交集。0.5×中位行高的容差就是为它准备的；
// 去掉容差 ⇒ 首行归不到 ⇒ 表头只剩首列（ok 仍为 true，静默缺一半）。
func TestTableHeaderToleranceAbsorbsTallerLineBox(t *testing.T) {
	doc := StructureDoc{Tables: []StructureTable{hdrGrid()}}
	lines := hdrGridLines()
	// 首行三条整体上移 6px：与单元格（y0=100）只剩 0.5*18=9px 容差能救回来。
	for i := 0; i < 3; i++ {
		lines[i].Box[1] -= 6
		lines[i].Box[3] -= 6
	}
	got, ok := TableHeaderContext(lines, doc, 400)
	if !ok {
		t.Fatal("行框略高于单元格时必须靠容差归得到（否则实机上表头永远缺首行）")
	}
	if want := "项目 | 金额 | 说明；甲方"; got != want {
		t.Fatalf("容差未生效：应 %q，实际 %q", want, got)
	}
	// 反向读数：移得太远（超过容差）就必须**归不到**，不许无限放宽——
	// 否则一行正文会被吸进表头（这是"容差写成无界"时的静默坏形态）。
	lines2 := hdrGridLines()
	for i := 0; i < 3; i++ {
		lines2[i].Box[1] -= 40
		lines2[i].Box[3] -= 40
	}
	if got2, _ := TableHeaderContext(lines2, doc, 400); strings.Contains(got2, "项目") {
		t.Fatalf("超出容差的行不该被吸进表头（实际 %q）⇒ 容差无界，正文会混进上下文", got2)
	}
}
