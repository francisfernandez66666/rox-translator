package fileproc

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// ============ docx 解析（word/document.xml + tables + headers/footers） ============

const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// docxDocument 解析 document.xml 结构（按 namespace 宽松解析文本）
type docxDocument struct {
	XMLName xml.Name `xml:"document"`
	Body    docxBody `xml:"body"`
}

type docxBody struct {
	Paragraphs []docxParagraph `xml:"p"`
	Tables     []docxTable     `xml:"tbl"`
}

type docxParagraph struct {
	Runs []docxRun `xml:"r"`
}

type docxRun struct {
	Text string `xml:"t"`
}

type docxTable struct {
	Rows []docxRow `xml:"tr"`
}

type docxRow struct {
	Cells []docxCell `xml:"tc"`
}

type docxCell struct {
	Paragraphs []docxParagraph `xml:"p"`
}

func extractDocx(path string, e *Extractor) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("docx 打开失败: %w", err)
	}
	defer zr.Close()

	// 解析主文档
	mainContent, err := readZipEntry(zr, "word/document.xml")
	if err != nil {
		return fmt.Errorf("docx 缺少 document.xml: %w", err)
	}
	var doc docxDocument
	if err := xml.Unmarshal(mainContent, &doc); err != nil {
		return fmt.Errorf("docx XML 解析失败: %w", err)
	}

	for _, p := range doc.Body.Paragraphs {
		text := paragraphText(p)
		if strings.TrimSpace(text) != "" {
			e.add(text)
		}
	}
	for _, tbl := range doc.Body.Tables {
		for _, row := range tbl.Rows {
			for _, cell := range row.Cells {
				for _, p := range cell.Paragraphs {
					text := paragraphText(p)
					if strings.TrimSpace(text) != "" {
						e.add(text)
					}
				}
			}
		}
	}

	// 页眉页脚
	for _, suffix := range []string{"header1", "header2", "header3", "footer1", "footer2", "footer3",
		"header", "footer"} {
		for _, name := range []string{"word/" + suffix + ".xml", "word/" + suffix + ".xml"} {
			if data, err := readZipEntry(zr, name); err == nil {
				var hdoc docxDocument
				if xml.Unmarshal(data, &hdoc) == nil {
					for _, p := range hdoc.Body.Paragraphs {
						text := paragraphText(p)
						if strings.TrimSpace(text) != "" {
							e.add(text)
						}
					}
				}
			}
		}
	}
	return nil
}

func readZipEntry(zr *zip.ReadCloser, name string) ([]byte, error) {
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("not found: %s", name)
}

func paragraphText(p docxParagraph) string {
	var sb strings.Builder
	for _, r := range p.Runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}

// ============ docx 写回（替换段落/表格/页眉页脚文本） ============

// ApplyDocx 写回 docx
func ApplyDocx(path, outPath string, translations map[string]string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()

	mainData, err := readZipEntry(zr, "word/document.xml")
	if err != nil {
		return err
	}
	newMain := translateDocxXML(mainData, translations)

	// 重建 zip
	osFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer osFile.Close()
	zw := zip.NewWriter(osFile)
	defer zw.Close()
	for _, f := range zr.File {
		data, err := readZipEntry(zr, f.Name)
		if err != nil {
			continue
		}
		if f.Name == "word/document.xml" {
			data = newMain
		} else if strings.HasPrefix(f.Name, "word/header") || strings.HasPrefix(f.Name, "word/footer") {
			data = translateDocxXML(data, translations)
		}
		w, err := zw.Create(f.Name)
		if err != nil {
			return err
		}
		if _, err := w.Write(data); err != nil {
			return err
		}
	}
	return zw.Close()
}

// translateDocxXML 在 XML 级别替换段落文本：找到所有 w:p 内第一个 w:r/w:t，替换内容
func translateDocxXML(data []byte, translations map[string]string) []byte {
	// 按 <w:p>...</w:p> 切分处理段落
	s := string(data)
	var out strings.Builder
	idx := 0
	for {
		start := strings.Index(s[idx:], "<w:p>")
		startEnd := start + 5 // "<w:p>" 长度
		end := strings.Index(s[idx+startEnd:], "</w:p>")
		if start < 0 || end < 0 {
			break
		}
		absStart := idx + start
		absEnd := idx + startEnd + end + len("</w:p>")
		para := s[absStart:absEnd]
		translated := translateDocxParagraph(para, translations)
		out.WriteString(s[idx:absStart])
		out.WriteString(translated)
		idx = absEnd
	}
	out.WriteString(s[idx:])
	return []byte(out.String())
}

var wTextRe = regexp.MustCompile(`<w:t[^>]*>([^<]*)</w:t>`)

// translateDocxParagraph 取段落原文，匹配译文，若命中则替换第一个 w:t 内容，清空其余
func translateDocxParagraph(para string, translations map[string]string) string {
	// 提取纯文本（去掉所有标签）
	var plain strings.Builder
	for _, m := range wTextRe.FindAllStringSubmatch(para, -1) {
		plain.WriteString(m[1])
	}
	original := strings.TrimSpace(plain.String())
	if original == "" {
		return para
	}
	translated, ok := translations[original]
	if !ok {
		return para
	}
	// 替换第一个 w:t 文本为译文，其余 w:t 置空
	matches := wTextRe.FindAllStringSubmatchIndex(para, -1)
	if len(matches) == 0 {
		return para
	}
	first := matches[0]
	// 译文过长时缩小字号（简化：交给前端/不处理）
	_ = first
	newPara := replaceFirstText(para, first, translated)
	// 清空其余
	rest := matches[1:]
	for _, m := range rest {
		contentStart := m[2]
		contentEnd := m[3]
		if contentStart < contentEnd && contentEnd <= len(newPara) {
			// 位置因前面替换可能变化，这里用简化方式：重建
		}
	}
	// 简化：重新处理——只保留第一个 run，其余 run 清空
	return clearExtraRuns(newPara, translated)
}

func replaceFirstText(para string, m []int, replacement string) string {
	// m = [start, end, contentStart, contentEnd]
	return para[:m[2]] + replacement + para[m[3]:]
}

// clearExtraRuns 把除第一个 w:t 外的所有 w:t 置空
func clearExtraRuns(para string, first string) string {
	re := regexp.MustCompile(`(<w:t[^>]*>)([^<]*)(</w:t>)`)
	firstEnd := strings.Index(para, "</w:t>")
	if firstEnd < 0 {
		return para
	}
	after := para[firstEnd+len("</w:t>"):]
	after = re.ReplaceAllString(after, `${1}${3}`)
	return para[:firstEnd+len("</w:t>")] + after
}
