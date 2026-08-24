// ============ 本文件职责中文说明 ============
// 文件翻译：面向 docx/pptx/xlsx 的整文件翻译主流程（复刻 skill.py _handle_file_translate）。
// 支持 KB 语言（先 KB 直配、未命中批量模型补漏）与"其他语言"（纯批量模型），
// 从用户 prompt 中解析"其他语言"（正则 + LLM 语言识别兜底），
// 翻译完成后按语言分别写回 translated/ 目录下独立文件并统计 KB/模型命中数。
// ========================================
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/xuri/excelize/v2"
	"translator/internal/config"
	"translator/internal/fileproc"
)

// FileTranslateResult 文件翻译结果
type FileTranslateResult struct {
	Skill string            `json:"skill"` // 能力标识（固定 "translation"）
	Reply string            `json:"reply"` // 面向用户的完成话术（含统计信息）
	Data  FileTranslateData `json:"data"`  // 结构化翻译数据（文本数/语言/命中统计等）
	Files []string          `json:"files"` // 生成的翻译文件绝对路径列表
	Error string            `json:"error"` // 失败原因（成功时为空）
}

// FileTranslateData 文件翻译数据
type FileTranslateData struct {
	TotalTexts  int               `json:"total_texts"`  // 从文件中提取的总文本段数
	TargetLangs []string          `json:"target_langs"` // 目标语言代码列表（最终确定）
	LangNames   map[string]string `json:"lang_names"`   // 目标语言代码 → 中文名
	KBHits      int               `json:"kb_hits"`      // 知识库命中段数
	ModelHits   int               `json:"model_hits"`   // 模型翻译段数
	FileContext string            `json:"file_context"` // 文件内容摘要/上下文（预留）
	// Translations 原文→译文映射（语言维度），供工单执行器回写 tm_segments 长期沉淀；
	// 不序列化进 SSE/HTTP 响应（体量大且前端无需）。
	Translations map[string]map[string]string `json:"-"`
}

