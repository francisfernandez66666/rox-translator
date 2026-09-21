// ============ 本文件职责中文说明 ============
// 产物结构保真闸门（P1 整改：漏译与格式破坏静默通过）。
//
// 现状两条盲区（实测工单 T20260921075004EF8）：
//   - leakedLang 只在未译率 >50% 才判失败 ⇒ 漏译 20.8% 的工单状态仍是「已完成」，
//     用户拿到残缺成品毫无提示（本文件把「未译段清单」透出，见 file.go 的 UntranslatedSegments）；
//   - 全部既有闸门只检查「中文有没有变少」，**没有任何一道看「格式有没有丢」** ⇒
//     RC-5（381 处加粗消失）、表格竖线丢失、围栏被吞这类破坏能一路潜伏到交付物。
//
// 本文件的指纹比对是**纯函数**（两份文本进、差异列表出），生产侧调用点在 HandleFile 写回完成后：
// 文本类产物（.md/.txt/.csv）直接读原文与产物两份真实文本比对；二进制产物（docx/pptx/pdf/xlsx）
// 读不到可比文本，回落到「按提取顺序拼装的段级文本视图」——该视图只能暴露漏译与残留中文，
// 暴露不了写回层的格式丢失（那一层由 fileproc 的保真断言负责），故注释里标了口径差异，别误以为
// 二进制格式也有同等覆盖。
// ========================================
package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// untranslatedSegmentsCap 未译段清单的透出上限（条）。
// 为什么设上限：清单会序列化进工单 payload 与 SSE 响应，长文档可能缺上千段
// （每条上百字节 ⇒ 单字段就可能几 MB），审批台也读不完；超限时另有计数说明「还有 N 条未列出」。
const untranslatedSegmentsCap = 200

// fidelityCJKMinLines / fidelityCJKMinRatio 残留中文告警的**双门槛**：
// 少于 3 行、或占比不超 2% 不告警——专有名词、品牌、代码标识符合法保留中文是常态
// （如 KB 规定不译的术语），单条命中就报警会让警告淹没在噪声里。
const (
	fidelityCJKMinLines = 3
	fidelityCJKMinRatio = 0.02
)

// fidelityMaxReadBytes 保真比对读原文件/产物的字节上限：超限视为不可比对（回落段级视图），
// 免得为一条警告把整份大文件（几十 MB 的 csv）读进内存。
const fidelityMaxReadBytes = 8 << 20 // 8 MiB

// structureFingerprint 一份文本的结构指纹（只统计与「格式有没有丢」相关的可数特征）。
type structureFingerprint struct {
	lines         int // 物理行数（含空行）：行对齐类写回必须 1:1
	nonEmptyLines int // 非空行数：残留中文占比的分母
	pipes         int // '|' 计数：表格列骨架是否被降级成散文（新发现③）
	fences        int // ``` 围栏标记计数：代码块有没有被吞掉（RC-3）
	bold          int // '**' 计数：加粗标记有没有丢（RC-5 的实测 381 处）
	inlineCode    int // 反引号计数（已扣围栏）：行内代码标记有没有丢
	strike        int // '~~' 计数：删除线标记有没有丢
	cjkLines      int // 含汉字行数：非 CJK 目标语下即「残留源语」
}

// computeStructureFingerprint 统计一份文本的结构指纹（纯函数，测试可直接喂字符串）。
func computeStructureFingerprint(text string) structureFingerprint {
	var f structureFingerprint
	f.lines = strings.Count(text, "\n") + 1
	f.pipes = strings.Count(text, "|")
	f.fences = strings.Count(text, "```")
	f.bold = strings.Count(text, "**")
	f.strike = strings.Count(text, "~~")
	// 行内代码：先按围栏数量扣掉围栏自身的反引号（一个 ``` 含 3 个 `），
	// 否则「代码块变多」会被误计成「行内代码变多」，两类丢失就分不开了。
	f.inlineCode = strings.Count(text, "`") - f.fences*3
	if f.inlineCode < 0 {
		f.inlineCode = 0
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f.nonEmptyLines++
		if HasCJK(line) {
			f.cjkLines++
		}
	}
	return f
}

