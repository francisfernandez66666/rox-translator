// ============ md_structure_test.go · 职责说明 ============
// Markdown 结构层（md_structure.go）的缺陷复现断言：一条用例钉一个已实证的线上缺陷。
// 全部来自工单 T20260921075004EF8 的往返实测（《缺陷记录_工单T20260921075004EF8_md标题中文未译_20260921.md》）：
//
//	RC-5 行内标记连内容被删 / 表格降级为散文 / 纯结构行进翻译表导致 prompt 泄漏进交付物 /
//	译文带换行破坏 1:1 行对齐 / RC-3 围栏整块不译 / RC-1 无空格结构前缀双向错。
//
// ★ 断言一律用「假翻译器」（把每个提取键换成保结构的纯 ASCII 伪译文）：
//
//	真实模型输出不可预测、且跑一次要烧 token；假翻译器只要**输出纯 ASCII**，
//	产物里任何残留的非 ASCII 就必然来自「骨架」——于是「标记有没有丢、URL 有没有被动」
//	这类格式保真问题可以精确归因到代码，而不是归因到模型。
//	（反向坑见缺陷记录 §13.1：假译文若写成 "EN"+原键，中文会跟着回显，漏译率永远算出 100%。）
//
// =============================================
package fileproc

import (
	"os"
	"strings"
	"testing"
	"unicode"
)

// mdKeys 提取该 Markdown 的全部待翻译键（= 实际会送模型的内容）。
func mdKeys(t *testing.T, md string) []string {
	t.Helper()
	ks, err := ExtractTexts(writeTemp(t, "in.md", md))
	if err != nil {
		t.Fatalf("ExtractTexts 失败: %v", err)
	}
	return ks
}

// mdApply 走真实的提取→翻译（假翻译器）→写回链路，返回产物内容。
func mdApply(t *testing.T, md string, tr map[string]string) string {
	t.Helper()
	src := writeTemp(t, "src.md", md)
	out := t.TempDir() + "/out.md"
	if err := ApplyAlignedText(".md", src, out, tr); err != nil {
		t.Fatalf("ApplyAlignedText 失败: %v", err)
	}
	return string(mustRead(t, out))
}

// mdIdentity 空译文表写回：产物必须与原文逐字节相同（「骨架永不太动」的最强断言）。
func mdIdentity(t *testing.T, md string) {
	t.Helper()
	if got := mdApply(t, md, map[string]string{}); got != md {
		t.Fatalf("未翻译时产物被改动:\n原文=%q\n产物=%q", md, got)
	}
}

// mdHas 断言键集合里存在某键。
func mdHas(t *testing.T, keys []string, want string) {
	t.Helper()
	for _, k := range keys {
		if k == want {
			return
		}
	}
	t.Fatalf("缺少提取键 %q，实际=%#v", want, keys)
}

// mdHasNot 断言键集合里不含某键（含「包含子串」形式的宽判）。
func mdHasNot(t *testing.T, keys []string, sub string) {
	t.Helper()
	for _, k := range keys {
		if strings.Contains(k, sub) {
			t.Fatalf("不应提取到含 %q 的键，实际=%q（全部键=%#v）", sub, k, keys)
		}
	}
}

// mdHasSub 断言「至少有一个键包含 sub」——用于标识符这类嵌在长句中间的内容：
// RC-5 的判据就是它们**有没有进过翻译表**（没进表 = 送不到模型 = 产物里永远找不回来）。
func mdHasSub(t *testing.T, keys []string, sub string) {
	t.Helper()
	for _, k := range keys {
		if strings.Contains(k, sub) {
			return
		}
	}
	t.Fatalf("没有任何键包含 %q，全部键=%#v", sub, keys)
}

// mdTranslateAll 对全部提取键用假翻译器生成译文表。
func mdTranslateAll(keys []string) map[string]string {
	tr := make(map[string]string, len(keys))
	for _, k := range keys {
		tr[k] = asciiPseudo(k)
	}
	return tr
}