// HandleFile 文件翻译主流程（复刻 skill.py _handle_file_translate）
func (e *Engine) HandleFile(ctx context.Context, filePath string, options map[string]interface{}, prog Progress) *FileTranslateResult {
	// 注入请求级用量记录器（供计量成本核算）
	ctx = e.WithUsageRecorder(ctx)
	if prog == nil {
		prog = func(string, int, int) {}
	}
	// ★ 租户可用性校验
	if err := e.tenantOK(ctx); err != nil {
		return &FileTranslateResult{Skill: "translation", Error: err.Error()}
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	// ★ 格式白名单与工单管道对齐（三期格式补齐）：Office 原格式回写；PDF 版式重建；
	// 其余文本类（txt/csv/srt/vtt/md/json/yaml）统一降级 xlsx 对照表产物
	allowedExt := map[string]bool{
		".docx": true, ".pptx": true, ".xlsx": true, ".pdf": true,
		".txt": true, ".csv": true, ".srt": true, ".vtt": true,
		".md": true, ".json": true, ".yaml": true, ".yml": true,
	}
	if !allowedExt[ext] {
		return &FileTranslateResult{Skill: "translation",
			Error: "不支持的格式（支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml）"}
	}
	if _, err := os.Stat(filePath); err != nil {
		return &FileTranslateResult{Skill: "translation", Error: "文件不存在或无法读取"}
	}

	langsRaw := TargetLangsFromOptions(options)
	kbLangs, directOther, hasOther := SplitOptions(langsRaw)

	// ★ 文件翻译也支持"更多语言"：target_langs 为空或选了 other 时，从 message 解析语言名
	if hasOther || (len(kbLangs) == 0 && len(directOther) == 0) {
		if msg, _ := options["message"].(string); msg != "" {
			parsed, _ := e.parseOtherLangsFromPrompt(ctx, msg)
			if len(parsed) > 0 {
				kbLangs = append(kbLangs, parsed...)
			}
		}
	}
	finalLangs := append([]string{}, kbLangs...)
	finalLangs = append(finalLangs, directOther...)
	// 仍未解析出语言 → 兜底英文（放回 kbLangs 走 KB 翻译路径）
	if len(finalLangs) == 0 {
		kbLangs = []string{"en"}
		finalLangs = []string{"en"}
	}

	// ★ 双模式：fast 快速模式跳过知识库直配——全部语言并入批量模型直翻
	fast := ModeFromOptions(options) == "fast"
	if fast && len(kbLangs) > 0 {
		directOther = append(directOther, kbLangs...)
		kbLangs = nil
		finalLangs = append([]string{}, directOther...)
	}

	// 第1步：理解文件结构
	prog("第1步/3：理解文件结构...", 1, 3)
	texts, err := fileproc.ExtractTexts(filePath)
	if err != nil || len(texts) == 0 {
		return &FileTranslateResult{Skill: "translation", Error: "无法从文件提取文本或文件为空"}
	}

	// 语言名映射
	langNames := map[string]string{}
	for _, lc := range finalLangs {
		langNames[lc] = config.LangNames[lc]
	}
	label := "文档"
	if len(finalLangs) == 1 {
		label = config.LangNames[finalLangs[0]]
		if label == "" {
			label = finalLangs[0]
		}
	}

	// 第2步：翻译
	kbHits := 0
	modelHits := 0

	// 语言 → 原文 → 译文
	langTranslations := map[string]map[string]string{}

	translationMu := sync.Mutex{}
	kbHitsMu := sync.Mutex{}
	modelHitsMu := sync.Mutex{}
	addTrans := func(lc, orig, translated string) {
		translationMu.Lock()
		defer translationMu.Unlock()
		if langTranslations[lc] == nil {
			langTranslations[lc] = map[string]string{}
		}
		langTranslations[lc][orig] = translated
	}
	addKBHit := func() {
		kbHitsMu.Lock()
		kbHits++
		kbHitsMu.Unlock()
	}
	addModelHit := func() {
		modelHitsMu.Lock()
		modelHits++
		modelHitsMu.Unlock()
	}

	// KB 语言：先 KB 直配，未命中的批量模型
	var wg sync.WaitGroup
	if len(kbLangs) > 0 {
		prog(fmt.Sprintf("第2步/3：翻译%s（0/%d）...", label, len(texts)), 2, 3)
		type textItem struct {
			idx  int
			text string
		}
		for _, lc := range kbLangs {
			wg.Add(1)
			go func(lc string) {
				defer wg.Done()
				// 第一遍：KB 匹配
				needModelIdx := []int{}
				for i, t := range texts {
					r, _ := e.TranslateOne(ctx, t, []string{lc}, true, config.StageKBMatch)
					if v, ok := r.Translations[lc]; ok && v != "" {
						addTrans(lc, t, v)
						addKBHit()
					} else {
						needModelIdx = append(needModelIdx, i)
					}
				}
				// 第二遍：批量模型补漏
				if len(needModelIdx) > 0 {
					needTexts := make([]string, len(needModelIdx))
					for i, idx := range needModelIdx {
						needTexts[i] = texts[idx]
					}
					batch := e.BatchTranslate(ctx, needTexts, lc, 15, nil)
					// ★ pro 模式批量审校：本块一次 LLM 调用逐条修正，失败/不符原样保留
					if !fast {
						batch = e.reviewBatchSafe(ctx, needTexts, batch, lc)
					}
					for i, idx := range needModelIdx {
						if batch[i] != "" && batch[i] != "[翻译失败]" {
							addTrans(lc, texts[idx], batch[i])
							addModelHit()
						}
					}
				}
			}(lc)
		}
	}

	// 其他语言：批量模型
	for _, lc := range directOther {
		wg.Add(1)
		go func(lc string) {
			defer wg.Done()
			batch := e.BatchTranslate(ctx, texts, lc, 15, nil)
			// ★ pro 模式批量审校（同上：整块一次调用）
			if !fast {
				batch = e.reviewBatchSafe(ctx, texts, batch, lc)
			}
			for i, t := range texts {
				if batch[i] != "" && batch[i] != "[翻译失败]" {
					addTrans(lc, t, batch[i])
					addModelHit()
				}
			}
		}(lc)
	}
	wg.Wait()
	prog("第2步/3：翻译完成", 2, 3)

	isXlsxInput := ext == ".xlsx"

	// 第3步：写回文件
	prog("第3步/3：写回文件+修正排版...", 3, 3)
	srcDir := filepath.Dir(filePath)
	baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	outputDir := filepath.Join(srcDir, "translated")
	os.MkdirAll(outputDir, 0o755)

	filesOut := []string{}

	if isXlsxInput {
		// ★ xlsx 多 Sheet 模式：单文件输出，每个目标语言一个 Sheet（Sheet 名=语言代码）
		outPath := filepath.Join(outputDir, baseName+"_translated.xlsx")
		if aerr := writeMultiSheetXlsx(filePath, outputDir, baseName, finalLangs, langTranslations); aerr != nil {
			return &FileTranslateResult{Skill: "translation", Error: "xlsx 写回失败: " + aerr.Error()}
		}
		filesOut = append(filesOut, outPath)
	} else {
		// ★ 非 xlsx 格式：每个目标语言独立产物文件，文件名标注语言
		for _, lc := range finalLangs {
			tr := langTranslations[lc]
			if len(tr) == 0 {
				continue
			}
			outBase := fmt.Sprintf("%s_%s%s", baseName, lc, ext)
			outPath := filepath.Join(outputDir, outBase)
			var aerr error
			switch ext {
			case ".docx":
				aerr = fileproc.ApplyDocx(filePath, outPath, tr)
			case ".pptx":
				aerr = fileproc.ApplyPptx(filePath, outPath, tr)
			case ".pdf":
				if perr := fileproc.WriteTranslatedPDFviaDocx(outPath, filePath, tr, lc); perr == nil {
					filesOut = append(filesOut, outPath)
					continue
				}
				aerr = fmt.Errorf("PDF 写回失败")
			case ".txt", ".csv", ".md":
				aerr = writeTranslatedText(outPath, texts, tr)
			default:
				aerr = fmt.Errorf("不支持的写回格式")
			}
			if aerr != nil {
				// 写回失败降级 xlsx 对照表
				aerr2 := fileproc.WriteComparisonXlsx(outPath+".xlsx", texts, tr)
				if aerr2 == nil {
					filesOut = append(filesOut, outPath+".xlsx")
					continue
				}
				continue
			}
			filesOut = append(filesOut, outPath)
		}
	}
	prog("第3步/3：完成", 3, 3)

	reply := fmt.Sprintf("✅ 文件翻译完成：共 %d 段文本，输出 %d 个文件（%s）",
		len(texts), len(filesOut), strings.Join(finalLangs, "/"))
	return &FileTranslateResult{
		Skill: "translation",
		Reply: reply,
		Data: FileTranslateData{
			TotalTexts:   len(texts),
			TargetLangs:  finalLangs,
			LangNames:    langNames,
			KBHits:       kbHits,
			ModelHits:    modelHits,
			Translations: langTranslations, // 原文→译文（不序列化），工单执行器回写 TM
		},
		Files: filesOut,
	}
}

// ============ 从 prompt 解析"其他语言" ============

var otherLangRegexes = []*regexp.Regexp{
	// 中文指令：翻译成/译为/翻译为 + 语言名 + 冒号
	regexp.MustCompile(`(?:翻译成|翻成|译成|翻译为|翻为|译为)\s*(.+?)\s*[：:]\s*`),
	// 中文指令：用 + 语言名 + 翻译
	regexp.MustCompile(`用\s*(.+?)\s*翻译\s*[：:，,]?\s*`),
	// 英文指令：translate to / translation in + 语言名
	regexp.MustCompile(`(?i)(?:translate\s+to|translat(?:e|ion)\s+in)\s+(.+?)\s*[：:，,]?\s*`),
	// 开头句式："XX语：正文"
	regexp.MustCompile(`^(\S+语)\s*[：:]\s*(.+)$`),
}

// parseOtherLangsFromPrompt 从用户提示语中解析非中英目标语言及其余文本。
// 参数 ctx: 上下文；text: 用户输入的提示语。
// 返回: (识别出的语言码列表, 剥离语言提示后的剩余文本)。识别不到语言时返回空列表与原文。
func (e *Engine) parseOtherLangsFromPrompt(ctx context.Context, text string) ([]string, string) {
	clean := text
	for _, re := range otherLangRegexes {
		if m := re.FindStringSubmatch(text); m != nil {
			hint := strings.TrimSpace(m[1])
			cleaned := ""
			if len(m) > 2 {
				cleaned = strings.TrimSpace(m[2])
			}
			code := LangCodeFromName(hint)
			if code == "" {
				code = e.LLMParseLang(ctx, hint)
			}
			if code == "" {
				// 去掉语言名部分，保留剩余作为正文
				cleaned = re.ReplaceAllString(text, "")
			}
			if code != "" {
				rest := strings.Replace(text, m[0], "", 1)
				cleaned = strings.TrimSpace(rest)
				cleaned = strings.TrimPrefix(cleaned, "：")
				cleaned = strings.TrimSpace(cleaned)
				if cleaned == "" && len(m) > 2 {
					cleaned = strings.TrimSpace(m[2])
				}
				return []string{code}, cleaned
			}
		}
	}
	// 兜底：开头是语言名+冒号 "泰语：xxx"
	if m := regexp.MustCompile(`^(\S+?)\s*[：:]\s*(.+)$`).FindStringSubmatch(text); m != nil {
		code := LangCodeFromName(m[1])
		if code != "" {
			return []string{code}, strings.TrimSpace(m[2])
		}
	}
	return nil, clean
}

// reviewBatchSafe 批量审校安全包装：过滤空译文与占位失败项，仅审校有效对；
// 审校结果不改变成功/失败判定（审校输出为空时保留原译文）。
func (e *Engine) reviewBatchSafe(ctx context.Context, sources, translations []string, lang string) []string {
	idxs := make([]int, 0, len(sources))
	var srcs, tgts []string
	for i, tr := range translations {
		if tr == "" || tr == "[翻译失败]" {
			continue
		}
		idxs = append(idxs, i)
		srcs = append(srcs, sources[i])
		tgts = append(tgts, tr)
	}
	if len(idxs) < 2 { // 单段走常规逐段审校收益低，跳过
		return translations
	}
	rev := e.ReviewTranslationBatch(ctx, srcs, tgts, lang, config.StageReview)
	out := make([]string, len(translations))
	copy(out, translations)
	for j, i := range idxs {
		if rev[j] != "" {
			out[i] = rev[j]
		}
	}
	return out
}

// writeMultiSheetXlsx xlsx 多 Sheet 模式：复制原始文件后，每个目标语言新增一个 Sheet，
// 遍历该语言译文逐格替换。Sheet 名=语言代码。
func writeMultiSheetXlsx(srcPath, outputDir, baseName string, langs []string, langTranslations map[string]map[string]string) error {
	outPath := filepath.Join(outputDir, baseName+"_translated.xlsx")
	f, err := excelize.OpenFile(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, lc := range langs {
		tr := langTranslations[lc]
		if len(tr) == 0 {
			continue
		}
		sheetName := lc // Sheet 名 = 语言代码
		srcSheets := f.GetSheetList()
		if _, err := f.NewSheet(sheetName); err != nil {
			continue
		}
		_ = srcSheets // 源内容保留在原 Sheet 中，新 Sheet 写入该语言的完整翻译
		for orig, translated := range tr {
			if orig == "" || translated == "" {
				continue
			}
			// 在新 Sheet 中按顺序写入源文和译文
			for _, sheet := range srcSheets {
				rows, rerr := f.GetRows(sheet)
				if rerr != nil {
					continue
				}
				found := false
				for ri, row := range rows {
					for ci, cellVal := range row {
						if strings.TrimSpace(cellVal) == orig && !found {
							cell, _ := excelize.CoordinatesToCellName(ci+1, ri+1)
							_ = f.SetCellValue(sheetName, cell, translated)
							found = true
							break
						}
					}
					if found {
						break
					}
				}
			}
		}
	}
	// 删除原始 Sheet 副本（保留第一个作为参考）
	return f.SaveAs(outPath)
}

// writeTranslatedText 纯文本类格式直写翻译结果（逐行替换）。
func writeTranslatedText(outPath string, sourceTexts []string, translations map[string]string) error {
	lines := make([]string, len(sourceTexts))
	for i, src := range sourceTexts {
		if tr, ok := translations[strings.TrimSpace(src)]; ok && tr != "" {
			lines[i] = tr
		} else {
			lines[i] = src // 未命中的保留原文
		}
	}
	return os.WriteFile(outPath, []byte(strings.Join(lines, "\n")), 0o644)
}
