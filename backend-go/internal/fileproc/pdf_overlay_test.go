package fileproc

import "testing"

// TestPdfOverlaySelftest 守护「原版式·原字体·原地替换」新链（2026-09-18 替代
// pdf2docx→DOCX→LibreOffice 重建链）的核心不变量：
//  1. 译文落地：redact + TextWriter 嵌回后文本层可提取（字体真实嵌入，非豆腐块——
//     守护 insert_textbox(fontfile) 在 pymupdf 1.28 不落地的坑，必须走 TextWriter）；
//  2. 原文抹除：已翻译段的原文不再出现；
//  3. 不越界：所有英文词 bbox 落在其单元格（矢量栅格）外扩 2.5pt 内——
//     守护换行宽度被单元格线钳制（横向不越列；纵向已改「字号照搬」口径，
//     缩字号自适应于 2026-09 废止，见 pdf_overlay.py cmd_apply 注释）；
//  4. 原件保留：表格矢量图形零丢失（redact 只删文字层）。
//
// 需要 pymupdf；不可用时脚本退出码 2 → 跳过（生产 venv 内实跑）。
func TestPdfOverlaySelftest(t *testing.T) {
	out, code, ok := runPySelftest(t, "pdf_overlay.py", "selftest")
	if !ok {
		t.Skip("Python 不可用，跳过 PDF 原地替换自检")
	}
	switch code {
	case 0:
		t.Logf("pdf_overlay selftest: %s", out)
	case 2:
		t.Skipf("pymupdf 不可用，跳过自检: %s", out)
	default:
		t.Fatalf("pdf_overlay 产物自检失败（退出码 %d）:\n%s", code, out)
	}
}
