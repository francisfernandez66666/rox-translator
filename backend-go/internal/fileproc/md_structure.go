// ============ md_structure.go · 职责说明 ============
// Markdown「结构骨架 + 可译片段（span）」的**唯一判定层**：
// 提取侧（extractMarkdown）与写回侧（ApplyAlignedText）调用同一个 mdParseLine，
// 两侧口径不可能再漂移。
//
// ★ 为什么要有这个文件（历史根因）：旧实现是「提取时把整行剥成一个裸文本键 →
// 写回时拿译文整行覆盖」，这一根因一次性造成了 6 类线上缺陷
// （工单 T20260921075004EF8，详见《缺陷记录_工单T20260921075004EF8_md标题中文未译_20260921.md》）：
//
//	RC-5 emphasisRe 有 6 个捕获组却只回填 $1 ⇒ *斜体*/`code`/~~删~~ 连内容一起消失（实测 ** 381→0）；
//	表格整行成为一个键 ⇒ 模型回散文，列结构被毁（实测表格行 210→194、| 972→923）；
//	|---|:--:|、•、纯序号单元格照送模型 ⇒ 模型在无语境短串上回显 prompt，脏指令一路写进交付物；
//	译文带 \n 时整行覆盖 ⇒ 产物与原文行数不再 1:1（712→716）；
//	RC-3 围栏双侧整块跳过 ⇒ 中文产品文档里的 ASCII 图/示例对话整块留中文（用户已裁定这是缺陷）；
//	RC-1 mdStructPrefixRe 要求标记后必须有空白 ⇒ `#标题` 把 `#` 送进模型，译成功反而丢 `#`。
//
// ★ 新模型的关键不变式（所有修法都由此推出）：
//  1. mdParseLine 只返回「可译正文」的字节区间，**骨架（结构前缀、管道、围栏标记、URL、缩进、
//     行注释标记、代码语句）永远落在 span 之外**；
//  2. 写回只在 span 区间内做替换 ⇒ span 之外的字节逐字节不动 ⇒ 行数 1:1、列数守恒、标记守恒是
//     **结构性保证**而不是补丁；
//  3. 提取与写回共用同一判定 ⇒ 「模型见过什么」与「文件里哪一段会被换掉」严格同一个东西。
//
// =============================================
package fileproc

import (
	"strings"
	"unicode"
)

// mdSpan 一行之内的一个「可译片段」。
// [start,end) 是**原始行**（仅去掉尾部 \r）的字节区间，且已去掉首尾空白——
// 首尾空白属于骨架，留在区间外，写回后缩进/单元格内边距不被译文挤掉。
type mdSpan struct {
	start int
	end   int
	key   string
	// cell 标记该片段来自表格单元格 / ASCII 框线单元格。
	// 为什么需要它：译文里若混进裸 `|`，写回后会把一行撑出多余列（旧实现「表格降级为散文」的成因）；
	// 只有知道「这是单元格」，写回时才敢把裸分隔符中和掉。
	cell bool
}

// mdRange 一段字节区间（尚未判定可译性）。
type mdRange struct {
	start int
	end   int
}

// mdCell 一个切分出的 cell：区间 + 是否真的被分隔符切过。
// cell 标记决定写回时要不要中和译文里的裸竖线（没切过 = 普通正文行，竖线是内容不是列边界）。
type mdCell struct {
	mdRange
	cell bool
}

// mdFenceClass 围栏块的处置类别。
// ★ 为什么必须分三类（缺陷记录 §5.7.6 用真实文档实证）：中文产品文档的 ``` 块里
// 0/10 写了语言标注，内容却分别是 ASCII 架构图 / 伪码 / Prompt 模板 / **安全词正则**。
// 「整块不译」会漏掉整图中文；「整块都译」会把安全词正则（`高压|触电|禁止拆卸|…`）翻成英文，
// ⇒ 正则再也匹配不到中文源文 ⇒ 安全句硬闸**静默失效**。只能按类别分派。
type mdFenceClass int

const (
	mdFenceProse   mdFenceClass = iota // 文本/示意图：整块按正文逐行翻
	mdFenceCode                        // 真源码：只翻注释，代码语句逐字节保留
	mdFenceLiteral                     // 数据/白名单/正则：整块逐字节保留
)

