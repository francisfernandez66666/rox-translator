// ============ 本文件职责中文说明 ============
// 文件翻译：面向 docx/pptx/xlsx/pdf 等格式的整文件翻译主流程（复刻 skill.py _handle_file_translate）。
// 支持 KB 语言（先 KB 直配、未命中批量模型补漏）与"其他语言"（纯批量模型），
// 从用户 prompt 中解析"其他语言"（正则 + LLM 语言识别兜底），
// 含 pro 模式批量审校、硬闸补漏（墙钟预算+零进展熔断）与漏翻可见性（Untranslated），
// 翻译完成后按语言分别写回产物文件（translated/<落盘名主干>/ 每上传件一个子目录）并统计 KB/模型命中数。
// ★B3（方案 A2）：主流程挂载逐段事件回调 emit（segment_done/segment_final/segments_sealed），
// 让 SSE 通道边翻边上屏；发射器与协议见 file_events.go，工单/非流式路径 emit=nil 零开销。
// ★ 2026-09-22 三处主链整改（本文件是落点）：
//   - 译文可用性判定收口到 translation_guard.go 的 IsTranslationUsable（KB/模型/硬闸 5 个写入点同一口径）；
//   - 产物文件名按目标语言翻译（file_name.go，失败一律回落原名，扩展名永不参与）；
//   - 写回后做结构指纹保真比对 + 透出未译段清单（file_fidelity.go → Data.GateWarnings / UntranslatedSegments）。
//
// ========================================
package engine

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xuri/excelize/v2"
	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/gate"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/tenant"
)

// FileTranslateResult 文件翻译结果
type FileTranslateResult struct {
	Skill string            `json:"skill"` // 能力标识（固定 "translation"）
	Reply string            `json:"reply"` // 面向用户的完成话术（含统计信息）
	Data  FileTranslateData `json:"data"`  // 结构化翻译数据（文本数/语言/命中统计等）
	Files []string          `json:"files"` // 生成的翻译文件绝对路径列表
	Error string            `json:"error"` // 失败原因（成功时为空）
	// ★ 工单双模式（2026-09-13）：还原模式（delivery=restore）完成后各语言的纯文案 .md
	//   旁路产物（兜底交付，不占主产物位）；纯文案模式（text）主产物即 .md，本字段留空。
	TextFiles []string `json:"text_files,omitempty"`
	// ★ 2026-09-03 需求：文件翻译结果携带实际用量。
	// 2026-09-19 积分口径：token 裸值仅内部计量（不外发），对外只出 points_used。
	TokensUsed int64 `json:"-"`
	PointsUsed int64 `json:"points_used"`
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
	// UntranslatedSegments 仍未译出的**源文段清单**（语言→段，按提取顺序）。
	// ★ P1 整改（漏译静默通过）：只有 Untranslated 计数时，用户与 QA 无从知道缺的是哪几段，
	//   「已完成 + 大量中文」照样能同时成立（实测漏译 20.8% 的工单）。清单直接可复制去人工补译。
	//   每语言上限 untranslatedSegmentsCap 条，防爆 SSE/工单 payload。
	UntranslatedSegments map[string][]string `json:"untranslated_segments,omitempty"`
	GateWarnings         []string            `json:"gate_warnings,omitempty"` // 整改 R1：主路径输出质量/文化闸门警告 + 结构保真警告
	// ★ 工单双模式（2026-09-13）：原格式写回失败（重试后仍败）已降级纯文案交付的语言代码。
	//   翻译内容已交付（.md 在 Files 中），工单仍为成功，但轨迹/通知据此提示"版式未还原"。
	DegradedLangs []string `json:"degraded_langs,omitempty"`
	// Translations 原文→译文映射（语言维度），供工单执行器回写 tm_segments 长期沉淀；
	// 不序列化进 SSE/HTTP 响应（体量大且前端无需）。
	Translations map[string]map[string]string `json:"-"`
	// SourceSegments 提取顺序的源文分段（与 Translations 的键一一对应）。
	// ★ 2026-09-18：Translations 是 map，**没有顺序**，无法据此还原「第 i 段」。工单层要把
	//   「源文段→译文段」的精确配对落进 ticket_segments 真值表（PDF 工单的对照编辑器靠它
	//   才不错位，详见 store/segments.go），故把有序源文段一并带出。同样不序列化。
	SourceSegments []string `json:"-"`
}

// writebackDelivery 写回交付形态（纯函数，便于单测）：
//   - 返回 "xlsx"：无原格式回写能力的格式（srt/vtt/json/yaml/yml），以 xlsx 对照表为唯一交付形态；
//   - 返回 "inplace"：有原格式写回能力的格式（pdf/docx/pptx/txt/csv/md），写回失败自动重试、不降级 xlsx。
func writebackDelivery(ext string) string {
	switch ext {
	case ".srt", ".vtt", ".json", ".yaml", ".yml":
		return "xlsx"
	default:
		return "inplace"
	}
}

// optionString options 通用字符串读取（非字符串/缺失返回空串）。
func optionString(v interface{}) string {
	s, _ := v.(string)
	return s
}

// anydocSourceExt 纯文案模式下需经 anydoc 转 MD 的格式（老格式/ODF/RTF/EPUB + PDF 快速提取路径）。
func anydocSourceExt(ext string) bool {
	return fileproc.AnydocFormats[ext] || ext == ".pdf"
}

// leakedLangFailRatio 漏译率判失败的阈值比例。**当前维持 2026-09-09 用户决策的 50% 不变**，
// 抽成常量的目的是让「收紧口径」成为一次可评审的单点改动而不是改代码逻辑：
// 实测本工单漏译 20.8% 仍被判「已完成」，说明 0.5 偏松（建议见汇报），但阈值一旦下调会
// 让存量工单大面积转失败（用户已扣积分），必须与产品/运营对齐并跑全量 UAT 后再动。
const leakedLangFailRatio = 0.5

// leakedLang 漏译率硬闸判定（纯函数，便于单测）：
// 某语言未译出段数占比 > leakedLangFailRatio（当前 50%）时返回该语言代码（触发工单失败）；否则返回 ""。
// remain 为未译出段数、total 为源文总段数；remain==0 或 total==0 不触发。
func leakedLang(lang string, remain, total int) string {
	if remain > 0 && total > 0 && float64(remain)/float64(total) > leakedLangFailRatio {
		return lang
	}
	return ""
}

