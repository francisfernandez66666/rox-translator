// ============ text_formats.go · 职责说明 ============
// fileproc 包纯文本类格式的提取与纯文案交付。
// 现役提取器：txt/csv 逐行、md Markdown（结构判定统一在 md_structure.go，提取与写回共用）。
// 全部复用 Extractor 的去重与规整逻辑；输出为待翻译文本片段列表。
// ⚠️ 2026-10-30 三事合一批：`srt/vtt` 字幕块、`json` 字符串值、`yaml` 标量三条提取器
// **连同 `WriteComparisonXlsx`（xlsx 双语对照表）一起删除**——这五种格式已从准入清单撤出
// （唯一事实源＝formats.go 的 TranslateFormatOrder，AGENTS §一·14），
// 留一条没人调用的提取器就是给下一位「这里是不是漏了分支」的猜谜。
// 历史工单已产出的对照表产物照常可下载：准入判据只作用于建单侧，不套到下载侧。
// ⚠️ pdf/txt/csv/md **有**原位写回，不走对照表；它们写回失败后的兜底是纯文案 .md
// （判据只有一处：engine/file.go 的 writebackDelivery，锁＝engine/file_gate_test.go）。
// =============================================
package fileproc

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ★ 结构层重做（2026-09-22）后删除的四条正则：imgRe / linkRe / emphasisRe / sepRe。
// 它们服务的是「提取时把整行剥成一个裸文本键」的旧模型，其中 emphasisRe 有 6 个捕获组却用
// ReplaceAllString(t,"$1") 回填，导致 *斜体*、`code`、~~删除~~ **连内容一起**从产物里消失（RC-5，
// 实测 ** 381→0、16 个反引号内技术标识符被整段删除）。新模型改为「骨架落在 span 之外、行内标记
// 照原样送模型并就地替换」，剥标记这一步本身不再存在，故不留兼容壳（留着只会被后来人当入口误用）。
// 结构判定统一在 md_structure.go。

// extractLines 逐行清洗后加入提取器（txt/csv 共用的行模式基座；md 走 md_structure 那条判定层）。
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

// WriteTranslationMd ★ 工单双模式（2026-09-13）：按提取顺序生成「纯文案」.md 产物。
// 用途：① 还原文件模式的兜底附加交付（版式还原失败/用户只要文案时下载）；
//
//	② 纯文案模式（anydoc 老格式）主交付；
//	③ ★ 2026-10-30 起还是**图片链与音频链的译文主面**（图片＝译后图＋本文件产物，
//	   音频＝同容器配音＋本文件产物；旧的 .srt 字幕档已取消）。
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

// UntranslatedItem 交付文档「未译出内容」那一节的一条记录。
// 行号与原文都点名到具体那一行（用户裁决 7：交付文档**只**记录未翻译内容，不写别的），
// Reason 由调用方给**成因原话**——三族（上游未返回／输出被截断／补译多轮仍非译文）不许混成
// 一句"部分未译出"，那是把归因藏起来。
type UntranslatedItem struct {
	Line   int    // 1 起的行号（按交付文档里的段落顺序数）
	Source string // 该段原文（图上/文内已按原样保留，这里点名它）
	Reason string // 成因一句话，由调用方按族给
}

// AppendUntranslatedNotes 在已写好的 .md 末尾追加「未译出内容」一节。
// 参数：outPath=目标 .md；items=未译出的段落（**空清单 ⇒ 什么都不写**，连标题都不出）。
// 为什么空清单不出该节：客户读到的"未译出：0 行"是一句**没有对象的话**，
// 而这一节的用途是让人定位到具体那一行；没有行就回到"交付物完整"的形态，不制造噪音。
func AppendUntranslatedNotes(outPath string, items []UntranslatedItem) error {
	if len(items) == 0 {
		return nil
	}
	b, err := os.ReadFile(outPath)
	if err != nil {
		return err
	}
	var sb strings.Builder
	sb.Write(b)
	// 原文末尾没有换行时先补一个：否则标题会和最后一句译文粘在同一行（markdown 里就不再是标题）。
	if len(b) > 0 && b[len(b)-1] != '\n' {
		sb.WriteString("\n")
	}
	fmt.Fprintf(&sb, "\n## 未译出内容（共 %d 行）\n", len(items))
	for _, it := range items {
		fmt.Fprintf(&sb, "- 第 %d 行：原文「%s」，%s\n", it.Line, it.Source, it.Reason)
	}
	return os.WriteFile(outPath, []byte(sb.String()), 0o644)
}