// mdLineCtx 跨行状态（Markdown 是行式语言，但围栏与块注释会跨行，必须把状态带在行间）。
// 提取与写回各自持有一个实例、按同一行序推进 ⇒ 同一行必然得到同一判定。
type mdLineCtx struct {
	inFence      bool
	fenceInfo    string       // 围栏语言标注（小写去空白），如 "go"、""
	fenceChar    byte         // 开围栏的字符（` 或 ~）
	fenceLen     int          // 开围栏的连续字符数，闭合需「同字符且不少于此数」
	fenceClass   mdFenceClass // 开围栏时算好，块内每行沿用（避免逐行重判产生漂移）
	blockComment string       // 非空 = 正处于跨行块注释内，值是该块注释的闭合标记（如 "*/"）
}

// mdParseLine ★ 唯一判定入口：返回该行所有可译片段，并推进跨行状态。
// 返回 nil 表示「整行是骨架或不可译」，写回侧必须原样保留。
func mdParseLine(raw string, ctx *mdLineCtx) []mdSpan {
	// 只在行尾 \r 层面做规整：写回侧已把 CRLF 统一成 \n，这里保证两侧偏移量基准一致。
	line := strings.TrimRight(raw, "\r")
	if marker, info, ok := mdFenceLine(line); ok {
		ctx.applyFenceMarker(marker, info)
		return nil // 围栏标记行本身永不翻译（含语言标注，如 ```go）
	}
	if !ctx.inFence {
		return mdParseProseLine(line)
	}
	switch ctx.fenceClass {
	case mdFenceLiteral:
		return nil // 数据/白名单/正则块：整块逐字节保留
	case mdFenceProse:
		return mdParseProseLine(line) // 无标注/文本块：整块按正文翻（ASCII 图框线由 cell 切分保住）
	default:
		return mdParseCodeLine(line, ctx) // 真源码块：只翻注释
	}
}

// applyFenceMarker 处理围栏开/合行。
// 闭合判定与 CommonMark 一致（同字符、长度不小于开围栏、且不带语言标注），
// 因为无标注产品文档里常出现「文本块内又贴了一段 ``` 示例」，宽松匹配会把块边界判歪。
func (ctx *mdLineCtx) applyFenceMarker(marker, info string) {
	if !ctx.inFence {
		ctx.inFence = true
		ctx.fenceChar = marker[0]
		ctx.fenceLen = len(marker)
		ctx.fenceInfo = strings.ToLower(strings.TrimSpace(info))
		ctx.fenceClass = fenceClassOf(ctx.fenceInfo)
		ctx.blockComment = ""
		return
	}
	if marker[0] == ctx.fenceChar && len(marker) >= ctx.fenceLen && info == "" {
		ctx.inFence = false
		ctx.fenceInfo = ""
		ctx.fenceChar = 0
		ctx.fenceLen = 0
		ctx.fenceClass = mdFenceProse
		ctx.blockComment = ""
	}
	// 否则：块内出现的一条形如围栏的行按普通内容处理（例如 prose 块里贴的 ``` 示例）
}

// mdFenceLine 判定该行是否为围栏行，返回（围栏标记, 语言标注, 是否围栏行）。
// 缩进超过 3 空格不算（那是「缩进代码块」语境，不能拿它翻围栏状态，否则整篇状态错位）。
func mdFenceLine(line string) (string, string, bool) {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
		if i > 3 {
			return "", "", false
		}
	}
	if i >= len(line) {
		return "", "", false
	}
	c := line[i]
	if c != '`' && c != '~' {
		return "", "", false
	}
	j := i
	for j < len(line) && line[j] == c {
		j++
	}
	if j-i < 3 {
		return "", "", false // 行内代码 `x` 只有 1 个反引号，三个才算围栏
	}
	info := strings.TrimSpace(line[j:])
	if c == '`' && strings.Contains(info, "`") {
		return "", "", false
	}
	return line[i:j], info, true
}

