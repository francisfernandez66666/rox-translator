// ============ 本文件职责中文说明 ============
// docx 文件解析与写回：从 word/document.xml（正文段落+表格）及页眉页脚中提取文本，
// 以及把译文写回 docx（XML 层面替换段落第一个 w:t 文本并清空其余 run）。
// 提取与写回保持一致的文本规整规则：跳过 hyperlink 内文本，避免原文/译文不匹配。
// =============================================
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

// wordNS WordprocessingML 主命名空间 URI（文档结构引用）。
const wordNS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"

// docxDocument 解析 document.xml 结构（按 namespace 宽松解析文本）
type docxDocument struct {
	XMLName xml.Name `xml:"document"` // 根元素 document
	Body    docxBody `xml:"body"`     // 文档主体
}

// docxBody 文档主体：段落与表格
type docxBody struct {
	Paragraphs []docxParagraph `xml:"p"`   // 顶层段落
	Tables     []docxTable     `xml:"tbl"` // 表格
}

// docxParagraph 段落：由多个 run 组成
type docxParagraph struct {
	Runs []docxRun `xml:"r"` // 段落内 run 列表
}

// docxRun 文本 run：包含实际文本内容
type docxRun struct {
	Text string `xml:"t"` // run 内文本内容
}

// docxTable 表格：由多行组成
type docxTable struct {
	Rows []docxRow `xml:"tr"` // 表格行
}

// docxRow 表格行：由多个单元格组成
type docxRow struct {
	Cells []docxCell `xml:"tc"` // 行内单元格
}

// docxCell 表格单元格：包含段落
type docxCell struct {
	Paragraphs []docxParagraph `xml:"p"` // 单元格内段落
}

