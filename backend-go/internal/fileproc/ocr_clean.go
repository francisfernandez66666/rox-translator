// ============ ocr_clean.go · 职责说明 ============
// OCR 清洗层（主方案 §7）：把识别结果标成 clean/uncertain/quarantine 三档，**只降级不删除**。
//
// 为什么必须"用上下文"而不是"加黑名单"（现网实证）：把「单个假名＝噪声」写成全局规则会误杀
// 日文正文；不写就漏（换任何误识字形照样漏）。本层问的是「这一份文档本来有没有这个书写体系」
// （画像）与「同列已经有了 20 个 manual」（众数）——两条都不依赖具体字符，也不依赖词典。
//
// 六条判据、三种闭环位置：
//   - frag：① 内闭环（ocr_recognize.py 的同行碎片合并），本层看到的是合并后的框；
//   - dup：本层前置去重（IoU>0.5 保留高置信度那条，另一条 quarantine/dup、不交付）；
//   - low_conf / column_mode / glyph_set / geom_anomaly：本层判档，前两条进 ③，后两条进 ③。
//
// 三条硬口径：
//  1. 只降级、不删除：quarantine 的行仍交给 ③，由 ③ 明确判 not_text 才丢弃；
//  2. 优先序固定：dup → column_mode → glyph_set → geom_anomaly → low_conf，Reason 只记第一条命中；
//  3. 任何异常都降级为"全 clean"（BuildOcrDoc 的 recover），清洗层 bug 不许把整单判死。
package fileproc

import (
	"math"
	"sort"
	"strings"

	"translator/internal/textscript"
)

// 三档 Verdict（对外契约：engine 的 ③ 按这三档分派，逐字钉在单测里）。
const (
	OcrVerdictClean      = "clean"
	OcrVerdictUncertain  = "uncertain"
	OcrVerdictQuarantine = "quarantine"
)

// 原因码（单值，按优先序取第一条命中）。这七个是**对外排障契约**，改字必须同步单测与文档。
const (
	OcrReasonLowConfSoft = "low_conf_soft"
	OcrReasonLowConfHard = "low_conf_hard"
	OcrReasonColumnMode  = "column_mode"
	OcrReasonGlyphSet    = "glyph_set"
	OcrReasonGeomAnomaly = "geom_anomaly"
	OcrReasonDup         = "dup"
	OcrReasonFrag        = "frag"
)

// 置信度分档（G4 口径修正后才有可比性；P0-B 之后按现网分布复核定档）。
const (
	ocrLowConfCleanMin = 0.9 // ≥0.9 判 clean
	ocrLowConfSoftMin  = 0.6 // [0.6,0.9) uncertain；<0.6 quarantine
)

// CleanOcrDoc 对一组行跑完整清洗；返回清洗后的行、全篇画像与列聚类结果。
//
// 输入会被**复制**：调用方的 Text 原值不受影响（不变式：Text 永远可追溯）。
func CleanOcrDoc(in []OcrLine) ([]OcrLine, textscript.Profile, []OcrColumn) {
	lines := make([]OcrLine, len(in))
	copy(lines, in)
	texts := make([]string, len(lines))
	for i := range lines {
		lines[i].Text = strings.TrimSpace(lines[i].Text)
		lines[i].Final = lines[i].Text
		lines[i].Verdict = OcrVerdictClean
		lines[i].Reason = ""
		lines[i].Alts = nil
		lines[i].Block = -1
		texts[i] = lines[i].Text
	}
	prof := textscript.OfLines(texts)
	cols, assign := ClusterColumns(lines)
	rows := clusterRows(lines)
	dup := duplicateSet(lines)
	for i := range lines {
		lines[i].Column = assign[i]
		if dup[i] {
			// 重复框：保留高置信度那条，另一条不交付（它不是"判非文字"，只是同一个东西被框了两次）。
			lines[i].Verdict = OcrVerdictQuarantine
			lines[i].Reason = OcrReasonDup
			lines[i].Final = ""
			continue
		}
		baseVerdict, baseReason := lowConfVerdict(lines[i])
		// 优先序：有证据的纠错 > 书写体系异常 > 几何异常 > 单纯低置信。
		if alt, ok := columnModeAlt(lines, i, cols); ok {
			lines[i].Verdict = OcrVerdictUncertain
			lines[i].Reason = OcrReasonColumnMode
			lines[i].Alts = []string{alt}
			continue
		}
		if glyphSetHit(lines[i], prof) {
			lines[i].Verdict = OcrVerdictQuarantine
			lines[i].Reason = OcrReasonGlyphSet
			continue
		}
		if geomAnomalyHit(lines, i, rows, cols) {
			lines[i].Verdict = OcrVerdictQuarantine
			lines[i].Reason = OcrReasonGeomAnomaly
			continue
		}
		lines[i].Verdict = baseVerdict
		lines[i].Reason = baseReason
	}
	return lines, prof, cols
}

