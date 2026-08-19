package kb

import (
	"encoding/csv"
	"errors"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ParseKBFile 解析知识库文件（.xlsx/.xls/.csv），返回行记录与语言列名。
// 第一行作为表头，第一个文本列视为源语言列（通常为兰卡/中文）。
func ParseKBFile(path string) ([]map[string]string, []string, error) {
	lower := strings.ToLower(path)
	if strings.HasSuffix(lower, ".csv") {
		return parseCSV(path)
	}
	if strings.HasSuffix(lower, ".xlsx") || strings.HasSuffix(lower, ".xls") {
		return parseExcel(path)
	}
	return nil, nil, errors.New("不支持的知识库格式，请上传 .xlsx / .csv")
}

func parseExcel(path string) ([]map[string]string, []string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if len(f.GetSheetList()) == 0 {
		return nil, nil, errors.New("Excel 无工作表")
	}
	sheet := f.GetSheetList()[0]
	rows, err := f.GetRows(sheet)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, errors.New("Excel 无数据")
	}
	records, cols := rowsToMap(rows)
	return records, cols, nil
}

func parseCSV(path string) ([]map[string]string, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 {
		return nil, nil, errors.New("CSV 无数据")
	}
	records, cols := rowsToMap(rows)
	return records, cols, nil
}

func rowsToMap(rows [][]string) ([]map[string]string, []string) {
	header := rows[0]
	cols := []string{}
	for i := range header {
		c := strings.TrimSpace(header[i])
		if c == "" {
			c = "列" + string(rune('A'+i))
		}
		cols = append(cols, c)
	}
	langCols := []string{}
	records := []map[string]string{}
	for _, r := range rows[1:] {
		rec := map[string]string{}
		has := false
		for i := range cols {
			var v string
			if i < len(r) {
				v = strings.TrimSpace(r[i])
			}
			if v != "" {
				has = true
			}
			rec[cols[i]] = v
		}
		if has {
			records = append(records, rec)
		}
	}
	// 语言列：去掉"源/原文"这类列后的其余文本列；这里直接取全部非空表头列
	for _, c := range cols {
		lc := strings.ToLower(strings.TrimSpace(c))
		if strings.Contains(c, "原文") || strings.Contains(c, "原语") ||
			lc == "zh" || lc == "zh-cn" || lc == "cn" || lc == "chinese" || lc == "源" || lc == "source" {
			continue
		}
		langCols = append(langCols, c)
	}
	if len(langCols) == 0 {
		langCols = cols
	}
	return records, langCols
}