// literalFenceInfos 是「整块逐字节保留」的语言标注白名单。
// ★ 为什么宁可整块不译：产品文档里的 ``` 块经常是**安全词正则 / 配置白名单 / 结构化数据**，
// 其中的中文是**匹配模式或数据值**而不是待翻译正文——翻了会让下游硬闸静默失效（缺陷记录 §5.7.6-③
// 的实测：第 9 块 `高压|触电|禁止拆卸|…` 一旦英文化，安全句检测再也命中不了中文源文）。
// 提供 nolocalize / literal / raw 这几个显式标注，是为了让「这块别翻」成为用户能精确表达的能力，
// 而不必依赖启发式猜（猜错的方向是不可逆的功能破坏）。
var literalFenceInfos = map[string]bool{
	"json": true, "json5": true, "jsonc": true, "regex": true, "nolocalize": true,
	"literal": true, "raw": true, "csv": true, "tsv": true, "base64": true,
	"env": true, "dotenv": true, "mermaid": true, "protobuf": true, "proto": true,
}

// proseFenceInfos 明确标注为「纯文本」的块：整块按正文翻。
var proseFenceInfos = map[string]bool{
	"": true, "text": true, "plain": true, "plaintext": true, "txt": true,
	"md": true, "markdown": true, "ascii": true,
}

// fenceClassOf 围栏类别分派。
// ★ 未知标注兜底为 prose 而不是 code：真实中文文档绝大多数块根本不写标注，
// 兜底成 code 等于「整块不译」，正是本工单要修的缺陷；判错方向的代价（示意图留中文）
// 远小于兜底成 prose（用户可用 ```nolocalize 精确豁免），故按缺陷记录 §5.7.4 的兜底口径走。
func fenceClassOf(info string) mdFenceClass {
	if literalFenceInfos[info] {
		return mdFenceLiteral
	}
	if proseFenceInfos[info] {
		return mdFenceProse
	}
	if _, ok := mdCommentSyntaxOf(info); ok {
		return mdFenceCode
	}
	return mdFenceProse
}

// ---------- 正文行（围栏外 + 文本围栏内共用） ----------

// mdParseProseLine 把一行正文拆成可译片段：结构前缀 → 表格/框线切 cell → 抠掉链接 URL 洞 → 可译判定。
func mdParseProseLine(line string) []mdSpan {
	var spans []mdSpan
	bodyStart := mdSplitPrefix(line)
	if bodyStart >= len(line) {
		return nil
	}
	if mdIsLinkDefinition(line, bodyStart) {
		return nil // 参考式链接定义行整行是骨架（id + 地址），翻任何一半都会断引用
	}
	for _, cell := range mdSplitCells(line[bodyStart:]) {
		cs, ce := bodyStart+cell.start, bodyStart+cell.end
		for _, seg := range mdSegmentsExcludingTargets(line[cs:ce]) {
			spans = mdAppendSpan(spans, line, cs+seg.start, cs+seg.end, cell.cell)
		}
	}
	return spans
}

// mdIsLinkDefinition 判定参考式链接定义行 `[id]: URL "标题"`（正文里用 `[id][..]` 引用它）。
// 这类行的 id 与地址都是**结构不是文案**：id 被翻就走不通引用，地址被模型改写就成死链，
// 而它们本来就是 ASCII 标识符 —— 整行不入表最省事也最不会错。
func mdIsLinkDefinition(line string, bodyStart int) bool {
	if bodyStart >= len(line) || line[bodyStart] != '[' {
		return false
	}
	c := strings.IndexByte(line[bodyStart:], ']')
	if c < 0 {
		return false
	}
	rest := line[bodyStart+c+1:]
	if !strings.HasPrefix(rest, ":") {
		return false
	}
	tail := strings.TrimLeft(rest[1:], " \t")
	for _, p := range []string{"http://", "https://", "mailto:", "//", "./", "../", "/", "#", "www."} {
		if strings.HasPrefix(tail, p) {
			return true
		}
	}
	return false
}

// mdAppendSpan 去首尾空白后做「可译判定」，通过才收录。
// 空白留在区间外是刻意的：写回替换 [start,end) 时原始缩进/单元格内边距不受译文长度影响。
func mdAppendSpan(dst []mdSpan, line string, start, end int, cell bool) []mdSpan {
	a, b := start, end
	for a < b && (line[a] == ' ' || line[a] == '\t') {
		a++
	}
	for b > a && (line[b-1] == ' ' || line[b-1] == '\t') {
		b--
	}
	if a >= b {
		return dst
	}
	key := line[a:b]
	if mdUntranslatable(key) {
		return dst
	}
	return append(dst, mdSpan{start: a, end: b, key: key, cell: cell})
}