// fetchBrandTerms 查询该租户 KB 中全部 module=brand + layer=1 术语，
// 返回 map[目标语言]map[源品牌名]规定译法（如 ru: 极石汽车→ROX）。用于：
//  1. 翻译前品牌保护（把源文品牌名替换为规定译法，避免模型音译成 Киджиш 等）；
//  2. 翻译后归一化（剥离 ROX vehicles/motor/汽车 等后缀）。
func (e *Engine) fetchBrandTerms(ctx context.Context, retrieve string) map[string]map[string]string {
	if e.St == nil || strings.TrimSpace(retrieve) == "" {
		return nil
	}
	tid := tenant.FromContext(ctx)
	if tid <= 0 {
		tid = 1
	}
	var orgID int64
	if uid := tenant.UserFromContext(ctx); uid > 0 {
		if u, uerr := e.St.GetUser(uid, tid); uerr == nil && u != nil {
			orgID = u.OrgID
		}
	}
	if len([]rune(retrieve)) > 200 {
		retrieve = string([]rune(retrieve)[:200])
	}
	ents, err := e.St.FindTermsBySubstring(tid, orgID, "zh", retrieve)
	if err != nil || len(ents) == 0 {
		return nil
	}
	out := map[string]map[string]string{}
	for _, ent := range ents {
		if ent == nil || ent.Module != "brand" || ent.Layer != 1 || ent.TargetLang == "" || strings.TrimSpace(ent.TargetText) == "" {
			continue
		}
		lc := ent.TargetLang
		if _, ok := out[lc]; !ok {
			out[lc] = map[string]string{}
		}
		src := strings.TrimSpace(ent.SourceText)
		if _, dup := out[lc][src]; !dup {
			out[lc][src] = strings.TrimSpace(ent.TargetText)
		}
	}
	return out
}

// protectSourceByLang 翻译前品牌保护：把源文中的源品牌名（如 极石/极石汽车）替换为该
// 目标语言的规定译法（如 ru→ROX），使模型直接输出 ROX、不会音译成 Киджиш 等。
// terms 为该语言的 src→target 映射；长词优先替换（极石汽车 先于 极石，避免拆残）。
// 返回替换后的源文数组与是否发生替换（无替换时原样返回原切片引用）。
func protectSourceByLang(texts []string, terms map[string]string) ([]string, bool) {
	if len(terms) == 0 {
		return texts, false
	}
	keys := make([]string, 0, len(terms))
	for k := range terms {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len([]rune(keys[i])) > len([]rune(keys[j])) })
	changed := false
	out := make([]string, len(texts))
	for i, t := range texts {
		s := t
		for _, k := range keys {
			v := terms[k]
			if strings.Contains(s, k) {
				s = strings.ReplaceAll(s, k, v)
				changed = true
			}
		}
		out[i] = s
	}
	if !changed {
		return texts, false
	}
	return out, true
}

// normalizeFileBrandTerms 文件路径品牌术语归一化（2026-09-10 需求，与对话路径一致）：
// 源文命中 KB module=brand + layer=1 术语时，对 langTranslations 中每个命中语言的
// 每条段落译文执行 gate.NormalizeBrandTerm，剥离「ROX vehicles/motor/автомобиль」
// 等自创后缀，统一品牌名为规定译法。翻译前另有 protectSourceByLang 做源文品牌保护
// （见文件主流程），此处为译后又一道兜底。返回被归一的语言数。
func (e *Engine) normalizeFileBrandTerms(ctx context.Context, texts []string, langTranslations map[string]map[string]string) int {
	if e.St == nil || len(texts) == 0 || len(langTranslations) == 0 {
		return 0
	}
	retrieve := strings.Join(texts, "\n")
	if len([]rune(retrieve)) > 200 {
		retrieve = string([]rune(retrieve)[:200])
	}
	bm := e.fetchBrandTerms(ctx, retrieve)
	if len(bm) == 0 {
		return 0
	}
	fixedLang := map[string]bool{}
	for lc, segs := range langTranslations {
		terms := bm[lc]
		if len(terms) == 0 {
			continue
		}
		for orig, tr := range segs {
			if strings.TrimSpace(tr) == "" {
				continue
			}
			changed := false
			n := tr
			for _, tgt := range terms {
				if nn := gate.NormalizeBrandTerm(n, tgt); nn != n {
					n = nn
					changed = true
				}
			}
			if changed {
				segs[orig] = n
				fixedLang[lc] = true
			}
		}
	}
	return len(fixedLang)
}