// asciiPseudo 伪译文：每一段汉字连续串换成 "ZhN"，其余字节（markdown 标记、ASCII 标识符、
// 标点、空格）逐字节保留。为什么这么设计：真实英译会把汉字换成 ASCII 但**不会动 `**` 与反引号**，
// 伪译文复现同一行为，从而能断言「产物里不该再有汉字、但标记与标识符必须还在」。
func asciiPseudo(s string) string {
	var b strings.Builder
	n := 0
	inHan := false
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			if !inHan {
				n++
				b.WriteString("Zh")
				inHan = true
			}
			continue
		}
		inHan = false
		b.WriteRune(r)
	}
	if n == 0 {
		// 纯 ASCII 键（如 `shrink_draft`）：真实模型会照译或原样返回，这里加后缀使其不等于源键，
		// 避免写回侧的「同文回显」守卫把它当未译而跳过，掩盖掉真正的替换路径。
		return s + "X"
	}
	return b.String()
}

// ---------- RC-1：无空格结构前缀 ----------

// TestMdNoSpaceStructurePrefix 复现 RC-1：`#标题`/`-列表项`/`1.编号项`/`>引用`（标记后无空白）。
// 旧实现两种结局都错：标记进了键（模型当注释原样回显 → 标题留中文）；翻译成功反而丢 `#`。
func TestMdNoSpaceStructurePrefix(t *testing.T) {
	md := strings.Join([]string{
		"#标题",
		"##副标题",
		"-列表项",
		">引用块",
		"1.编号项",
		"1.2 版本说明",      // 版本号不是编号列表：不能被拆成 `1.` + `2 版本说明`
		"**关键设计原则**：正文", // `**` 起手是强调不是列表符
	}, "\n") + "\n"

	keys := mdKeys(t, md)
	for _, want := range []string{"标题", "副标题", "列表项", "引用块", "编号项"} {
		mdHas(t, keys, want)
	}
	mdHas(t, keys, "1.2 版本说明")
	mdHas(t, keys, "**关键设计原则**：正文")
	for _, bad := range []string{"#", ">", "标题\n"} {
		for _, k := range keys {
			if strings.HasPrefix(k, bad) {
				t.Fatalf("结构标记 %q 进了送模型的键: %q（全部键=%#v）", bad, k, keys)
			}
		}
	}

	got := mdApply(t, md, map[string]string{
		"标题": "Title", "列表项": "Item", "引用块": "Quote", "编号项": "Step",
	})
	// 期望 `# Title` 而非 `#Title`：CommonMark 里井号后无空白就不算标题，补空格才能保住层级
	for _, want := range []string{"# Title", "- Item", "> Quote", "1. Step"} {
		if !strings.Contains(got, want) {
			t.Fatalf("缺少 %q，产物=%q", want, got)
		}
	}
	if strings.Contains(got, "#Title") || strings.Contains(got, "-Item") {
		t.Fatalf("标记与正文粘连，层级/列表语义丢失: %q", got)
	}
}

// ---------- 缺陷③：纯结构/分隔行进翻译表 ⇒ 模型回显 prompt 污染交付物 ----------

// TestMdStructureOnlyLinesNotExtracted `|---|:--:|`、`•`、纯数字 cell、框线行一律不入库。
func TestMdStructureOnlyLinesNotExtracted(t *testing.T) {
	fence := "```"
	md := strings.Join([]string{
		"|---|:--:|---|",
		"•",
		"----",
		"=====",
		"| 1 | 2 | 3 |",
		"(3)",
		"│  │  │",
		fence,
		"┌──────┬──────┐",
		"└──────┴──────┘",
		fence,
		"| 序号 | 真正的表头 |",
	}, "\n") + "\n"

	keys := mdKeys(t, md)
	mdHas(t, keys, "序号") // 反向确认：真正有文字的 cell 照常入库
	for _, bad := range []string{"---", "===", "•", "│", "┌", "(3)"} {
		mdHasNot(t, keys, bad)
	}
	for _, k := range keys {
		if strings.Trim(k, "-:| ") == "" {
			t.Fatalf("分隔行进了翻译表: %q（这类无语境短串是模型回显 prompt 的高发区）", k)
		}
	}
	mdIdentity(t, md) // 一个译文都不给 ⇒ 整篇逐字节不变
}

