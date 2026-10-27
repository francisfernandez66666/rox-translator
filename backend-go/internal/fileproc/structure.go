// ============ structure.go · 职责说明 ============
// 结构保真（S1 几何重建 / S2 PP-StructureV3）的 Go 侧落点。
//
// 两条腿分工（主方案 §6 与《接入方案_结构保真》）：
//   - 几何重建（本文件，零依赖）：行聚类 + 列聚类 + 规则网格判定，产出 OcrLine.Column
//     与 OcrColumn（清洗层的 column_mode / geom_anomaly 直接消费它）；
//   - PP-StructureV3（structure_v3.go）：只取版面/表格几何，不取识别文本，产出 Block 与真单元格。
//
// 三条硬口径：
//  1. 结构只**加强**判据、不放松任何阈值；没有结构数据时 Column/Block 一律 -1，清洗层自然退化；
//  2. 行数守恒：AssignBlocks 必须返回与输入等长的归属数组，未归属用 -1（不许"少了几行没人知道"）；
//  3. 列聚类与 ocr_recognize.py 的阅读顺序用**同一把尺子**（容差 0.5×当前行中位行高），
//     两处各写一份就迟早出现「同一行被框成两行」的错位。
package fileproc

import (
	"math"
	"sort"
	"strings"
)

// StructureBlock 一个版面块：只有标签与几何，**不携带文本**（结构腿不参与识别）。
type StructureBlock struct {
	Label string     // title/paragraph/table/figure/header/footer/formula/seal/…
	Box   [4]float64 // 原图像素 [x0,y0,x1,y1]
	Order int        // 阅读顺序（0 起连续序号）
}

// StructureCell 表格单元格（含合并跨度）。S2 才产出；S1 恒为空。
type StructureCell struct {
	Row, Col         int
	RowSpan, ColSpan int
	Box              [4]float64
}

// StructureTable 表格结构（与 Blocks 里 label=table 的那一项按 Box 等值关联）。
type StructureTable struct {
	Box   [4]float64
	Rows  int
	Cols  int
	Cells []StructureCell
	HTML  string // 仅诊断留痕，不直接进交付
}

// StructureDoc 一份文件的版面结构。空值（Blocks 为空）表示"没有结构数据"，不是错误。
type StructureDoc struct {
	Blocks []StructureBlock
	Tables []StructureTable
	Engine string // onnx / paddle / ""（未启用）
	Reason string // 降级原因码：unavailable/timeout/bad_input/empty
}

// rowCluster 一次行聚类的结果：每个框的行号（0 起，按阅读顺序）与每行的框索引。
type rowCluster struct {
	rowOf []int
	rows  [][]int
}

// clusterRows 按 y 中心滚动聚行（容差 = 0.5×当前行中位行高）。
//
// 与 ocr_recognize.py 的 _order_boxes 同一把尺子：那边决定"送翻顺序"，这里决定"哪几个框算同一行"。
// 两处的容差若不一致，清洗层的同列众数就会拿错同列行集合（表现为"偶尔漏纠一个 manua"）。
func clusterRows(lines []OcrLine) rowCluster {
	n := len(lines)
	out := rowCluster{rowOf: make([]int, n)}
	idx := make([]int, 0, n)
	for i := range lines {
		if len(lines[i].Box) >= 4 {
			idx = append(idx, i)
		} else {
			out.rowOf[i] = -1
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ca := (lines[idx[a]].Box[1] + lines[idx[a]].Box[3]) / 2
		cb := (lines[idx[b]].Box[1] + lines[idx[b]].Box[3]) / 2
		if ca != cb {
			return ca < cb
		}
		return lines[idx[a]].Box[0] < lines[idx[b]].Box[0]
	})
	cur := []int{}
	flush := func() {
		if len(cur) == 0 {
			return
		}
		rid := len(out.rows)
		row := append([]int{}, cur...)
		sort.SliceStable(row, func(a, b int) bool { return lines[row[a]].Box[0] < lines[row[b]].Box[0] })
		out.rows = append(out.rows, row)
		for _, i := range row {
			out.rowOf[i] = rid
		}
		cur = cur[:0]
	}
	for _, i := range idx {
		b := lines[i].Box
		cy := (b[1] + b[3]) / 2
		if len(cur) == 0 {
			cur = append(cur, i)
			continue
		}
		// 当前行中位行高与中位中心：容差跟着这一行走，而不是全局固定值。
		hs := make([]float64, 0, len(cur))
		cs := make([]float64, 0, len(cur))
		for _, j := range cur {
			jb := lines[j].Box
			hs = append(hs, math.Max(1, jb[3]-jb[1]+1))
			cs = append(cs, (jb[1]+jb[3])/2)
		}
		sort.Float64s(hs)
		sort.Float64s(cs)
		h := hs[len(hs)/2]
		med := cs[len(cs)/2]
		if math.Abs(cy-med) <= 0.5*h {
			cur = append(cur, i)
		} else {
			flush()
			cur = append(cur, i)
		}
	}
	flush()
	return out
}