// mdUntranslatable 判定一段文本是否**绝不送模型**。
// 规则：全空白，或整段不含任何 unicode 字母（`|---|:--|`、`----`、`•`、`1`、`(3)`、
// `│┌└─` 纯框线、`**≥ 90%**` 这类纯符号数值）。
// ★ 为什么宁可漏译也不送：无上下文短串是模型**回显 system prompt** 的高发区——
// 上一版就是 `|---|:--:|---|` 这类分隔行进了翻译表，模型回显了
// "Only output the final translated text, enclosed entirely within ..."，
// 而 stripPseudoTags 只剥 ASCII 标签、把指令正文留在交付物里（产物实测 9 处脏指令 + 一个孤零零的
// `and`）。这类脏数据会一路写进客户拿到的文件，**污染面远大于少翻一个符号**。
func mdUntranslatable(s string) bool {
	if strings.TrimSpace(s) == "" {
		return true
	}
	for _, r := range s {
		if unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

// mdSplitPrefix 扫描行首「结构前缀」，返回其字节长度（含前缀后的原始空白）。
// ★ 为什么用程序化扫描而不是一条正则（RC-1 的直接教训）：
// 旧 mdStructPrefixRe 要求标记后必须跟 `[ \t]+`，于是 `#标题`/`-列表项`/`1.编号项`/`>引用`
// 全部匹配不上 ⇒ 标记留在键里送模型（模型当注释原样回显），而翻译真成功时 pfx 为空又会把 `#` 丢掉
// ——两种结局都错。程序化扫描允许「标记后无空白」，同时还能表达正则表达不了的排除条件：
// `**` 起手是强调不是列表、`1.2 版本` 的 `1.` 不是编号。
func mdSplitPrefix(line string) int {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++ // 缩进属于骨架：留在前缀里，写回时不经过任何翻译
	}
	if i >= len(line) {
		return len(line)
	}
	switch c := line[i]; {
	case c == '#':
		j := i
		for j < len(line) && line[j] == '#' {
			j++
		}
		if j-i > 6 {
			return 0 // 七个以上井号不是标题，别把正文吞进前缀
		}
		return mdSkipSpaces(line, j)
	case c == '>':
		return mdSkipSpaces(line, i+1)
	case c == '-' || c == '*' || c == '+':
		if i+1 < len(line) && line[i+1] == c {
			return 0 // `---`/`***`：分隔线或 `**加粗**` 起手，不是列表符
		}
		if c != '-' && !(i+1 >= len(line) || line[i+1] == ' ' || line[i+1] == '\t') {
			return 0 // CommonMark：* 与 + 之后必须空白才是列表，否则是斜体起手
		}
		return mdSkipSpaces(line, i+1)
	case c >= '0' && c <= '9':
		j := i
		for j < len(line) && line[j] >= '0' && line[j] <= '9' {
			j++
		}
		if j >= len(line) || (line[j] != '.' && line[j] != ')') {
			return 0
		}
		if j+1 < len(line) && line[j+1] >= '0' && line[j+1] <= '9' {
			return 0 // `1.2 版本` 是版本号，不是「1. + 2 版本」
		}
		return mdSkipSpaces(line, j+1)
	}
	for _, b := range mdBulletMarks {
		if strings.HasPrefix(line[i:], b) {
			return mdSkipSpaces(line, i+len(b))
		}
	}
	return 0
}

// mdBulletMarks 中文文档手写列表常用的圆点类标记（ASCII 的 - * + 之外）。
// 当骨架处理而不是送模型：模型经常吃掉列表符，一旦吃掉整行列表语义就没了。
var mdBulletMarks = []string{"•", "·", "◦", "▪", "‣", "●", "○"}

// mdSkipSpaces 跳过前缀之后的空白，返回正文起点。
func mdSkipSpaces(s string, i int) int {
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return i
}

// mdSplitCells 按分隔符把一段正文切成 cell（不含分隔符本身）。
// 返回的每个 cell 带 cell 标记：只要本段真出现过分隔符，所有 cell 都置位（含首尾的空 cell）。
// ★ 为什么要切 cell（修「表格降级为散文」）：整行成为一个键时，模型只回一句散文，
// 管道与列数当场消失（实测表格行 210→194、`|` 972→923）。切开后分隔符落在 span 之外，
// **列数守恒由结构保证**；ASCII 框线图同理（`│` 也当分隔符，框线字符逐字节不动）。
// `\|` 是 markdown 表格里的转义竖线，必须不切，否则含转义格的列会凭空多一列。
func mdSplitCells(s string) []mdCell {
	var out []mdCell
	start, hasSep := 0, false
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++ // 转义下一个字符（含 `\|`）
			continue
		}
		if s[i] == '|' {
			out = append(out, mdCell{mdRange: mdRange{start, i}})
			start = i + 1
			hasSep = true
			continue
		}
		if s[i] == 0xE2 && i+2 < len(s) && s[i+1] == 0x94 && s[i+2] == 0x82 { // U+2502 │
			out = append(out, mdCell{mdRange: mdRange{start, i}})
			start = i + 3
			hasSep = true
		}
	}
	out = append(out, mdCell{mdRange: mdRange{start, len(s)}, cell: hasSep})
	if hasSep {
		for i := range out {
			out[i].cell = true
		}
	}
	return out
}

