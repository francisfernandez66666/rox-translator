// ============ text_formats.go · 职责说明 ============
// fileproc 包纯文本类格式提取器。
// 包含 srt/vtt 字幕、md Markdown、json、yaml、txt/csv 文本的提取。
// 全部复用 Extractor 的去重与规整逻辑；输出为待翻译文本片段列表。
// ★ md 的「哪一段可译、哪一段是骨架」判定不在本文件，统一在 md_structure.go（提取与写回共用）。
// 另提供 WriteComparisonXlsx：无原格式回写能力的格式（pdf/txt/csv/srt/vtt/md/json/yaml）
// 统一降级生成「源文+译文」xlsx 对照表作为翻译产物。
// =============================================
package fileproc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

// ★ 结构层重做（2026-09-22）后删除的四条正则：imgRe / linkRe / emphasisRe / sepRe。
// 它们服务的是「提取时把整行剥成一个裸文本键」的旧模型，其中 emphasisRe 有 6 个捕获组却用
// ReplaceAllString(t,"$1") 回填，导致 *斜体*、`code`、~~删除~~ **连内容一起**从产物里消失（RC-5，
// 实测 ** 381→0、16 个反引号内技术标识符被整段删除）。新模型改为「骨架落在 span 之外、行内标记
// 照原样送模型并就地替换」，剥标记这一步本身不再存在，故不留兼容壳（留着只会被后来人当入口误用）。
// 结构判定统一在 md_structure.go。

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

// extractMarkdown 提取 Markdown 正文：逐行走 md_structure 的统一判定层。
// ★ 口径改造（2026-09-22，工单 T20260921075004EF8）：旧实现在这里做四件事——
// 围栏整段剔除、剥图片/链接语法、emphasisRe 剥行内标记、sepRe 丢分隔线——
// 结果「提取出的是一个裸文本键、写回时整行覆盖」，一次性造成了 6 类线上缺陷
// （行内标记连内容被删、表格降级散文、纯结构行进翻译表导致 prompt 泄漏、围栏整块不译、
// 无空格结构前缀把 `#` 送模型）。现在全部收敛到 mdParseLine：
// 这里只负责「把每个可译片段的键交给提取器」，骨架由写回侧按同一判定就地保留。
// 参数：path=文件路径，e=提取器。
func extractMarkdown(path string, e *Extractor) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	// ctx 跨行推进围栏/块注释状态：必须与写回侧同一顺序、同一函数，否则键对不上（口径漂移）
	ctx := &mdLineCtx{}
	for _, raw := range strings.Split(string(b), "\n") {
		for _, sp := range mdParseLine(raw, ctx) {
			e.add(sp.key)
		}
	}
	return nil
}

// ApplyAlignedText ★ D5（2026-09-12）+ 结构层重做（2026-09-22）：txt/csv/md 按「原文件行序」对齐写回。
// txt/csv：整行为键（旧口径不变），未命中行原样保留，译文做换行归一。
// md：走 md_structure 的 span 模型——**只在可译片段区间内就地替换，片段外的字节（结构前缀、
// 管道/框线、围栏标记与语言标注、缩进、URL、代码语句、注释标记）逐字节不动**。
// 由此天然获得三条不变式：产物与原文行数 1:1、表格列数守恒、行内标记不丢。
// 参数：ext=.txt/.csv/.md；srcPath=原文件；outPath=输出；translations=键→译文。
func ApplyAlignedText(ext, srcPath, outPath string, translations map[string]string) error {
	b, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	nl := "\n"
	if strings.Contains(string(b), "\r\n") {
		nl = "\r\n"
	}
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	// 锚点重算（RC-6）要对照「原文标题 ↔ 译文标题」，必须先留一份未改写的原文行
	srcLines := append([]string(nil), lines...)
	md := ext == ".md"
	ctx := &mdLineCtx{}
	for i, raw := range lines {
		if md {
			lines[i] = mdApplyLine(raw, ctx, translations)
			continue
		}
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		tr, ok := translations[t]
		if !ok {
			continue // ★ 未命中保留原文（旧实现同样保留，但整表重建时该行位置已错乱）
		}
		if out := mdFoldNewlines(tr); out != "" {
			lines[i] = out
		}
	}
	// ★ RC-6（2026-09-22）：链接目标被骨架保护 ⇒ 目录链接文字翻了、`#中文锚点` 没翻，
	//   译文目录整段点不动。这里按「原文标题 ↔ 译文标题」成对重算锚点（不改任何正文与真实 URL）。
	if md {
		lines = mdRemapAnchors(srcLines, lines)
	}
	return os.WriteFile(outPath, []byte(strings.Join(lines, nl)), 0o644)
}

