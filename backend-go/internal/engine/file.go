// ============ 本文件职责中文说明 ============
// 文件翻译：面向 docx/pptx/xlsx/pdf 等格式的整文件翻译主流程（复刻 skill.py _handle_file_translate）。
// 支持 KB 语言（先 KB 直配、未命中批量模型补漏）与"其他语言"（纯批量模型），
// 从用户 prompt 中解析"其他语言"（正则 + LLM 语言识别兜底），
// 含 pro 模式批量审校、硬闸补漏（墙钟预算+零进展熔断）与漏翻可见性（Untranslated），
// 翻译完成后按语言分别写回 translated/ 目录下独立文件并统计 KB/模型命中数。
// ========================================
package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xuri/excelize/v2"
	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/llm"
)

// FileTranslateResult 文件翻译结果
type FileTranslateResult struct {
	Skill string            `json:"skill"` // 能力标识（固定 "translation"）
	Reply string            `json:"reply"` // 面向用户的完成话术（含统计信息）
	Data  FileTranslateData `json:"data"`  // 结构化翻译数据（文本数/语言/命中统计等）
	Files []string          `json:"files"` // 生成的翻译文件绝对路径列表
	Error string            `json:"error"` // 失败原因（成功时为空）
	// ★ 2026-09-03 需求：文件翻译结果携带实际消耗的 LLM token 数
	TokensUsed int64 `json:"tokens_used"`
}

// FileTranslateData 文件翻译数据
type FileTranslateData struct {
	TotalTexts  int               `json:"total_texts"`  // 从文件中提取的总文本段数
	TargetLangs []string          `json:"target_langs"` // 目标语言代码列表（最终确定）
	LangNames   map[string]string `json:"lang_names"`   // 目标语言代码 → 中文名
	KBHits      int               `json:"kb_hits"`      // 知识库命中段数
	ModelHits   int               `json:"model_hits"`   // 模型翻译段数
	FileContext string            `json:"file_context"` // 文件内容摘要/上下文（预留）
	// Untranslated 硬闸结束后仍未译出的段数（按语言）。
	// ★ 2026-08-26 漏翻可见性修复：此前预算耗尽/零进展熔断只写日志——「尾部表格漏翻」
	//   在工单层面完全不可见，QA/审批无从发现。现随 Data 序列化进工单 payload，
	//   审批台/QA 报告可据此提示人工补译。>0 时 Reply 亦追加告警文案。
	Untranslated map[string]int `json:"untranslated,omitempty"`
	GateWarnings []string       `json:"gate_warnings,omitempty"` // 整改 R1：主路径输出质量/文化闸门警告
	// Translations 原文→译文映射（语言维度），供工单执行器回写 tm_segments 长期沉淀；
	// 不序列化进 SSE/HTTP 响应（体量大且前端无需）。
	Translations map[string]map[string]string `json:"-"`
}