// mdSegmentsExcludingTargets 把 `[文字](URL)` / `![alt](URL)` 里的 `(URL)` 抠成洞，返回洞外的连续片段。
// ★ 为什么 URL 必须送不到模型也改不掉：
//  1. 链接地址（含 `https://`、目录锚点）被模型改写或丢弃后**无法从译文恢复**；
//  2. 本仓既有回归 TestExtractMd 就断言「提取键里不得出现 https://」。
//
// 处理方式是洞——只挖掉 `(URL)` 这一段，`[链接文字]` 与强调标记照旧送模型（标记是内容的一部分，
// 模型保留良好；旧实现「剥标记再猜位置回填」才是 RC-5 的根因）。
// 被挖掉的 URL 落在 span 之外 ⇒ 写回时逐字节不动，URL 保住由结构保证。
func mdSegmentsExcludingTargets(s string) []mdRange {
	holes := mdLinkTargetHoles(s)
	if len(holes) == 0 {
		return []mdRange{{0, len(s)}}
	}
	var out []mdRange
	prev := 0
	for _, h := range holes {
		if h[0] > prev {
			out = append(out, mdRange{prev, h[0]})
		}
		prev = h[1]
	}
	if prev < len(s) {
		out = append(out, mdRange{prev, len(s)})
	}
	return out
}

// mdLinkTargetHoles 找出 `](...)` 形态的链接目标区间（含左右括号）。
// 只认「前面出现过未配对的 [」的括号，避免把正文里的普通 `(说明)` 也挖成洞。
func mdLinkTargetHoles(s string) [][2]int {
	var holes [][2]int
	lastOpen := -1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			lastOpen = i
		case ']':
			if lastOpen < 0 {
				continue
			}
			lastOpen = -1
			if i+1 < len(s) && s[i+1] == '(' {
				if close, ok := mdMatchParen(s, i+1); ok {
					holes = append(holes, [2]int{i + 1, close + 1})
					i = close
				}
			}
		}
	}
	return holes
}

