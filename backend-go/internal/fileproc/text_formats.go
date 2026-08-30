// ============ text_formats.go · 职责说明 ============
// fileproc 包纯文本类格式提取器。
// 包含 srt/vtt 字幕、md Markdown、json、yaml、txt/csv 文本的提取。
// 全部复用 Extractor 的去重与规整逻辑；输出为待翻译文本片段列表。
// 另提供 WriteComparisonXlsx：无原格式回写能力的格式（pdf/txt/csv/srt/vtt/md/json/yaml）
// 统一降级生成「源文+译文」xlsx 对照表作为翻译产物。
// =============================================
package fileproc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// markdown 行内语法正则：图片 / 链接 / 强调标记 / 分隔线
var (
	imgRe      = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	linkRe     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	emphasisRe = regexp.MustCompile(`\*\*([^*]+)\*\*|\*([^*]+)\*|__([^_]+)__|_([^_]+)_|` + "`" + `([^` + "`" + `]+)` + "`" + `|~~([^~]+)~~`)
	sepRe      = regexp.MustCompile(`^(-{3,}|={3,}|\*{3,}|#{1,6})$`)
)

// extractLines 逐行清洗后加入提取器（txt/csv/md/yaml 共用的行模式基座）。
// 参数：path=文件路径，e=提取器，clean=行级清洗函数（返回 "" 表示丢弃该行）。
func extractLines(path string, e *Extractor, clean func(string) string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimRight(line, "\r")
		if out := clean(line); out != "" {
			e.add(out)
		}
	}
	return nil
}

// extractSubtitle 提取 srt/vtt 字幕文本：
// 按空行分块，跳过序号行（纯数字）、时间轴行（含 "-->"）与 vtt 头（WEBVTT/NOTE/STYLE），
// 块内剩余行合并为一条字幕文本。
// 参数：path=文件路径，e=提取器。
func extractSubtitle(path string, e *Extractor) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for _, block := range strings.Split(string(b), "\n\n") {
		var lines []string
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, "\r"))
			if line == "" || isCueNoise(line) {
				continue
			}
			lines = append(lines, line)
		}
		if len(lines) > 0 {
			e.add(strings.Join(lines, " "))
		}
	}
	return nil
}

// isCueNoise 判断字幕块中的非正文行：纯数字序号 / 时间轴 / vtt 控制关键字。
func isCueNoise(line string) bool {
	if strings.Contains(line, "-->") {
		return true
	}
	pureNum := true
	for _, r := range line {
		if r < '0' || r > '9' {
			pureNum = false
			break
		}
	}
	if pureNum {
		return true
	}
	for _, kw := range []string{"WEBVTT", "NOTE", "STYLE", "REGION"} {
		if strings.HasPrefix(line, kw) {
			return true
		}
	}
	return false
}

// extractMarkdown 提取 Markdown 正文：
// 围栏代码块整段剔除；剥离图片/链接语法与强调标记后入正文；纯结构行（分隔线/仅标题符）丢弃。
// 参数：path=文件路径，e=提取器。
func extractMarkdown(path string, e *Extractor) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	inCode := false
	for _, raw := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inCode = !inCode // 围栏开合翻转（围栏行与块内容均不入库）
			continue
		}
		if inCode || t == "" || sepRe.MatchString(t) {
			continue
		}
		t = imgRe.ReplaceAllString(t, "")
		t = linkRe.ReplaceAllString(t, "$1")
		t = emphasisRe.ReplaceAllString(t, "$1")
		t = strings.TrimSpace(t)
		if t != "" {
			e.add(t)
		}
	}
	return nil
}

// extractJSON 递归收集 JSON 中全部字符串值（键名不入库）。
func extractJSON(path string, e *Extractor) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	walkJSON(v, e)
	return nil
}

// walkJSON 递归遍历 JSON 值：字符串入提取器，数组/对象继续下钻。
func walkJSON(v interface{}, e *Extractor) {
	switch tv := v.(type) {
	case string:
		e.add(tv)
	case []interface{}:
		for _, item := range tv {
			walkJSON(item, e)
		}
	case map[string]interface{}:
		for _, val := range tv {
			walkJSON(val, e)
		}
	}
}

// extractYAML 提取 YAML 标量值：跳过注释/文档分隔行，剥离列表符与键前缀。
func extractYAML(path string, e *Extractor) error {
	return extractLines(path, e, func(line string) string {
		t := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if t == "" || strings.HasPrefix(t, "#") || t == "---" || t == "..." {
			return ""
		}
		t = strings.TrimPrefix(t, "- ")
		t = strings.TrimSpace(t)
		// key: value → 取 value（无引号包裹的裸值）；纯键行（如嵌套节点）丢弃
		if i := strings.Index(t, ": "); i >= 0 {
			t = strings.TrimSpace(t[i+2:])
		} else if strings.HasSuffix(t, ":") {
			return ""
		}
		// 剥成对引号
		if len(t) >= 2 && (t[0] == '"' && t[len(t)-1] == '"' || t[0] == '\'' && t[len(t)-1] == '\'') {
			t = t[1 : len(t)-1]
		}
		return t
	})
}

// WriteComparisonXlsx 生成「源文→译文」xlsx 对照表（无原格式回写能力格式的统一产物）。
// 参数：outPath=输出文件路径；sourceTexts=源文片段列表；translations=原文 → 该语言译文映射。
func WriteComparisonXlsx(outPath string, sourceTexts []string, translations map[string]string) error {
	f := excelize.NewFile()
	sheet := "Sheet1"
	_ = f.SetCellValue(sheet, "A1", "source_text")
	_ = f.SetCellValue(sheet, "B1", "translated_text")
	for i, src := range sourceTexts {
		row := i + 2
		_ = f.SetCellValue(sheet, cellNameA(row), src)
		_ = f.SetCellValue(sheet, cellNameB(row), translations[src])
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return f.SaveAs(outPath)
}

// cellNameA/cellNameB 第 A/B 列指定行的单元格名（对照表固定两列）。
func cellNameA(row int) string { return "A" + strconv.Itoa(row) }
// cellNameB 生成第 row 行 B 列的单元格坐标名（xlsx 对照表输出用）。
func cellNameB(row int) string { return "B" + strconv.Itoa(row) }