// extractDocx 解析 docx 文件：提取正文段落、表格文本与页眉页脚文本。
// 参数：path=docx 文件路径，e=文本提取器（去重累加）。
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

	// 提取顶层段落文本
	for _, p := range doc.Body.Paragraphs {
		text := paragraphText(p)
		if strings.TrimSpace(text) != "" {
			e.add(text) // 非空文本入提取器
		}
	}
	// 提取表格单元格文本
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

	// 页眉页脚：遍历常见命名（header1-3/footer1-3/header/footer）
	for _, suffix := range []string{"header1", "header2", "header3", "footer1", "footer2", "footer3",
		"header", "footer"} {
		for _, name := range []string{"word/" + suffix + ".xml", "word/" + suffix + ".xml"} {
			if data, err := readZipEntry(zr, name); err == nil {
				var hdoc docxDocument
				// 页眉页脚解析失败不影响主流程
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

// readZipEntry 从 zip 读取指定名称文件的全部字节。
// 参数：zr=zip 读取器，name=zip 内文件路径；返回文件内容字节。
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

// paragraphText 拼接段落内全部 run 的文本（不含 hyperlink，与写回逻辑对齐）。
// 参数：p=段落结构；返回拼接后的纯文本。
func paragraphText(p docxParagraph) string {
	var sb strings.Builder
	for _, r := range p.Runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}

// ============ docx 写回（替换段落/表格/页眉页脚文本） ============

// ApplyDocx 写回 docx：按原文→译文映射替换正文、页眉页脚中的文本并重建 zip 文件。
// 参数：path=原 docx 路径，outPath=输出 docx 路径，translations=原文→译文映射。
// 返回错误。
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
	newMain := translateDocxXML(mainData, translations) // 先翻译主文档

	// 重建 zip：逐文件写出，主文档用译文版，页眉页脚也翻译
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
			continue // 读取失败跳过该文件
		}
		if f.Name == "word/document.xml" {
			data = newMain // 替换为主文档译文
		} else if strings.HasPrefix(f.Name, "word/header") || strings.HasPrefix(f.Name, "word/footer") {
			data = translateDocxXML(data, translations) // 翻译页眉页脚
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

// translateDocxXML 在 XML 级别替换段落文本：找到所有 w:p 内第一个 w:r/w:t，替换内容。
// 参数：data=原始 XML 字节，translations=原文→译文映射；返回替换后的 XML 字节。
func translateDocxXML(data []byte, translations map[string]string) []byte {
	// 按 <w:p>...</w:p> 切分处理段落（兼容带属性的 <w:p ...>）
	s := string(data)
	var out strings.Builder
	idx := 0
	for {
		start := strings.Index(s[idx:], "<w:p")
		if start < 0 {
			break
		}
		start += idx
		// 跳过属性直到标签结束（忽略自闭合 <w:p/> 与无内容段落）
		tagEnd := strings.IndexAny(s[start:], ">")
		if tagEnd < 0 {
			break
		}
		tagEnd += start
		closeTag := s[tagEnd-1]
		if closeTag == '/' {
			// 自闭合段落（<w:p/>）原样保留
			out.WriteString(s[idx : tagEnd+1])
			idx = tagEnd + 1
			continue
		}
		startEnd := tagEnd + 1
		end := strings.Index(s[startEnd:], "</w:p>")
		if end < 0 {
			break
		}
		absEnd := startEnd + end + len("</w:p>")
		para := s[start:absEnd]
		translated := translateDocxParagraph(para, translations) // 翻译该段落
		out.WriteString(s[idx:start])
		out.WriteString(translated)
		idx = absEnd
	}
	out.WriteString(s[idx:])
	return []byte(out.String())
}

// translateDocxParagraph 取段落原文，匹配译文，若命中则替换第一个 w:t 内容，清空其余。
// 参数：para=段落 XML，translations=原文→译文映射；返回替换后的段落 XML。
func translateDocxParagraph(para string, translations map[string]string) string {
	// 提取纯文本（去掉所有标签；跳过 w:hyperlink 内文本，与 ExtractTexts 提取逻辑一致）
	plain, firstIdx := paragraphRunText(para)
	original := strings.TrimSpace(plain)
	if original == "" {
		return para // 空段落不改
	}
	translated, ok := translations[original]
	if !ok || firstIdx < 0 {
		return para // 未命中译文则原样返回
	}
	// 定位第一个 w:t 的闭合标签起始位置（保留 </w:t> 标签本身）
	closeStart := strings.Index(para[firstIdx:], "</w:t>")
	if closeStart < 0 {
		return para
	}
	closeStart += firstIdx
	// 替换第一个 w:t 内容为译文（保留其闭合标签）
	newPara := para[:firstIdx] + translated + para[closeStart:]
	// 清空第一个 w:t 之后所有 w:t 的内容（含 hyperlink 内文本）
	after := newPara[firstIdx+len(translated):]
	cleaned := emptyWTextRe.ReplaceAllString(after, `${1}${3}`)
	return newPara[:firstIdx+len(translated)] + cleaned
}

// paragraphRunText 提取段落中非 hyperlink 内的 w:t 文本，返回拼接文本及第一个 w:t 内容起点。
// hyperlink 内的文本不计入原文，与 extractDocx 的 paragraphText 保持一致。
// 参数：para=段落 XML；返回纯文本与第一个 w:t 内容起始位置（无则 -1）。
func paragraphRunText(para string) (string, int) {
	var plain strings.Builder
	firstIdx := -1
	i := 0
	for i < len(para) {
		hidx := strings.Index(para[i:], "<w:hyperlink")
		tidx := strings.Index(para[i:], "<w:t")
		if tidx < 0 {
			break // 无更多 w:t 文本
		}
		if hidx >= 0 && hidx < tidx {
			// 跳过整个 hyperlink 块（其内文本不参与原文匹配）
			openEnd := i + hidx + strings.Index(para[i+hidx:], ">") + 1
			closeIdx := strings.Index(para[openEnd:], "</w:hyperlink>")
			if closeIdx < 0 {
				break
			}
			i = openEnd + closeIdx + len("</w:hyperlink>")
			continue
		}
		// 定位 w:t 内容
		openStart := i + tidx
		contentStart := strings.Index(para[openStart:], ">") + openStart + 1
		closeIdx := strings.Index(para[contentStart:], "</w:t>")
		if closeIdx < 0 {
			break
		}
		text := para[contentStart : contentStart+closeIdx]
		if firstIdx < 0 && strings.TrimSpace(text) != "" {
			firstIdx = contentStart // 记录首个非空 w:t 内容起点
		}
		plain.WriteString(text)
		i = contentStart + closeIdx + len("</w:t>")
	}
	return plain.String(), firstIdx
}

// emptyWTextRe 匹配 <w:t>..</w:t>，用于清空内容
var emptyWTextRe = regexp.MustCompile(`(<w:t[^>]*>)([^<]*)(</w:t>)`)
