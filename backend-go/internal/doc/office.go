// ============ office.go · 职责说明 ============
// doc 包：离线零依赖的 Office 文档段落抽取与回写（对照编辑器 docx/pdf 支持）。
//   - DocxParagraphs：纯 Go 解压 docx 的 word/document.xml，按 <w:p> 提段落文本（保留顺序）。
//   - DocxRewrite：读模板 docx，将每段文本替换为 repl[i]（多 run 折叠为单 run 文本，保留段落/样式结构），
//     写出新 docx——用于对照编辑器「审批后回写」导出修订稿。
//   - PDFToParagraphs：借 python 环境 pdf2docx（后端已装 /opt/translator/.venv）把 PDF 转 docx 再抽段落；
//     离线无 python/pdf2docx 时返回明确错误（前端提示「需安装 PDF 解析依赖」）。
// 设计约束：不引入第三方库（离线 go get 不可用）；PDF 解析依赖既有 python venv（pdf2docx）。
package doc

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// DocxParagraphs 提取 docx 全部非空段落文本（按文档顺序）。
// 参数 path: docx 文件路径。返回: 段落文本切片；错误。
func DocxParagraphs(path string) ([]string, error) {
	rc, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("打开 docx 失败: %w", err)
	}
	defer rc.Close()
	r, err := openDocXML(rc)
	if err != nil {
		return nil, err
	}
	return parseParagraphs(r)
}

// openDocXML 从 zip 读取器取 word/document.xml 内容（兼容 word/document2.xml 等变体）。
func openDocXML(rc *zip.ReadCloser) (io.Reader, error) {
	for _, f := range rc.File {
		if strings.HasSuffix(f.Name, "word/document.xml") {
			return f.Open()
		}
	}
	return nil, fmt.Errorf("docx 缺少 word/document.xml")
}

// parseParagraphs 流式解析 XML，按 <w:p> 聚合 <w:t> 文本为段落。
func parseParagraphs(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(r)
	var paras []string
	var buf strings.Builder
	inP := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				inP = true
				buf.Reset()
			}
		case xml.EndElement:
			if t.Name.Local == "p" {
				if buf.Len() > 0 {
					paras = append(paras, buf.String())
				}
				inP = false
			}
		case xml.CharData:
			if inP {
				buf.Write(t)
			}
		}
	}
	return paras, nil
}

// DocxRewrite 读模板 docx，将每段文本替换为 repl[i]（不足用原文本），写新 docx。
// 多 run 段落折叠为单 run 文本，保留段落与样式结构（MVP 取舍：丢失段内加粗等 run 级格式）。
// 参数 src: 模板 docx；out: 输出 docx；repl: 每段替换文本（按段落索引）。
func DocxRewrite(src, out string, repl []string) error {
	rc, err := zip.OpenReader(src)
	if err != nil {
		return fmt.Errorf("打开模板 docx 失败: %w", err)
	}
	defer rc.Close()
	// 复制除 document.xml 外的所有条目
	tmp, err := os.CreateTemp("", "docx-rewrite-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	zw := zip.NewWriter(tmp)

	replFunc := func(w io.Writer) error { return rewriteDocument(rc, w, repl) }
	found := false
	for _, f := range rc.File {
		if strings.HasSuffix(f.Name, "word/document.xml") {
			found = true
			if err := writeZipFileFromFunc(zw, f, replFunc); err != nil {
				zw.Close()
				return err
			}
			continue
		}
		in, err := f.Open()
		if err != nil {
			zw.Close()
			return err
		}
		if err := writeZipFile(zw, f, in); err != nil {
			in.Close()
			zw.Close()
			return err
		}
		in.Close()
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("docx 缺少 word/document.xml")
	}
	_ = os.Remove(out)
	if err := copyFile(tmp.Name(), out); err != nil {
		return err
	}
	return nil
}

// rewriteDocument 读取原 document.xml，流式改写段落文本后写入 w。
func rewriteDocument(rc *zip.ReadCloser, w io.Writer, repl []string) error {
	var src io.Reader
	for _, f := range rc.File {
		if strings.HasSuffix(f.Name, "word/document.xml") {
			src, _ = f.Open()
			break
		}
	}
	if src == nil {
		return fmt.Errorf("缺 document.xml")
	}
	enc := xml.NewEncoder(w)
	dec := xml.NewDecoder(src)
	paraIdx := -1
	paraTextEmitted := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				paraIdx++
				paraTextEmitted = false
			}
			if err := enc.EncodeToken(t); err != nil {
				return err
			}
		case xml.EndElement:
			if err := enc.EncodeToken(t); err != nil {
				return err
			}
		case xml.CharData:
			if paraIdx >= 0 {
				// 段落内文本（w:t 内容）：仅首段文本替换为修订译文，其余 run 文本折叠丢弃
				text := string(t)
				if !paraTextEmitted {
					rep := text
					if paraIdx < len(repl) && repl[paraIdx] != "" {
						rep = repl[paraIdx]
					}
					if err := enc.EncodeToken(xml.CharData(rep)); err != nil {
						return err
					}
					paraTextEmitted = true
				}
			} else {
				if err := enc.EncodeToken(t); err != nil {
					return err
				}
			}
		default:
			if err := enc.EncodeToken(tok); err != nil {
				return err
			}
		}
	}
	return enc.Flush()
}

// writeZipFile 复制 zip 条目原文。
func writeZipFile(zw *zip.Writer, f *zip.File, r io.Reader) error {
	w, err := zw.Create(f.Name)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	return err
}

// writeZipFileFromFunc 以函数生成 zip 条目内容。
func writeZipFileFromFunc(zw *zip.Writer, f *zip.File, gen func(io.Writer) error) error {
	w, err := zw.Create(f.Name)
	if err != nil {
		return err
	}
	return gen(w)
}

// PDFToParagraphs 借 pdf2docx 将 PDF 转 docx 后抽取段落（需 python venv 含 pdf2docx）。
// 环境变量 PDF2DOCX_PYTHON 指定解释器，缺省 /opt/translator/.venv/bin/python。
func PDFToParagraphs(path string) ([]string, error) {
	py := strings.TrimSpace(os.Getenv("PDF2DOCX_PYTHON"))
	if py == "" {
		py = "/opt/translator/.venv/bin/python"
	}
	tmp, err := os.CreateTemp("", "pdf2docx-*.docx")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	tmp.Close()
	script := fmt.Sprintf("from pdf2docx import Converter\nc=Converter(%q)\nc.convert(%q)\nc.close()", path, tmp.Name())
	cmd := exec.Command(py, "-c", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("pdf2docx 转换失败（需安装 /opt/translator/.venv 含 pdf2docx）: %v: %s", err, string(out))
	}
	return DocxParagraphs(tmp.Name())
}

// copyFile 将源文件整体拷贝到目标路径（用于把改写后的临时 zip 落地为最终 docx）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

// IsOfficeDoc 判定是否为可在线逐段编辑的 Office 文档（docx/docm）。
func IsOfficeDoc(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx", ".docm":
		return true
	}
	return false
}

// IsPDF 判定是否为 PDF。
func IsPDF(path string) bool {
	return strings.ToLower(filepath.Ext(path)) == ".pdf"
}