// ClusterColumns 几何列聚类：1D 投影切列 → 每列统计众数/中位置信度。
//
// 返回列集合与逐行归属（与 lines 等长，-1 表示未聚出列）。
// 前置（任一不成立即"无列结构"，全部 -1）：
//   - 至少 2 列；
//   - 每列至少 2 个框。
//
// 这是「不许猜」的一条：单列文档、错位件、拍照倾斜件都可能切不出列——
// 那时 column_mode 与 geom_anomaly 的第③条自动关闭，而不是拿一条错列去做归一。
func ClusterColumns(lines []OcrLine) ([]OcrColumn, []int) {
	n := len(lines)
	assign := make([]int, n)
	for i := range assign {
		assign[i] = -1
	}
	type iv struct {
		i      int
		x0, x1 float64
	}
	ivs := make([]iv, 0, n)
	heights := make([]float64, 0, n)
	for i := range lines {
		b := lines[i].Box
		if len(b) < 4 {
			continue
		}
		ivs = append(ivs, iv{i, b[0], b[2]})
		heights = append(heights, math.Max(1, b[3]-b[1]+1))
	}
	if len(ivs) < 4 {
		return nil, assign
	}
	sort.Float64s(heights)
	medH := heights[len(heights)/2]
	gapMin := 0.8 * medH
	sort.SliceStable(ivs, func(a, b int) bool { return ivs[a].x0 < ivs[b].x0 })

	// 投影切列：把排序后的 x 区间按"间隙 ≥0.8×中位行高"切开。
	type band struct{ lo, hi float64 }
	var bands []band
	for _, v := range ivs {
		if len(bands) == 0 {
			bands = append(bands, band{v.x0, v.x1})
			continue
		}
		last := &bands[len(bands)-1]
		if v.x0-last.hi >= gapMin {
			bands = append(bands, band{v.x0, v.x1})
			continue
		}
		if v.x1 > last.hi {
			last.hi = v.x1
		}
	}
	// 注意：单 band 不是失败——"一整列重复的单元格"就是单列文档，交给下面的有效列口径判。
	// 归属：按框中心落在哪个 band；中心落在 band 外（重叠区）时按重叠宽度最大者。
	colOf := make([]int, n)
	for i := range colOf {
		colOf[i] = -1
	}
	for _, v := range ivs {
		b := lines[v.i].Box
		cx := (b[0] + b[2]) / 2
		best, bestOv := -1, 0.0
		for ci, bd := range bands {
			lo, hi := math.Max(b[0], bd.lo), math.Min(b[2], bd.hi)
			ov := hi - lo
			if cx >= bd.lo && cx <= bd.hi {
				ov += medH // 中心在带内优先
			}
			if ov > bestOv {
				best, bestOv = ci, ov
			}
		}
		colOf[v.i] = best
	}
	// 统计每列：框数、众数文本、第二众数、中位置信度。
	counts := make([]map[string]int, len(bands))
	confs := make([][]float64, len(bands))
	for i := range lines {
		c := colOf[i]
		if c < 0 {
			continue
		}
		if counts[c] == nil {
			counts[c] = map[string]int{}
		}
		if t := strings.TrimSpace(lines[i].Text); t != "" {
			counts[c][t]++
		}
		if !lines[i].ConfMissing {
			confs[c] = append(confs[c], lines[i].Conf)
		}
	}
	cols := make([]OcrColumn, len(bands))
	for ci := range bands {
		cols[ci] = OcrColumn{Index: ci, Valid: false}
		mode, modeN, secondN := "", 0, 0
		for t, k := range counts[ci] {
			if k > modeN || (k == modeN && t < mode) {
				secondN = modeN
				mode, modeN = t, k
			} else if k > secondN && t != mode {
				secondN = k
			}
		}
		cols[ci].ModalText = mode
		cols[ci].ModalCount = modeN
		cols[ci].SecondCount = secondN
		if len(confs[ci]) > 0 {
			sort.Float64s(confs[ci])
			cols[ci].MedianConf = confs[ci][len(confs[ci])/2]
			cols[ci].Valid = true
		}
	}
	// 列内成员与"每列至少 2 个框"的前置。
	members := make([]int, len(bands))
	for i := range lines {
		if colOf[i] >= 0 {
			members[colOf[i]]++
			cols[colOf[i]].Lines = append(cols[colOf[i]].Lines, i)
		}
	}
	// 有效列的口径（主方案 §6.2 的前置）：
	//   · 多列文档：至少 2 个"每列 ≥2 框"的列；成员不足 2 的带保留但不参与判据（Valid=false）；
	//   · 单列文档：该列至少 3 个框且**纵向不重叠**（一列单元格），否则视为"没有列结构"。
	// 单列也算列结构很重要：一整列重复的 manual 是最容易纠正的一类，不该因为"只有一列"关掉。
	if len(bands) >= 2 {
		valid := 0
		for ci := range cols {
			if members[ci] >= 2 {
				valid++
			} else {
				cols[ci].Valid = false
			}
		}
		if valid < 2 {
			return nil, assign
		}
	} else {
		if members[0] < 3 || !mostlyNoOverlap(lines, cols[0].Lines) {
			return nil, assign
		}
	}
	for i := range lines {
		assign[i] = colOf[i]
	}
	return cols, assign
}