// lowConfVerdict 置信度分档。
//
// ConfMissing 一律按 uncertain 处理：读数缺失不等于可信，但也**绝不判死**——
// 拿"没有读数"当"很低"会在老脚本/老权重上把整篇打成 quarantine。
func lowConfVerdict(ln OcrLine) (string, string) {
	if ln.ConfMissing {
		return OcrVerdictUncertain, OcrReasonLowConfSoft
	}
	if ln.Conf >= ocrLowConfCleanMin {
		return OcrVerdictClean, ""
	}
	if ln.Conf >= ocrLowConfSoftMin {
		return OcrVerdictUncertain, OcrReasonLowConfSoft
	}
	return OcrVerdictQuarantine, OcrReasonLowConfHard
}

// columnModeAlt 同列众数纠正：返回候选写法与是否命中。
//
// 判据（全部成立才命中，任一不成立即本条关闭）：
//   - 有列结构、众数出现 ≥3 次、且没有第二个势均力敌的众数（≥3 次）；
//   - 本行是**长度 ≥4 的纯 ASCII 单词**，且不含数字（1003→1008 这类一律不碰）；
//   - 本行在列内只出现 1~2 次（只纠"少数派"）；
//   - 本行置信度低于同列中位（读数缺失不比，直接不纠）；
//   - 与众数大小写不同不算错（Manual/manual 是有意排版），全角半角不做归一；
//   - 长度差 ≤1 且编辑距离 ≤1。
func columnModeAlt(lines []OcrLine, i int, cols []OcrColumn) (string, bool) {
	ln := lines[i]
	col := ln.Column
	if col < 0 || col >= len(cols) {
		return "", false
	}
	c := cols[col]
	if !c.Valid || c.ModalCount < 3 || c.SecondCount >= 3 {
		return "", false
	}
	if !isASCIIWord(ln.Text) || hasASCIIDigit(ln.Text) {
		return "", false
	}
	mode := c.ModalText
	if !isASCIIWord(mode) || hasASCIIDigit(mode) {
		return "", false
	}
	if mode == ln.Text || strings.EqualFold(mode, ln.Text) {
		return "", false
	}
	if ln.ConfMissing || ln.Conf >= c.MedianConf {
		return "", false
	}
	if n := countInColumn(lines, col, ln.Text); n < 1 || n > 2 {
		return "", false
	}
	if absInt(len([]rune(mode))-len([]rune(ln.Text))) > 1 {
		return "", false
	}
	if !withinOneEdit(ln.Text, mode) {
		return "", false
	}
	return mode, true
}

// glyphSetHit 书写体系异常：孤立、非本文书写体系的短行。
//
// 只看"全文本来没有这个书写体系，而它只以孤立短字出现"——**不看列内多数**：
// 日文天生「汉字＋假名＋拉丁」混排，任何"与列内多数不同＝噪声"的判据在混编文档里必然批量误伤。
// 真·多语种档整条关闭（textscript.Profile.IsRare 已把这一档编码进去）。
func glyphSetHit(ln OcrLine, prof textscript.Profile) bool {
	if !ln.ConfMissing && ln.Conf >= ocrLowConfCleanMin {
		return false // 高置信不判：宁可漏，不许误杀（丢真实内容的代价远高于多一行噪声）
	}
	return textscript.IsLoneForeign(ln.Text, prof)
}

