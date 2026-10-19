// ============ xlsx_test.go · 职责说明 ============
// G17（2026-10-19）xlsx 写回**命中数**口径的回归锁：
//   - 命中时回传命中单元格数 > 0，且单元格确实被替换；
//   - 零命中时回传 0（engine 据此走退化通道，不再静默交付未翻译件）。
//
// 为什么值得一条锁：命中数与"文档里到底换过没有"是同一件事，读错一次（比如恒回非 0）
// 就会让「译文键与表格内容不对应」的整单被当成成功交付。
// =============================================
package fileproc

import (
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

// makeTestXlsx 造一个单单元格 xlsx 供命中数判据使用；返回路径。
func makeTestXlsx(t *testing.T, dir, cell string) string {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	if err := f.SetCellValue(sheet, "A1", cell); err != nil {
		t.Fatalf("写测试单元格失败: %v", err)
	}
	p := filepath.Join(dir, "in.xlsx")
	if err := f.SaveAs(p); err != nil {
		t.Fatalf("落测试 xlsx 失败: %v", err)
	}
	_ = f.Close()
	return p
}

// TestApplyXlsxHitCount 命中与零命中两档各判一次（G17）。
func TestApplyXlsxHitCount(t *testing.T) {
	dir := t.TempDir()
	src := makeTestXlsx(t, dir, "刹车系统")

	// 命中：hits>0，且单元格内容确实变成译文。
	out := filepath.Join(dir, "hit.xlsx")
	hits, err := ApplyXlsx(src, out, map[string]string{"刹车系统": "Brake system"})
	if err != nil {
		t.Fatalf("命中档不该出错: %v", err)
	}
	if hits != 1 {
		t.Fatalf("命中应回 1，实得 %d（命中数口径漂了会让零命中判据失明）", hits)
	}
	f, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatalf("打开产物失败: %v", err)
	}
	defer f.Close()
	if got, _ := f.GetCellValue(f.GetSheetName(0), "A1"); got != "Brake system" {
		t.Fatalf("命中档单元格未被替换: %q", got)
	}

	// 零命中：hits==0（engine 据此降级），产物仍会生成——退化由上层决定，不由 fileproc 判死。
	out2 := filepath.Join(dir, "miss.xlsx")
	hits2, err2 := ApplyXlsx(src, out2, map[string]string{"根本不存在的原文": "whatever"})
	if err2 != nil {
		t.Fatalf("零命中不是错误（退化由 engine 决定）: %v", err2)
	}
	if hits2 != 0 {
		t.Fatalf("零命中应回 0，实得 %d ⇒ 未翻译件会被静默当成功交付", hits2)
	}
}