// ---------- 缺陷②：表格行降级为散文 ----------

// TestMdTableCellColumnsConserved 表格必须逐 cell 翻译：分隔符与列数守恒。
func TestMdTableCellColumnsConserved(t *testing.T) {
	md := strings.Join([]string{
		"| 序号 | 项目 | 说明 |",
		"|---|---|---|",
		"| 1 | **成本** | 校对对象 = `shrink_draft` |",
		"| \\|转义\\| | 仍是一格 | 值 |",
	}, "\n") + "\n"

	keys := mdKeys(t, md)
	for _, want := range []string{"序号", "项目", "说明", "**成本**", "校对对象 = `shrink_draft`", "仍是一格"} {
		mdHas(t, keys, want)
	}
	got := mdApply(t, md, mdTranslateAll(keys))

	srcLines := strings.Split(strings.TrimRight(md, "\n"), "\n")
	outLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(srcLines) != len(outLines) {
		t.Fatalf("行数不 1:1: %d vs %d", len(srcLines), len(outLines))
	}
	for i := range srcLines {
		if a, b := strings.Count(srcLines[i], "|"), strings.Count(outLines[i], "|"); a != b {
			t.Fatalf("第 %d 行列分隔符计数不守恒 %d→%d\n%s\n%s", i+1, a, b, srcLines[i], outLines[i])
		}
		if a, b := mdCols(srcLines[i]), mdCols(outLines[i]); a != b {
			t.Fatalf("第 %d 行列数不守恒 %d→%d", i+1, a, b)
		}
	}
	// `\|` 转义格不被当成列边界（旧实现整行剥键时根本区分不了）
	if !strings.Contains(outLines[3], `\|`) {
		t.Fatalf("转义竖线被破坏: %q", outLines[3])
	}
	for _, line := range outLines[1:] {
		if strings.Contains(line, "|---") && line != "|---|---|---|" {
			t.Fatalf("分隔行被改写: %q", line)
		}
	}
}

// mdCols 表格行的列数（跳过 `\|` 转义）。
func mdCols(line string) int {
	if !strings.Contains(line, "|") {
		return 0
	}
	var cells []string
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] == '\\' {
			i++
			continue
		}
		if line[i] == '|' {
			cells = append(cells, line[start:i])
			start = i + 1
		}
	}
	cells = append(cells, line[start:])
	n := 0
	for _, c := range cells {
		if t := strings.TrimSpace(c); t != "" && strings.Trim(t, "-:") != "" {
			n++
		}
	}
	return n
}

// ---------- 缺陷①（RC-5）：行内标记与内容被删 ----------