// mdApplyLine 对一行做 span 级就地替换，返回新行（无命中时返回原行，逐字节相同）。
func mdApplyLine(raw string, ctx *mdLineCtx, translations map[string]string) string {
	spans := mdParseLine(raw, ctx)
	line := strings.TrimRight(raw, "\r") // 与 mdParseLine 的偏移量基准保持一致
	if len(spans) == 0 {
		return line
	}
	out := line
	hitStart := -1 // 最左侧被替换片段的位置（倒序遍历结束后即为第一个命中片段的起点）
	// ★ 从行尾往行首替换：span 互不重叠且按升序生成，倒序替换才能保证「前面那些 span 的偏移量」
	//   不被后面替换造成的长度变化打断（正序替换第二个 span 就会错位到别的内容上）。
	for i := len(spans) - 1; i >= 0; i-- {
		sp := spans[i]
		tr, ok := translations[sp.key]
		if !ok {
			continue
		}
		rep := mdNormalizeTranslation(tr, sp.cell)
		if rep == "" || rep == sp.key {
			continue // 空译文或同文回显：等于没翻，保留原文更诚实
		}
		rep = mdProtectLinkTargets(sp.key, rep)
		out = out[:sp.start] + rep + out[sp.end:]
		hitStart = sp.start
	}
	if pfxLen := mdSplitPrefix(line); pfxLen > 0 && hitStart == pfxLen {
		// RC-1 的另一半：源文件写的是 `#标题`（标记后无空白），只粘回标记会得到 `#Title`——
		// CommonMark 里它**不再是标题**（渲染成字面量），层级当场丢失，故补一个空格。
		// 只有「第一个被替换的片段紧贴前缀」时才处理：否则会把表格中间的 cell 前也塞进空格。
		out = mdEnsurePrefixSpace(out, pfxLen)
	}
	return out
}

// mdEnsurePrefixSpace 结构前缀若不以空白收尾（`#标题`/`-列表项`/`1.编号项` 这类无空格写法），
// 补一个空格使其仍是合法的 markdown 标记。原文本来有空格的绝不改动（不做无谓的格式抖动）。
func mdEnsurePrefixSpace(line string, pfxLen int) string {
	if pfxLen <= 0 || pfxLen > len(line) {
		return line
	}
	if line[pfxLen-1] == ' ' || line[pfxLen-1] == '\t' {
		return line // 原写法已有空白分隔，保持逐字节一致
	}
	if pfxLen >= len(line) || line[pfxLen] == ' ' || line[pfxLen] == '\t' {
		return line // 防御：前缀已含尾部空白时不该再补
	}
	if strings.TrimSpace(line[:pfxLen]) == "" {
		return line // 纯缩进前缀（列表续行/代码缩进）：塞空格会改缩进语义
	}
	return line[:pfxLen] + " " + line[pfxLen:]
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

// WriteTranslationMd ★ 工单双模式（2026-09-13）：按提取顺序生成「纯文案」.md 产物。
// 用途：① 还原文件模式的兜底附加交付（版式还原失败/用户只要文案时下载）；
//
//	② 纯文案模式（anydoc→MD 管线的 json/yaml/srt/vtt 类）主交付。
//
// 规则：命中段输出译文、未命中段保留原文；段间空一行（段落语义），
// 表格单元格等短段以独立段落呈现——定位为"内容忠实"的纯文本稿，不承诺版式。
// 参数：outPath=输出文件；sourceTexts=提取顺序的源文片段；translations=原文→译文映射。
func WriteTranslationMd(outPath string, sourceTexts []string, translations map[string]string) error {
	var b strings.Builder
	for _, src := range sourceTexts {
		tr := strings.TrimSpace(translations[src])
		if tr == "" {
			tr = src
		}
		b.WriteString(tr)
		b.WriteString("\n\n")
	}
	if dir := filepath.Dir(outPath); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(outPath, []byte(b.String()), 0o644)
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
