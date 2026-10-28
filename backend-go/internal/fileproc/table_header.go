// ============ table_header.go · 职责说明 ============
// P5：表头上下文抽取（★ 2026-10-28 计价与成本优化批，方案 §6.2）。
//
// 目的只有一个：让**同一张表里的术语**前后一致。文件链是逐段翻的（每段一次调用），
// 模型看不到别的行，于是同一列里的同一个词会在一页内出现两三种译法，
// 客户拿到的是"每行单看都对、整表对不上"。把表头（首行 + 首列）作为**语境**送进
// system 前缀，是这一族最便宜的修法：一次文件单只多几百 token，换来整表术语一致。
//
// 四条硬口径（改这块前必读）：
//  1. **只作语境、不进待译内容**：表头文本只挂 system 前缀；挂进 user 会被纯 MT 模型
//     当成"也要翻的一段"（同 batchContextNote 用英文写的理由）。
//  2. **抽不出就是没有**：无结构数据 / 没表格 / 单元格里没有文字 ⇒ ("", false)，
//     调用方按"没有上下文"继续，**不降级、不报错、不记 warning**（这是可选增强，不是判据）。
//  3. **只取第一张表**：上下文越长越容易被模型当成待译内容，而且多表之间术语本来就不共享。
//     其余表的数量由调用方记日志读数（`tables=N`），本函数保持纯函数——它被单测直接调用，
//     里面夹一个 logger 会让"抽不出"与"抽出但没记日志"混在一起。
//  4. **截断按 rune**：`maxChars` 是字符数不是字节数，切在半个 rune 上会往 prompt 里塞非法 UTF-8
//     （模型那边表现为"这段有乱码"而不是报错）。
//
// 几何口径：行→单元格按**交集面积最大**归属（一行只进一个单元格）；行框四值/八值一律先归一到
// 包围盒（与 AssignBlocks、clusterRows 同一把尺子）；单元格上/下各放 0.5×中位行高的容差，
// 用来吸收"行框比单元格高一点点"这种常态（PP-StructureV3 的单元格框是框线内沿，行框含行距）。
// ==========================================
package fileproc

import (
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// FileHeaderContextEnabled 表头上下文是否启用：只有显式 on/1/true 才开（默认关）。
// 为什么默认关：它会给每次批量调用加几百 token 的前缀，且只在**表格类文档**上有效——
// 先让它在真件上量一轮（预估与实扣同尺，见 HeaderContextAllowanceTokens），再决定默认值。
func FileHeaderContextEnabled() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("FILE_HEADER_CONTEXT")))
	return v == "on" || v == "1" || v == "true"
}

