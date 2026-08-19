package fileproc

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ============ pptx 解析（slide 文本 + 表格） ============

// pptxTextEl 宽松结构：解析 sp 的 txBody 和表格
type pptxTextEl struct {
	Paragraphs []pptxPara `xml:"p"`
}

type pptxPara struct {
	Runs []pptxRun `xml:"r"`
}

type pptxRun struct {
	Text string `xml:"t"`
}

// 用宽松 Unmarshal：先取所有 <a:t> 文本 + 段落边界
// 这里直接用正则扫描 a:t 文本，再按 p 边界切分

type pptxShapeRaw struct {
	XMLName xml.Name   `xml:"sp"`
	Content []pptxText `xml:",any"`
}

type pptxText struct {
	XMLName xml.Name
	Content string `xml:",chardata"`
}

func extractPptx(path string, e *Extractor) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("pptx 打开失败: %w", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		name := f.Name
		if !strings.HasPrefix(name, "ppt/slides/slide") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		data, err := readZipEntry(zr, name)
		if err != nil {
			continue
		}
		extractPptxSlide(data, e)
	}
	return nil
}

// extractPptxSlide 解析单个 slide 的文本：
// - shape（<p:sp>）内连续不以句末标点结尾的段落合并为一条翻译单元（\n 连接）
// - 表格单元格作为整体提取（段落间 \n 连接）
// 与 ApplyPptx 的分组写回逻辑保持一致
func extractPptxSlide(data []byte, e *Extractor) {
	s := string(data)

	for _, sp := range spBlockRe.FindAllString(s, -1) {
		var paras []string
		for _, m := range pptParaRe.FindAllString(sp, -1) {
			t := strings.TrimSpace(rawPptxText(m))
			if t != "" {
				paras = append(paras, t)
			}
		}
		var group []string
		for _, p := range paras {
			group = append(group, p)
			if endsSentence(p) {
				addPptxGroup(e, group)
				group = nil
			}
		}
		if len(group) > 0 {
			addPptxGroup(e, group)
		}
	}

	// 表格单元格：整体提取（段落间 \n 连接）
	for _, tc := range cellRe.FindAllString(s, -1) {
		if t := strings.TrimSpace(cellText(tc)); t != "" {
			e.add(t)
		}
	}
}

// addPptxGroup 单段直接加入，多段合并段以 \n 连接加入
func addPptxGroup(e *Extractor, group []string) {
	if len(group) == 1 {
		e.add(group[0])
	} else {
		e.add(strings.Join(group, "\n"))
	}
}

// cellText 拼接表格单元格内各段落文本（段落间 \n）
func cellText(cell string) string {
	var parts []string
	for _, para := range pptParaRe.FindAllString(cell, -1) {
		parts = append(parts, strings.TrimSpace(rawPptxText(para)))
	}
	return strings.Join(parts, "\n")
}

// ============ pptx 写回 ============