// mdMatchParen 从 open 处的 `(` 找配对的 `)`（支持一层嵌套，URL 里带括号的场景）。
func mdMatchParen(s string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

// ---------- 代码围栏内的行（只翻注释） ----------

// mdParseCodeLine 真源码块内的一行：只有注释部分成为可译片段，代码语句一个字节都不进 span。
// 返回的 span 偏移量**一律以原始行为基准**（用户裁定口径：只替换注释正文，`/*`、`*`、`*/`、
// `<!--`、`-->` 这些标记字符本身留在 span 之外，写回后原样保留；span 偏移量一律以**原始行**为基准）。
func mdParseCodeLine(line string, ctx *mdLineCtx) []mdSpan {
	syn, ok := mdCommentSyntaxOf(ctx.fenceInfo)
	if !ok {
		return nil
	}
	// ① 已在跨行块注释内：整行正文都是注释，直到闭合标记为止
	if ctx.blockComment != "" {
		end := len(line)
		if k := strings.Index(line, ctx.blockComment); k >= 0 {
			end = k
			ctx.blockComment = "" // 本行闭合；同行闭合标记之后再开一块的极端场景不做支持（文档级罕见）
		}
		start := mdSkipSpaces(line, 0)
		// `/* … */` 续行常写成 ` * 说明`：那个行首 `*` 是续行标记不是内容，排除在 span 外
		if syn.blockClose == "*/" && start < end && line[start] == '*' &&
			!(start+1 < end && line[start+1] == '*') {
			start = mdSkipSpaces(line, start+1)
		}
		return mdAppendSpan(nil, line, start, end, false)
	}
	// ② 块注释起手：同行闭合 ⇒ 只取中间正文；跨行 ⇒ 置状态并取到行尾
	if syn.blockOpen != "" {
		if k := strings.Index(line, syn.blockOpen); k >= 0 && !mdInString(line, k) {
			bodyStart := k + len(syn.blockOpen)
			if c := strings.Index(line[bodyStart:], syn.blockClose); c >= 0 {
				return mdAppendSpan(nil, line, bodyStart, bodyStart+c, false)
			}
			ctx.blockComment = syn.blockClose
			return mdAppendSpan(nil, line, bodyStart, len(line), false)
		}
	}
	// ③ 行注释（整行注释 / 行尾注释）：行尾注释只替换标记之后的部分，代码前缀在 span 之外逐字节不动
	for _, hit := range mdLineCommentStarts(line, syn) {
		if mdIsDirectiveComment(line[hit.pos:]) {
			continue // 指令白名单命中：整条注释保留（翻了会坏构建/lint 工具链）
		}
		return mdAppendSpan(nil, line, hit.pos+len(hit.marker), len(line), false)
	}
	return nil
}

// mdCommentHit 一次注释命中：起始位置 + 命中的标记（span 从「位置 + 标记长度」起算，标记本身留下）。
type mdCommentHit struct {
	pos    int
	marker string
}

// mdLineCommentStarts 按引号感知扫描定位行注释起点，返回**最早**的一处。
// ★ 两个必须做对的细节（缺陷记录 §5.7.3）：
//  1. 字符串里的伪注释：`println("// 不是注释")`、`u := "https://x.dev"` ——
//     不做引号状态跟踪就会把字符串内容当注释翻掉，直接破坏代码；
//  2. `#` 只有前置空白（或行首）才算注释起点，否则 `C#`、`color=#fff`、`a#b` 被误判
//     （`#include` 这类前置空白的预处理指令由指令白名单兜住）。
func mdLineCommentStarts(line string, syn mdCommentSyntax) []mdCommentHit {
	var hits []mdCommentHit
	i := 0
	for i < len(line) {
		if mdAdvanceStringState(line, &i) {
			continue // 整段字符串字面量已跳过，i 停在字符串之后
		}
		if i >= len(line) {
			break
		}
		for _, m := range syn.line {
			if !strings.HasPrefix(line[i:], m) {
				continue
			}
			if !mdCommentBoundaryOK(line, i, m) {
				continue
			}
			if m == "//" && i > 0 && line[i-1] == ':' {
				continue // `://`（URL）不是注释
			}
			hits = append(hits, mdCommentHit{pos: i, marker: m})
		}
		i++
	}
	if len(hits) == 0 {
		return nil
	}
	// 只取最早的那处：其后的同名标记属于注释正文，再切一刀会把注释腰斩成两条无语境短键
	first := hits[0]
	for _, h := range hits[1:] {
		if h.pos < first.pos {
			first = h
		}
	}
	return []mdCommentHit{first}
}

// mdAdvanceStringState 跟踪引号状态：若 i 落在字符串内则前进到字符串末尾之后并返回 true。
// 支持 " ' 与反引号（js 模板串），`\` 转义按对跳过。
func mdAdvanceStringState(s string, i *int) bool {
	for *i < len(s) {
		c := s[*i]
		if c == '\\' {
			*i += 2
			continue
		}
		if c != '"' && c != '\'' && c != '`' {
			return false
		}
		quote := c
		*i++
		for *i < len(s) {
			if s[*i] == '\\' {
				*i += 2
				continue
			}
			if s[*i] == quote {
				(*i)++
				break
			}
			(*i)++
		}
		return true
	}
	return false
}

// mdInString 判断位置 pos 是否落在字符串字面量内部（块注释起手判定用）。
// 独立扫描一遍而不复用游标，是因为调用方只关心「这一个点」的语境，不必维护整行状态机。
func mdInString(s string, pos int) bool {
	for i := 0; i < pos && i < len(s); {
		c := s[i]
		if c == '\\' {
			i += 2
			continue
		}
		if c != '"' && c != '\'' && c != '`' {
			i++
			continue
		}
		end := i + 1
		for end < len(s) {
			if s[end] == '\\' {
				end += 2
				continue
			}
			if s[end] == c {
				break
			}
			end++
		}
		if pos > i && pos <= end {
			return true
		}
		i = end + 1
	}
	return false
}

// mdCommentBoundaryOK 注释标记起点的语境判定：`#`/`--`/`%`/`;` 这类标记语义太宽，
// 必须前置空白或位于行首才算注释。
func mdCommentBoundaryOK(line string, pos int, marker string) bool {
	switch marker {
	case "#", "--", "%", ";":
		return pos == 0 || line[pos-1] == ' ' || line[pos-1] == '\t'
	}
	return true
}

// mdDirectiveCommentPrefixes 指令白名单：这些「注释」是喂给构建/lint/编码检测工具的指令，
// 翻了会直接坏工具链（`//go:build` 失效 = 条件编译丢失；`#!/bin/sh` 失效 = 脚本无法执行）。
var mdDirectiveCommentPrefixes = []string{
	"//go:", "// +build", "//nolint", "//lint:", "//export", "// NOLINT",
	"# noqa", "# type:", "# -*-", "#!/", "#include", "#pragma", "# coding:",
	"<?php", "<%! ", "/* eslint-", "--!-",
}

// mdIsDirectiveComment 判断一条注释（以标记起手）是否为不可翻指令。
func mdIsDirectiveComment(comment string) bool {
	t := strings.TrimLeft(comment, " \t")
	for _, p := range mdDirectiveCommentPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// ---------- 写回侧工具 ----------

// mdFoldNewlines 把译文里的换行/回车/制表符折成空格并去首尾空白（**不压内部连续空格**）。
// txt/csv 用它：那两种格式没有「列」概念，内部空格常是对齐用的，不该顺手压掉。
func mdFoldNewlines(tr string) string {
	s := strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "\t", " ").Replace(tr)
	return strings.TrimSpace(s)
}

// mdNormalizeTranslation 译文归一（md 写回用）：折行 + 折叠连续空白 + cell 内中和裸分隔符。
// ★ 为什么必须做（修「产物与原文行数不再 1:1」）：模型偶尔返回带 \n 的多行文本，
// 旧实现整行覆盖时会把 1 行撑成多行（实测原 712 行 → 产物 716 行）。
// 新模型下 span 替换本身不可能跨行，这里再兜一层，保证「绝对不允许产物行数 ≠ 原文行数」。
// cell=true 时额外中和裸分隔符，防止译文把表格/框线图撑出多余列。
func mdNormalizeTranslation(tr string, cell bool) string {
	s := mdFoldNewlines(tr)
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		isSpace := r == ' '
		if isSpace && prevSpace {
			continue
		}
		prevSpace = isSpace
		b.WriteRune(r)
	}
	s = strings.TrimSpace(b.String())
	if cell {
		s = mdEscapeCellPipes(s)
	}
	return s
}