// AssignBlocks 把 OCR 行按**行框中心点**归到块；一行只能属于一个块。
//
// 冲突（中心点落在两个块的重叠区）按「中心点在块内 + 块面积更小者优先」；
// 仍冲突则归先出现的块并计 conflict++（进读数，不静默丢行）。
// 硬判据：len(assign) == len(lines)，未归属一律 -1（主方案口径 5：每行必有终态）。
func AssignBlocks(lines []OcrLine, doc StructureDoc) (assign []int, conflict int) {
	assign = make([]int, len(lines))
	for i := range assign {
		assign[i] = -1
	}
	if len(doc.Blocks) == 0 {
		return assign, 0
	}
	for i := range lines {
		b := lines[i].Box
		if len(b) < 4 {
			continue
		}
		cx := (b[0] + b[2]) / 2
		cy := (b[1] + b[3]) / 2
		best, bestArea := -1, math.Inf(1)
		for bi, blk := range doc.Blocks {
			if cx < blk.Box[0] || cx > blk.Box[2] || cy < blk.Box[1] || cy > blk.Box[3] {
				continue
			}
			area := (blk.Box[2] - blk.Box[0]) * (blk.Box[3] - blk.Box[1])
			if best < 0 || area < bestArea {
				best, bestArea = bi, area
				continue
			}
			if area == bestArea {
				conflict++
			}
		}
		assign[i] = best
	}
	return assign, conflict
}

// mostlyNoOverlap 判断一组框是否"纵向基本不重叠"（同一列的单元格形态）。
// 判据：按 y0 排序后相邻两框的重叠高度 >50% 较小行高的比例 ≤20%。
func mostlyNoOverlap(lines []OcrLine, idx []int) bool {
	ys := make([]int, 0, len(idx))
	for _, i := range idx {
		if len(lines[i].Box) >= 4 {
			ys = append(ys, i)
		}
	}
	if len(ys) < 2 {
		return false
	}
	sort.SliceStable(ys, func(a, b int) bool { return lines[ys[a]].Box[1] < lines[ys[b]].Box[1] })
	bad := 0
	for k := 1; k < len(ys); k++ {
		a := lines[ys[k-1]].Box
		b := lines[ys[k]].Box
		ha := math.Max(1, a[3]-a[1]+1)
		hb := math.Max(1, b[3]-b[1]+1)
		ov := math.Min(a[3], b[3]) - math.Max(a[1], b[1]) + 1
		if ov > 0.5*math.Min(ha, hb) {
			bad++
		}
	}
	return float64(bad)/float64(len(ys)-1) <= 0.2
}