// geomAnomalyHit 几何异常：语言无关的强信号，三档全开。
//
// 前提：本行长度 ≤2 个字符；三条几何里命中 ≥2 条才判：
//
//	① 宽高比 ≈1（0.6~1.7）；
//	② 框中心偏离同一行（无行信息时退同列）的行基线中位 > 0.6×行高；
//	③ 框与同列其它框的列缘 x0 对齐率 <80%（本实现用"偏离列缘中位 > 0.5×列中位行高"等价表达）。
//
// 为什么只在长度 ≤2 时生效：整句/单词行天然会命中①②，那是排版不是噪声。
func geomAnomalyHit(lines []OcrLine, i int, rows rowCluster, cols []OcrColumn) bool {
	ln := lines[i]
	b := ln.Box
	if len(b) < 4 {
		return false
	}
	if len([]rune(strings.TrimSpace(ln.Text))) > 2 {
		return false
	}
	h := math.Max(1, b[3]-b[1]+1)
	w := math.Max(1, b[2]-b[0]+1)
	hits := 0
	if ar := w / h; ar >= 0.6 && ar <= 1.7 {
		hits++
	}
	// ② 行基线：同行的其它框（无则退同列）。
	var mates []int
	if rid := rows.rowOf[i]; rid >= 0 && rid < len(rows.rows) {
		for _, j := range rows.rows[rid] {
			if j != i {
				mates = append(mates, j)
			}
		}
	}
	if len(mates) == 0 && ln.Column >= 0 && ln.Column < len(cols) {
		for _, j := range cols[ln.Column].Lines {
			if j != i {
				mates = append(mates, j)
			}
		}
	}
	if base, ok := medianCenterY(lines, mates); ok {
		if math.Abs((b[1]+b[3])/2-base) > 0.6*h {
			hits++
		}
	}
	// ③ 列缘对齐：**对不上任何一列**才算命中（"开关夹在两个文字列中间"的形态）。
	// 本行可能被分到一个只有它自己的带（band），拿它自己的 x0 当中位会让判据永远对齐——
	// 所以这里只拿"成员 ≥2 的有效列"的 x0 中位来比。
	if !alignsWithAnyColumn(lines, i, cols) && hasValidColumn(cols) {
		hits++
	}
	return hits >= 2
}

// duplicateSet 标出重复框（IoU>0.5）：保留置信度高者，另一条标记。
//
// ConfMissing 按 -1 处理（比任何真实读数都低）：两条都缺失时保留先出现的（阅读顺序靠前者），
// 结果仍然是确定的，不会因为 map 遍历顺序变化。
func duplicateSet(lines []OcrLine) []bool {
	out := make([]bool, len(lines))
	for i := 0; i < len(lines); i++ {
		for j := i + 1; j < len(lines); j++ {
			if out[i] || out[j] {
				continue
			}
			if iou(lines[i].Box, lines[j].Box) <= 0.5 {
				continue
			}
			if confOrNeg(lines[j]) > confOrNeg(lines[i]) {
				out[i] = true
			} else {
				out[j] = true
			}
		}
	}
	return out
}

// iou 两个矩形框的交并比；任一框不成形（<4 值/非成对）返回 0。
func iou(a, b []float64) float64 {
	if len(a) < 4 || len(b) < 4 {
		return 0
	}
	x0 := math.Max(a[0], b[0])
	y0 := math.Max(a[1], b[1])
	x1 := math.Min(a[2], b[2])
	y1 := math.Min(a[3], b[3])
	if x1 <= x0 || y1 <= y0 {
		return 0
	}
	inter := (x1 - x0) * (y1 - y0)
	areaA := (a[2] - a[0]) * (a[3] - a[1])
	areaB := (b[2] - b[0]) * (b[3] - b[1])
	if areaA <= 0 || areaB <= 0 {
		return 0
	}
	return inter / (areaA + areaB - inter)
}

// confOrNeg 置信度读数，缺失按 -1（低于任何真实读数）。
func confOrNeg(ln OcrLine) float64 {
	if ln.ConfMissing {
		return -1
	}
	return ln.Conf
}