// FileHeaderContextMaxChars 表头上下文的字符上限：默认 400，钳在 80..2000。
// 上限的理由：超长表头会把正文挤出 output 预算（首行 20 列的宽表很容易上千字符），
// 而下限 80 是为了"截到只剩一个词"这种退化形态不出现——那种上下文比没有更误导。
func FileHeaderContextMaxChars() int {
	const def, lo, hi = 400, 80, 2000
	v := strings.TrimSpace(os.Getenv("FILE_HEADER_CONTEXT_MAX_CHARS"))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

// normBox4 把四值矩形或八值多边形归一到包围盒 [x0,y0,x1,y1]；不足四点回 ok=false。
// ⚠️ 不在这里做"排序/纠正"：坐标本身可能是乱序的（八值多边形顺时针/逆时针都合法），
// 所以取 min/max 而不是假定 [x0,y0,x1,y1] 已有序。
func normBox4(b []float64) ([4]float64, bool) {
	if len(b) < 4 {
		return [4]float64{}, false
	}
	x0, y0 := math.Inf(1), math.Inf(1)
	x1, y1 := math.Inf(-1), math.Inf(-1)
	for i := 0; i+1 < len(b); i += 2 {
		x0 = math.Min(x0, b[i])
		x1 = math.Max(x1, b[i])
		y0 = math.Min(y0, b[i+1])
		y1 = math.Max(y1, b[i+1])
	}
	if math.IsInf(x0, 1) {
		return [4]float64{}, false
	}
	return [4]float64{x0, y0, x1, y1}, true
}

// medianLineHeight 现算行高中位数（容差尺子的基准）。
// 用中位数而不是均值：一页里通常有标题（高）与脚注（矮），均值会被极端值带偏，
// 而"半个中位行高"这个容差档正是 clusterRows 用的同一把尺子。
func medianLineHeight(lines []OcrLine) float64 {
	hs := make([]float64, 0, len(lines))
	for _, l := range lines {
		if b, ok := normBox4(l.Box); ok {
			if h := b[3] - b[1]; h > 0 {
				hs = append(hs, h)
			}
		}
	}
	if len(hs) == 0 {
		return 0
	}
	sort.Float64s(hs)
	return hs[len(hs)/2]
}

// overlapArea 两个包围盒的交集面积（无交集回 0）。
func overlapArea(a, b [4]float64) float64 {
	w := math.Min(a[2], b[2]) - math.Max(a[0], b[0])
	h := math.Min(a[3], b[3]) - math.Max(a[1], b[1])
	if w <= 0 || h <= 0 {
		return 0
	}
	return w * h
}

// truncateRunes 按 rune 硬截断（不切半个字符）；maxChars<=0 视为不截。
func truncateRunes(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	rs := []rune(s)
	if len(rs) <= maxChars {
		return s
	}
	return string(rs[:maxChars])
}

// headerCellText 取一个表格单元格里归属行的文本。
// 归属判据 = 与单元格（上下各放 0.5×中位行高容差）的**交集面积最大**且 >0；
// 同分时取文档里先出现的那一行（阅读顺序在前）。取 Final（清洗/补全后的终稿），
// 空则退 Text —— 表头可能落在 quarantine 行上，那行 Final 为空但仍是一次识别结果，
// 拿它进语境是安全的（语境不进交付物，只影响模型用词）。
func headerCellText(lines []OcrLine, cell [4]float64, tol float64) string {
	expanded := [4]float64{cell[0], cell[1] - tol, cell[2], cell[3] + tol}
	best, bestArea := -1, 0.0
	for i, l := range lines {
		b, ok := normBox4(l.Box)
		if !ok {
			continue
		}
		a := overlapArea(b, expanded)
		if a > bestArea {
			best, bestArea = i, a
		}
	}
	if best < 0 {
		return ""
	}
	if t := strings.TrimSpace(lines[best].Final); t != "" {
		return t
	}
	return strings.TrimSpace(lines[best].Text)
}

// TableHeaderContext 从结构 + OCR 行里抽表头（首行 + 首列）文本。
// 参数：lines=OCR 行（含框与终稿文本），doc=结构数据（Tables 为空即"没有结构"），
// maxChars=硬截断字符数（≤0 视为不截）。
// 返回：(header, true)；无结构数据/无表格/抽不出即 ("", false)——调用方按"没有上下文"继续。
//
// 返回形态：首行各单元格文本用 " | " 连接，再拼首列（首列各单元格用 "；" 连接，两段之间也用 "；"）。
// 例：`项目 | 金额 | 说明；甲方；合同编号`。
func TableHeaderContext(lines []OcrLine, doc StructureDoc, maxChars int) (string, bool) {
	if len(doc.Tables) == 0 || len(lines) == 0 {
		return "", false
	}
	t := doc.Tables[0] // 只取第一张（理由见文件头第 3 条）
	if len(t.Cells) == 0 {
		return "", false
	}
	tol := medianLineHeight(lines) * 0.5

	// 逐格取文本（同一个单元格只取一次交集最大的那一行）。
	type cellTxt struct {
		row, col int
		text     string
	}
	cells := make([]cellTxt, 0, len(t.Cells))
	minRow, minCol := math.MaxInt32, math.MaxInt32
	for _, c := range t.Cells {
		if c.Row < minRow {
			minRow = c.Row
		}
		if c.Col < minCol {
			minCol = c.Col
		}
		cells = append(cells, cellTxt{row: c.Row, col: c.Col, text: headerCellText(lines, c.Box, tol)})
	}

	rowParts := make([]string, 0, len(cells))
	colParts := make([]string, 0, len(cells))
	// 按 (row, col) 排序，保证"首行从左到右""首列从上到下"不依赖结构数组的顺序。
	sort.SliceStable(cells, func(i, j int) bool {
		if cells[i].row != cells[j].row {
			return cells[i].row < cells[j].row
		}
		return cells[i].col < cells[j].col
	})
	for _, c := range cells {
		if c.text == "" {
			continue
		}
		if c.row == minRow {
			rowParts = append(rowParts, c.text)
		}
		if c.col == minCol && c.row != minRow {
			colParts = append(colParts, c.text)
		}
	}
	if len(rowParts) == 0 && len(colParts) == 0 {
		return "", false
	}
	out := strings.Join(rowParts, " | ")
	if len(colParts) > 0 {
		if out != "" {
			out += "；"
		}
		out += strings.Join(colParts, "；")
	}
	out = truncateRunes(out, maxChars)
	if strings.TrimSpace(out) == "" {
		return "", false
	}
	return out, true
}