// HandleFile 文件翻译主流程（复刻 skill.py _handle_file_translate）
func (e *Engine) HandleFile(ctx context.Context, filePath string, options map[string]interface{}, prog Progress) *FileTranslateResult {
	// 注入请求级用量记录器（供计量成本核算）
	ctx = e.WithUsageRecorder(ctx)
	// ★ 文件/后台批任务：走专用 LLM 信号量池（容量更大、与交互池隔离），
	//   避免长文档高并发打满共享交互信号量、饿死前台即时翻译、拖垮全站（整改）。
	ctx = llm.WithFileMode(ctx)
	// ★ 缩翻（任务7）：options["max_length"]>0 时启用最长字符限制
	if n := maxLengthOption(options); n > 0 {
		ctx = WithMaxLength(ctx, n)
	}
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
	// ★ PDF：改用 pdf2docx 提取段落（键与写回目标一致，表格/短文本必中），
	//   缓存 DOCX 供多语言写回复用；失败回退 pdftotext 键（产物降级 xlsx 对照表）。
	//   图片内容按产品策略不翻译（2026-08-25 OCR 已整体移除）。
	var pdfCacheDocx string
	if strings.EqualFold(filepath.Ext(filePath), ".pdf") {
		if t2, cache, e2 := fileproc.ExtractTextsPdfDocx(ctx, filePath); e2 == nil && len(t2) > 0 && cache != "" {
			texts = t2
			pdfCacheDocx = cache
			defer os.Remove(cache)
		}
	}

	// ★ 嵌入管线级预取（评审整改 R2）：pro 模式且走 KB 语义检索时，
	//   对去重后的全部源文一次 EmbedBatch 预热缓存并向 ctx 注入向量表——
	//   Embed 调用量从「段数×语言数」降为「去重段数/32」，消除逐段逐语言回源。
	if !fast && len(kbLangs) > 0 {
		if idx := e.getIndex(); idx != nil && len(idx.Vecs) > 0 {
			eb, ek, em, eok := e.resolveStageModel(ctx, config.StageKBEmbed)
			if !eok {
				em = ""
			}
			ctx = e.prefetchEmbeddings(ctx, texts, eb, ek, em)
		}
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
		var kbDoneC int64
		for _, lc := range kbLangs {
			wg.Add(1)
			go func(lc string) {
				defer wg.Done()
				defer recoverPipeline("file_kb:" + lc) // 整改 D4
				// 第一遍：KB 匹配（★ 并行 8 路：长文档逐段串行是耗时大头）
				kbHitIdx := make([]bool, len(texts))
				kbVal := make([]string, len(texts))
				semKB := make(chan struct{}, 8)
				var wgKB sync.WaitGroup
				for i, t := range texts {
					wgKB.Add(1)
					go func(i int, t string) {
						defer wgKB.Done()
						defer recoverPipeline("file_kb_seg") // 整改 D4
						semKB <- struct{}{}
						defer func() { <-semKB }()
						r, err := e.TranslateOne(ctx, t, []string{lc}, true, config.StageKBMatch)
						if err == nil {
							if v, ok := r.Translations[lc]; ok && v != "" {
								kbHitIdx[i] = true
								kbVal[i] = v
							}
						}
						done := atomic.AddInt64(&kbDoneC, 1)
						if done%20 == 1 || int(done) == len(texts) {
							log.Printf("[kb-match] lang=%s progress=%d/%d", lc, done, len(texts))
						}
					}(i, t)
				}
				wgKB.Wait()
				needModelIdx := []int{}
				for i := range texts {
					if kbHitIdx[i] {
						addTrans(lc, texts[i], kbVal[i])
						addKBHit()
					} else {
						needModelIdx = append(needModelIdx, i)
					}
				}
				log.Printf("[kb-match] lang=%s 命中=%d 走模型=%d", lc, len(texts)-len(needModelIdx), len(needModelIdx))
				// 第二遍：批量模型补漏
				if len(needModelIdx) > 0 {
					needTexts := make([]string, len(needModelIdx))
					for i, idx := range needModelIdx {
						needTexts[i] = texts[idx]
					}
					batch := e.BatchTranslate(ctx, needTexts, lc, 15,
						func(done, total int) { prog("file_translate|初翻|"+lc, done, total) })
					// ★ pro 模式批量审校：本块一次 LLM 调用逐条修正，失败/不符原样保留
					if !fast {
						batch = e.reviewBatchSafe(ctx, needTexts, batch, lc,
							func(done, total int) { prog("file_translate|校对|"+lc, done, total) })
					}
					for i, idx := range needModelIdx {
						// ★ 回显检测：模型原样返回源文 = 未翻译，视为缺失走重试
						if batch[i] != "" && batch[i] != "[翻译失败]" && batch[i] != texts[idx] {
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
			defer recoverPipeline("file_batch:" + lc) // 整改 D4
			batch := e.BatchTranslate(ctx, texts, lc, 15,
				func(done, total int) { prog("file_translate|初翻|"+lc, done, total) })
			// ★ pro 模式批量审校（同上：整块一次调用）
			if !fast {
				batch = e.reviewBatchSafe(ctx, texts, batch, lc,
					func(done, total int) { prog("file_translate|校对|"+lc, done, total) })
			}
			for i, t := range texts {
				// ★ 回显检测（与 KB 路径一致）：模型原样返回源文 = 未翻译，视为缺失走重试，
				// 否则非中文目标时源文会被当成「译文」静默写入成品（整改：directOther 原漏回显检测）。
				if batch[i] != "" && batch[i] != "[翻译失败]" && batch[i] != t {
					addTrans(lc, t, batch[i])
					addModelHit()
				}
			}
		}(lc)
	}
	wg.Wait()
	// ★ 复核补漏（文件工单复核第1层）：扫描各语言未命中段落，重试一次批量模型。
	// 治「有的中文还没翻译就贴上来」：首翻失败的段不再静默缺失。
	// ★ 硬闸重试（方案语义）：对缺失段「重启 LLM 翻译」；仍回显则保留原文并计入告警。
	// ★ 成本预算护栏（不改变上述语义本体）：
	//   ① 墙钟预算 FILE_HARDGATE_MAX_SEC（默认 600s）——2026-08-26 漏翻整改：
	//      预算检查从「仅轮首」细化为「轮首+逐段之间」，防止一轮内部超支导致
	//      尾部段落（如末尾表格的「无」）从未获得补翻机会；
	//   ② 零进展熔断：连续 2 轮缺失数不减 → 判定模型稳定回显/失败，退出循环；
	//   ③ 每轮先小批量（bs=10，<sN> 显式配对）重译——对超短段（「无/有/日期」）
	//      的抗回显性显著优于逐段单发，且吞吐高一个量级；批量后仍缺的段再逐段兜底。
	untranslated := map[string]int{} // 语言 → 硬闸结束后仍未译出段数（进 Data 供审批/QA 可见）
	for _, lc := range finalLangs {
		missing0 := []string{}
		for _, t := range texts {
			if _, ok := langTranslations[lc][t]; !ok {
				missing0 = append(missing0, t)
			}
		}
		if len(missing0) == 0 {
			continue
		}
		budget := hardGateBudget()
		loopStart := time.Now()
		prevMissing := -1
		zeroProgressRounds := 0
		for attempt := 1; ; attempt++ {
			if ctx.Err() != nil {
				break
			}
			still := []string{}
			for _, t := range texts {
				if _, ok := langTranslations[lc][t]; !ok {
					still = append(still, t)
				}
			}
			if len(still) == 0 {
				break
			}
			if elapsed := time.Since(loopStart); elapsed > budget {
				log.Printf("[tm-hardgate] lang=%s 预算耗尽（%s），剩余 %d 段未译", lc, elapsed.Round(time.Second), len(still))
				break
			}
			if prevMissing >= 0 && len(still) >= prevMissing {
				zeroProgressRounds++
				if zeroProgressRounds >= 2 {
					log.Printf("[tm-hardgate] lang=%s 连续 %d 轮零进展（缺失 %d 段），停止重试", lc, zeroProgressRounds, len(still))
					break
				}
			} else {
				zeroProgressRounds = 0
			}
			prevMissing = len(still)
			log.Printf("[tm-hardgate] lang=%s round=%d 重启LLM重译 %d 段", lc, attempt, len(still))
			// 轮间间隔防打爆供应商；可被取消打断（不再无条件睡死 500ms）
			select {
			case <-ctx.Done():
			case <-time.After(500 * time.Millisecond):
			}
			if ctx.Err() != nil {
				break
			}
			// 本轮第1优先：小批量重译（bs=10）。BatchTranslate 内部含动态批与低解析率二次收编，
			// 对短段回显的纠正率高于逐段单发。
			batch := e.BatchTranslate(ctx, still, lc, 10,
				func(done, total int) { prog("file_translate|初翻|"+lc, done, total) })
			for i, m := range still {
				if v := batch[i]; v != "" && v != "[翻译失败]" && v != m {
					addTrans(lc, m, v)
				}
			}
			// 本轮第2优先：批量后仍缺失的段逐段兜底（全新调用，绕过 KB/缓存）。
			// ★ 段粒度预算检查：每段之间复核剩余预算，保证尾部段落也能分到时间。
			for _, m := range still {
				if _, ok := langTranslations[lc][m]; ok {
					continue
				}
				if time.Since(loopStart) > budget {
					break
				}
				r, err := e.TranslateOne(ctx, m, []string{lc}, false, config.StageAIInitial)
				if err != nil {
					continue
				}
				if v, ok := r.Translations[lc]; ok && v != "" && v != "[翻译失败]" && v != m {
					addTrans(lc, m, v)
				}
			}
		}
		// ★ 可见性收尾：无论因预算/零进展/取消退出，剩余缺失数必须浮出水面
		remain := 0
		for _, t := range texts {
			if _, ok := langTranslations[lc][t]; !ok {
				remain++
			}
		}
		if remain > 0 {
			log.Printf("[tm-hardgate] lang=%s 结束：仍有 %d/%d 段未译出（已写入工单 Untranslated 供人工补译）", lc, remain, len(texts))
			untranslated[lc] = remain
		}
	}
	prog("第2步/3：翻译完成", 2, 3)

	// 整改 R1：文件主翻译路径统一走约束闸门 + 语言文化闸门。
	// 硬约束闸门（数字/格式/非源语言/乱码等）必须强制：首轮不过带反馈重翻一次，
	// 否则错误会直接落入成品文件（如成本表数字错）。文化闸门仍仅警告。
	// fast 模式同样强制硬闸（交付物正确性优先于速度）。
	gateWarnings := e.applySegmentGates(ctx, langTranslations, true)

	isXlsxInput := ext == ".xlsx"

	// 第3步：写回文件
	prog("第3步/3：写回文件+修正排版...", 3, 3)
	srcDir := filepath.Dir(filePath)
	baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))
	outputDir := filepath.Join(srcDir, "translated")
	os.MkdirAll(outputDir, 0o755)

	filesOut := []string{}

	if isXlsxInput {
		outPath := filepath.Join(outputDir, baseName+"_translated.xlsx")
		if len(finalLangs) == 1 {
			// ★ 单目标语言：原地替换单元格为译文，产物文件即译文本身
			// （符合「把文件翻成 X 语」的预期；原文件保持不变，下载的 _translated.xlsx 为译文）。
			// 此前多 Sheet 模式会把原文 Sheet 留在首位、译文放新增 Sheet，Excel 默认打开原文 Sheet
			// 造成「还是中文」的误解（实际译文在 en Sheet 中已正确生成）。
			if aerr := fileproc.ApplyXlsx(filePath, outPath, langTranslations[finalLangs[0]]); aerr != nil {
				return &FileTranslateResult{Skill: "translation", Error: "xlsx 写回失败: " + aerr.Error()}
			}
		} else {
			// ★ 多目标语言：单文件多 Sheet，每个目标语言一个 Sheet（Sheet 名=语言代码）
			if aerr := writeMultiSheetXlsx(filePath, outputDir, baseName, finalLangs, langTranslations); aerr != nil {
				return &FileTranslateResult{Skill: "translation", Error: "xlsx 写回失败: " + aerr.Error()}
			}
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
				if pdfCacheDocx != "" {
					// 两阶段：复用提取期缓存 DOCX（键对齐，图片/排版零破坏）
					if perr := fileproc.ApplyTranslatedPdfFromDocx(ctx, outPath, pdfCacheDocx, tr, lc); perr == nil {
						filesOut = append(filesOut, outPath)
						continue
					}
					if perr := fileproc.WriteTranslatedPDFviaDocx(ctx, outPath, filePath, tr, lc); perr == nil {
						filesOut = append(filesOut, outPath)
						continue
					}
					aerr = fmt.Errorf("PDF 写回失败")
				} else {
					aerr = fmt.Errorf("PDF 写回失败")
				}
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
	// ★ 漏翻可见性：存在未译出段时在完成话术与结构化数据中同时告警（审批/QA 可据此补译）
	if len(untranslated) > 0 {
		parts := make([]string, 0, len(untranslated))
		for _, lc := range finalLangs {
			if n := untranslated[lc]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s×%d", lc, n))
			}
		}
		reply += fmt.Sprintf("；⚠️ 有 %d 段未能译出已保留原文（%s），请人工检查", len(parts), strings.Join(parts, ","))
	}
	if len(gateWarnings) > 0 {
		reply += fmt.Sprintf("；⚠️ 质量校验提示 %d 条，详见结构化返回", len(gateWarnings))
	}
	// ★ 2026-09-03 需求：文件翻译结果附带实际 token 消耗
	tp, tc := e.UsageTokens(ctx)
	tokensUsed := tp + tc
	return &FileTranslateResult{
		Skill: "translation",
		Reply: reply,
		Data: FileTranslateData{
			TotalTexts:   len(texts),
			TargetLangs:  finalLangs,
			LangNames:    langNames,
			KBHits:       kbHits,
			ModelHits:    modelHits,
			Untranslated: untranslated,     // 语言→未译出段数（>0 时审批台可见）
			Translations: langTranslations, // 原文→译文（不序列化），工单执行器回写 TM
			GateWarnings: gateWarnings,     // 整改 R1：主路径输出质量/文化闸门警告
		},
		Files:      filesOut,
		TokensUsed: tokensUsed,
	}
}

// ============ 从 prompt 解析"其他语言" ============

// otherLangRegexes 从用户 prompt 中识别「目标语言指令」的正则集合（中英文常见句式），
// 命中后捕获组即目标语言名，用于无显式语言参数时推断输出语言。
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
func (e *Engine) reviewBatchSafe(ctx context.Context, sources, translations []string, lang string, onDone func(done, total int)) []string {
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
		if onDone != nil {
			onDone(len(translations), len(translations))
		}
		return translations
	}
	if onDone != nil {
		onDone(0, len(translations))
	}
	rev := e.ReviewTranslationBatch(ctx, srcs, tgts, lang, config.StageReview)
	out := make([]string, len(translations))
	copy(out, translations)
	for j, i := range idxs {
		if rev[j] != "" {
			out[i] = rev[j]
		}
	}
	if onDone != nil {
		onDone(len(translations), len(translations))
	}
	return out
}

// hardGateBudget 硬闸补漏循环的墙钟预算（FILE_HARDGATE_MAX_SEC，默认 600s）。
// 用途：限制「无限轮次重试 × 实费计费」的成本上限（评审 B5）；产品语义（尽力译出全部
// 段落）在预算内不变，超预算后剩余段保留原文。
func hardGateBudget() time.Duration {
	if v := strings.TrimSpace(os.Getenv("FILE_HARDGATE_MAX_SEC")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 10 * time.Minute
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