// HandleFile 文件翻译主流程（复刻 skill.py _handle_file_translate）
// ★B3（方案 A2）：新增逐段事件回调 emit（与 prog 同闭包风格，仅 SSE 文件通道注入；
// 工单 worker / 非流式接口传 nil，事件层整体空转零开销）。事件协议与稳定键见 file_events.go。
func (e *Engine) HandleFile(ctx context.Context, filePath string, options map[string]interface{}, prog Progress, emit SegmentEmit) *FileTranslateResult {
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
	// ★ 工单双模式（2026-09-13）：options["delivery"]=="text" → 纯文案模式（anydoc/原生提取，
	//   交付译文 .md，不还原版式）；restore/空 → 还原文件模式（既有原位回写管线，行为不变）。
	deliveryText := strings.EqualFold(strings.TrimSpace(optionString(options["delivery"])), "text")
	// ★ 格式白名单与工单管道对齐（三期格式补齐）：Office 原格式回写；PDF 版式重建；
	// 其余文本类（txt/csv/srt/vtt/md/json/yaml）统一降级 xlsx 对照表产物；
	// 纯文案模式额外准入 anydoc 独占格式（doc/xls/ppt 老格式、odt/ods/odp、rtf/epub 等）。
	allowedExt := map[string]bool{
		".docx": true, ".pptx": true, ".xlsx": true, ".pdf": true,
		".txt": true, ".csv": true, ".srt": true, ".vtt": true,
		".md": true, ".json": true, ".yaml": true, ".yml": true,
	}
	if !allowedExt[ext] {
		if !deliveryText || !fileproc.AnydocFormats[ext] {
			if deliveryText {
				return &FileTranslateResult{Skill: "translation",
					Error: "不支持的格式（纯文案模式支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml/doc/xls/ppt/odt/ods/odp/rtf/epub 等）"}
			}
			return &FileTranslateResult{Skill: "translation",
				Error: "不支持的格式（支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml）"}
		}
	}
	// 显式判一次可读性：落盘件可能已被保留期清理删掉，提前拦下才能给出「文件不存在」
	// 这种用户可行动的文案，而不是把底层 open 错误原样抛进工单轨迹。
	if _, err := os.Stat(filePath); err != nil {
		return &FileTranslateResult{Skill: "translation", Error: "文件不存在或无法读取"}
	}

	// 目标语言解析：显式参数拆成「KB 覆盖语言」与「其他语言」两拨——两拨的翻译路径不同
	// （前者先 KB 直配、未命中才走批量补漏；后者直接批量模型），后面各起协程并发处理。
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
	// ★ 纯文案模式（2026-09-13）：anydoc 独占格式（含 PDF 快速路径）先经 Rust 本地转换
	//   成 GFM Markdown 落临时对齐源，再走既有 MD 结构感知提取器（剥离结构标记→纯文本键）。
	var mdAlignPath string
	if deliveryText && anydocSourceExt(ext) {
		md, aerr := fileproc.AnydocToMarkdown(ctx, filePath)
		if aerr != nil {
			return &FileTranslateResult{Skill: "translation", Error: aerr.Error()}
		}
		mdAlignPath = filePath + ".anydoc.src.md"
		if werr := os.WriteFile(mdAlignPath, []byte(md), 0o644); werr != nil {
			return &FileTranslateResult{Skill: "translation", Error: "纯文案模式中间文件写入失败: " + werr.Error()}
		}
		defer os.Remove(mdAlignPath)
	} else if deliveryText && ext == ".md" {
		mdAlignPath = filePath // 源本就是 Markdown：写回对齐直接用原文件，不产出副本
	}
	var texts []string
	var err error
	if mdAlignPath != "" {
		texts, err = fileproc.ExtractTexts(mdAlignPath)
	} else {
		texts, err = fileproc.ExtractTexts(filePath)
	}
	if err != nil || len(texts) == 0 {
		return &FileTranslateResult{Skill: "translation", Error: "无法从文件提取文本或文件为空"}
	}
	// ★ PDF（2026-09-18 新链）：原地替换（redact+overlay）——原版式/原字体/不越界。
	//   提取键与写回目标完全一致（同一矢量栅格切分口径），不再产出 pdf2docx 缓存 DOCX。
	//   图片内容按产品策略不翻译；纯文案模式不走此链（anydoc 提取，交付 .md）。
	if !deliveryText && strings.EqualFold(filepath.Ext(filePath), ".pdf") {
		if t2, e2 := fileproc.ExtractTextsPdfOverlay(ctx, filePath); e2 == nil && len(t2) > 0 {
			// 整体替换而非合并：新键的切分口径与写回目标严格同一（见 pdf_overlay.py 的
			// _merged_segments），混用会让「段序」与「译文键」错位，真值表也落不对。
			texts = t2
		} else if e2 != nil {
			// 提取失败不判死：overlay 依赖部署环境的 pymupdf，缺依赖/超时只意味着拿不到
			// 单元格级切分，上面 ExtractTexts 的既有 PDF 文本键（pdftotext，缺则纯 Go 库）
			// 仍能出译文——降级为版式重建交付，绝不因新链不可用把整单打回。
			log.Printf("[file] PDF overlay 提取失败（回退既有提取键）: %v", e2)
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

	// 语言名映射（供前端与话术展示；未收录的语言码取不到中文名，下游按原码显示）
	langNames := map[string]string{}
	for _, lc := range finalLangs {
		langNames[lc] = config.LangNames[lc]
	}
	// 进度话术用的主语：单目标语言时直接报语言名，多语言时统称「文档」
	label := "文档"
	if len(finalLangs) == 1 {
		label = config.LangNames[finalLangs[0]]
		if label == "" {
			label = finalLangs[0]
		}
	}

	// 第2步：翻译
	// 下面三个聚合量会被「每语言一个 goroutine、语言内再每段一个 goroutine」并发写入，
	// 因此一律配锁访问（Go 并发写 map 是直接 fatal，不是可恢复 error）。
	kbHits := 0
	modelHits := 0

	// 语言 → 原文 → 译文
	langTranslations := map[string]map[string]string{}

	// ★ 品牌术语预取（2026-09-10 需求）：一次查询取出 KB module=brand+layer=1 术语，
	//   供文件各语言「翻译前品牌保护」与「翻后归一化」共用（map[lang]map[src]target）。
	//   源文品牌名（极石/极石汽车）将被替换为规定译法（ROX），杜绝模型音译成
	//   Киджиш 等（音译无法靠事后剥后缀修正）。
	var brandTermsAll map[string]map[string]string
	{
		retrieveTexts := texts
		if len(retrieveTexts) > 8 {
			retrieveTexts = texts[:8]
		}
		termRetrieve := strings.Join(retrieveTexts, "\n")
		brandTermsAll = e.fetchBrandTerms(ctx, termRetrieve)
		if len(brandTermsAll) > 0 {
			log.Printf("[brandterm] 文件路径加载品牌术语 %d 个语言（翻译前保护+翻后归一化）", len(brandTermsAll))
		}
	}

	// 三把锁按写入域拆分（译文表 / KB 命中数 / 模型命中数）：计数是高频短临界区，
	// 与 map 写入共用一把锁会让锁竞争随段数线性放大。
	translationMu := sync.Mutex{}
	kbHitsMu := sync.Mutex{}
	modelHitsMu := sync.Mutex{}
	addTrans := func(lc, orig, translated string) {
		// ★ 裸写入器：本函数**不做**可用性判定（S8 敏感拦截段要直接预填占位交付，
		//   见 sensitiveBlockedSegments）。因此所有调用方必须先过 IsTranslationUsable
		//   再写入——「键存在」= 「已译出」是硬闸与 untranslated 统计的共同前提（P0 整改 RC-2）。
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

	// ★ S8 敏感词兑底闸（输入侧·段级）：命中段不送 KB/模型（上游零暴露），
	// 各语言直接预填占位交付；其余段照常翻译。
	blockedSeg := e.sensitiveBlockedSegments(ctx, texts, append(append([]string{}, kbLangs...), directOther...), addTrans)

	// ★B3（A2）：逐段事件发射器（emit=nil 时 nil-safe 全空转，工单/非流式路径零开销）。
	// 挂载点=方案 A2 第 2 条：KB 直译即中即推 / 批回调按块推初翻 / 语言收尾 diff+封印。
	se := newSegEmitter(emit, texts, blockedSeg)
	if se.on() {
		// 拦截段已预填占位：立即补推 segment_done，前端把这些行标成「已拦截」而非空白
		for _, lc := range append(append([]string{}, kbLangs...), directOther...) {
			for _, t := range texts {
				if blockedSeg[t] {
					se.done(lc, t, langTranslations[lc][t])
				}
			}
		}
	}

	// KB 语言：先 KB 直配，未命中的批量模型
	// wg 是本步骤唯一的收敛屏障：硬闸补漏、闸门与写回都依赖「所有语言的译文已齐」，
	// 所以任何跨语言读取都必须排在 wg.Wait() 之后。
	var wg sync.WaitGroup
	if len(kbLangs) > 0 {
		prog(fmt.Sprintf("第2步/3：翻译%s（0/%d）...", label, len(texts)), 2, 3)
		type textItem struct {
			idx  int
			text string
		}
		// 进度计数被该语言的 8 路段级协程并发自增，必须用 atomic（普通 int 会丢计数）
		var kbDoneC int64
		for _, lc := range kbLangs {
			wg.Add(1)
			go func(lc string) {
				defer wg.Done()
				defer recoverPipeline("file_kb:" + lc) // 整改 D4
				// 第一遍：KB 匹配（★ 并行 8 路：长文档逐段串行是耗时大头）
				// 两个等长切片按「段下标」回填而非写共享 map：段与段互不相关，
				// 既免掉每段一次加锁，也天然保住提取顺序（写回阶段依赖该顺序）。
				kbHitIdx := make([]bool, len(texts))
				kbVal := make([]string, len(texts))
				// 缓冲通道当信号量：同时最多 8 段在打 KB 检索/embedding，防大文档把上游打爆
				semKB := make(chan struct{}, 8)
				var wgKB sync.WaitGroup
				for i, t := range texts {
					if blockedSeg[t] { // ★ S8：拦截段不进模型链路
						continue
					}
					wgKB.Add(1)
					go func(i int, t string) {
						defer wgKB.Done()
						defer recoverPipeline("file_kb_seg") // 整改 D4
						semKB <- struct{}{}
						defer func() { <-semKB }()
						r, err := e.TranslateOne(ctx, t, []string{lc}, true, config.StageKBMatch)
						if err == nil {
							// ★ P0 整改 RC-2：命中判定与模型路径同口径走 IsTranslationUsable，
							//   **不能只判非空**——KB/TM 里「源文=译文」的脏行会把中文当成
							//   「命中的译文」写进成品，且键一旦写入，硬闸按「键存在即已译出」
							//   会永久跳过该段（不重试、不计 untranslated、不告警）。
							//   判不过 = 不算命中 ⇒ 该段落入 needModelIdx 走模型补漏。
							if v, ok := r.Translations[lc]; ok && IsTranslationUsable(t, v) {
								kbHitIdx[i] = true
								kbVal[i] = v
								se.done(lc, t, v) // ★B3：KB 直译命中即推（A2 挂载点1，替代攒齐屏障的上屏时延）
							}
						}
						done := atomic.AddInt64(&kbDoneC, 1)
						// 日志节流：每 20 段一条 + 首段（余数 1，用来确认协程真的起来了）+ 末段必打；
						// 千段文档逐段打日志会让日志本身成为主要成本。
						if done%20 == 1 || int(done) == len(texts) {
							log.Printf("[kb-match] lang=%s progress=%d/%d", lc, done, len(texts))
						}
					}(i, t)
				}
				wgKB.Wait()
				// ★ 命中/补漏分派收口到纯函数 collectKBPass（内部再走一次 IsTranslationUsable，
				//   写入点判定优先于上游并行判定，杜绝「只判非空」的旧口径复活）。
				kbAccepted, needModelIdx := collectKBPass(texts, kbHitIdx, kbVal, blockedSeg)
				for _, i := range kbAccepted {
					addTrans(lc, texts[i], kbVal[i])
					addKBHit()
				}
				log.Printf("[kb-match] lang=%s 命中=%d 走模型=%d 拦截=%d", lc,
					len(kbAccepted), len(needModelIdx), len(texts)-len(kbAccepted)-len(needModelIdx))
				// 第二遍：批量模型补漏
				if len(needModelIdx) > 0 {
					needTexts := make([]string, len(needModelIdx))
					for i, idx := range needModelIdx {
						needTexts[i] = texts[idx]
					}
					// ★ 品牌保护（2026-09-10）：把该语言源文中的品牌名替换为规定译法，
					//   使模型输出 ROX 而非音译 Киджиш；返回的 batch 仍按原 needTexts 索引对应。
					protSrc, _ := protectSourceByLang(needTexts, brandTermsAll[lc])
					batch := e.BatchTranslate(ctx, protSrc, lc, 15,
						// ★B3：段回调升级为「进度+逐段 segment_done」；gidx=needModelIdx 将批下标映射回
						// texts 全局段号（事件带原始源文而非品牌保护后的 protSrc，保证前端行对得上号）
						se.batchCB(lc, func(done, total int) { prog("file_translate|初翻|"+lc, done, total) }, needModelIdx))
					// ★ pro 模式批量审校：本块一次 LLM 调用逐条修正，失败/不符原样保留
					if !fast {
						batch = e.reviewBatchSafe(ctx, protSrc, batch, lc,
							func(done, total int) { prog("file_translate|校对|"+lc, done, total) })
					}
					for i, idx := range needModelIdx {
						// ★ 回显检测统一走 IsTranslationUsable（原此处手写 3 个不等式）：
						//   空/失败占位/与源文同文/指令回显残留任一命中即视为未译出，
						//   留给硬闸重试并计入 untranslated，绝不静默写进成品。
						if i < len(batch) && IsTranslationUsable(texts[idx], batch[i]) {
							addTrans(lc, texts[idx], batch[i])
							addModelHit()
							se.final(lc, texts[idx], batch[i], "reviewed") // ★B3：审校后终稿（同文自动去重）
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
			// ★ 品牌保护（2026-09-10）：同 KB 路径，源文品牌名替换为规定译法防音译。
			// ★ S8：仅送审未拦截段（命中段不出现于任何上游调用）。
			sendIdx := make([]int, 0, len(texts))
			for i, t := range texts {
				if !blockedSeg[t] {
					sendIdx = append(sendIdx, i)
				}
			}
			sendTexts := make([]string, len(sendIdx))
			for i, idx := range sendIdx {
				sendTexts[i] = texts[idx]
			}
			protSrc, _ := protectSourceByLang(sendTexts, brandTermsAll[lc])
			batch := e.BatchTranslate(ctx, protSrc, lc, 15,
				// ★B3：同 KB 路径——进度 + 逐段 segment_done（sendIdx 映射回全局段号/原始源文）
				se.batchCB(lc, func(done, total int) { prog("file_translate|初翻|"+lc, done, total) }, sendIdx))
			// ★ pro 模式批量审校（同上：整块一次调用）
			if !fast {
				batch = e.reviewBatchSafe(ctx, protSrc, batch, lc,
					func(done, total int) { prog("file_translate|校对|"+lc, done, total) })
			}
			for i, idx := range sendIdx {
				t := texts[idx]
				// ★ 回显检测（与 KB 路径同一口径，见 translation_guard.go）：模型原样返回源文
				// = 未翻译，视为缺失走重试，否则非中文目标时源文会被当成「译文」静默写入成品。
				if i < len(batch) && IsTranslationUsable(t, batch[i]) {
					addTrans(lc, t, batch[i])
					addModelHit()
					if !fast {
						se.final(lc, t, batch[i], "reviewed") // ★B3：fast 模式无审校，终稿统一留给收尾 sweep
					}
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
	// ★ P1 整改（漏译静默通过）：只给数量等于什么都没给——审批/QA 无从定位是哪几段。
	//   同口径把「仍未译出的源文段清单」一并透出（上限见 untranslatedSegmentsCap，防爆 payload）。
	untranslatedSegments := map[string][]string{}
	for _, lc := range finalLangs {
		// ★ missingSegments 与写入点判定（IsTranslationUsable）配对：不可用译文不写入 ⇒ 键存在
		//   严格等价于已译出，故 KB 脏行不再「既不算命中也不被统计」地凭空消失（RC-2 落点）。
		missing0 := missingSegments(texts, langTranslations[lc])
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
			stillIdx := []int{} // ★B3：与 still 等长的 texts 全局段号映射（逐段事件行键）
			// 判定口径与 missingSegments 严格一致（键存在=已译出）：此处额外要段号，故未直接复用。
			for i, t := range texts {
				if _, ok := langTranslations[lc][t]; !ok {
					still = append(still, t)
					stillIdx = append(stillIdx, i)
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
			// 对短段回显的纠正率高于逐段单发。品牌保护同前：源文品牌名先替换为规定译法防音译。
			protStill, _ := protectSourceByLang(still, brandTermsAll[lc])
			batch := e.BatchTranslate(ctx, protStill, lc, 10,
				// ★B3：硬闸重译块同样逐段推进度+segment_done（补漏段此前无行，前端据此补行）
				se.batchCB(lc, func(done, total int) { prog("file_translate|初翻|"+lc, done, total) }, stillIdx))
			for i, m := range still {
				// ★ 硬闸写入点同样走 IsTranslationUsable（原手写 3 个不等式 + 无指令残留判定）：
				//   仍回显/仍残留指令 ⇒ 不写入，该段留在缺失集合里直至被收尾统计浮出。
				if i < len(batch) && IsTranslationUsable(m, batch[i]) {
					addTrans(lc, m, batch[i])
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
				if v, ok := r.Translations[lc]; ok && IsTranslationUsable(m, v) {
					addTrans(lc, m, v)
					se.done(lc, m, v) // ★B3：逐段兜底补译成功即推初翻上屏
				}
			}
		}
		// ★ 可见性收尾：无论因预算/零进展/取消退出，剩余缺失段必须浮出水面——
		//   数量进 untranslated（工单失败判定与轨迹告警用），**清单**进 untranslatedSegments
		//   （审批/QA 与用户据此人工补译，不必再靠肉眼比对成品）。
		missList := missingSegments(texts, langTranslations[lc])
		if len(missList) > 0 {
			log.Printf("[tm-hardgate] lang=%s 结束：仍有 %d/%d 段未译出（已写入工单 Untranslated 供人工补译）", lc, len(missList), len(texts))
			untranslated[lc] = len(missList)
			untranslatedSegments[lc] = limitUntranslatedSegments(missList)
		}
	}
	prog("第2步/3：翻译完成", 2, 3)

	// ★ 漏译率硬闸（2026-09-09 用户决策：硬闸必须生效）：文件管线此前对「译文键与源文键
	//   不匹配/模型整单失败」（untranslated 全额，如工单 T20260909205438QJH 312/312）仅记
	//   警告仍 completed 输出残缺产物。现在：某语言未译出比例 >50% 直接置工单失败返回错误
	//   （用户重新发起即可），绝不把大面积漏译的产物交付给用户；≤50% 维持既有警告口径。
	for _, lc := range finalLangs {
		if lang := leakedLang(lc, untranslated[lc], len(texts)); lang != "" {
			log.Printf("[file-gate] %s 漏译 %d/%d 段（超 50%%），置工单失败", lang, untranslated[lc], len(texts))
			return &FileTranslateResult{Skill: "translation", Error: fmt.Sprintf("%s 翻译失败：%d/%d 段未能译出（模型调用异常或译文键不匹配），请稍后重新发起", config.LangNames[lang], untranslated[lc], len(texts))}
		}
	}

	// ★ 品牌术语归一化（2026-09-10 需求）：文件译文段落同样剥离「ROX vehicles/motor」等
	//   品牌自创后缀，统一为 KB 规定译法（module=brand 术语），再进入约束闸门与写回。
	if normalized := e.normalizeFileBrandTerms(ctx, texts, langTranslations); normalized > 0 {
		log.Printf("[brandterm] 文件路径品牌术语归一化 %d 个语言译文", normalized)
	}

	// 整改 R1：文件主翻译路径统一走约束闸门 + 语言文化闸门。
	// 硬约束闸门（数字/格式/非源语言/乱码等）必须强制：首轮不过带反馈重翻一次，
	// 否则错误会直接落入成品文件（如成本表数字错）。文化闸门仍仅警告。
	// fast 模式同样强制硬闸（交付物正确性优先于速度）。
	// ★ S8 敏感词兑底闸（输出侧）：模型自产敏感内容在质量复核前统一替换占位。
	if n := e.sensitiveSweepOutput(ctx, langTranslations); n > 0 {
		result := fmt.Sprintf("[sensitive] 文件通道输出侧兑底替换 %d 段", n)
		log.Println(result)
	}
	gateWarnings := e.applySegmentGates(ctx, langTranslations, true)
	// ★B3（A2 挂载点3·语言收尾）：品牌归一/敏感词兑底/质量闸门三道覆写全部落地后，
	// 对「最后已发文本」做 diff 补推 segment_final(stage=gated)，随后逐语言 segments_sealed。
	se.sweep(langTranslations, finalLangs)

	// xlsx 单独一条写回分支：它是唯一能「一份文件承载多语言」的格式（原地替换或多 Sheet），
	// 产物无法按语言归属，故 langArtifact 留空、保真闸门自动回落段级文本视图口径。
	isXlsxInput := ext == ".xlsx"

	// 第3步：写回文件
	prog("第3步/3：写回文件+修正排版...", 3, 3)
	// ★ #65（2026-09-22 用户裁定两步同做）：产物目录与取名口径同时改造——
	//   ① outputDir 由「全工单共用的 translated/」改为 translated/<落盘名主干>/，每上传件独享；
	//   ② baseName 用**原件展示名**（调用方经 options["source_name"] 传入）而非内部落盘名，
	//      交付物文件名不再带 19 位纳秒时间戳。①是②的前置：唯一性此前全靠这串时间戳撑着，
	//      不分目录就剥前缀会让同名原件跨工单/跨租户互相覆盖产物。
	baseName := artifactDisplayBase(filePath, options)
	outputDir := artifactOutputDir(filePath)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return &FileTranslateResult{Skill: "translation", Error: "产物目录创建失败: " + err.Error()}
	}

	filesOut := []string{}
	var degraded []string // ★ 双模式（2026-09-13）：原格式还原失败已降级纯文案的语言（工单仍成功）
	// 语言 → 该语言主产物路径（结构保真闸门用：文本类产物可直接读原文与产物比对指纹）。
	// xlsx 合并交付（多语言一份文件）无法按语言归属，留空即回落段级文本视图口径。
	langArtifact := map[string]string{}

	// ★ RC-4（2026-09-22 用户裁定「产物文件名也翻掉」）：产物文件名主干按目标语言翻译。
	//   每种语言**只调一次**模型并缓存，该语言的各产物点（还原件 / 纯文案 .md / 旁路 .md / xlsx）
	//   共用同一个 base —— 逐产物点各发一次调用纯属浪费积分。
	//   翻译失败/回显/异常一律回落原名（见 file_name.go：文件名是体验项，绝不能让工单失败）。
	nameBaseMu := sync.Mutex{}
	nameBaseCache := map[string]string{}
	// artifactBase 返回该语言产物名主干；suffix 是紧跟其后的部分（如 "_en_text.md"），
	// 传进来是为了从单个文件名成分的 255 字节预算里预留后缀长度，保证**整体**不超限。
	artifactBase := func(lc, suffix string) string {
		nameBaseMu.Lock()
		raw, ok := nameBaseCache[lc]
		if !ok {
			raw = e.translateFileNameBase(ctx, baseName, lc)
			nameBaseCache[lc] = raw
		}
		nameBaseMu.Unlock()
		if clean := sanitizeArtifactBaseName(raw, suffix); clean != "" {
			return clean
		}
		// 清洗后为空（模型返回全是危险字符 / base 超预算被截空）：保持既有行为直用原名，
		// 但同样过一次截断，避免原名本身超 255 字节时写出被文件系统静默截断的名字。
		if clean := sanitizeArtifactBaseName(baseName, suffix); clean != "" {
			return clean
		}
		return baseName
	}

	// writeTextMd 生成某语言的纯文案 .md 产物（按提取顺序输出；未译段保留原文）。
	writeTextMd := func(lc string) (string, error) {
		suffix := fmt.Sprintf("_%s_text.md", lc)
		p := filepath.Join(outputDir, artifactBase(lc, suffix)+suffix)
		return p, fileproc.WriteTranslationMd(p, texts, langTranslations[lc])
	}

	if deliveryText {
		// —— 纯文案模式：交付 {base}_{lang}.md，不做任何 office 格式回写 ——
		for _, lc := range finalLangs {
			tr := langTranslations[lc]
			if len(tr) == 0 {
				continue
			}
			suffix := fmt.Sprintf("_%s.md", lc)
			outPath := filepath.Join(outputDir, artifactBase(lc, suffix)+suffix)
			var aerr error
			switch {
			case ext == ".md" || anydocSourceExt(ext):
				aerr = fileproc.ApplyAlignedText(".md", mdAlignPath, outPath, tr) // 行对齐+结构前缀粘回
			case ext == ".txt" || ext == ".csv":
				aerr = fileproc.ApplyAlignedText(ext, filePath, outPath, tr) // 行序对齐，非译文行原样保留
			default: // json/yaml/srt/vtt：按提取顺序输出译文段落
				aerr = fileproc.WriteTranslationMd(outPath, texts, tr)
			}
			if aerr != nil {
				_ = os.Remove(outPath)
				return &FileTranslateResult{Skill: "translation", Error: fmt.Sprintf("%s 文案产物生成失败：%s", config.LangNames[lc], aerr.Error())}
			}
			filesOut = append(filesOut, outPath)
			langArtifact[lc] = outPath // 纯文案产物即该语言主交付物（.md 可直读比对指纹）
		}
	} else if isXlsxInput {
		// ★ 产物名（RC-4）：xlsx 是「一份文件含全部语言」的合并交付，无单一语言可归属，
		//   按首目标语言翻 base（单语言场景本就是该语言；多语言场景取首语言名，与 Sheet 名=语言码一致）。
		xlsxSuffix := "_translated.xlsx"
		xlsxBase := baseName
		if len(finalLangs) > 0 {
			xlsxBase = artifactBase(finalLangs[0], xlsxSuffix)
		}
		outPath := filepath.Join(outputDir, xlsxBase+xlsxSuffix)
		var aerr error
		if len(finalLangs) == 1 {
			// ★ 单目标语言：原地替换单元格为译文，产物文件即译文本身
			// （符合「把文件翻成 X 语」的预期；原文件保持不变，下载的 _translated.xlsx 为译文）。
			// 此前多 Sheet 模式会把原文 Sheet 留在首位、译文放新增 Sheet，Excel 默认打开原文 Sheet
			// 造成「还是中文」的误解（实际译文在 en Sheet 中已正确生成）。
			aerr = fileproc.ApplyXlsx(filePath, outPath, langTranslations[finalLangs[0]])
		} else {
			// ★ 多目标语言：单文件多 Sheet，每个目标语言一个 Sheet（Sheet 名=语言代码）
			//   base 与上面 outPath 同一份已翻译名，否则降级/成功两条路会产出两个不同文件名。
			aerr = writeMultiSheetXlsx(filePath, outputDir, xlsxBase, finalLangs, langTranslations)
		}
		if aerr == nil {
			filesOut = append(filesOut, outPath)
		} else {
			// ★ 双模式（2026-09-13）：回写失败降级纯文案交付（翻译是资产，回写是增值）
			log.Printf("[file-degrade] xlsx 回写失败，降级纯文案: %v", aerr)
			for _, lc := range finalLangs {
				if len(langTranslations[lc]) == 0 {
					continue
				}
				if p, perr := writeTextMd(lc); perr == nil {
					filesOut = append(filesOut, p)
					langArtifact[lc] = p // 降级交付的 .md 是该语言真实产物，保真比对应读它
					degraded = append(degraded, lc)
				}
			}
			if len(degraded) == 0 {
				return &FileTranslateResult{Skill: "translation", Error: "xlsx 写回失败: " + aerr.Error()}
			}
		}
	} else {
		// ★ 非 xlsx 格式：每个目标语言独立产物文件，文件名标注语言
		for _, lc := range finalLangs {
			tr := langTranslations[lc]
			if len(tr) == 0 {
				continue
			}
			// ★ 产物名（RC-4）：base 已按该语言翻译，后缀（"_en.docx"）保持既有口径不动。
			//   对照表交付形态的真实文件名多带一层 ".xlsx"，故把它一并计入后缀做字节预留。
			nameSuffix := fmt.Sprintf("_%s%s", lc, ext)
			if writebackDelivery(ext) == "xlsx" {
				nameSuffix += ".xlsx"
			}
			outPath := filepath.Join(outputDir, artifactBase(lc, nameSuffix)+nameSuffix)
			// ★ 2026-09-09 产品决策：无原格式回写能力的格式（srt/vtt/json/yaml）以 xlsx 对照表
			//   为唯一交付形态（设计如此，非降级）；还原模式下另有纯文案 .md 旁路产物（L788+）。
			if writebackDelivery(ext) == "xlsx" {
				if xerr := fileproc.WriteComparisonXlsx(outPath, texts, tr); xerr != nil {
					return &FileTranslateResult{Skill: "translation", Error: fmt.Sprintf("%s 对照表生成失败：%s", config.LangNames[lc], xerr.Error())}
				}
				filesOut = append(filesOut, outPath)
				langArtifact[lc] = outPath // 二进制对照表：保真闸门自动回落段级视图口径
				continue
			}
			// ★ 2026-09-09 产品决策：写回失败不降级 xlsx 对照表（避免「翻译 PDF 却下载到 Excel」），
			//   自动重试（子进程转换偶发失败：LibreOffice profile 锁/资源竞争）；
			//   ★ 2026-09-13 双模式：重试仍败改为「纯文案 .md 降级交付 + warning」，不再整单判死。
			var aerr error
			for attempt := 0; attempt <= 2; attempt++ {
				aerr = nil
				switch ext {
				case ".docx":
					aerr = fileproc.ApplyDocx(filePath, outPath, tr)
				case ".pptx":
					aerr = fileproc.ApplyPptx(filePath, outPath, tr)
				case ".pdf":
					// ★ 2026-09-18 新链：原地替换（redact+overlay）——原版式/原字体/
					//   不越界/自适应（单元格矢量线钳制换行宽度，字号 1.0→0.45 逐级自适应）。
					//   失败降级 fpdf 版式重建（WriteTranslatedPDF），再败走既有纯文案 .md 双模式。
					//   旧 pdf2docx→DOCX→LibreOffice 重建链自本日起退役（版式漂移/白块/
					//   字体探测等全部问题源头），代码暂留供回滚，不再被本路径调用。
					// ⚠️ 上行「字号 1.0→0.45 逐级自适应」是链路上线初期的旧口径，现已废止：
					//   pdf_overlay.py 定稿为「字号照搬原文、不缩字不丢文」，溢出一律靠换行 +
					//   向下扩容消化（详见该文件 cmd_apply 的字号照搬原则）。勿据旧注释改代码。
					// 入参是**原始 PDF**而非任何中间 DOCX：写回只做文字层手术，
					// 版式载体必须保持原件（键=提取时的同一矢量栅格切分，见上面 texts）。
					// ★ P0-7（2026-09-18）：返回命中统计，零命中/低命中由 fileproc 判错，
					//   与子进程失败同走「降级版式重建」路径——不再交付未翻译原样件。
					if st, perr := fileproc.ApplyTranslatedPdfOverlay(ctx, outPath, filePath, tr, lc); perr == nil {
						if st.Overflow > 0 {
							log.Printf("[file] %s PDF 原地替换完成（%d/%d 段，其中 %d 段轻微越界仍原字号写出）", lc, st.Replaced, st.Requested, st.Overflow)
						}
						break
					} else {
						log.Printf("[file] %s PDF 原地替换失败，降级版式重建: %v", lc, perr)
					}
					// 兜底重建只需要「有序段列表 + 原文→译文映射」，不依赖 overlay 的几何信息，
					// 故原地替换崩了仍能用同一份 tr 兜底，不至于这一语言颗粒无收。
					if perr := fileproc.WriteTranslatedPDF(ctx, outPath, texts, tr); perr == nil {
						log.Printf("[file] %s PDF 原地替换不可用，已降级版式重建交付", lc)
						break
					} else {
						log.Printf("[file] %s PDF 版式重建兜底失败: %v", lc, perr)
					}
					aerr = fmt.Errorf("PDF 写回失败")
				case ".txt", ".csv", ".md":
					aerr = fileproc.ApplyAlignedText(ext, filePath, outPath, tr) // ★ D5：按原行对齐写回
				default:
					aerr = fmt.Errorf("不支持的写回格式")
				}
				if aerr == nil {
					break // 写回成功
				}
				// 重试前短暂退避（子进程转换释放资源），最后一次失败不再退避
				if attempt < 2 && !sleepCtx(ctx, 2*time.Second) {
					break // ★ D9：客户端已断开，停止烧下一轮子进程/LLM 资源
				}
			}
			if aerr != nil {
				// ★ 双模式（2026-09-13）：重试仍败不再整单判死——该语言交付物降级为
				//   纯文案 .md（翻译是资产、回写是增值），工单成功但带 warning 轨迹与通知。
				_ = os.Remove(outPath)
				if p, perr := writeTextMd(lc); perr == nil {
					log.Printf("[file-degrade] %s 写回失败（%v），已降级纯文案交付 %s", config.LangNames[lc], aerr, filepath.Base(p))
					filesOut = append(filesOut, p)
					langArtifact[lc] = p // 降级后的 .md 才是该语言真实交付物，保真比对应读它
					degraded = append(degraded, lc)
					continue
				}
				return &FileTranslateResult{Skill: "translation", Error: fmt.Sprintf("%s 译文写回失败（已自动重试3次，纯文案兜底亦生成失败）：%s", config.LangNames[lc], aerr.Error())}
			}
			filesOut = append(filesOut, outPath)
			langArtifact[lc] = outPath
		}
	}

	// ★ 还原模式纯文案旁路产物（2026-09-13 双模式层次1）：还原成功时也逐语言生成 .md
	//   附加交付物——用户对还原不满意可直接取文案；降级语言的主产物已是 .md，不重复生成。
	var textOut []string
	if !deliveryText {
		for _, lc := range finalLangs {
			if len(langTranslations[lc]) == 0 {
				continue
			}
			hit := false
			for _, d := range degraded {
				if d == lc {
					hit = true
					break
				}
			}
			if hit {
				continue
			}
			if p, perr := writeTextMd(lc); perr == nil {
				textOut = append(textOut, p)
			} else {
				log.Printf("[file-text] 纯文案旁路产物生成失败（%s，不影响主交付）: %v", lc, perr)
			}
		}
	}
	prog("第3步/3：完成", 3, 3)

	// ★ P1 整改（漏译与格式破坏静默通过）：产物落地后再做一次**结构指纹保真**比对。
	//   既有质量闸门全部只看「中文有没有变少」，没有任何一道看「格式有没有丢」，
	//   所以 RC-5（加粗标记成批消失）、表格降级、围栏被吞都能一路潜伏到交付物。
	//   警告并入 GateWarnings（与既有闸门同一口径进工单轨迹/审批台），只提示不判死——
	//   结构变化既可能是真丢失也可能是合法重排（PDF 版式重建），交由人工判断。
	fidelityWarnings := collectFidelityWarnings(filePath, ext, texts, langTranslations, langArtifact, finalLangs)
	gateWarnings = append(gateWarnings, fidelityWarnings...)
	if len(fidelityWarnings) > 0 {
		// 结构化日志（AGENTS 二：新代码走 observability，不加 log.Printf 存量）
		observability.Warn(ctx, "文件产物结构保真闸门发出警告（不判失败，明细见工单 gate_warnings）",
			"count", len(fidelityWarnings), "langs", strings.Join(finalLangs, ","), "file", filepath.Base(filePath))
	}

	reply := fmt.Sprintf("✅ 文件翻译完成：共 %d 段文本，输出 %d 个文件（%s）",
		len(texts), len(filesOut), strings.Join(finalLangs, "/"))
	// ★ 漏翻可见性：存在未译出段时在完成话术与结构化数据中同时告警（审批/QA 可据此补译）。
	// ★ P1 整改：此前**只给数量**，等于什么都没给（用户只能拿肉眼比对成品找缺哪几段）——
	//   未译段清单已随 Data.UntranslatedSegments 透出，故话术明示清单所在字段。
	if len(untranslated) > 0 {
		parts := make([]string, 0, len(untranslated))
		for _, lc := range finalLangs {
			if n := untranslated[lc]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s×%d", lc, n))
			}
		}
		reply += fmt.Sprintf("；⚠️ 有 %d 段未能译出已保留原文（%s），具体段落见返回的 untranslated_segments，请人工补译",
			len(parts), strings.Join(parts, ","))
	}
	if len(gateWarnings) > 0 {
		reply += fmt.Sprintf("；⚠️ 质量校验提示 %d 条，详见结构化返回", len(gateWarnings))
	}
	// ★ 双模式（2026-09-13）：降级语言在话术中明示（产物已交付 .md 纯文案，非整单失败）
	if len(degraded) > 0 {
		names := make([]string, 0, len(degraded))
		for _, lc := range degraded {
			if n := config.LangNames[lc]; n != "" {
				names = append(names, n)
			} else {
				names = append(names, lc)
			}
		}
		reply += fmt.Sprintf("；⚠️ %s 版式还原失败，已降级为纯文案交付（.md），如需还原版式可重新发起工单", strings.Join(names, "、"))
	}
	// ★ 2026-09-03 需求：文件翻译结果附带实际 token 消耗
	// 用量来自 ctx 的请求级收集器（本次全链路：初翻/校对/embedding 都记在同一处），
	// 故必须在 ctx 还活着时读取；对外只出 points_used，token 裸值走 json:"-" 不外发。
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
			// 语言→仍未译出的源文段清单（每语言上限 200 条）：数量之外给出「缺哪几段」
			UntranslatedSegments: untranslatedSegments,
			// 有序源文段（不序列化）：工单执行器据此把精确配对落 ticket_segments 真值表
			SourceSegments: texts,
			GateWarnings:   gateWarnings, // 整改 R1：质量/文化闸门警告 + P1 结构保真警告
			DegradedLangs:  degraded,     // ★ 双模式：版式还原失败已降级纯文案的语言
		},
		Files:      filesOut,
		TextFiles:  textOut, // ★ 还原模式纯文案旁路产物（工单执行器登记为附加交付物）
		TokensUsed: tokensUsed,
		// ★ F-49①（〇-U 批 I-4）：对外积分按**实收**口径折（与台账扣费同源），不再用裸真实用量；
		// 旧写法比实际扣费少一个 markup，客户拿 points_used 折算必然对不上账。
		PointsUsed: e.PointsOfTokens(e.UsageDisplayTokens(ctx)),
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
		// 建 Sheet 失败只跳过该语言，不能让已成功的语言颗粒无收
		if _, err := f.NewSheet(sheetName); err != nil {
			continue
		}
		_ = srcSheets // 源内容保留在原 Sheet 中，新 Sheet 写入该语言的完整翻译
		for orig, translated := range tr {
			if orig == "" || translated == "" {
				continue
			}
			// 在新 Sheet 中按顺序写入源文和译文
			// 遍历源 Sheet 只是为了「定位原文所在的单元格坐标」，写入目标是新语言 Sheet；
			// found 后即 break ⇒ 同一段原文在源文件里出现多次时，只有第一次出现的位置被回填。
			for _, sheet := range srcSheets {
				rows, rerr := f.GetRows(sheet)
				if rerr != nil {
					continue
				}
				found := false
				for ri, row := range rows {
					for ci, cellVal := range row {
						// 比对两侧都 trim：译文键由 ExtractTexts 产出（已去首尾空白），单元格原值常带空格。
						// CoordinatesToCellName 是 1 基，而 GetRows 的下标是 0 基，故各 +1。
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
	// SaveAs 写的是 outputDir 下的新文件（与 srcPath 不同目录），原件永不被改动。
	return f.SaveAs(outPath)
}