// countInColumn 同列内某文本出现次数（按 trim 后逐字相等，不做大小写归一）。
func countInColumn(lines []OcrLine, col int, text string) int {
	n := 0
	for _, ln := range lines {
		if ln.Column == col && ln.Text == text {
			n++
		}
	}
	return n
}

// isASCIIWord 长度 ≥4 的纯 ASCII 字母串（column_mode 的适用面）。
func isASCIIWord(s string) bool {
	r := []rune(s)
	if len(r) < 4 {
		return false
	}
	for _, c := range r {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') {
			return false
		}
	}
	return true
}

// hasASCIIDigit 是否含数字（含数字的串一律不参与同列归一，防 1003→1008）。
func hasASCIIDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

// withinOneEdit 判断两串编辑距离 ≤1（一次替换/插入/删除）。
func withinOneEdit(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if absInt(len(ra)-len(rb)) > 1 {
		return false
	}
	if len(ra) == len(rb) {
		diff := 0
		for i := range ra {
			if ra[i] != rb[i] {
				diff++
				if diff > 1 {
					return false
				}
			}
		}
		return true
	}
	// 保证 ra 是短的那一个。
	if len(ra) > len(rb) {
		ra, rb = rb, ra
	}
	i, j, skipped := 0, 0, false
	for i < len(ra) && j < len(rb) {
		if ra[i] == rb[j] {
			i++
			j++
			continue
		}
		if skipped {
			return false
		}
		skipped = true
		j++
	}
	return true
}

// absInt 整数绝对值（本文件内部用）。
func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// medianCenterY 一组行的行中心中位数。
func medianCenterY(lines []OcrLine, idx []int) (float64, bool) {
	cs := make([]float64, 0, len(idx))
	for _, j := range idx {
		b := lines[j].Box
		if len(b) < 4 {
			continue
		}
		cs = append(cs, (b[1]+b[3])/2)
	}
	if len(cs) == 0 {
		return 0, false
	}
	sort.Float64s(cs)
	return cs[len(cs)/2], true
}

// alignsWithAnyColumn 本行的 x0 是否与**任一有效列**的列缘对齐。
// 容差＝半个列中位行高（拍照/斜体的正常抖动落在容差内）。
func alignsWithAnyColumn(lines []OcrLine, i int, cols []OcrColumn) bool {
	b := lines[i].Box
	if len(b) < 4 {
		return true // 没有坐标就不判"不对齐"（拿"没有读数"当证据是本仓明令禁止的形态）
	}
	for ci := range cols {
		if len(cols[ci].Lines) < 2 {
			continue
		}
		medX0, ok := medianX0Value(lines, cols[ci].Lines)
		if !ok {
			continue
		}
		mh := medianHeight(lines, cols[ci].Lines)
		if mh <= 0 {
			continue
		}
		if math.Abs(b[0]-medX0) <= 0.5*mh {
			return true
		}
	}
	return false
}

// hasValidColumn 是否存在"成员 ≥2 的有效列"（没有就不许用"对不上列缘"当证据）。
func hasValidColumn(cols []OcrColumn) bool {
	for ci := range cols {
		if len(cols[ci].Lines) >= 2 {
			return true
		}
	}
	return false
}

// medianX0Value 一组行的 x0 中位数。
func medianX0Value(lines []OcrLine, idx []int) (float64, bool) {
	xs := make([]float64, 0, len(idx))
	for _, j := range idx {
		b := lines[j].Box
		if len(b) < 4 {
			continue
		}
		xs = append(xs, b[0])
	}
	if len(xs) == 0 {
		return 0, false
	}
	sort.Float64s(xs)
	return xs[len(xs)/2], true
}

// medianHeight 一组行的行高中位数（无有效框返回 0）。
func medianHeight(lines []OcrLine, idx []int) float64 {
	hs := make([]float64, 0, len(idx))
	for _, j := range idx {
		b := lines[j].Box
		if len(b) < 4 {
			continue
		}
		hs = append(hs, math.Max(1, b[3]-b[1]+1))
	}
	if len(hs) == 0 {
		return 0
	}
	sort.Float64s(hs)
	return hs[len(hs)/2]
}