// TestMdInlineMarkersAndIdentifiersSurvive `**` `*` `__` `_` 反引号 `~~` 六类标记与其内容必须留在产物里。
// 旧 emphasisRe 有 6 个捕获组却只回填 $1 ⇒ 除 `**` 外全部连内容一起消失（实测 `shrink_draft`、
// `full_len`、`^[^<].*` 等 16 个技术标识符从产物中被整段删除）。
func TestMdInlineMarkersAndIdentifiersSurvive(t *testing.T) {
	md := strings.Join([]string{
		"> **文档状态**：定稿 · 更新版（2026-08-17，含 Gemini）",
		"- 工单开 shrink → 校对对象 = `shrink_draft`（在人工长度预算内改刀）",
		"*斜体* 与 __粗体__ 与 _单下划线_ 与 ~~删除~~ 混排",
		"指标口径见 `full_len`、`no_shrink_safe` 与 `翻译系统成本速算表_最简版.xlsx`",
	}, "\n") + "\n"

	keys := mdKeys(t, md)
	// 标识符必须**在送模型的内容里**（旧实现提取阶段就没入库 ⇒ 写回无从恢复）
	for _, want := range []string{"shrink_draft", "full_len", "no_shrink_safe", "翻译系统成本速算表_最简版.xlsx"} {
		mdHasSub(t, keys, want)
	}
	for _, k := range keys {
		if strings.HasPrefix(k, "> ") || strings.HasPrefix(k, "- ") {
			t.Fatalf("结构前缀没被剥离，会随译文一起丢: %q", k)
		}
	}

	for _, m := range []string{"**", "__", "_", "*", "~~", "`"} {
		if got, want := strings.Count(mdApply(t, md, mdTranslateAll(keys)), m), strings.Count(md, m); got != want {
			t.Fatalf("标记 %q 计数不守恒 %d→%d（RC-5 的复现口径）", m, want, got)
		}
	}
	got := mdApply(t, md, mdTranslateAll(keys))
	// 标识符必须**原样还在产物里**：旧实现是「提取阶段就没入库」，写回时无从恢复 ⇒ 整段消失
	for _, want := range []string{"`shrink_draft", "`full_len", "`no_shrink_safe", "~~", "Zh"} {
		if !strings.Contains(got, want) {
			t.Fatalf("标记/标识符从产物中消失: 缺 %q\n产物=%q", want, got)
		}
	}
	if n := strings.Count(got, "文档状态"); n != 0 {
		t.Fatalf("加粗内容未被替换（应随 span 一起翻掉）: %q", got)
	}
}

// ---------- 缺陷⑤（RC-3）：围栏三类分派 ----------

// TestMdFenceClassDispatch 真源码（只翻注释）/ 数据与白名单（整块保留）/ 无标注文本（整块翻）。
func TestMdFenceClassDispatch(t *testing.T) {
	f := "```"
	goBlock := []string{
		f + "go",
		"//go:build linux",
		"func main() { shrink() }",
		"u := \"https://x.dev//a\" // 计算成本",
		f,
	}
	jsonBlock := []string{
		f + "json",
		"{ \"提示\": \"高压危险\", \"score\": 1 }",
		f,
	}
	nolockBlock := []string{
		f + "nolocalize",
		"高压|触电|电击|禁止拆卸|禁止触摸|禁止打开|高温烫伤",
		f,
	}
	asciiBlock := []string{
		f, // 无语言标注：中文产品文档的实测主流形态
		"┌──────────────┬──────────────┐",
		"│  用户下单     │  系统计费     │",
		"└──────────────┴──────────────┘",
		f,
	}
	var lines []string
	lines = append(lines, "# 成本模型")
	lines = append(lines, goBlock...)
	lines = append(lines, "")
	lines = append(lines, jsonBlock...)
	lines = append(lines, "")
	lines = append(lines, nolockBlock...)
	lines = append(lines, "")
	lines = append(lines, asciiBlock...)
	md := strings.Join(lines, "\n") + "\n"

	keys := mdKeys(t, md)
	mdHas(t, keys, "计算成本")         // 行尾注释的注释段
	mdHas(t, keys, "用户下单")         // 无标注块：框线内说明照翻
	mdHas(t, keys, "成本模型")         // 围栏外正文照常
	mdHasNot(t, keys, "func main") // 代码语句不入库
	mdHasNot(t, keys, "shrink()")
	mdHasNot(t, keys, "go:build") // 指令白名单
	mdHasNot(t, keys, "高压")       // json / nolocalize 整块不入库（翻了会让安全句硬闸静默失效）
	mdHasNot(t, keys, "x.dev")    // 字符串字面量里的伪注释不得被当注释翻

	got := mdApply(t, md, mdTranslateAll(keys))
	outLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	srcLines := strings.Split(strings.TrimRight(md, "\n"), "\n")
	if len(outLines) != len(srcLines) {
		t.Fatalf("围栏改动导致行数不一致: %d vs %d", len(outLines), len(srcLines))
	}
	same := func(want string) {
		t.Helper()
		if !strings.Contains(got, want) {
			t.Fatalf("应逐字节保留的内容被改动: 缺 %q\n产物=%q", want, got)
		}
	}
	same("//go:build linux")
	same("func main() { shrink() }")
	same("u := \"https://x.dev//a\" //")
	same("{ \"提示\": \"高压危险\", \"score\": 1 }") // json 整块保留
	same("高压|触电|电击|禁止拆卸|禁止触摸|禁止打开|高温烫伤")       // nolocalize 整块保留
	same("┌──────────────┬──────────────┐")
	same("└──────────────┴──────────────┘")
	if strings.Count(got, "│") != strings.Count(md, "│") {
		t.Fatalf("框线字符计数不守恒: %d→%d", strings.Count(md, "│"), strings.Count(got, "│"))
	}
	if !strings.Contains(got, "// ZhX") && !strings.Contains(got, "// Zh") {
		t.Fatalf("行尾注释未被翻译: %q", got)
	}
	if strings.Contains(got, "计算成本") || strings.Contains(got, "用户下单") {
		t.Fatalf("注释/示意图中文残留: %q", got)
	}
}

