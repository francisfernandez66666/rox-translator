// Package doc 的 Office 文档段落抽取与回写单元测试。
// 通过构造最小 docx（仅 word/document.xml）验证 DocxParagraphs 与 DocxRewrite 行为。
package doc

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

// sampleDocXML 最小 docx 文档正文样例（含三段，第三段保留前后空白以验证空格保留）。
const sampleDocXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>Hello world</w:t></w:r></w:p>
<w:p><w:r><w:t>Second paragraph</w:t></w:r></w:p>
<w:p><w:r><w:t xml:space="preserve">  Third  </w:t></w:r></w:p>
</w:body>
</w:document>`

// writeSampleDocx 将样例 document.xml 打包为最小 docx 写到 path（测试辅助）。
func writeSampleDocx(t *testing.T, path string) {
	t.Helper()
	zf, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer zf.Close()
	zw := zip.NewWriter(zf)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(sampleDocXML)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// TestDocxParagraphs 校验可正确抽取三段并保留原始文本内容（含前后空白）。
func TestDocxParagraphs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sample.docx")
	writeSampleDocx(t, src)
	paras, err := DocxParagraphs(src)
	if err != nil {
		t.Fatalf("DocxParagraphs: %v", err)
	}
	if len(paras) != 3 {
		t.Fatalf("期望 3 段，实际 %d: %v", len(paras), paras)
	}
	if paras[0] != "Hello world" || paras[2] != "  Third  " {
		t.Fatalf("段落内容不符: %v", paras)
	}
}

// TestDocxRewrite 校验回写时按索引替换段落文本；未提供修订时保留原文。
func TestDocxRewrite(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sample.docx")
	writeSampleDocx(t, src)
	out := filepath.Join(dir, "out.docx")
	repl := []string{"译：你好世界", "", "译：第三段"}
	if err := DocxRewrite(src, out, repl); err != nil {
		t.Fatalf("DocxRewrite: %v", err)
	}
	paras, err := DocxParagraphs(out)
	if err != nil {
		t.Fatalf("读回写出错: %v", err)
	}
	if len(paras) != 3 {
		t.Fatalf("回写后段数不符: %v", paras)
	}
	if paras[0] != "译：你好世界" {
		t.Fatalf("首段未被替换: %q", paras[0])
	}
	if paras[1] != "Second paragraph" {
		t.Fatalf("未提供修订时应保留原文本: %q", paras[1])
	}
	if paras[2] != "译：第三段" {
		t.Fatalf("第三段未被替换: %q", paras[2])
	}
}
