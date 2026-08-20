// ============ 本文件职责中文说明 ============
// xlsx 文件解析与写回：提取所有 sheet 的单元格字符串（去重），
// 以及把译文写回 xlsx——替换匹配的单元格值，译文过长时缩小字号并自动调整列宽。
// =============================================
package fileproc

import (
	"strings"

	"github.com/xuri/excelize/v2"
)

// extractXlsx 提取所有 sheet 的单元格字符串。
// 参数：path=xlsx 文件路径，e=文本提取器（去重累加）。
func extractXlsx(path string, e *Extractor) error {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	// 遍历所有 sheet 的每一行每一格
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue // 单 sheet 失败不影响其余
		}
		for _, row := range rows {
			for _, cell := range row {
				if strings.TrimSpace(cell) != "" {
					e.add(cell) // 非空单元格提取
				}
			}
		}
	}
	return nil
}

// ApplyXlsx 写回 xlsx：替换匹配的单元格值，译文过长时缩小字号。
// 参数：path=原 xlsx 路径，outPath=输出路径，translations=原文→译文映射。
func ApplyXlsx(path, outPath string, translations map[string]string) error {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return err
	}
	defer f.Close()

	changed := false
	// 遍历所有 sheet 的单元格，命中译文则写入
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for ri, row := range rows {
			for ci, cellVal := range row {
				orig := strings.TrimSpace(cellVal)
				if orig == "" {
					continue // 空单元格跳过
				}
				translated, ok := translations[orig]
				if !ok || translated == "" {
					continue // 未命中跳过
				}
				// 行列号转 Excel 单元格名（A1 风格）
				cellName, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
				if err := f.SetCellValue(sheet, cellName, translated); err != nil {
					continue
				}
				// 译文显著变长时缩小字号
				if float64(len([]rune(translated))) > float64(len([]rune(orig)))*1.3 {
					styleID, err := f.GetCellStyle(sheet, cellName)
					if err == nil {
						if style, err := f.GetStyle(styleID); err == nil {
							old := style.Font.Size
							if old > 9 {
								// 字号下限：>9pt 才缩小 2pt，避免过小不可读
								style.Font.Size = old - 2
								newStyleID, _ := f.NewStyle(style)
								_ = f.SetCellStyle(sheet, cellName, cellName, newStyleID)
							}
						}
					}
				}
				changed = true
			}
		}
		// 调整列宽
		adjustXlsxColWidth(f, sheet)
	}
	if !changed {
		// 即便无变化也复制文件
	}
	return f.SaveAs(outPath)
}

// adjustXlsxColWidth 按列最大字符数自动调整列宽（范围 8~50）。
// 参数：f=excelize 文件句柄，sheet=工作表名。
func adjustXlsxColWidth(f *excelize.File, sheet string) {
	cols, _ := f.GetCols(sheet)
	for ci, col := range cols {
		maxLen := 0
		// 求该列最大单元格字符数（rune 数）
		for _, cell := range col {
			l := len([]rune(cell))
			if l > maxLen {
				maxLen = l
			}
		}
		if maxLen > 0 {
			width := float64(maxLen + 2) // 内容宽度 + 2 边距
			if width > 50 {
				width = 50 // 列宽上限
			}
			if width < 8 {
				width = 8 // 列宽下限
			}
			name, _ := excelize.ColumnNumberToName(ci + 1)
			_ = f.SetColWidth(sheet, name, name, width)
		}
	}
}