// TestMdBlockCommentMarkersKept 跨行块注释：正文翻，`/*`、`*`、`*/` 标记逐字节留在原位。
func TestMdBlockCommentMarkersKept(t *testing.T) {
	f := "```"
	md := strings.Join([]string{
		f + "js",
		"/* 计费口径说明",
		" * 按句计费",
		" */",
		"const rate = 1;",
		f,
	}, "\n") + "\n"
	keys := mdKeys(t, md)
	mdHas(t, keys, "计费口径说明")
	mdHas(t, keys, "按句计费")
	mdHasNot(t, keys, "const rate")
	tr := mdTranslateAll(keys)
	got := mdApply(t, md, tr)
	for _, want := range []string{"/* ", " * ", " */", "const rate = 1;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("块注释标记或代码被吞掉: 缺 %q\n产物=%q", want, got)
		}
	}
	if strings.Contains(got, "计费") {
		t.Fatalf("块注释正文未翻译: %q", got)
	}
}

// ---------- 缺陷④：译文带换行 ⇒ 行数 1:1 ----------

// TestMdTranslationNewlineKeepsLineCount 译文含 \n / \r\n 时产物行数必须与原文严格相等。
func TestMdTranslationNewlineKeepsLineCount(t *testing.T) {
	md := "# 标题\n\n正文一行\n"
	got := mdApply(t, md, map[string]string{
		"标题":   "Title\nSecond\nThird",
		"正文一行": "Body\r\nline two",
	})
	if g, e := len(strings.Split(got, "\n")), len(strings.Split(md, "\n")); g != e {
		t.Fatalf("译文换行撑破行对齐: %d 行 vs 原文 %d 行\n产物=%q", g, e, got)
	}
	if !strings.Contains(got, "# Title Second Third") {
		t.Fatalf("换行未折叠为空格: %q", got)
	}
}

// ---------- 链接 URL 保护 ----------

// TestMdLinkUrlProtected 模型改写/丢弃/凭空造 URL 三种情况都不得污染产物。
func TestMdLinkUrlProtected(t *testing.T) {
	md := "详见 [产品方案书](https://x.dev/doc?a=1) 与 [外链](https://y.dev)。\n"
	keys := mdKeys(t, md)
	mdHasNot(t, keys, "https://") // URL 绝不送模型（也是既有 TestExtractMd 的口径）
	mdHas(t, keys, "详见 [产品方案书]")
	// 模型自己补了一个假地址 ⇒ 抠掉假目标，骨架洞里的真地址接回去
	got := mdApply(t, md, map[string]string{"详见 [产品方案书]": "See [Proposal](https://evil.invalid)"})
	if !strings.Contains(got, "[Proposal](https://x.dev/doc?a=1)") {
		t.Fatalf("真 URL 未保住或被假 URL 顶替: %q", got)
	}
	// 键里带 `](` 的形态（括号不配对导致没切成洞）⇒ 按位置回填源地址
	back := mdProtectLinkTargets("[旧](original-url)", "[Old](rewritten)")
	if back != "[Old](original-url)" {
		t.Fatalf("未按位置回填源 URL: %q", back)
	}
	if got := mdProtectLinkTargets("[旧](u1) 与 [二](u2)", "[A](x)"); got != "[A](x)" {
		t.Fatalf("次数不等时不应强塞: %q", got)
	}
}