// ApplyPptx 写回 pptx：按段落原文映射替换文本
func ApplyPptx(path, outPath string, translations map[string]string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()

	osFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer osFile.Close()
	zw := zip.NewWriter(osFile)

	for _, f := range zr.File {
		data, err := readZipEntry(zr, f.Name)
		if err != nil {
			continue
		}
		if strings.HasPrefix(f.Name, "ppt/slides/slide") && strings.HasSuffix(f.Name, ".xml") {
			data = translatePptxXML(data, translations)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	return osFile.Close()
}

// ============ pptx 写回（含自动缩放） ============

// pptxHitPara shape 中命中翻译的段落信息
type pptxHitPara struct {
	orig      string // 原始段落 XML
	after     string // 替换后段落 XML
	trans     string // 译文
	origSize  int    // 原始字号（百分之一磅），0 表示未知
}

var (
	pptParaRe = regexp.MustCompile(`(?s)<a:p(?:[ >]).*?</a:p>`)
	aTextRe   = regexp.MustCompile(`(<a:t[^>]*>)([^<]*)(</a:t>)`)
	spBlockRe = regexp.MustCompile(`(?s)<p:sp(?:[ >]).*?</p:sp>`)
	bodyPrRe  = regexp.MustCompile(`(?s)<a:bodyPr\b.*?>`)
	extRe     = regexp.MustCompile(`(?s)<a:ext\b[^>]*/>`)
	cellRe    = regexp.MustCompile(`(?s)<a:tc\b.*?</a:tc>`)
)

// translatePptxXML 对 shape 做文本替换 + 字号自动缩放 + autofit；表格单元格单独缩放
func translatePptxXML(data []byte, translations map[string]string) []byte {
	s := string(data)
	// 1. shape 级：段落替换 + 溢出估算缩放字号/行距 + autofit=shrink
	out := spBlockRe.ReplaceAllStringFunc(s, func(block string) string {
		return translatePptxShape(block, translations)
	})
	// 2. 表格单元格：文本替换 + 按长度比缩小字号
	out = cellRe.ReplaceAllStringFunc(out, func(cell string) string {
		return translatePptxCell(cell, translations)
	})
	// 3. 兜底：剩余未处理段落（graphicFrame 等）
	out = pptParaRe.ReplaceAllStringFunc(out, func(para string) string {
		return translatePptxParagraph(para, translations)
	})
	return []byte(out)
}

// translatePptxShape 处理单个 shape：按分组合并匹配翻译（与提取逻辑一致），
// 再 shape 级字号/行距适配 + autofit=shrink
func translatePptxShape(block string, translations map[string]string) string {
	paraRanges := pptParaRe.FindAllStringSubmatchIndex(block, -1)
	paraXMLs := make([]string, len(paraRanges))
	for i, m := range paraRanges {
		paraXMLs[i] = block[m[0]:m[1]]
	}

	// 同提取逻辑分组：连续不以句末标点结尾的段落合并
	var groups [][]int
	var cur []int
	for i, p := range paraXMLs {
		cur = append(cur, i)
		if endsSentence(strings.TrimSpace(rawPptxText(p))) {
			groups = append(groups, cur)
			cur = nil
		}
	}
	if len(cur) > 0 {
		groups = append(groups, cur)
	}

	var hits []pptxHitPara
	for _, g := range groups {
		groupTexts := make([]string, len(g))
		for k, i := range g {
			groupTexts[k] = strings.TrimSpace(rawPptxText(paraXMLs[i]))
		}
		full := strings.Join(groupTexts, "\n")
		trans, ok := translations[full]
		if ok && trans != "" {
			// 合并段命中：译文按 \n 拆分写回各段
			parts := strings.Split(trans, "\n")
			for k, i := range g {
				pt := ""
				if k < len(parts) {
					pt = parts[k]
				} else if len(parts) > 0 {
					pt = parts[len(parts)-1]
				}
				pt = strings.TrimSpace(pt)
				if pt == "" {
					pt = groupTexts[k]
				}
				after := replacePptxParagraph(paraXMLs[i], pt)
				hits = append(hits, pptxHitPara{orig: paraXMLs[i], after: after, trans: pt, origSize: pptxParaOrigSize(paraXMLs[i])})
			}
			continue
		}
		// 合并未命中：逐段独立匹配
		for _, i := range g {
			t, ok := translations[groupTexts[i]]
			if ok && t != "" {
				after := replacePptxParagraph(paraXMLs[i], t)
				hits = append(hits, pptxHitPara{orig: paraXMLs[i], after: after, trans: t, origSize: pptxParaOrigSize(paraXMLs[i])})
			}
		}
	}
	if len(hits) == 0 {
		return block
	}

	// shape 尺寸（EMU）+ 合并译文估算溢出 → 适配字号
	cx, cy := pptxShapeSize(block)
	fitSize100, _ := fitPptxText(hits, cx, cy)
	for i := range hits {
		if fitSize100 > 0 && fitSize100 < hits[i].origSize {
			hits[i].after = setPptxParaSize(hits[i].after, fitSize100)
		}
	}

	// 重建 shape
	out := block
	for _, h := range hits {
		out = strings.Replace(out, h.orig, h.after, 1)
	}
	// 设置 bodyPr autofit="shrink"，让 PowerPoint 打开时自动缩小
	return setBodyPrAutofit(out)
}

// translatePptxParagraph 替换单个段落（不命中返回原样）
func translatePptxParagraph(para string, translations map[string]string) string {
	trans, ok := matchPptxParagraph(para, translations)
	if !ok {
		return para
	}
	return replacePptxParagraph(para, trans)
}

// matchPptxParagraph 返回段落命中的译文
func matchPptxParagraph(para string, translations map[string]string) (string, bool) {
	orig := strings.TrimSpace(rawPptxText(para))
	if orig == "" {
		return "", false
	}
	trans, ok := translations[orig]
	if !ok || trans == "" {
		return "", false
	}
	return trans, true
}

// replacePptxParagraph 替换段落第一个 a:t 内容为译文，其余 a:t 清空
func replacePptxParagraph(para, translated string) string {
	mm := aTextRe.FindAllStringSubmatchIndex(para, -1)
	if len(mm) == 0 {
		return para
	}
	first := mm[0]
	head := para[:first[4]]   // <a:t...>
	tail := para[first[5]:]   // 以 </a:t> 开头
	closeEnd := strings.Index(tail, "</a:t>")
	if closeEnd < 0 {
		return para
	}
	closeEnd += len("</a:t>")
	rest := tail[closeEnd:]
	cleaned := aTextRe.ReplaceAllString(rest, `${1}${3}`)
	return head + translated + tail[:closeEnd] + cleaned
}

// rawPptxText 拼接段落全部 a:t 文本
func rawPptxText(para string) string {
	var sb strings.Builder
	for _, m := range aTextRe.FindAllStringSubmatch(para, -1) {
		sb.WriteString(m[2])
	}
	return sb.String()
}

// pptxParaOrigSize 取段落第一个含 sz 的字号（百分之一磅），无则 0
func pptxParaOrigSize(para string) int {
	for _, re := range []*regexp.Regexp{
		regexp.MustCompile(`<a:rPr\b[^>]*\bsz="(\d+)"`),
		regexp.MustCompile(`<a:defRPr\b[^>]*\bsz="(\d+)"`),
	} {
		for _, m := range re.FindAllStringSubmatch(para, -1) {
			if v, err := strconv.Atoi(m[1]); err == nil && v > 0 {
				return v
			}
		}
	}
	return 0
}

// pptxShapeSize 提取 shape 尺寸 cx/cy（EMU），取不到返回 0
func pptxShapeSize(block string) (int64, int64) {
	m := extRe.FindString(block)
	if m == "" {
		return 0, 0
	}
	var cx, cy int64
	if mm := regexp.MustCompile(`\bcx="(\d+)"`).FindStringSubmatch(m); mm != nil {
		cx, _ = strconv.ParseInt(mm[1], 10, 64)
	}
	if mm := regexp.MustCompile(`\bcy="(\d+)"`).FindStringSubmatch(m); mm != nil {
		cy, _ = strconv.ParseInt(mm[1], 10, 64)
	}
	return cx, cy
}

// fitPptxText 用合并译文估算溢出，返回适配字号（百分之一磅）与行距倍数
func fitPptxText(hits []pptxHitPara, cx, cy int64) (int, float64) {
	// 提取首个有效原始字号（pt）
	origPt := 0
	for _, h := range hits {
		if h.origSize > 0 {
			origPt = h.origSize / 100
			break
		}
	}
	if origPt == 0 {
		origPt = 18
	}
	if cx <= 0 || cy <= 0 {
		// shape 尺寸不可用：译文明显变长时小幅缩小
		longer := false
		for _, h := range hits {
			if len([]rune(h.trans)) > len([]rune(rawPptxText(h.orig)))*3/2 {
				longer = true
				break
			}
		}
		if longer {
			return origPt * 100 * 9 / 10, 1.0
		}
		return 0, 1.15
	}
	fullText := ""
	for _, h := range hits {
		if strings.TrimSpace(h.trans) != "" {
			fullText += h.trans + "\n"
		}
	}
	return estimatePptxOverflow(fullText, cx, cy, origPt)
}

// estimatePptxOverflow 估算文本是否溢出，返回适配字号（百分之一磅）与行距倍数
// 算法与 Python 版 _estimate_text_overflow 一致
func estimatePptxOverflow(text string, shapeW, shapeH int64, fontPt int) (int, float64) {
	if text == "" || shapeW <= 0 || shapeH <= 0 {
		return fontPt * 100, 1.15
	}
	// 平均字符宽度比例：拉丁≈0.55em，CJK≈1.0em
	cjk, total := 0, 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF || r >= 0x3400 && r <= 0x4DBF ||
			r >= 0x3000 && r <= 0x303F || r >= 0xFF01 && r <= 0xFF60 {
			cjk++
		}
		total++
	}
	if total == 0 {
		total = 1
	}
	cjkRatio := float64(cjk) / float64(total)
	avgCharW := 0.55*(1-cjkRatio) + 1.0*cjkRatio

	minPt, maxPt := 8, fontPt

	// 从原始字号向下尝试
	for tryPt := float64(maxPt); tryPt >= float64(minPt)-0.49; tryPt -= 0.5 {
		sizeEmu := int64(tryPt * 12700)
		charW := int64(float64(sizeEmu) * avgCharW)
		if charW < 1 {
			charW = 1
		}
		charsPerLine := shapeW / charW
		if charsPerLine < 1 {
			charsPerLine = 1
		}
		numLines := 0
		for _, line := range strings.Split(text, "\n") {
			lines := int((len([]rune(line)) + int(charsPerLine) - 1) / int(charsPerLine))
			if lines < 1 {
				lines = 1
			}
			numLines += lines
		}
		lineH := int64(float64(sizeEmu) * 1.15)
		if int64(numLines)*lineH <= shapeH {
			return int(math.Round(tryPt) * 100), 1.15
		}
	}
	// 最小字号也放不下，收紧行间距
	for spacing := 1.0; spacing >= 0.79; spacing -= 0.05 {
		sizeEmu := int64(minPt * 12700)
		charW := int64(float64(sizeEmu) * avgCharW)
		if charW < 1 {
			charW = 1
		}
		charsPerLine := shapeW / charW
		if charsPerLine < 1 {
			charsPerLine = 1
		}
		numLines := 0
		for _, line := range strings.Split(text, "\n") {
			lines := int((len([]rune(line)) + int(charsPerLine) - 1) / int(charsPerLine))
			if lines < 1 {
				lines = 1
			}
			numLines += lines
		}
		lineH := int64(float64(sizeEmu) * spacing)
		if int64(numLines)*lineH <= shapeH {
			return minPt * 100, math.Round(spacing*100) / 100
		}
	}
	return minPt * 100, 0.8
}

// setPptxParaSize 设置段落第一个 run 的字号为 sz（百分之一磅）
func setPptxParaSize(para string, sz int) string {
	re := regexp.MustCompile(`(<a:rPr\b[^>]*\bsz=")(\d+)(")`)
	if m := re.FindStringSubmatchIndex(para); m != nil {
		return para[:m[4]] + strconv.Itoa(sz) + para[m[5]:]
	}
	// 无 rPr：在第一个 a:r 上插入 rPr
	re2 := regexp.MustCompile(`(<a:r\b[^>]*><)`)
	if m := re2.FindStringSubmatchIndex(para); m != nil {
		insert := `<a:rPr lang="zh-CN" sz="` + strconv.Itoa(sz) + `"/>`
		return para[:m[1]] + insert + para[m[1]:]
	}
	return para
}

// setAttr 替换标签中的属性值
func setAttr(tag, attr, val string) string {
	re := regexp.MustCompile(`(\s` + attr + `=)("[^"]*")`)
	if m := re.FindStringSubmatchIndex(tag); m != nil {
		return tag[:m[4]] + `"` + val + `"` + tag[m[5]:]
	}
	return tag
}

// setBodyPrAutofit 给 txBody 的 bodyPr 设置 autofit="shrink"
func setBodyPrAutofit(block string) string {
	return bodyPrRe.ReplaceAllStringFunc(block, func(bp string) string {
		if strings.Contains(bp, "autofit=") {
			return setAttr(bp, "autofit", "shrink")
		}
		return strings.Replace(bp, "<a:bodyPr", `<a:bodyPr autofit="shrink"`, 1)
	})
}

// translatePptxCell 处理表格单元格：整体匹配翻译（与提取一致），译文按 \n 拆分写回各段，
// 译文变长时按比例缩小字号
func translatePptxCell(cell string, translations map[string]string) string {
	orig := strings.TrimSpace(cellText(cell))
	if orig == "" {
		return cell
	}
	trans, ok := translations[orig]
	if !ok || trans == "" {
		return cell
	}
	// 拆分译文写回各段
	paras := pptParaRe.FindAllStringSubmatchIndex(cell, -1)
	parts := strings.Split(trans, "\n")
	out := cell
	for k, m := range paras {
		para := cell[m[0]:m[1]]
		pt := ""
		if k < len(parts) {
			pt = parts[k]
		} else if len(parts) > 0 {
			pt = parts[len(parts)-1]
		}
		pt = strings.TrimSpace(pt)
		replaced := replacePptxParagraph(para, pt)
		out = strings.Replace(out, para, replaced, 1)
	}
	// 译文变长则按长度比缩小字号
	origLen := len([]rune(orig))
	transLen := len([]rune(trans))
	if transLen > origLen && origLen > 0 {
		sz := pptxParaOrigSize(cell)
		if sz > 0 {
			fit := sz * origLen * 105 / (transLen * 100)
			if fit < 800 {
				fit = 800
			}
			if fit < sz {
				out = setPptxParaSize(out, fit)
			}
		}
	}
	return out
}
