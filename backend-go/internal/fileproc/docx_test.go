// ============ docx_test.go · 职责说明 ============
// G17（2026-10-19）docx 写回**命中数**口径的回归锁（口径同 xlsx_test.go）：
//   - 文档正文命中时 hits>0，且 word/document.xml 里文本被替换；
//   - 零命中时 hits==0（engine 走退化通道，不静默交付未翻译件）。
//
// =============================================
package fileproc

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTestDocx 造一个最小 docx（只含 word/document.xml），返回路径。
func makeTestDocx(t *testing.T, dir, text string) string {
	t.Helper()
	p := filepath.Join(dir, "in.docx")
	f, err := os.Create(p)
	if err != nil {
		t.Fatalf("创建测试 docx 失败: %v", err)
	}
	zw := zip.NewWriter(f)
	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("创建 document.xml 失败: %v", err)
	}
	doc := `<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>` + text + `</w:t></w:r></w:p></w:body></w:document>`
	if _, err := w.Write([]byte(doc)); err != nil {
		t.Fatalf("写 document.xml 失败: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("收敛测试 zip 失败: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭测试 docx 失败: %v", err)
	}
	return p
}

// docxMainText 读回产物里的 word/document.xml 正文。
func docxMainText(t *testing.T, path string) string {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("打开产物失败: %v", err)
	}
	defer zr.Close()
	data, err := readZipEntry(zr, "word/document.xml")
	if err != nil {
		t.Fatalf("读产物 document.xml 失败: %v", err)
	}
	return string(data)
}

// TestApplyDocxHitCount 命中与零命中两档各判一次（G17）。
func TestApplyDocxHitCount(t *testing.T) {
	dir := t.TempDir()
	src := makeTestDocx(t, dir, "刹车系统")

	out := filepath.Join(dir, "hit.docx")
	hits, err := ApplyDocx(src, out, map[string]string{"刹车系统": "Brake system"})
	if err != nil {
		t.Fatalf("命中档不该出错: %v", err)
	}
	if hits != 1 {
		t.Fatalf("命中应回 1，实得 %d", hits)
	}
	if body := docxMainText(t, out); !strings.Contains(body, "Brake system") {
		t.Fatalf("命中档段落未被替换: %s", body)
	}

	out2 := filepath.Join(dir, "miss.docx")
	hits2, err2 := ApplyDocx(src, out2, map[string]string{"根本不存在的原文": "whatever"})
	if err2 != nil {
		t.Fatalf("零命中不是错误: %v", err2)
	}
	if hits2 != 0 {
		t.Fatalf("零命中应回 0，实得 %d ⇒ 未翻译件会被静默当成功交付", hits2)
	}
}
