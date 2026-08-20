// ============ 本文件职责中文说明 ============
// 知识库文件导入解析：支持 .xlsx / .xls / .csv 三种格式。
// 第一行作为表头；解析为 map 行记录并识别"语言列"（排除 原文/原语/zh/中文/source 等源列，
// 其余文本表头视为目标语言列），供前端选择导入到对应语言。
// =============================================
package kb

import (
	"encoding/csv"
	"errors"
	"os"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ParseKBFile 解析知识库文件（.xlsx/.xls/.csv），返回行记录与语言列名。
// 参数：path=文件路径；第一行作为表头，第一个文本列视为源语言列（通常为中文）。
// 返回：行记录切片（map[表头]=单元格值）与语言列名列表。
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

// parseExcel 解析 Excel 文件：取第一个工作表的数据。
// 参数：path=文件路径；返回行记录与语言列名。
func parseExcel(path string) ([]map[string]string, []string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	if len(f.GetSheetList()) == 0 {
		return nil, nil, errors.New("Excel 无工作表")
	}
	sheet := f.GetSheetList()[0] // 只取第一个工作表
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

// parseCSV 解析 CSV 文件（标准 RFC 4180）。
// 参数：path=文件路径；返回行记录与语言列名。
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

// rowsToMap 把 [][]string 转成 map 行记录并识别语言列。
// 参数：rows=全部行（首行为表头）；返回行记录切片与语言列名列表。
func rowsToMap(rows [][]string) ([]map[string]string, []string) {
	header := rows[0]
	cols := []string{}
	// 表头去空格；空表头补"列A/列B..."占位
	for i := range header {
		c := strings.TrimSpace(header[i])
		if c == "" {
			c = "列" + string(rune('A'+i))
		}
		cols = append(cols, c)
	}
	langCols := []string{}
	records := []map[string]string{}
	// 逐数据行构建 map 记录；全空行跳过
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
		// 源文本列（原文/原语/zh/cn/chinese/source 等）不作为目标语言列
		if strings.Contains(c, "原文") || strings.Contains(c, "原语") ||
			lc == "zh" || lc == "zh-cn" || lc == "cn" || lc == "chinese" || lc == "源" || lc == "source" {
			continue
		}
		langCols = append(langCols, c)
	}
	if len(langCols) == 0 {
		langCols = cols // 未识别出语言列时退化为全部列
	}
	return records, langCols
}