// TestMdLinkDefinitionIsSkeleton 参考式链接定义行整行是结构：id 与地址都不许进翻译表。
func TestMdLinkDefinitionIsSkeleton(t *testing.T) {
	md := "见文档 [指南][guide]。\n\n[guide]: https://x.dev/guide \"使用指南\"\n"
	keys := mdKeys(t, md)
	mdHasNot(t, keys, "https://")
	mdHasNot(t, keys, "]:")
	mdHas(t, keys, "见文档 [指南][guide]。") // 正文里的引用标记照原样送模型（与 `**` 同一取舍）
	mdIdentity(t, md)                  // 定义行一个字节都不动，引用才不会断
}

// ---------- 真实文档往返（本机有样本才跑） ----------

// TestMdRealDocRoundTrip 用线上原件 产品方案书_v5.1.md 做全量往返：
// 断言的是**结构指纹守恒**（行数、`**`、反引号、围栏、表格与框线字符计数）与
// 「提取键里没有结构垃圾」——缺陷记录 §11「通用加固」要求的那道独立闸门。
func TestMdRealDocRoundTrip(t *testing.T) {
	const sample = "/Users/zhangzifei/Downloads/产品方案书_v5.1.md"
	b, err := os.ReadFile(sample)
	if err != nil {
		t.Skip("真实样本不在本机，跳过（结构性断言由本文件其余用例覆盖）")
	}
	src := strings.ReplaceAll(string(b), "\r\n", "\n")
	keys, err := ExtractTexts(sample)
	if err != nil {
		t.Fatalf("ExtractTexts: %v", err)
	}
	for _, k := range keys {
		if strings.HasPrefix(k, "#") || strings.HasPrefix(k, "|--") || strings.Trim(k, "-:| ") == "" {
			t.Fatalf("真实文档里出现结构垃圾键: %q", k)
		}
	}
	out := t.TempDir() + "/real_en.md"
	if err := ApplyAlignedText(".md", sample, out, mdTranslateAll(keys)); err != nil {
		t.Fatalf("写回: %v", err)
	}
	got := string(mustRead(t, out))
	srcLines, outLines := strings.Split(src, "\n"), strings.Split(got, "\n")
	if len(srcLines) != len(outLines) {
		t.Fatalf("行数不 1:1: %d → %d（旧实现实测 712→716）", len(srcLines), len(outLines))
	}
	// 结构指纹：这些计数必须完全相等（RC-5 潜伏至今就是因为没有这道闸门）
	for _, fp := range []string{"**", "`", "|", "│", "┌", "█", "---"} {
		if a, b2 := strings.Count(src, fp), strings.Count(got, fp); a != b2 {
			t.Fatalf("结构指纹 %q 不守恒: %d → %d", fp, a, b2)
		}
	}
	fence := "```"
	if a, b2 := strings.Count(src, fence), strings.Count(got, fence); a != b2 {
		t.Fatalf("围栏标记数不守恒: %d → %d", a, b2)
	}
	// 残留汉字量必须大幅下降（残留 = 漏译；残留全部来自**骨架洞**：URL/锚点、json/nolocalize 块、
	// 代码语句、纯符号 cell —— 这是设计上的保留，不是漏译）
	if h := hanCount(got); h >= hanCount(src)/3 {
		t.Fatalf("汉字残留过多: %d（原文 %d），说明大量正文未被替换", h, hanCount(src))
	}
	for _, gone := range []string{"静默失效", "术语锁", "校对对象"} {
		if strings.Contains(got, gone) {
			t.Fatalf("正文中文残留: %q", gone)
		}
	}
}

// hanCount 汉字数（漏译/残留的粗粒度度量）。
func hanCount(s string) int {
	n := 0
	for _, r := range s {
		if unicode.Is(unicode.Han, r) {
			n++
		}
	}
	return n
}
