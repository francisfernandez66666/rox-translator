package fileproc

import (
	"strings"

	"github.com/xuri/excelize/v2"
)

// extractXlsx 提取所有 sheet 的单元格字符串
func extractXlsx(path string, e *Extractor) error {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for _, row := range rows {
			for _, cell := range row {
				if strings.TrimSpace(cell) != "" {
					e.add(cell)
				}
			}
		}
	}
	return nil
}

// ApplyXlsx 写回 xlsx：替换匹配的单元格值，译文过长时缩小字号
func ApplyXlsx(path, outPath string, translations map[string]string) error {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return err
	}
	defer f.Close()

	changed := false
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for ri, row := range rows {
			for ci, cellVal := range row {
				orig := strings.TrimSpace(cellVal)
				if orig == "" {
					continue
				}
				translated, ok := translations[orig]
				if !ok || translated == "" {
					continue
				}
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

func adjustXlsxColWidth(f *excelize.File, sheet string) {
	cols, _ := f.GetCols(sheet)
	for ci, col := range cols {
		maxLen := 0
		for _, cell := range col {
			l := len([]rune(cell))
			if l > maxLen {
				maxLen = l
			}
		}
		if maxLen > 0 {
			width := float64(maxLen + 2)
			if width > 50 {
				width = 50
			}
			if width < 8 {
				width = 8
			}
			name, _ := excelize.ColumnNumberToName(ci + 1)
			_ = f.SetColWidth(sheet, name, name, width)
		}
	}
}