// structureFidelityWarnings 比对原文与产物的结构指纹，返回人类可读的差异警告（含具体差值）。
// 参数 lang: 目标语言码；src/out: 两份文本（原文 / 交付产物或其段级视图）。
// 判定口径：**差值不为 0 就告警**（不是失败）——本闸门只透出信号、不改工单状态，
// 因为「结构变了」既可能是真丢失（RC-5）也可能是合法重排（如 PDF 版式重建），
// 交由审批/QA 判断，比静默交付安全得多。
func structureFidelityWarnings(lang, src, out string) []string {
	label := langLabel(lang)
	sf, of := computeStructureFingerprint(src), computeStructureFingerprint(out)
	var w []string
	if sf.lines != of.lines {
		// 行数差用「+/-」直显：多行=译文夹带换行或模型追加说明，少行=写回吞行（新发现④）
		w = append(w, fmt.Sprintf("%s 产物行数与原文不一致：原文 %d 行 / 产物 %d 行（差 %+d），"+
			"行对齐类写回可能吞行或多行", label, sf.lines, of.lines, of.lines-sf.lines))
	}
	for _, c := range []struct {
		name     string
		src, out int
	}{
		{"表格 '|' 数", sf.pipes, of.pipes},
		{"``` 围栏数", sf.fences, of.fences},
		{"** 加粗标记数", sf.bold, of.bold},
		{"行内反引号数", sf.inlineCode, of.inlineCode},
		{"~~ 删除线数", sf.strike, of.strike},
	} {
		if c.src != c.out {
			w = append(w, fmt.Sprintf("%s 产物%s与原文不一致：原文 %d / 产物 %d（差 %+d），"+
				"该格式标记在写回链上丢失", label, c.name, c.src, c.out, c.out-c.src))
		}
	}
	// 残留源语（漏译）信号：仅对非 CJK 目标语判定——简/繁/日/韩的译文本就是汉字，无从区分。
	if !targetUsesCJKScript(lang) && of.nonEmptyLines > 0 {
		ratio := float64(of.cjkLines) / float64(of.nonEmptyLines)
		if of.cjkLines >= fidelityCJKMinLines && ratio > fidelityCJKMinRatio {
			w = append(w, fmt.Sprintf("%s 产物仍有 %d 行含中文（占产物非空行 %.1f%%，原文共 %d 行），"+
				"疑似漏译或 KB 脏行命中，请人工核对", label, of.cjkLines, ratio*100, sf.lines))
		}
	}
	return w
}

// limitUntranslatedSegments 把未译段清单裁到透出上限（纯函数，便于断言上限生效）。
func limitUntranslatedSegments(list []string) []string {
	if len(list) <= untranslatedSegmentsCap {
		return list
	}
	return list[:untranslatedSegmentsCap]
}

// segmentTextView 把「有序源文段 + 原文→译文映射」拼成可比对指纹的文本视图。
// 用途：二进制产物（docx/pptx/pdf/xlsx）无原文可比时退而求其次——段内换行折成空格，
// 保证「视图行数 == 段数」，否则模型返回的多行译文会让行数比对产生假阳性。
// 未译出的段按口径「保留原文」交付，故视图里填原文（会被残留中文检查抓到，正是我们要的信号）。
func segmentTextView(texts []string, tr map[string]string) (srcView, outView string) {
	flatten := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	srcLines := make([]string, 0, len(texts))
	outLines := make([]string, 0, len(texts))
	for _, t := range texts {
		srcLines = append(srcLines, flatten(t))
		v := t
		if x, ok := tr[t]; ok {
			v = x
		}
		outLines = append(outLines, flatten(v))
	}
	return strings.Join(srcLines, "\n"), strings.Join(outLines, "\n")
}

// collectFidelityWarnings 写回完成后对每个目标语言做结构保真比对，汇总警告条目。
// 参数：srcPath/srcExt=原始文件与其扩展名；texts=有序源文段；langTranslations=各语言译文；
// langArtifact=各语言主产物路径（xlsx 合并产物等无法按语言归属时留空，走段级视图回落）。
func collectFidelityWarnings(srcPath, srcExt string, texts []string,
	langTranslations map[string]map[string]string, langArtifact map[string]string, langs []string) []string {
	srcReal, srcOK := readPlainTextView(srcPath, srcExt)
	var warnings []string
	for _, lc := range langs {
		tr := langTranslations[lc]
		if len(tr) == 0 {
			continue // 该语言一条译文都没有：上游漏译硬闸已处理，此处不重复告警
		}
		art := langArtifact[lc]
		if outReal, ok := readPlainTextView(art, ""); ok && srcOK {
			warnings = append(warnings, structureFidelityWarnings(lc, srcReal, outReal)...)
			continue
		}
		// 二进制产物或读取失败：回落段级视图（只能暴露漏译/残留中文，见本文件头口径说明）
		sv, ov := segmentTextView(texts, tr)
		warnings = append(warnings, structureFidelityWarnings(lc, sv, ov)...)
	}
	return capWarnings(warnings)
}

// plainTextExts 可直接读出文本做指纹比对的扩展名（其余格式的产物按二进制处理）。
var plainTextExts = map[string]bool{".md": true, ".markdown": true, ".txt": true, ".csv": true}

// readPlainTextView 读取一份可直读的文本产物；不可读（不存在/超限/二进制格式）返回 ok=false。
// 读失败绝不报错给上游：保真闸门是附加信号，没信号也不能挡住已完成的交付。
func readPlainTextView(path, ext string) (string, bool) {
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	// 两道扩展名校验：调用方显式给的 ext（原始文件可能是无扩展名的临时名）与产物路径后缀，
	// 任一明确不是文本格式就不读——二进制文件按文本读会得到一堆乱码指纹，比没指纹更误导。
	if e := strings.ToLower(ext); e != "" && !plainTextExts[e] {
		return "", false
	}
	if e := strings.ToLower(filepath.Ext(path)); e != "" && !plainTextExts[e] {
		return "", false
	}
	st, err := os.Stat(path)
	if err != nil || st.IsDir() || st.Size() > fidelityMaxReadBytes {
		return "", false
	}
	b, rerr := os.ReadFile(path)
	if rerr != nil {
		return "", false
	}
	return string(b), true
}