// mdEscapeCellPipes 把 cell 译文里的裸分隔符中和成 markdown 转义形式 `\|`
// （渲染后仍是 `|`，但不再被当作列边界 ⇒ 列数守恒）。
// ★ 为什么不能简单 ReplaceAll("|", `\|`)：源 cell 里本来就可能写的是 `\|`（表格内的转义竖线），
// 无差别替换会得到 `\\|` —— 反斜杠先把自己转义掉，剩下的 `|` 反而**变成**了新列边界，
// 一行表格当场多出一列（实测正是这条路径把列数从 3 撑到 4）。故按「转义对」逐个扫描。
func mdEscapeCellPipes(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i])
			i++
			b.WriteByte(s[i])
			continue
		}
		if s[i] == '|' {
			b.WriteString(`\|`)
			continue
		}
		if strings.HasPrefix(s[i:], "│") { // 框线字符降级为 ASCII 竖线再转义
			b.WriteString(`\|`)
			i += 2
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// mdProtectLinkTargets 处理「片段键里挖掉了 URL」之后的译文：
//   - 源片段本就没有 `](`，译文却出现 ⇒ 模型凭空造了链接目标，抠掉（真实 URL 在骨架洞里，动不到）；
//   - 源片段与译文的 `](` 数量相等 ⇒ 按位置把源 URL 回填进译文（防模型改写/截断地址）；
//   - 数量不等 ⇒ 保留译文不强塞（强塞会把内容错位）。
func mdProtectLinkTargets(src, tr string) string {
	srcN, trN := strings.Count(src, "]("), strings.Count(tr, "](")
	if srcN == 0 {
		if trN == 0 {
			return tr
		}
		return mdStripLinkTargets(tr)
	}
	if srcN != trN {
		return tr
	}
	urls := mdExtractTargets(src)
	out := tr
	for _, u := range urls {
		k := strings.Index(out, "](")
		if k < 0 {
			break
		}
		end, ok := mdMatchParen(out, k+1)
		if !ok {
			break
		}
		out = out[:k+1] + u + out[end+1:]
	}
	return out
}

// mdStripLinkTargets 删除译文里所有 `](...)` 链接目标（保留 `]`，让骨架洞接得上真实 URL）。
func mdStripLinkTargets(s string) string {
	out := s
	for {
		k := strings.Index(out, "](")
		if k < 0 {
			return out
		}
		end, ok := mdMatchParen(out, k+1)
		if !ok {
			return out[:k+1]
		}
		out = out[:k+1] + out[end+1:]
	}
}

// mdExtractTargets 按出现顺序取出 src 中所有 `(...)` 链接目标内容。
func mdExtractTargets(s string) []string {
	var out []string
	for i := 0; i < len(s); i++ {
		if s[i] != '(' {
			continue
		}
		if i == 0 || s[i-1] != ']' {
			continue
		}
		if end, ok := mdMatchParen(s, i); ok {
			out = append(out, s[i:end+1])
			i = end
		}
	}
	return out
}

// ---------- 注释语法表 ----------

// mdCommentSyntax 一种语言的注释写法。
type mdCommentSyntax struct {
	line       []string
	blockOpen  string
	blockClose string
}

// mdCommentSyntaxOf 查围栏标注对应的注释语法。
func mdCommentSyntaxOf(info string) (mdCommentSyntax, bool) {
	s, ok := fenceCommentSyntax[info]
	return s, ok
}

// buildMdCommentSyntax 语言标注 → 注释语法。
// ★ 为什么注释标记必须按语言分派、不能全局一刀切（缺陷记录 §5.7.3-2）：
// Python 的 `//` 是整除运算符、`#` 在 C 系是预处理指令、`--` 在 SQL 是注释但在别的语言是减法。
// 全局扫标记会把代码当注释翻掉，或把注释当代码留下。
func buildMdCommentSyntax() map[string]mdCommentSyntax {
	slashBlock := mdCommentSyntax{line: []string{"//"}, blockOpen: "/*", blockClose: "*/"}
	slashOnly := mdCommentSyntax{line: []string{"//"}}
	hash := mdCommentSyntax{line: []string{"#"}}
	dash := mdCommentSyntax{line: []string{"--"}, blockOpen: "/*", blockClose: "*/"}
	pct := mdCommentSyntax{line: []string{"%"}}
	html := mdCommentSyntax{blockOpen: "<!--", blockClose: "-->"}
	semi := mdCommentSyntax{line: []string{";", "#"}}
	m := map[string]mdCommentSyntax{}
	add := func(syn mdCommentSyntax, infos ...string) {
		for _, i := range infos {
			m[i] = syn
		}
	}
	add(slashBlock, "go", "golang", "js", "javascript", "ts", "typescript", "jsx", "tsx", "vue",
		"java", "c", "cpp", "cc", "h", "hpp", "cs", "csharp", "rust", "rs", "kotlin", "kt",
		"swift", "php", "dart", "groovy", "scala", "objc", "m", "zig", "v")
	add(slashOnly, "proto", "protobuf", "thrift", "glsl", "shader")
	add(hash, "py", "python", "sh", "bash", "zsh", "shell", "yaml", "yml", "rb", "ruby",
		"toml", "conf", "dockerfile", "docker", "perl", "r", "makefile", "mk", "properties",
		"gradle", "tcl", "cmake")
	add(dash, "sql", "lua", "haskell", "hs", "ada", "vhdl")
	add(pct, "tex", "latex", "erlang")
	add(html, "xml", "svg", "html", "htm", "svelte", "wvue")
	add(semi, "asm", "ini", "lisp", "clj", "clojure", "elisp", "ss")
	return m
}

// fenceCommentSyntax 由 buildMdCommentSyntax 构建（见上）。
var fenceCommentSyntax = buildMdCommentSyntax()
