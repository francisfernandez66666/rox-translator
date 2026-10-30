// ============ 本文件职责中文说明 ============
// 文件翻译：面向 docx/pptx/xlsx/pdf 等格式的整文件翻译主流程（复刻 skill.py _handle_file_translate）。
// 支持 KB 语言（先 KB 直配、未命中批量模型补漏）与"其他语言"（纯批量模型），
// 从用户 prompt 中解析"其他语言"（正则 + LLM 语言识别兜底），
// 含 pro 模式批量审校、硬闸补漏（墙钟预算+最多 3 轮）与漏翻可见性（Untranslated），
// 翻译完成后按语言分别写回产物文件（translated/<落盘名主干>/ 每上传件一个子目录）并统计 KB/模型命中数。
// ★B3（方案 A2）：主流程挂载逐段事件回调 emit（segment_done/segment_final/segments_sealed），
// 让 SSE 通道边翻边上屏；发射器与协议见 file_events.go，工单/非流式路径 emit=nil 零开销。
// ★ 2026-09-22 三处主链整改（本文件是落点）：
//   - 译文可用性判定收口到 translation_guard.go 的 fileTranslationUsable（KB/模型/硬闸 5 个写入点同一口径）；
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
	"translator/internal/domain"
	"translator/internal/fileproc"
	"translator/internal/gate"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/tenant"
)

// fileHardgateMaxAttempts 硬闸补漏轮次上限（★ 2026-10-28 用户令：由 5 轮收敛为 3 轮，
// 降低重试风暴造成的 token 放大；仍保留"每轮只补缺失段"的口径）。
// 3 轮后仍缺失的段按原文交付，并由 UntranslatedSegments/GateWarnings 出声。
const fileHardgateMaxAttempts = 3

// adaptiveBatchSize 按「段数上限 + 源文 token 预算」算单批段数（★ 2026-10-28 计价/成本批）。
//
// 为什么不再写死 15/10：固定批大小在短行文档（表格/字幕）上把固定指令开销重复几十次，
// 在长段落文档上又可能把一次请求顶到模型上下文上限。这里两个约束取小：
//
//	· maxSegs  ：单批段数上限（默认 40，防止行数过多导致对齐失败）；
//	· maxTokens：单批源文 token 预算（默认 6,000，约占 8k 上下文的一半，给输出留余量）。
//
// 平均段长用 ScriptTokens 现算（与预估同一把尺子），不取字符数。
func adaptiveBatchSize(texts []string, maxSegs, maxTokens int) int {
	if maxSegs < 1 {
		maxSegs = 1
	}
	if maxTokens < 1 {
		maxTokens = 1
	}
	if len(texts) == 0 {
		return maxSegs
	}
	var total int64
	for _, t := range texts {
		total += ScriptTokens(t)
	}
	avg := total / int64(len(texts))
	if avg < 1 {
		avg = 1
	}
	byTokens := int(int64(maxTokens) / avg)
	if byTokens < 1 {
		byTokens = 1
	}
	if byTokens < maxSegs {
		return byTokens
	}
	return maxSegs
}

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
	// TMHits 本单译文记忆（缓存 A+B）命中段数，**全部目标语言合计**（★ 2026-10-28 计价与成本批）。
	// 只记数、不记原文——客户内容不进日志/观测（AGENTS §一·13 那条"日志不留原文"同口径）。
	// ⚠️ 与 `tm_review.hit_count`（同一句译文被重复看到几次的待审候选计数）**不是一回事**：
	//   这一维答的是"这一单有多少段根本没打上游"，用来核对降价测算里的命中率。
	// 零值不序列化（omitempty）：老客户端与"一次没命中"的形态逐字相同，不引入空字段噪音。
	TMHits      int    `json:"tm_hits,omitempty"`
	FileContext string `json:"file_context"` // 文件内容摘要/上下文（预留）
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
//   - 返回 "image"：图片格式（P1a，经 OCR 投喂翻译）——交付形态＝**译后图片（同容器，P4 原位叠字）
//     ＋译文 .md**；叠字渲染不出来时退回「只有译文 .md」（择优产出，绝不判死整单）；
//   - 返回 "audio"：音频格式（P2，经 ASR 转写投喂翻译）——★ 2026-10-30 改档后交付形态＝**两件**：
//     译后音频（同容器，见 dubTranslatedAudio）＋译文 .md。
//     **不再产字幕 .srt、不再随包原音频副本**（用户裁决：客户要的是能听的译文，字幕与原音轨是打包噪音）；
//     TTS 失败或目标语种无语音 ⇒ 只有 .md，并把该语言记进 degraded/gate_warnings 点名原因；
//   - 返回 "inplace"：有原格式写回能力的格式（pdf/docx/pptx/txt/csv/md），写回失败自动重试、不降级。
//
// ⚠️ 旧档 `"xlsx"`（srt/vtt/json/yaml/yml 以双语对照表为唯一交付形态）**已于 2026-10-30 退役**：
// 这五种纯文本容器同时撤出准入（fileproc.TranslateFormatOrder），所以这里不再有那一档，
// `isComparisonTableFormat`／`fileproc.WriteComparisonXlsx` 一起退役。
// 等价锁＝writeback_delivery_test.go / file_gate_test.go 拿 TranslateFormatOrder **全量**逐条比对——
// 清单里还剩一个格式而这里判不出对应档，那条锁就红（两份列表再漂一次当场抓到）。
func writebackDelivery(ext string) string {
	switch {
	// ★ 媒体两档**委托** fileproc 的集合判定，不在这里再抄一份扩展名列表
	//   （2026-10-16：这两份列表漂过——engine 那份加了 .heic/opus，fileproc 那份没加，
	//   表现是"上传口放行、这里判 inplace"，客户拿到一个走了 Office 回写分支的图片）。
	//   等价性由 writeback_delivery_test.go 拿 TranslateFormatOrder 全量逐条钉住。
	case fileproc.IsImageFormat(ext):
		return "image"
	case fileproc.IsAudioFormat(ext):
		return "audio"
	default:
		return "inplace"
	}
}

// retryImageSegments 图片链「未译出行先充分重试」（2026-10-26 用户裁决 5），并就地写回补出的译文。
// 参数：texts=按提取顺序的原文段；tr=原文→译文（本函数会**就地补**能救回来的行）；lc=目标语言。
// 返回：补译之后仍没有可用译文的段（交付文档「未译出内容」那一节的素材，行号按 texts 顺序 1 起）。
// 三条取向：
//  1. 已有可用译文的段**一个字符都不再送**（重复扣费），判据就用同一把 fileTranslationUsable；
//  2. 逐行都走**单段请求**（`RetryLineUntilUsable`）——长度爆炸那一族的病因在批量上下文里；
//  3. 与文本逐行链同一个**上游断早停**：连续 N 段补译都拿不回东西 ⇒ 判上游不通，
//     剩余段不再逐行烧调用（客户白等白扣），但**仍逐段计入未译出**，交付面一行都不藏。
//
// untranslatedNotesFromSegments 从「按提取顺序的段」与「原文→译文」映射里导出未译出段清单。
// 参数：texts=提取顺序的原文段（决定行号口径）；tr=该语言译文映射（缺失/空/失败占位都算未译出）。
// 返回：交付文档文末那一节的素材；全部译出 ⇒ **空切片**（调用方据此连标题都不写）。
// 与图片那一口共用同一个 `fileproc.UntranslatedItem` 结构与同一份追加函数——
// 两个交付口各写一份"什么算没译出"，迟早一边松一边紧（本仓「多把尺子」那一族）。
func untranslatedNotesFromSegments(texts []string, tr map[string]string) []fileproc.UntranslatedItem {
	out := make([]fileproc.UntranslatedItem, 0, 4)
	for i, src := range texts {
		if strings.TrimSpace(src) == "" {
			continue // 空段没有可译内容，报出来是把设计当缺陷给客户看
		}
		v := strings.TrimSpace(tr[src])
		if v != "" && !IsFailurePlaceholder(v) {
			continue
		}
		out = append(out, fileproc.UntranslatedItem{
			Line:   i + 1,
			Source: src,
			Reason: "模型多轮补译后仍无可用输出，已按原文交付（成因见工单质量提示：上游未返回／输出被截断）",
		})
	}
	return out
}

// retryImageSegments 把图片件里「多轮补译仍不合格」的行单独再打一枪，并交出仍未译出的清单。
// 为什么图片侧要自己再走一遍：图片的交付面是「原位回填的那张图」，一行留白在图上是一片空白，
// 比正文里少一句更难被发现，所以先把未译项充分重试（用户令⑤），实在不行才回落原文并在 .md 里点名。
// 第二个返回量＝这一族是否已判上游整条不通（true ⇒ 调用方不再逐行烧调用，只回填并出一行 WARN）。
func (e *Engine) retryImageSegments(ctx context.Context, texts []string, tr map[string]string, lc string) ([]fileproc.UntranslatedItem, bool) {
	out := make([]fileproc.UntranslatedItem, 0, 8)
	rounds := e.LineRetryMax()
	deadStreak, deadMin := 0, e.LineDeadStreakMin()
	upstreamDead := false
	for i, src := range texts {
		if strings.TrimSpace(src) == "" {
			continue // 空段没有可译内容，不进未译出清单（把"设计"当"缺陷"报给客户是另一种坏）
		}
		have := strings.TrimSpace(tr[src])
		if have != "" && !IsFailurePlaceholder(have) && e.fileTranslationUsable(ctx, src, have, lc) {
			deadStreak = 0 // 这一行本来就有力可及的译文 ⇒ 连续失败段被隔断
			continue
		}
		if upstreamDead {
			out = append(out, fileproc.UntranslatedItem{
				Line: i + 1, Source: src,
				Reason: fmt.Sprintf("上游未返回可用译文（连续 %d 段为空/失败占位后本段未再逐行重拨），已按原样保留", deadMin),
			})
			continue
		}
		v, ok, diag := e.retryLineUntilUsableDiag(ctx, src, lc)
		if ok {
			tr[src] = v
			deadStreak = 0
			continue
		}
		deadStreak++
		if deadStreak >= deadMin && !upstreamDead {
			upstreamDead = true
			// ★ 2026-10-27（工单 T20261027152356CMY 复盘 C 项）：整语种一行，早停必须被值班看见。
			// 为什么不逐行刷屏：回填行数另有出口（返回的未译出清单逐行进 .md），这里只报"这一单上游是坏的"。
			// 与文本链 orchestrator/text_lines.go 的同名 WARN 对齐——两条链对同一件事必须同一种出声方式，
			// 否则排障时又只能靠交付物反推（本单就是这么反推出来的）。
			observability.Warn(ctx, "图片工单逐行补译判上游断，剩余未译行一次性按原文回填",
				"lang", lc, "line", i+1, "streak", deadMin, "rounds", rounds,
				"last_kind", diag.LastCandKind)
		}
		reason := fmt.Sprintf("模型补译 %d 轮仍无可用输出，已按原样保留", rounds)
		emptyHave := have == "" || IsFailurePlaceholder(have)
		if emptyHave {
			reason = fmt.Sprintf("上游未返回可用译文（补译 %d 轮仍为空），已按原样保留", rounds)
		} else if IsLengthExplosion(src, have) {
			reason = "上游输出被截断/长度异常（补译轮内未恢复），已按原样保留"
		}
		// ★ 2026-10-27（C 项）：**回落必须点名成因**，且读数随行报出来。
		// 本单（136）排障时这一行 WARN 不存在，只能拿交付 .md 与 gate_warnings 反推"到底是没回、
		// 回了空的、还是回了被我们判死的"——三者在交付面上同形（都写"仍为空"），成因却完全不同。
		// 现在由 retryLineUntilUsableDiag 给出三样读数：
		//   last_kind=empty      ⇒ 上游压根没给（换模型/换问法才可能救，不是判据问题）；
		//   last_kind=explosion  ⇒ 给了但被截断/长度异常；
		//   last_kind=skeleton   ⇒ 回了中文被洗成符号骨架（判据 3″ 那一族）；
		//   last_kind=echo       ⇒ 回了提示词回显；
		//   last_kind=unusable   ⇒ 以上都不是，落到通用不可用（要按 length_info 与原文形态再看）。
		// 只记读数不记候选原文：日志不进客户面，但模型输出是客户内容的衍生物（AGENTS 日志口径）。
		observability.Warn(ctx, "图片工单逐行补译仍不合格，按原文回落",
			"lang", lc, "line", i+1, "rounds", rounds,
			"main_rounds", diag.MainRounds,
			"review_tried", diag.ReviewTried,
			"last_kind", diag.LastCandKind,
			"last_cand_len", diag.LastCandLen,
			"pre_had_value", !emptyHave,
			"pre_explosion", !emptyHave && IsLengthExplosion(src, have),
			"length_info", LengthExplosionInfo(src, have))
		out = append(out, fileproc.UntranslatedItem{Line: i + 1, Source: src, Reason: reason})
	}
	return out, upstreamDead
}

// dubWithFallback 配音产物的**两段式容器决策**（纯逻辑，便于单测；I/O 由 synth 注入）。
//
// 口径（用户令 2026-10-16「处理完回成原文件格式」）：
//  1. 先按**输入容器**合成（fileproc.DubContainerFor 给出的那一个）——mp3 进 mp3 出；
//  2. 该容器合成失败且它不是 .mp3（转码不可用：服务器缺 ffmpeg 或该容器编码器）⇒ 回落 .mp3；
//  3. 回落也失败、或原容器本就是 .mp3 而失败 ⇒ 返回错误，调用方只记 warning。
//
// 返回 (最终容器, 是否发生回落, 错误)。容器为空串且 err!=nil 表示"这一次没有语音"。
// ★ 为什么抽出来：这是"回原格式"承诺的唯一判定点，必须能被单测直接喂失败，
// 不然它只活在一条需要真 TTS 引擎才能走到的分支里（等于没有断言）。
func dubWithFallback(srcExt string, synth func(container string) error) (string, bool, error) {
	container := fileproc.DubContainerFor(srcExt)
	if err := synth(container); err == nil {
		return container, false, nil
	} else if container == ".mp3" {
		return "", false, err
	}
	if err := synth(".mp3"); err != nil {
		return "", false, err
	}
	return ".mp3", true, nil
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

// leakedLangRepairRatio 漏译率触发「缺失段补译」的阈值比例。
//
// ★ 2026-10-24 用户令：50% → 5%。语义同时改变：
//   - 旧口径：>50% 直接判工单失败（客户拿不到产物）；
//   - 新口径：>5% 触发**缺失段补译**并补回；补不回也**不判失败、不退款**，
//     完成 + 显式缺失标记（untranslated/untranslated_segments/quality_flagged）。
//
// 严格大于：20 段缺 1 段=5% 不触发；10 段缺 1 段=10% 触发。
// 运行期可由 system_config.file_leak_repair_ratio 覆盖（回滚设 0.5）。
const leakedLangRepairRatio = 0.05

// leakedRatio 读取当前漏译触发比例（env/system_config 覆盖，非法值回退默认）。
func leakedRatio() float64 {
	if v := strings.TrimSpace(os.Getenv("FILE_LEAK_REPAIR_RATIO")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return leakedLangRepairRatio
}

// leakedLang 漏译率判定（纯函数，便于单测）：
// 某语言未译出段数占比 > 当前阈值（默认 5%）时返回该语言代码（触发补译/标记）；否则返回 ""。
// remain 为未译出段数、total 为源文总段数；remain==0 或 total==0 不触发。
func leakedLang(lang string, remain, total int) string {
	return leakedLangAt(lang, remain, total, leakedRatio())
}

// leakedLangAt 与 leakedLang 同判据，但比例由调用方给（便于 Engine 读 system_config）。
func leakedLangAt(lang string, remain, total int, ratio float64) string {
	if remain > 0 && total > 0 && float64(remain)/float64(total) > ratio {
		return lang
	}
	return ""
}

// LeakRatioExceeded 供服务层判断「本单是否需打质检存疑」：任一语言缺失比例超过当前阈值。
// 与 leakedLangAt 同判据（不另写 0.05 字面量），保证 5% 口径只有一处。
func LeakRatioExceeded(untranslated map[string]int, total int) bool {
	for _, n := range untranslated {
		if leakedLangAt("x", n, total, leakedRatio()) != "" {
			return true
		}
	}
	return false
}

// LeakRatioExceeded 实例方法：按本引擎的 system_config 阈值判断（服务层用这个，
// 避免服务层只读 env 而漏掉管理台调档）。
func (e *Engine) LeakRatioExceeded(untranslated map[string]int, total int) bool {
	for _, n := range untranslated {
		if leakedLangAt("x", n, total, e.fileLeakRatio()) != "" {
			return true
		}
	}
	return false
}

// fileLeakRatio 运行期阈值：env > system_config.file_leak_repair_ratio > 默认 0.05。
func (e *Engine) fileLeakRatio() float64 {
	if v := strings.TrimSpace(os.Getenv("FILE_LEAK_REPAIR_RATIO")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	if e != nil && e.St != nil {
		if v, _ := e.St.GetConfig("file_leak_repair_ratio"); strings.TrimSpace(v) != "" {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f > 0 && f <= 1 {
				return f
			}
		}
	}
	return leakedLangRepairRatio
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

// countBrandPresenceDrops ★ G4（2026-10-19）：统计「源文含品牌名、译文却逐字找不到规定译法」
// 的段落数。品牌名是**不可译**的常量（挂件出栈有 guardReplyBrand，工单/文件链此前没有同一道
// 闸，D5 实测同一原件两次交付首行不同：`Can LangCross speak?` vs `Can you speak LangCross?`）。
//
// 判据（per-language）：源段命中某品牌源名（KB module=brand+layer=1 的 SourceText）时，
// 该段译文必须至少包含其中一个对应的规定译法；一段都没包含 ⇒ 记一次「品牌被翻掉」。
// 长词/短词不在此处再判（protectSourceByLang 已在翻译前按长词优先替换）。
//
// **fail-soft 方向（§一·13③）**：brandTerms 为空（名单取不到）时一律返回 0——「拿不到清单」
// 是「不知道品牌名是什么」，不是「什么都算丢」；宁可这一轮不判，也不许把正常交付误报成缺品牌。
func countBrandPresenceDrops(langTranslations map[string]map[string]string, brandTerms map[string]map[string]string) (int, map[string]bool) {
	drops := 0
	langs := map[string]bool{}
	if len(langTranslations) == 0 || len(brandTerms) == 0 {
		return 0, langs
	}
	for lc, segs := range langTranslations {
		terms := brandTerms[lc]
		if len(terms) == 0 {
			continue
		}
		for orig, tr := range segs {
			if strings.TrimSpace(tr) == "" {
				continue
			}
			hit, present := false, false
			for src, tgt := range terms {
				if src == "" || !strings.Contains(orig, src) {
					continue
				}
				hit = true
				if tgt != "" && strings.Contains(tr, tgt) {
					present = true
					break
				}
			}
			if hit && !present {
				drops++
				langs[lc] = true
			}
		}
	}
	return drops, langs
}

// checkFileBrandPresence 取该租户品牌术语后委托给 countBrandPresenceDrops（纯函数便于单测）。
// 名单取不到即 fail-soft（返回 0）。
func (e *Engine) checkFileBrandPresence(ctx context.Context, texts []string, langTranslations map[string]map[string]string) (int, map[string]bool) {
	if e.St == nil || len(texts) == 0 || len(langTranslations) == 0 {
		return 0, map[string]bool{}
	}
	retrieve := strings.Join(texts, "\n")
	if len([]rune(retrieve)) > 200 {
		retrieve = string([]rune(retrieve)[:200])
	}
	return countBrandPresenceDrops(langTranslations, e.fetchBrandTerms(ctx, retrieve))
}

// extractFailureReason 把「提取这一步没产出」收敛成该给客户看的那句话（★ 2026-10-15 OCR/ASR 批）。
// 只有**图片／音频**这两条新腿放宽成「原样带出底层错误」，理由有三层：
//   - fileproc 给这两族写的错误句子**本身就是可行动口径**（见 ocr.go / asr.go）：
//     「图片文字识别（OCR）不可用：…未部署 OCR 模型资产（assets/ocr/*.onnx）…取权重步骤见
//     assets/ocr/README.md」「ASR 未识别出语音内容（音频可能无人声、纯音乐或已损坏）」——
//     前者告诉运维去哪投放权重，后者告诉客户换个文件重传，两句都比通用文案有用；
//   - 旧形态把这两句压成「无法从文件提取文本或文件为空」（《改造方案》§85 行的原话：
//     「这就是扫描件 PDF 今天的死法」），客户看到"文件为空"会去检查文件，而真正缺的是模型资产，
//     工单轨迹里也只剩这一句，运维无从判断该装依赖还是该回客户；
//   - 其余格式（Office／PDF／文本）的底层错误是第三方库的英文原文，可能夹带路径与堆栈片段，
//     把它们投到客户屏幕既读不懂也不安全 ⇒ **旧那句一字未改**，改动面收到最小。
//
// err==nil 而 texts 为空（真空文件／全空白）也走这里的空串分支，由调用方保留旧文案。
func extractFailureReason(extractPath string, err error) string {
	if err == nil {
		return ""
	}
	e := strings.ToLower(filepath.Ext(extractPath))
	if fileproc.IsImageFormat(e) || fileproc.IsAudioFormat(e) {
		return err.Error()
	}
	return ""
}

// minimalInstrRescueMarker —— 「极简指令兜底腿救回一段」这一形态在二进制里的**字面证据**。
// 为什么要有它（口径同 identityPrefillMarker / listen_systemd.go 的 sdTakeoverMarker，2026-10-18 真踩）：
// 发布件是 `-ldflags="-s -w"` 编出来的，"函数名留不留在件里"不可依赖（内联即消失），
// 于是"编的是不是本棵工作树"只能靠一个**被运行期真正引用**的字符串常量：本常量在下面的观测日志里出现，
// 发布脚本 `deploy/release_20261019b.sh` 的 P0-B 特征串与它**逐字等值**。
// 等值锁＝release_marker_gate_test.go 的 TestMinimalInstrRescueMarkerMatchesReleaseScript（含三枚反证）。
const minimalInstrRescueMarker = "MINIMAL_INSTR_SEG_RESCUE_V1"

// translateSegmentMinimal 硬闸「逐段兜底」末段腿：**极简指令**取一份候选译文。
// 为什么要单独一条腿（★ 2026-10-19，现网工单 107 实证）：
// 正常腿（批量 / 逐段，system 里带「币种保真」「输出纯度」两条中文尾注）对**短段**会退化——
// 纯翻译模型（tencent/Hunyuan-MT-7B）把 system 里那两条中文尾注当成待译内容，产出
// `and`（注记的连词尾巴）或整段注记英译；`fileTranslationUsable` 据实判其不可用 ⇒ 该段每一轮都失败，
// 最终 `金额`、`当前：全部允许·仓位留空/0 =使用全局单票金额` 这类真中文原样留在成品与译图里。
// 实测：同一 12 段带尾注 11/12 出 `and`，换成「骨架+契约」这一档 12/12 正确。
// ★ 本函数**只产候选、不做采纳判定**——采纳仍由调用点的 `fileTranslationUsable` 一把尺子说了算
//
//	（与 file.go 其余写入点同形，写入点守护的机械闸门据此仍认得这一处）。
func (e *Engine) translateSegmentMinimal(ctx context.Context, seg, lc string) (string, bool) {
	r, err := e.TranslateOne(WithMinimalInstruction(ctx), seg, []string{lc}, false, config.StageAIInitial)
	if err != nil || r == nil {
		return "", false
	}
	v, ok := r.Translations[lc]
	return v, ok
}

// HandleFile 文件翻译主流程（复刻 skill.py _handle_file_translate）
// ★B3（方案 A2）：新增逐段事件回调 emit（与 prog 同闭包风格，仅 SSE 文件通道注入；
// 工单 worker / 非流式接口传 nil，事件层整体空转零开销）。事件协议与稳定键见 file_events.go。
func (e *Engine) HandleFile(ctx context.Context, filePath string, options map[string]interface{}, prog Progress, emit SegmentEmit) *FileTranslateResult {
	// 注入请求级用量记录器（供计量成本核算）
	ctx = e.WithUsageRecorder(ctx)
	// ★ 2026-10-28 计价与成本批：注入请求级「译文记忆命中数」计数器（S4 载荷 tm_hits）。
	//   位置与用量记录器并列：两者都是「本单读数」，都在这一单的 ctx 里活、随这一单结束消失。
	//   计数器由批量腿（含逐段兜底腿）在命中处自增，这里只负责建与最后读出——
	//   三个语言协程并发跑，故必须是原子量（见 withTMHitCounter）。
	ctx, tmHitsCounter := withTMHitCounter(ctx)
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
	// ★ 2026-10-28 图片版式回退批：**图片件的单批段数上限收紧到 15**（其余格式仍是 40）。
	// 为什么只对图片：图片的交付面是「按 OCR 框原位回填的那张图」，段↔框一一对应，
	// 批量里漏一行／串行一行就会把译文画到**错误的框**上（客户看到的不是"少一句"而是"字跑到别处"），
	// 而文档件错一行只是正文少一句。图片件段数本就少（几十段量级），收紧上限多出的固定指令开销
	// 远小于文档件——自适应 40 那一档是为长文档/表格省指令开销设计的（计价批口径），不该覆盖到图片。
	imgSegCap := 40
	// ★ 2026-10-29 用户令「对于 ocr 结果，ai 只补全，不发散翻译」：
	//   这一份文件的内容**是不是 OCR 出来的**，决定批量提示词要不要挂「只直译」那一句
	//   （`BatchOptions.LiteralOCR`）。图片件与扫描件 PDF 都算（后者走同一条 OCR 识别腿），
	//   所以判据在下面 `PendingOcrDoc` 命中时也会翻成 true——**不在三处调用点各判一次**。
	ocrSourced := fileproc.IsImageFormat(ext)
	if ocrSourced {
		imgSegCap = 15
	}
	// ★ P4：图片件的 OCR 坐标缓存在提取期入列并**钉住**（在途），这里用 defer 保证
	//   **任何返回路径都解除钉住**——包括第 3 步写回中途 return 的错误路径。
	//   为什么必须 defer 而不是循环后调：语言循环里有多个 return（字幕/文档生成失败即整单失败），
	//   循环后那一行会被它们跳过，在途钉就漏在缓存里，高并发下最终全员钉住、淘汰退化成纯 FIFO。
	//   放在这里（ext 一定值）而不是图片分支里：提取发生在第 1 步，比第 3 步的 image 分支早得多。
	if writebackDelivery(ext) == "image" {
		defer fileproc.ReleaseImageBoxes(filePath)
	}
	// ★ 2026-10-28：OCR 在途队列也要释放。**不能**挂在 image 条件里：
	//   扫描件 PDF 不画图、但同样有 OCR 队列；漏放只会让那条记录长期占着"不可淘汰"名额。
	defer fileproc.ReleaseOcrDoc(filePath)
	// ★ 工单双模式（2026-09-13）：options["delivery"]=="text" → 纯文案模式（anydoc/原生提取，
	//   交付译文 .md，不还原版式）；restore/空 → 还原文件模式（既有原位回写管线，行为不变）。
	deliveryText := strings.EqualFold(strings.TrimSpace(optionString(options["delivery"])), "text")
	// ★ 格式白名单（三期格式补齐；★ 2026-10-14 #43 后清单的唯一事实源在 fileproc/formats.go）：
	// Office 原格式回写；PDF 走三级降级链（原版式覆盖→版式重建→纯文案）；
	// txt/csv/md 按原行对齐原位写回（★ 2026-10-30：srt/vtt/json/yaml/yml 已撤出准入，不再有 xlsx 对照表这一档）。
	// 交付形态的判据只有本文件的 writebackDelivery 一处（下方第 3 步产物生成）。
	// 纯文案模式额外准入 anydoc 独占格式（doc/xls/ppt 老格式、odt/ods/odp、rtf/epub 等）。
	// ★ 2026-10-14 #43：这份清单原本在本包与 internal/api 各有一份字面量（共四份），
	//   唯一事实源现在是 `fileproc.IsTranslateFormat`。这里为什么必须一起收：
	//   它是**执行口**的第二道判定——上传口放行、这一口判不支持时，
	//   客户看到的是"单建起来了、跑一半报不支持"，而且钱已经扣了。
	if !fileproc.IsTranslateFormat(ext, deliveryText) {
		// ★ 2026-10-30 C-10：拒绝文案的唯一出口＝`fileproc.RejectHint`。
		// 撤出的五个纯文本容器给「请直接粘贴内容到文本翻译」，其余给现值清单句——
		// 清单是现算的，所以改档位这句话自己跟着变，不留第二份对外文案。
		return &FileTranslateResult{Skill: "translation",
			Error: fileproc.RejectHint(ext, deliveryText, "不支持的格式")}
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
	extractFrom := filePath // 实际被提取的那个路径（纯文案模式下可能是 anydoc 落盘的 .md 副本）
	if mdAlignPath != "" {
		extractFrom = mdAlignPath
		texts, err = fileproc.ExtractTexts(mdAlignPath)
	} else {
		texts, err = fileproc.ExtractTexts(filePath)
	}
	if err != nil || len(texts) == 0 {
		// ★ 2026-10-15：图片/音频两条新腿把 fileproc 那句**可行动的**原因原样带出去
		//   （判据与理由见 extractFailureReason）；其余格式继续走下面那句既有文案，一字未改。
		if reason := extractFailureReason(extractFrom, err); reason != "" {
			return &FileTranslateResult{Skill: "translation", Error: reason}
		}
		return &FileTranslateResult{Skill: "translation", Error: "无法从文件提取文本或文件为空"}
	}
	// ★ PDF（2026-09-18 新链）：原地替换（redact+overlay）——原版式/原字体/不越界。
	//   提取键与写回目标完全一致（同一矢量栅格切分口径），不再产出 pdf2docx 缓存 DOCX。
	//   ★ 2026-10-14 OCR 恢复：扫描件/图片型内容降级走 extractImageOCR（见 fileproc/ocr.go）；
	//   纯文案模式不走此链（anydoc 提取，交付 .md）。
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

	// ★ 2026-10-28（主方案 §9）：OCR 清洗（②）＋ AI 判定补偿（③）＋ 映射重建（④）。
	//   顺序三条硬约束：
	//     · 必须在 identitySegments 之前——预填要用**清洗后**的文本；
	//     · 必须在语言循环之前——判定的是"这行是不是文字/这个字是什么"，与目标语言无关；
	//     · Commit 必须在 ③ 之后——改过字就要按 Final 重建行↔框映射，否则回填按旧键找不到译文。
	// gateWarnings 在这里先起头（原本声明在写回前）：OCR 阶段的告警必须**同块记账**，
	// 否则「图没回填/识别存疑」对客户与审批台都是静默的（file_delivery_honesty_gate_test.go 的判据）。
	gateWarnings := make([]string, 0, 4)
	if doc, ok := fileproc.PendingOcrDoc(filePath); ok {
		ocrSourced = true // 扫描件 PDF 的内容同样是 OCR 出来的，交付口径与图片件一致
		st := e.ReconcileOcrDoc(ctx, doc)
		if t2, cst := fileproc.CommitOcrDoc(filePath, doc, texts); !cst.Stale {
			texts = t2
		} else if cst.Stale {
			// 文本已被别的链路替换（如 PDF overlay 成功）：不重建映射，回填按旧坐标取不到译文时保留原文。
			// 出声而不是静默，因为"图上那几行没换掉"是本仓最难发现的一类残留。
			observability.Warn(ctx, "OCR 清洗结果与提取文本不同源，未重建回填映射",
				"file", filepath.Base(filePath))
			gateWarnings = append(gateWarnings, "图片识别结果与提取文本不同源，原位回填已跳过（译文照常交付）")
		}
		if st.Unclear > 0 {
			// 识别存疑是**事实**，不是承诺：只报计数，不进「未译出内容」清单（那清单只收没有译文的行）。
			gateWarnings = append(gateWarnings, fmt.Sprintf("%d 行图片识别置信度偏低，译文可能基于不确定的识别结果", st.Unclear))
		}
	}

	// ★ 2026-10-28 P5：表头上下文（默认关）。**一次文件单只抽一次**，缓存到本函数局部变量，
	//   三个批量调用点共用同一份——每处各抽一次会让同一张表在不同语言下拿到不同的表头
	//   （结构腿是同一次推理，但抽取时刻不同 ⇒ 清洗后的行文本可能已变），且白付 N 次子进程。
	//   抽不出（没开结构腿/没表格/单元格里没文字）⇒ 空串，提示词逐字不变（不降级、不记 warning）。
	fileHeaderCtx := ""
	if fileproc.FileHeaderContextEnabled() {
		if od, ok := fileproc.PendingOcrDoc(filePath); ok {
			if sdoc, serr := fileproc.DetectStructure(ctx, filePath); serr == nil {
				if len(sdoc.Tables) > 1 {
					// 只取首张表的读数：上下文越长越容易被模型当成待译内容（见 table_header.go 头注第 3 条）。
					observability.Info(ctx, "表头上下文只取首张表格", "file", filepath.Base(filePath), "tables", len(sdoc.Tables))
				}
				if hdr, hok := fileproc.TableHeaderContext(od.Lines, sdoc, fileproc.FileHeaderContextMaxChars()); hok {
					fileHeaderCtx = hdr
				}
			}
		}
	}
	if fileHeaderCtx != "" {
		observability.Info(ctx, "表头上下文已注入批量提示词",
			"file", filepath.Base(filePath), "chars", len([]rune(fileHeaderCtx)))
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
	// ★ 2026-10-29 第十二轮：文档级领域词表（只进本单提示词；开关关/低置信/查不到 ⇒ 不做）。
	//   一次/单；文件链预算 5s（异步工单可以多等）。同时挂进 ctx，给末段补译（SingleLangTranslate）用。
	var glossaryProf *domain.Profile
	glossaryLim := e.glossaryLimits("file")
	if prof := e.glossaryProfile(ctx, e.tenantID(ctx), "file", sampleTexts(texts, 2000), finalLangs, glossaryLim); prof != nil {
		glossaryProf = prof
		ctx = WithGlossary(ctx, prof, glossaryLim.MaxTerms)
	}
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
		//   见 sensitiveBlockedSegments）。因此所有调用方必须先过 fileTranslationUsable
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

	// ★ M1（2026-10-21 UAT 收口）：记录「模型曾对这一段回绝」。判据本体在
	// translation_guard.go 的 refusalResidue（与正文/文件名同一把尺子），这里只做记账：
	// 收尾阶段再剔除「后来又补译成功」的段，只把仍然缺失的那些写进 GateWarnings，
	// 让客户/审批台看得见「这一行模型没翻，已保留原文」而不是一单静默的残缺交付。
	refusalMu := sync.Mutex{}
	refusalSeen := map[string]map[string]bool{}
	noteRefusal := func(lc, src, cand string) {
		if !refusalResidue(src, cand) {
			return
		}
		refusalMu.Lock()
		defer refusalMu.Unlock()
		if refusalSeen[lc] == nil {
			refusalSeen[lc] = map[string]bool{}
		}
		refusalSeen[lc][src] = true
	}

	// ★ S8 敏感词兑底闸（输入侧·段级）：命中段不送 KB/模型（上游零暴露），
	// 各语言直接预填占位交付；其余段照常翻译。
	blockedSeg := e.sensitiveBlockedSegments(ctx, texts, append(append([]string{}, kbLangs...), directOther...), addTrans)

	// ★ 2026-10-17 第二批：**无需翻译**段（数字/货币/日期/时间/符号）预填原文交付。
	// 与 blockedSeg 同属「已预填、不进上游、不进补漏」，但交付值是原文本身而不是占位符
	// （判据与预填同源，见 translation_guard.go 的 NeedsIdentityDelivery／identitySegments）。
	// 工单 T20261017234421W7H 实测：这些段送翻只会换回整排 "and"，而回落原文才是正确交付。
	identitySeg := identitySegments(texts, append(append([]string{}, kbLangs...), directOther...), addTrans)

	// preFilled＝「已交付、不再送上游」的段集合（敏感拦截 ∪ 无需翻译）。
	// ★ 模型派发的三处（KB 检索 / KB 补漏 / 其它语言批量）一律只看它——禁止各自再拼一遍，
	//   否则漏掉一路就会把「无需翻译」的段重新送去换 "and"（下一批同样的报障）。
	preFilled := mergeSegSets(blockedSeg, identitySeg)

	// 现网排障抓手：本批确实有段被判「无需翻译」⇒ 原文直出、零上游暴露。
	// 这行同时是 identityPrefillMarker 的运行时引用点（不留这行，常量会被编掉、发布脚本的特征串就失去对象，
	// 见 translation_guard.go 里该常量的注释）。
	if len(identitySeg) > 0 {
		observability.Info(ctx, "文件翻译：无需翻译段按原文预填交付",
			"marker", identityPrefillMarker, "segments", len(identitySeg))
	}

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
					if preFilled[t] { // ★ S8 拦截段 + 无需翻译段都不进模型链路
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
							// ★ P0 整改 RC-2：命中判定与模型路径同口径走 fileTranslationUsable，
							//   **不能只判非空**——KB/TM 里「源文=译文」的脏行会把中文当成
							//   「命中的译文」写进成品，且键一旦写入，硬闸按「键存在即已译出」
							//   会永久跳过该段（不重试、不计 untranslated、不告警）。
							//   判不过 = 不算命中 ⇒ 该段落入 needModelIdx 走模型补漏。
							if v, ok := r.Translations[lc]; ok {
								if e.fileTranslationUsable(ctx, t, v, lc) {
									kbHitIdx[i] = true
									kbVal[i] = v
									se.done(lc, t, v) // ★B3：KB 直译命中即推（A2 挂载点1，替代攒齐屏障的上屏时延）
								} else {
									noteRefusal(lc, t, v) // ★ M1：KB 脏行是回绝话术也要记账（随后走模型补漏）
								}
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
				// ★ 命中/补漏分派收口到纯函数 collectKBPass（内部再走一次 fileTranslationUsable，
				//   写入点判定优先于上游并行判定，杜绝「只判非空」的旧口径复活）。
				kbAccepted, needModelIdx := collectKBPass(texts, kbHitIdx, kbVal, preFilled,
					func(src, out string) bool { return e.fileTranslationUsable(ctx, src, out, lc) })
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
					batch := e.BatchTranslateOpts(ctx, protSrc, lc, adaptiveBatchSize(protSrc, imgSegCap, 6000),
						// ★B3：段回调升级为「进度+逐段 segment_done」；gidx=needModelIdx 将批下标映射回
						// texts 全局段号（事件带原始源文而非品牌保护后的 protSrc，保证前端行对得上号）
						se.batchCB(lc, func(done, total int) { prog("file_translate|初翻|"+lc, done, total) }, needModelIdx),
						// ★ P5：表头语境（关闭时为空串 ⇒ 提示词与记忆键逐字不变）
						BatchOptions{HeaderContext: fileHeaderCtx, LiteralOCR: ocrSourced, Glossary: glossaryProf.TermsFor(lc, glossaryLim.MaxTerms), GlossaryLabel: glossaryProf.DisplayLabel()})
					// ★ pro 模式批量审校：本块一次 LLM 调用逐条修正，失败/不符原样保留
					if !fast {
						batch = e.reviewBatchSafe(ctx, protSrc, batch, lc,
							func(done, total int) { prog("file_translate|校对|"+lc, done, total) })
					}
					for i, idx := range needModelIdx {
						// ★ 回显检测统一走 fileTranslationUsable（原此处手写 3 个不等式）：
						//   空/失败占位/与源文同文/指令回显残留任一命中即视为未译出，
						//   留给硬闸重试并计入 untranslated，绝不静默写进成品。
						if i >= len(batch) {
							continue
						}
						if e.fileTranslationUsable(ctx, texts[idx], batch[i], lc) {
							addTrans(lc, texts[idx], batch[i])
							addModelHit()
							se.final(lc, texts[idx], batch[i], "reviewed") // ★B3：审校后终稿（同文自动去重）
						} else {
							noteRefusal(lc, texts[idx], batch[i]) // ★ M1：回绝话术记账（收尾进 GateWarnings）
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
				if !preFilled[t] {
					sendIdx = append(sendIdx, i)
				}
			}
			sendTexts := make([]string, len(sendIdx))
			for i, idx := range sendIdx {
				sendTexts[i] = texts[idx]
			}
			protSrc, _ := protectSourceByLang(sendTexts, brandTermsAll[lc])
			batch := e.BatchTranslateOpts(ctx, protSrc, lc, adaptiveBatchSize(protSrc, imgSegCap, 6000),
				// ★B3：同 KB 路径——进度 + 逐段 segment_done（sendIdx 映射回全局段号/原始源文）
				se.batchCB(lc, func(done, total int) { prog("file_translate|初翻|"+lc, done, total) }, sendIdx),
				// ★ P5：表头语境（同上）
				BatchOptions{HeaderContext: fileHeaderCtx, LiteralOCR: ocrSourced, Glossary: glossaryProf.TermsFor(lc, glossaryLim.MaxTerms), GlossaryLabel: glossaryProf.DisplayLabel()})
			// ★ pro 模式批量审校（同上：整块一次调用）
			if !fast {
				batch = e.reviewBatchSafe(ctx, protSrc, batch, lc,
					func(done, total int) { prog("file_translate|校对|"+lc, done, total) })
			}
			for i, idx := range sendIdx {
				t := texts[idx]
				// ★ 回显检测（与 KB 路径同一口径，见 translation_guard.go）：模型原样返回源文
				// = 未翻译，视为缺失走重试，否则非中文目标时源文会被当成「译文」静默写入成品。
				if i < len(batch) {
					if e.fileTranslationUsable(ctx, t, batch[i], lc) {
						addTrans(lc, t, batch[i])
						addModelHit()
						if !fast {
							se.final(lc, t, batch[i], "reviewed") // ★B3：fast 模式无审校，终稿统一留给收尾 sweep
						}
					} else {
						noteRefusal(lc, t, batch[i]) // ★ M1：回绝话术记账
					}
				}
			}
		}(lc)
	}
	wg.Wait()
	// ★ 复核补漏（文件工单复核第1层）：扫描各语言未命中段落，重试一次批量模型。
	// 治「有的中文还没翻译就贴上来」：首翻失败的段不再静默缺失。
	// ★ 硬闸重试（方案语义）：对缺失段「重启 LLM 翻译」；仍回显则保留原文并计入告警。
	// ★ 2026-10-21 用户令：所有翻译统一重试 5 次，超过 5 次仍缺的段按原文交付并告警。
	// ★ 成本预算护栏（不改变上述语义本体）：
	//   ① 墙钟预算 FILE_HARDGATE_MAX_SEC（默认 600s）——2026-08-26 漏翻整改：
	//      预算检查从「仅轮首」细化为「轮首+逐段之间」，防止一轮内部超支导致
	//      尾部段落（如末尾表格的「无」）从未获得补翻机会；
	//   ② 轮次上限固定 3 轮（fileHardgateMaxAttempts），不再按「连续零进展」提前熔断——
	//      用户令要求 5 次机会必须给足；
	//   ③ 每轮先小批量（bs=10，<sN> 显式配对）重译——对超短段（「无/有/日期」）
	//      的抗回显性显著优于逐段单发，且吞吐高一个量级；批量后仍缺的段再逐段兜底。
	untranslated := map[string]int{} // 语言 → 硬闸结束后仍未译出段数（进 Data 供审批/QA 可见）
	// F-01：硬闸收尾阶段的补译/缺失告警先收在这里，等 applySegmentGates 之后再并入 gateWarnings
	// （gateWarnings 的声明在写回前，不能提前引用）。
	leakWarnings := []string{}
	// ★ P1 整改（漏译静默通过）：只给数量等于什么都没给——审批/QA 无从定位是哪几段。
	//   同口径把「仍未译出的源文段清单」一并透出（上限见 untranslatedSegmentsCap，防爆 payload）。
	untranslatedSegments := map[string][]string{}
	// ★ N-01（2026-10-25 对账补腿）：token 成本预算线（判据与档位见 file_token_budget.go）。
	//   上面那条墙钟预算只拦「跑得久」，拦不住「每一次都很贵」——UAT 工单 115 正是
	//   0.2KB／4 行文件在 600s 内跑掉约 20 行 × 2.06 万 token，实扣 1,037 积分（预估的 24 倍）。
	//   基准取**此刻的首翻实际用量**：文件链逐批重发指令，整篇公式估算会结构性低估它。
	//   只约束下面的重试/补译腿，首翻已经跑完，不受任何影响；无计量口径＝不判（fail-open）。
	firstPassTokens, hasUsage := collectorTokens(ctx)
	repairBudget := int64(-1)
	if hasUsage {
		repairBudget = e.repairTokenBudgetCap(ctx, firstPassTokens)
	}
	// 到线即停：出一行 WARN（运维）＋一句进 leakWarnings（审批台与客户面）。
	// 「按现有降级链交付」在界面上与"全部译出"长得一样，所以这一句是唯一的可见出口。
	// 判定与出声收在这一个闭包里（三个复核点位共用），调用方只问「停不停」。
	stopForTokenBudget := func(lc string, remaining int) bool {
		over, spent := e.repairBudgetSpent(ctx, repairBudget)
		if !over {
			return false
		}
		leakWarnings = append(leakWarnings, e.warnRepairBudgetStopped(ctx, lc, spent, repairBudget, remaining))
		return true
	}
	for _, lc := range finalLangs {
		// ★ missingSegments 与写入点判定（fileTranslationUsable）配对：不可用译文不写入 ⇒ 键存在
		//   严格等价于已译出，故 KB 脏行不再「既不算命中也不被统计」地凭空消失（RC-2 落点）。
		missing0 := missingSegments(texts, langTranslations[lc])
		if len(missing0) == 0 {
			continue
		}
		budget := hardGateBudget()
		loopStart := time.Now()
		for attempt := 1; attempt <= fileHardgateMaxAttempts; attempt++ {
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
			// ★ N-01：轮首复核 token 预算（与墙钟预算并列，两条各拦一种失控形态）。
			if stopForTokenBudget(lc, len(still)) {
				break
			}
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
			batch := e.BatchTranslateOpts(ctx, protStill, lc, adaptiveBatchSize(protStill, imgSegCap, 6000),
				// ★B3：硬闸重译块同样逐段推进度+segment_done（补漏段此前无行，前端据此补行）
				se.batchCB(lc, func(done, total int) { prog("file_translate|初翻|"+lc, done, total) }, stillIdx),
				// ★ P5：硬闸重译同样带表头语境——补译的那几行正是最容易跑偏术语的一批
				BatchOptions{HeaderContext: fileHeaderCtx, LiteralOCR: ocrSourced, Glossary: glossaryProf.TermsFor(lc, glossaryLim.MaxTerms), GlossaryLabel: glossaryProf.DisplayLabel()})
			for i, m := range still {
				// ★ 硬闸写入点同样走 fileTranslationUsable（原手写 3 个不等式 + 无指令残留判定）：
				//   仍回显/仍残留指令 ⇒ 不写入，该段留在缺失集合里直至被收尾统计浮出。
				if i < len(batch) {
					if e.fileTranslationUsable(ctx, m, batch[i], lc) {
						addTrans(lc, m, batch[i])
						// ★ 2026-10-25：硬闸这一轮同样是**模型**译出的段，必须计进 model_hits。
						//   改之前只 addTrans 不计数 ⇒ 初翻全被判回显、靠硬闸救回整篇的单，
						//   工单读数写「模型翻译 0 段」而成品里每一行都是模型译文（命中计数与实际
						//   写入脱钩，同 addTrans 裸写入器那条注释针对的形态）。补译段不重复计：
						//   still 只含此刻仍缺失的段，初翻已写入的段进不了这个循环。
						addModelHit()
					} else {
						noteRefusal(lc, m, batch[i]) // ★ M1：硬闸这一轮仍是回绝 ⇒ 记账
					}
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
				// ★ N-01：逐段之间同样复核 token 预算（与上面那条墙钟复核同位）——
				//   一轮内部的逐段兜底可以是几十次整块调用，只在轮首判等于给一轮开了无限额度。
				if stopForTokenBudget(lc, len(missingSegments(texts, langTranslations[lc]))) {
					break
				}
				if v, ok := e.translateSegmentMinimal(ctx, m, lc); ok {
					if !e.fileTranslationUsable(ctx, m, v, lc) {
						noteRefusal(lc, m, v) // ★ M1：逐段兜底仍是回绝 ⇒ 记账
						continue
					}
					addTrans(lc, m, v)
					addModelHit()     // ★ 同上：极简腿救回的段也是模型译文，计数必须跟着写入走
					se.done(lc, m, v) // ★B3：逐段兜底补译成功即推初翻上屏
					// 观测腿：这条日志既是"极简腿真被走到"的现场读数，也是 minimalInstrRescueMarker
					// 的运行期引用点（没有它，常量会被内联/优化掉，发布件特征串两向都不可信）。
					// 只出语言与标记，**不出段文本**（工单内容属客户资料，日志不留原文）。
					observability.Info(ctx, "文件翻译：极简指令兜底腿救回未译段", "lang", lc, "mark", minimalInstrRescueMarker)
				}
			}
		}
		// ★ F-01（2026-10-24 用户令）：5% 阈值。硬闸 3 轮后仍超阈值 ⇒ 再做定向补译，
		//   只送缺失段（极简指令腿），补回即止；不判失败、不退款、不自动整单重发。
		if lang := leakedLangAt(lc, len(missingSegments(texts, langTranslations[lc])), len(texts), e.fileLeakRatio()); lang != "" {
			calls := 0
			maxCalls := e.fileRepairMaxCalls()
			for round := 1; round <= e.fileRepairMaxRounds(); round++ {
				still := missingSegments(texts, langTranslations[lc])
				if len(still) == 0 {
					break
				}
				if ctx.Err() != nil || calls >= maxCalls {
					break
				}
				// ★ N-01：追加补译同样受 token 预算约束（次数上限只管"打几次"，管不住"每次多大"）。
				if stopForTokenBudget(lc, len(still)) {
					break
				}
				log.Printf("[file-repair] lang=%s round=%d 定向补译 %d 段（阈值 %.0f%%）", lc, round, len(still), e.fileLeakRatio()*100)
				for _, m := range still {
					if ctx.Err() != nil || calls >= maxCalls {
						break
					}
					calls++
					if v, ok := e.translateSegmentMinimal(ctx, m, lc); ok && e.fileTranslationUsable(ctx, m, v, lc) {
						addTrans(lc, m, v)
						addModelHit() // ★ 同上一处：定向补译也是模型译文，计数跟着写入走（still 只含仍缺失段 ⇒ 不重复计）
						se.done(lc, m, v)
					}
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
			if leakedLangAt(lc, len(missList), len(texts), e.fileLeakRatio()) != "" {
				leakWarnings = append(leakWarnings, fmt.Sprintf(
					"%s 仍有 %d/%d 段未译出（超过 %.0f%% 阈值，已尝试定向补译但仍缺失；请人工检查）",
					config.LangNames[lc], len(missList), len(texts), e.fileLeakRatio()*100))
			}
		}
	}
	// ★ M1 收尾：把「模型回绝过、且最终仍未补译成功」的段收成读数。
	// 剔除已补译的段——只有仍然缺失的那些才真的按原文交付，才需要客户/审批台看见。
	// 只出语言与段数，**不出段文本**（工单内容属客户资料，日志不留原文）。
	refusalBlocked := map[string]int{}
	for lc, set := range refusalSeen {
		n := 0
		for src := range set {
			if _, ok := langTranslations[lc][src]; !ok {
				n++
			}
		}
		if n > 0 {
			refusalBlocked[lc] = n
		}
	}
	prog("第2步/3：翻译完成", 2, 3)

	// ★ 漏译阈值（★ 2026-10-24 用户令：50%→5%，超阈值补译而非判失败）：
	//   超阈值的语言已在硬闸收尾段做过定向补译；这里不再返回错误/判失败。
	//   仍缺失的段通过 untranslated/untranslated_segments/gate_warnings 显式可见，
	//   工单继续完成（不退款、不自动整单重发）。
	for _, lc := range finalLangs {
		if lang := leakedLangAt(lc, untranslated[lc], len(texts), e.fileLeakRatio()); lang != "" {
			log.Printf("[file-gate] %s 漏译 %d/%d 段（超 %.0f%%），已按补译/标记口径交付", lang, untranslated[lc], len(texts), e.fileLeakRatio()*100)
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
	gateWarnings = append(gateWarnings, e.applySegmentGates(ctx, langTranslations, true)...)
	gateWarnings = append(gateWarnings, leakWarnings...)
	// ★ G4（2026-10-19 D5）：品牌名是不可译常量，工单/文件链此前没有挂件那道 guardReplyBrand。
	//   译文里品牌名逐字缺失 ⇒ 出声 + 记账（客户与审批台可见）。名单取不到时 checkFileBrandPresence
	//   内部 fail-soft 返回 0（拿不到清单＝不知道品牌名，不误报）。
	if drops, langs := e.checkFileBrandPresence(ctx, texts, langTranslations); drops > 0 {
		observability.Warn(ctx, "文件交付品牌名缺失", "drops", drops, "langs", len(langs))
		gateWarnings = append(gateWarnings, fmt.Sprintf("brand_dropped=%d（%d 个语言的译文里品牌名缺失，可能被翻掉）", drops, len(langs)))
	}
	// ★ M1（2026-10-21 UAT 收口）：模型拒译段已回落原文 —— 必须出声。
	// 旧形态这句回绝直接当译文进成品（五件交付物同名），客户拿到一份"看起来成功"的
	// 未翻译产物；现在正文回落原文、单据仍可交付，但 gate_warnings 里逐语言点名段数。
	if len(refusalBlocked) > 0 {
		langs := make([]string, 0, len(refusalBlocked))
		total := 0
		for lc, n := range refusalBlocked {
			langs = append(langs, lc)
			total += n
		}
		sort.Strings(langs)
		observability.Warn(ctx, "文件翻译：模型拒译段已回落原文", "segments", total, "langs", strings.Join(langs, ","))
		gateWarnings = append(gateWarnings, fmt.Sprintf(
			"refusal_blocked=%d（%s 的译文里模型返回拒译话术，已保留原文交付，请人工补译）",
			total, strings.Join(langs, "/")))
	}
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

	// dubTranslatedAudio 音频入口的**配音产物**（用户令 2026-10-16：「处理完回成原文件格式」）。
	// 两段式：先按**输入容器**合成（mp3 进 mp3 出、m4a 进 m4a 出，用户拿回去能直接替换原件）；
	// 该容器转码不可用（服务器缺 ffmpeg 或该容器编码器，脚本侧回 ERR:Encode）⇒ 回落 .mp3，
	// 并在 GateWarnings 里如实点明容器已改——不丢声音，也不假装容器对得上。
	// 返回 (产物路径列表, 面向客户的 warning)，两者均可为空。合成整体失败只记 warning：
	// 配音是增值产物，字幕与译文才是资产，**绝不**因为没出声把整单判死。
	// 容器映射的唯一事实源是 fileproc.DubContainerFor，这里不另抄一份后缀判断。
	dubTranslatedAudio := func(lc string, lines []string) ([]string, string) {
		paths := map[string]string{}
		synth := func(container string) error {
			suffix := e.artifactLangSuffix(lc) + container
			p := filepath.Join(outputDir, artifactBase(lc, suffix)+suffix)
			if serr := fileproc.SpeakLines(ctx, lines, lc, p); serr != nil {
				return serr
			}
			paths[container] = p
			return nil
		}
		container, fellBack, serr := dubWithFallback(ext, synth)
		if serr != nil {
			observability.Warn(ctx, "译文配音跳过（不影响译文 .md 交付）",
				"lang", lc, "error", serr.Error())
			return nil, fmt.Sprintf("%s 未生成译文配音（%s）；译文 .md 已完整交付", config.LangNames[lc], serr.Error())
		}
		if fellBack {
			observability.Warn(ctx, "译文配音回落 mp3（原容器转码不可用）", "lang", lc)
			return []string{paths[container]}, fmt.Sprintf(
				"%s 配音已回落 mp3（原容器 %s 转码不可用，服务器需安装 ffmpeg）；译文 .md 不受影响",
				config.LangNames[lc], fileproc.DubContainerFor(ext))
		}
		return []string{paths[container]}, ""
	}

	// writeTextMd 生成某语言的纯文案 .md 产物（按提取顺序输出；未译段保留原文）。
	// 参数 tr 传**交付口径**的译文（deliveryTranslations 的产物），不直接读 langTranslations：
	// 拒译句/标识符还原/单行收敛这些出栈卫生都在那一层，md 与写回件必须同一份译文。
	writeTextMd := func(lc string, tr map[string]string) (string, error) {
		suffix := e.artifactLangSuffix(lc) + "-text.md"
		p := filepath.Join(outputDir, artifactBase(lc, suffix)+suffix)
		return p, fileproc.WriteTranslationMd(p, texts, tr)
	}

	// ★ 2026-10-30 三事合一批 · 组5「两段式交付（md 先行）」
	// artStage 是本次 HandleFile 的产物事件发射器（未订阅＝空函数，即时翻译链零开销）。
	// mdPaths 记本语言已落地的纯文案 .md：末尾 textOut 段只搬运它、**不再重写**。
	// 为什么 md 要先写：客户在轮询里先看到可读的 md、再等到原格式件；旧形态是"全都好了才一起出现"，
	// 大文件（PDF 版式重建）动辄几十秒，那段时间页面上是一片空白。
	artStage := newArtifactStage(ctx, filePath)
	mdPaths := map[string]string{}
	degradedHit := func(lc string) bool {
		for _, d := range degraded {
			if d == lc {
				return true
			}
		}
		return false
	}
	// appendUntranslatedNotes 给已写好的 .md 追加「未译出内容」一节（AGENTS §19 裁决 7）。
	// md 从"旁路"升成主阅读面之后，它若不含这一节，客户看到的就是一份「看起来全译好了」的文档
	// ——这正是 §19 要消灭的形态。空清单 ⇒ 整节不出现（连标题都不写）。
	// 追加失败只出声＋进 gate_warnings，不判死：译文已落盘，不能因为一节附注没写上把整单打成失败。
	appendUntranslatedNotes := func(lc, p string, notes []fileproc.UntranslatedItem) {
		if len(notes) == 0 {
			return
		}
		if nerr := fileproc.AppendUntranslatedNotes(p, notes); nerr != nil {
			observability.Warn(ctx, "交付文档「未译出内容」一节追加失败", "lang", lc, "error", nerr.Error())
			gateWarnings = append(gateWarnings, fmt.Sprintf("%s 交付文档的「未译出内容」一节没能写入（%s）；本次未能点名具体哪几段未译出", config.LangNames[lc], nerr.Error()))
			return
		}
		gateWarnings = append(gateWarnings, fmt.Sprintf("%s 有 %d/%d 段未译出，已按原文交付并在交付文档文末逐段点名", config.LangNames[lc], len(notes), len(texts)))
	}
	// writeLangMD 写纯文案 .md（含未译出点名）并立刻发 kind=md 事件——「md 先行」的唯一写点。
	// 任何格式分支都必须先调它、再产出 original；重写同一语言会重复追加「未译出」那一节，
	// 所以调用方一律先看 mdPaths[lc]。
	writeLangMD := func(lc string, tr map[string]string, notes []fileproc.UntranslatedItem) (string, error) {
		p, perr := writeTextMd(lc, tr)
		if perr != nil {
			return "", perr
		}
		appendUntranslatedNotes(lc, p, notes)
		mdPaths[lc] = p
		artStage.emitOne(ArtifactKindMD, lc, p, false, false)
		return p, nil
	}

	if deliveryText {
		// —— 纯文案模式：交付 {base}_{lang}.md，不做任何 office 格式回写 ——
		for _, lc := range finalLangs {
			tr := deliveryTranslations(lc, langTranslations[lc])
			if len(tr) == 0 {
				continue
			}
			suffix := e.artifactLangSuffix(lc) + ".md"
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
			// ★ 2026-10-26 裁决 7（「交付文档只记录未翻译的内容」）在**第二个交付口**的同一把尺子：
			//   纯文案 .md 就是这一语言的主交付物，未译出的段必须在文末点名。
			//   ★ 2026-10-30 组5：改用统一助手（口径与图片/Office 各口一致）。
			appendUntranslatedNotes(lc, outPath, untranslatedNotesFromSegments(texts, tr))
			filesOut = append(filesOut, outPath)
			langArtifact[lc] = outPath // 纯文案产物即该语言主交付物（.md 可直读比对指纹）
			// ★ 两段式：纯文案模式没有「原格式」这一件，md 就是主产物 ⇒ primary=1，
			//   否则前端会按「该组还没有 original」把它画成「生成中…」（等一个永远不会来的件）。
			mdPaths[lc] = outPath
			artStage.emitOne(ArtifactKindMD, lc, outPath, true, false)
		}
	} else if isXlsxInput {
		// ★ 两段式阶段①：md 先行。xlsx 是「一份文件含全部语言」的合并交付，写回只有一次，
		//   等到写回完再写 md 就等于「md 永远晚于 original」——在那条链上 md 先行会完全落空。
		//   md 逐语言写（每语言一行 artifact，path 各不相同），original 是同一份 xlsx（每语言一行、同 path）。
		for _, lc := range finalLangs {
			tr := deliveryTranslations(lc, langTranslations[lc])
			if len(tr) == 0 {
				continue
			}
			if _, perr := writeLangMD(lc, tr, untranslatedNotesFromSegments(texts, tr)); perr != nil {
				// md 失败不判死：原格式件仍可能交付成功（它与 md 是两条独立写路径）。
				observability.Warn(ctx, "xlsx 纯文案 .md 先行生成失败（不影响原格式交付）", "lang", lc, "error", perr.Error())
				gateWarnings = append(gateWarnings, fmt.Sprintf("%s 纯文案 .md 生成失败（不影响原格式交付）：%s", config.LangNames[lc], perr.Error()))
			}
		}
		// ★ 产物名（RC-4）：xlsx 是「一份文件含全部语言」的合并交付，无单一语言可归属，
		//   按首目标语言翻 base（单语言场景本就是该语言；多语言场景取首语言名，与 Sheet 名=语言码一致）。
		xlsxSuffix := "-multi.xlsx"
		if len(finalLangs) == 1 {
			xlsxSuffix = e.artifactLangSuffix(finalLangs[0]) + ".xlsx"
		}
		xlsxBase := baseName
		if len(finalLangs) > 0 {
			xlsxBase = artifactBase(finalLangs[0], xlsxSuffix)
		}
		outPath := filepath.Join(outputDir, xlsxBase+xlsxSuffix)
		var aerr error
		xlsxHits := -1 // ★ G17：-1＝本轮无命中数口径（多语言多 Sheet 路径）
		if len(finalLangs) == 1 {
			// ★ 单目标语言：原地替换单元格为译文，产物文件即译文本身
			// （符合「把文件翻成 X 语」的预期；原文件保持不变，下载的 base-<lang>.xlsx 为译文）。
			// 此前多 Sheet 模式会把原文 Sheet 留在首位、译文放新增 Sheet，Excel 默认打开原文 Sheet
			// 造成「还是中文」的误解（实际译文在 en Sheet 中已正确生成）。
			xlsxHits, aerr = fileproc.ApplyXlsx(filePath, outPath, langTranslations[finalLangs[0]])
		} else {
			// ★ 多目标语言：单文件多 Sheet，每个目标语言一个 Sheet（Sheet 名=语言代码）
			//   base 与上面 outPath 同一份已翻译名，否则降级/成功两条路会产出两个不同文件名。
			aerr = writeMultiSheetXlsx(filePath, outputDir, xlsxBase, finalLangs, langTranslations)
		}
		// ★ G17：零命中与写回失败同走退化通道——不再静默交付未翻译的 xlsx。
		if aerr == nil && xlsxHits == 0 {
			aerr = fmt.Errorf("xlsx 写回零命中（没有任何单元格被替换，疑似译文键与表格内容不对应）")
		}
		if aerr == nil {
			filesOut = append(filesOut, outPath)
			for _, lc := range finalLangs {
				if len(langTranslations[lc]) == 0 {
					continue
				}
				langArtifact[lc] = outPath
				// ★ 两段式阶段②：一份 xlsx 承载多语言 ⇒ 每个语种各一行 original、指向同一 path
				//   （唯一索引含 lang，不会冲突）。
				artStage.emitOne(ArtifactKindOriginal, lc, outPath, true, false)
			}
		} else {
			_ = os.Remove(outPath)
			// ★ 双模式（2026-09-13）：回写失败降级纯文案交付（翻译是资产，回写是增值）。
			// ★ 两段式：md 已在阶段①发过 ⇒ 这里**不再发 original**，只把对应 md 行标成 degraded
			//   （服务层按唯一键 upsert，是"更新一行"不是"多一行"）。
			log.Printf("[file-degrade] xlsx 回写失败，降级纯文案: %v", aerr)
			for _, lc := range finalLangs {
				if len(langTranslations[lc]) == 0 {
					continue
				}
				p := mdPaths[lc]
				if p == "" {
					// 阶段①的 md 没写成（极少见）：现场补一次，宁可晚到也不能让该语言颗粒无收。
					if p2, perr := writeLangMD(lc, deliveryTranslations(lc, langTranslations[lc]), untranslatedNotesFromSegments(texts, deliveryTranslations(lc, langTranslations[lc]))); perr == nil {
						p = p2
					}
				}
				if p == "" {
					continue
				}
				filesOut = append(filesOut, p)
				langArtifact[lc] = p // 降级交付的 .md 是该语言真实产物，保真比对应读它
				degraded = append(degraded, lc)
				artStage.emitOne(ArtifactKindMD, lc, p, false, true)
			}
			if len(degraded) == 0 {
				return &FileTranslateResult{Skill: "translation", Error: "xlsx 写回失败: " + aerr.Error()}
			}
		}
	} else {
		// ⚠️ 这里原本会把**原始音频本体**随产物拷一份（理由是要给客户把 .srt 挂回原音轨自行核对）。
		// ★ 2026-10-30 三事合一批：字幕产物已取消（音频交付＝译文 .md ＋ 同容器配音两件），
		//   那条挂载理由随之不成立 ⇒ 副本不再随包（同一份字节 × N 语言本来也是浪费，见 AGENTS §一·14）。
		// ★ P1a：图片格式经 OCR 投喂翻译，交付面没有「原图副本」这一说，逐语言出译后图＋.md。
		// ★ 每个目标语言独立产物文件，文件名标注语言
		for _, lc := range finalLangs {
			tr := deliveryTranslations(lc, langTranslations[lc])
			if len(tr) == 0 {
				continue
			}
			// ★ 产物名（RC-4）：base 已按该语言翻译，后缀（"_en.docx"）保持既有口径不动。
			nameSuffix := e.artifactLangSuffix(lc) + ext
			outPath := filepath.Join(outputDir, artifactBase(lc, nameSuffix)+nameSuffix)
			// ⚠️ 这里曾经有一整档「xlsx 双语对照表」交付（srt/vtt/json/yaml/yml 的唯一形态，
			//   2026-09-09 产品决策）。★ 2026-10-30 三事合一批把那五种格式**撤出准入**，
			//   对照表这一档连同 `fileproc.WriteComparisonXlsx` 一起退役（AGENTS §一·14）。
			//   历史工单已产出的对照表照常可下载——准入判据只作用于建单侧，绝不套到下载侧。
			// ★ P1a：图片格式（经 OCR 投喂翻译）无原格式可原位回写，交付形态为译文 .md 文档。
			// 产物名 {base}_{lang}.md（与 md 源码翻译同源 WriteTranslationMd，按提取顺序输出译文段）。
			if writebackDelivery(ext) == "image" {
				imgSuffix := e.artifactLangSuffix(lc) + ".md"
				imgPath := filepath.Join(outputDir, artifactBase(lc, imgSuffix)+imgSuffix)
				// ★ W4（2026-10-26 用户裁决 5「图片未翻译出行应该先对未翻译项进行充分重试，
				//   实在失败再在 md 里标注」＋裁决 7「交付文档只记录未翻译内容」）：
				//   旧形态是"首翻没给这一行 ⇒ 图上与 .md 直接按原文顶上"，日志里只剩一个 residue 计数——
				//   **重试这个动作物理上从来没发生过**，客户拿到的图上那一行还是中文。
				//   现在先过一遍单行补译梯（与文本逐行链同一把尺子、同一档位键），
				//   仍拿不到可用译文的行才进交付文档那一节。
				//   必须在 WriteTranslationMd **之前**跑：.md 也按原文兜底，先补再写才不会把原文当译文交付。
				// ★ 2026-10-29：单行源文 → 单行译文收敛（模型回「manual\nManual」会把两行画进一个框）。
				tr = normalizeImageTranslations(tr)
				unresolved, imgUpstreamDead := e.retryImageSegments(ctx, texts, tr, lc)
				// ★ 2026-10-29 第十一轮：末段补译写回 tr 之后，**整份再走一遍交付卫生链**。
				//   :1341 的术语/质量闸、:1563 的 deliveryTranslations、:1598 的 normalizeImageTranslations
				//   都排在 retryImageSegments 之前；补译填出来的那几行因此绕过了它们
				//   （术语不覆盖 / manual→handbook 不被拉回 / manual\nManual 不被收敛）。
				//   三条腿都幂等（拒译句剥离、尾残渣剥离、标识符逐字、单行收敛），不会二次损坏。
				tr = normalizeImageTranslations(deliveryTranslations(lc, tr))
				if ierr := fileproc.WriteTranslationMd(imgPath, texts, tr); ierr != nil {
					return &FileTranslateResult{Skill: "translation", Error: fmt.Sprintf("%s 译文文档生成失败：%s", config.LangNames[lc], ierr.Error())}
				}
				// 那一节必须追加在 .md 写完之后（追加到一个还不存在的文件上＝整节静默丢失，
				// 而"客户看不见哪些行没翻"正是裁决 7 要消灭的形态）。写失败只出声不判死：
				// 译文与图都已到货，不能因为一节附注没写上就把整单打成失败。
				if uerr := fileproc.AppendUntranslatedNotes(imgPath, unresolved); uerr != nil {
					observability.Warn(ctx, "图片交付文档「未译出内容」一节追加失败", "lang", lc, "error", uerr.Error())
					// ★ 出声必须同块记账（file_delivery_honesty_gate_test.go 那条闸判的红）：
					//   这一节是客户在交付面上**唯一**能看见"哪几行没翻"的载体，它没写上＝退化，
					//   只留一行日志就等于把交付面缺东西写成"一切正常"。
					gateWarnings = append(gateWarnings, fmt.Sprintf("%s 交付文档的「未译出内容」一节没能写入（%s）；本次未能点名具体哪几行没译出", config.LangNames[lc], uerr.Error()))
				}
				// ★ 两段式阶段①：图片的 md 必须在这里发——它的内容依赖**补译之后**的 tr（R-5：
				//   图片链的未译出清单走 retryImageSegments 的未译段，不填 UntranslatedSegments），
				//   但顺序仍在译后图（阶段②）之前，md 先行的语义不变。
				mdPaths[lc] = imgPath
				artStage.emitOne(ArtifactKindMD, lc, imgPath, false, false)
				if imgUpstreamDead {
					observability.Warn(ctx, "图片原位链逐段补译判上游断，剩余未译段不再逐行烧调用",
						"lang", lc, "untranslated_segments", len(unresolved))
					// 同上：早停本身是一次"这些行我们不再尝试"的决定，客户面必须看得见它，
					// 否则表现就是"图上有几行还是原文"而 nowhere 说明原因（本仓「静默降级」那一族）。
					gateWarnings = append(gateWarnings, fmt.Sprintf("%s 上游连续未返回可用译文，已停止逐段重试；%d 段按原文保留", config.LangNames[lc], len(unresolved)))
				}
				filesOut = append(filesOut, imgPath)
				langArtifact[lc] = imgPath // 译文 .md：保真比对应读它
				// ★ P4「图片原位翻译」：在**同一份 OCR 坐标**上把译文画回原图，随 .md 一起交付。
				//   它是**择优产出**：渲染不出来（缺中文字体 / 坐标没缓存住 / 行长溢出）就退回
				//   “只有译文 .md”的既有形态——译文已经翻好，绝不能让“图没画出来”把整单判死。
				inPlaceSuffix := e.artifactLangSuffix(lc) + ext
				inPlacePath := filepath.Join(outputDir, artifactBase(lc, inPlaceSuffix)+inPlaceSuffix)
				// ★ G2/G18（2026-10-19）：原位回填失败不再只写一行日志——档位名进 GateWarnings，
				//   该语言同时进 degraded（交付形态实为「仅译文 .md」，与话术口径一致）。
				if stats, rerr := fileproc.RenderTranslatedImage(ctx, filePath, inPlacePath, RenderSafeTranslations(tr)); rerr != nil {
					reason := fileproc.ImageRenderReason(rerr)
					observability.Warn(ctx, "图片原位回填跳过（退回仅译文 .md）", "error", rerr.Error(), "reason", reason, "artifact", filepath.Base(inPlacePath))
					gateWarnings = append(gateWarnings, fmt.Sprintf("%s 图片原位回填未生成（%s）：%s；译文 .md 已完整交付", config.LangNames[lc], reason, rerr.Error()))
					degraded = append(degraded, lc)
					// ★ 两段式：md 已发 ⇒ 不再发 original，把 md 行标成 degraded（服务层按唯一键 upsert）。
					artStage.emitOne(ArtifactKindMD, lc, imgPath, false, true)
				} else {
					filesOut = append(filesOut, inPlacePath)
					// ★ 两段式阶段②：译后图即该语言主产物。
					artStage.emitOne(ArtifactKindOriginal, lc, inPlacePath, true, false)
					// ★ G3-c：有行没译出（由原文顶上）必须出声——否则「图上还是中文」被静默交付。
					if stats.Residue > 0 {
						observability.Warn(ctx, "图片原位回填存在未译出行（保留原文）", "lang", lc, "residue", stats.Residue, "total", stats.Total)
						gateWarnings = append(gateWarnings, fmt.Sprintf("%s 图片原位回填有 %d/%d 行未译出（保留原文）", config.LangNames[lc], stats.Residue, stats.Total))
					}
					// ★ 2026-10-21 UAT 收口 M9：图形符号整行（箭头/三角/emoji）被剔除不重画——
					//   原图标逐像素保留，但客户在完成页上要能读到"这一行不是漏译"。只计数、不判失败。
					// ★ 2026-10-29 同计数并入第二档：单字符非汉字的行（开关被误读成 `0`/`O` 那一族）
					//   同样整框不画。它们既不是漏译、也不该进 Residue（那会让客户收到一条
					//   指向不存在问题的「有 N 行未译出」提示，而真正的漏译被淹在里面）。
					if stats.Dropped > 0 {
						observability.Info(ctx, "图片原位回填剔除图形符号行（原样保留）", "lang", lc, "dropped", stats.Dropped, "total", stats.Total)
						gateWarnings = append(gateWarnings, fmt.Sprintf("%s 图片原位回填有 %d 行图形符号／图标类误读（箭头、开关、单字符）已原样保留，未重绘", config.LangNames[lc], stats.Dropped))
					}
				}
				continue
			}
			// ★ P2：音频格式（经 ASR 转写投喂翻译）同样没有「原位回写」这回事——译文是文本，
			//   不可能塞回原音频。
			// ★ 2026-10-30 三事合一批改档（AGENTS §一·14）：交付**两件**——
			//     ① {base}_{lang}.md          译文纯文案（阅读/校对/再加工的主面）
			//     ② {base}_{lang}.<原容器>    同容器译后配音（照搬输入容器，mp3 进 mp3 出）
			//   旧形态的 ③ 译文字幕 .srt（带原时间轴）与 ④ 原音频副本**一并取消**：
			//   字幕存在的理由是把译文挂回原音轨自行核对，而配音本身就是可听的交付物，两件并存是噪音。
			//   ⚠️ 「拿不到配音 ⇒ 这一组只有 .md、原格式按钮禁用并给原因」那一档**落在产物层**表达
			//   （组5 的 ticket_artifacts 行 `degraded`），这里**不抬** `degraded_langs`：
			//   泰语这类「语音引擎本就没有该语种」是能力档，不是质检存疑，混进去就是把设计报成缺陷。
			if writebackDelivery(ext) == "audio" {
				mdSuffix := e.artifactLangSuffix(lc) + ".md"
				audPath := filepath.Join(outputDir, artifactBase(lc, mdSuffix)+mdSuffix)
				if merr := fileproc.WriteTranslationMd(audPath, texts, tr); merr != nil {
					return &FileTranslateResult{Skill: "translation", Error: fmt.Sprintf("%s 译文文档生成失败：%s", config.LangNames[lc], merr.Error())}
				}
				// ★ 两段式阶段①：md 先行（配音可能要等 TTS 子进程几十秒，那段时间客户先拿到可读的译文）。
				//   未译出的行同样点名（B18）：音频链的 md 也是主阅读面。
				appendUntranslatedNotes(lc, audPath, untranslatedNotesFromSegments(texts, tr))
				mdPaths[lc] = audPath
				artStage.emitOne(ArtifactKindMD, lc, audPath, false, false)
				filesOut = append(filesOut, audPath)
				langArtifact[lc] = audPath // 译文 .md：保真比应对读它
				// ★ P2 收口：译文配音（择优产出）。语种越界（语音引擎没有该语种，判据现读
				//   `fileproc.TtsCoveredLangs()` 的**有效档**而非声明档）或合成失败 ⇒ 跳过并在
				//   GateWarnings 明示，工单仍完成——配音是增值，译文 .md 才是资产。
				//   缝合顺序按 **texts 的提取顺序**：字幕取消后不再有「与 srt 同源」那条约束，
				//   而 texts 是提取层给出的唯一有序口径（同一件跑两次必然同顺序＝产物可复算）。
				ttsLines := make([]string, 0, len(texts))
				for _, t := range texts {
					if v, ok := tr[t]; ok && strings.TrimSpace(v) != "" {
						ttsLines = append(ttsLines, v)
					}
				}
				if len(ttsLines) == 0 {
					// ★ G18（2026-10-19）：一段译文都没拿到时不再静默——进 GateWarnings，
					//   让审批/QA 看得出「这一语言没有配音」是内容层原因而非合成层原因。
					observability.Warn(ctx, "译文配音跳过（无可用译文行）", "lang", lc)
					gateWarnings = append(gateWarnings, fmt.Sprintf("%s 未生成译文配音（无可用译文行）", config.LangNames[lc]))
					// ★ 两段式：没有 original ⇒ md 行标 degraded（UI 右钮禁用并给原因），不发 original。
					artStage.emitOne(ArtifactKindMD, lc, audPath, false, true)
				} else {
					// ★ 2026-10-16 用户令「处理完回成原文件格式」：配音容器**照搬输入音频的容器**
					//   （mp3 进就 mp3 出、m4a 进就 m4a 出），用户拿回去能直接替换原件。
					//   容器映射的唯一事实源是 fileproc.DubContainerFor（表在 tts_speak.py 的
					//   CONTAINER_ARGS，两侧等值有单测锁）。转码不可用（缺 ffmpeg/编码器）⇒ 回落 .mp3；
					//   合成失败 ⇒ 只记 warning。配音是增值产物，**绝不**因此判死整单。
					dubFiles, dubWarn := dubTranslatedAudio(lc, ttsLines)
					filesOut = append(filesOut, dubFiles...)
					if len(dubFiles) > 0 {
						// ★ 两段式阶段②：音频只有这一件 original（译后配音，同容器）。
						artStage.emitOne(ArtifactKindOriginal, lc, dubFiles[0], true, false)
					} else {
						// 合成失败（不是"没有该语种"那一档，但同样没有 original）⇒ md 标 degraded。
						artStage.emitOne(ArtifactKindMD, lc, audPath, false, true)
					}
					if dubWarn != "" {
						gateWarnings = append(gateWarnings, dubWarn)
					}
				}
				continue
			}
			// ★ 2026-10-30 组5 · 两段式阶段①：**md 先行**。
			// 为什么放在这里而不是语言循环开头：图片/音频的 md 内容依赖各自的重试/收敛结果，
			// 它们在自己的分支里写；office/pdf/txt/csv 的 md 与写回件取的是同一份 tr，
			// 所以统一在这一处先落 md、再走下面的原格式写回（大文件版式重建动辄几十秒，
			// 那段时间客户已经能读到译文，而不是对着空白页等）。
			// .md 输入件是唯一例外：回写件本身就是 markdown ⇒ md 与 original 同一路径
			// （同路径去重口径，B10），阶段① 直接把行对齐写回做掉、写回段复用结果。
			notes := untranslatedNotesFromSegments(texts, tr)
			mdIsOriginal := false
			if ext == ".md" {
				if werr := fileproc.ApplyAlignedText(".md", filePath, outPath, tr); werr == nil {
					appendUntranslatedNotes(lc, outPath, notes)
					mdPaths[lc] = outPath
					mdIsOriginal = true
					artStage.emitOne(ArtifactKindMD, lc, outPath, false, false)
				} else {
					// 先行写回失败：交给下面的写回段重试（它才是"版式还原"的权威路径），
					// 这里只出声、不判死——重试成功时补发 md（同一路径 ⇒ 服务层收敛成一行）。
					observability.Warn(ctx, "md 输入件纯文案先行写回失败，转由写回段重试", "lang", lc, "error", werr.Error())
				}
			} else if _, perr := writeLangMD(lc, tr, notes); perr != nil {
				// md 失败不判死：原格式件仍可能成功（两条独立写路径），末尾 textOut 段还会补一次。
				observability.Warn(ctx, "纯文案 .md 先行生成失败（不影响原格式交付）", "lang", lc, "error", perr.Error())
				gateWarnings = append(gateWarnings, fmt.Sprintf("%s 纯文案 .md 生成失败（不影响原格式交付）：%s", config.LangNames[lc], perr.Error()))
			}
			if mdIsOriginal {
				filesOut = append(filesOut, outPath)
				langArtifact[lc] = outPath
				// 同一路径再发一次 original（primary=1）：服务层据此把 md 行收敛成主产物，不插第二行。
				artStage.emitOne(ArtifactKindOriginal, lc, outPath, true, false)
				continue
			}
			// ★ 2026-09-09 产品决策：写回失败不降级 xlsx 对照表（避免「翻译 PDF 却下载到 Excel」），
			//   自动重试（子进程转换偶发失败：LibreOffice profile 锁/资源竞争）；
			//   ★ 2026-09-13 双模式：重试仍败改为「纯文案 .md 降级交付 + warning」，不再整单判死。
			var aerr error
			// ★ G17（2026-10-19）：office 写回回传命中数；0 命中＝文档里没换过任何文本，
			//   与「键不对应/译文整段没落进去」同形，绝不静默交付未翻译件。
			//   writeHits == -1 表示该格式本轮无命中数口径（txt/csv/md 行对齐写回），跳过判定。
			writeHits := -1
			pdfFallbackReason := ""
			for attempt := 0; attempt <= 2; attempt++ {
				aerr = nil
				writeHits = -1
				pdfFallbackReason = ""
				switch ext {
				case ".docx":
					writeHits, aerr = fileproc.ApplyDocx(filePath, outPath, tr)
				case ".pptx":
					writeHits, aerr = fileproc.ApplyPptx(filePath, outPath, tr)
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
						writeHits = st.Replaced
						if st.Overflow > 0 {
							observability.Info(ctx, "PDF 原地替换完成（部分段轻微越界仍原字号写出）",
								"lang", lc, "replaced", st.Replaced, "requested", st.Requested, "overflow", st.Overflow)
						}
						break
					} else {
						// ★ G18（2026-10-19）：原版式覆盖失败不再只写日志——失败原因先记下，
						//   若版式重建兜住了，出栈时给一条 GateWarning（单次，与重试次数无关）。
						pdfFallbackReason = perr.Error()
					}
					// 兜底重建只需要「有序段列表 + 原文→译文映射」，不依赖 overlay 的几何信息，
					// 故原地替换崩了仍能用同一份 tr 兜底，不至于这一语言颗粒无收。
					// 版式重建路径无命中数口径（writeHits 保持 -1，跳过零命中判定）。
					if perr := fileproc.WriteTranslatedPDF(ctx, outPath, texts, tr); perr == nil {
						break
					} else {
						aerr = fmt.Errorf("PDF 写回失败：原版式覆盖 %s；版式重建 %s", pdfFallbackReason, perr.Error())
					}
				case ".txt", ".csv", ".md":
					aerr = fileproc.ApplyAlignedText(ext, filePath, outPath, tr) // ★ D5：按原行对齐写回
				default:
					aerr = fmt.Errorf("不支持的写回格式")
				}
				if aerr == nil && writeHits == 0 {
					aerr = fmt.Errorf("写回零命中：文档中没有任何文本被替换（疑似译文键与文档内容不对应）")
				}
				if aerr == nil {
					break // 写回成功
				}
				// 重试前短暂退避（子进程转换释放资源），最后一次失败不再退避
				if attempt < 2 && !sleepCtx(ctx, 2*time.Second) {
					break // ★ D9：客户端已断开，停止烧下一轮子进程/LLM 资源
				}
			}
			if aerr == nil && pdfFallbackReason != "" {
				// ★ G18：原版式覆盖失败但版式重建兜住了——交付成立，但必须出声（单次）。
				gateWarnings = append(gateWarnings, fmt.Sprintf("%s PDF 原版式覆盖失败，已改用版式重建交付：%s", config.LangNames[lc], pdfFallbackReason))
			}
			if aerr != nil {
				// ★ 双模式（2026-09-13）：重试仍败不再整单判死——该语言交付物降级为
				//   纯文案 .md（翻译是资产、回写是增值），工单成功但带 warning 轨迹与通知。
				_ = os.Remove(outPath)
				// ★ 两段式：md 已在阶段① 发过 ⇒ **不再发 original**，该语言进 degraded_langs，
				//   并把 md 行标成 degraded（服务层按唯一键 upsert，是"更新一行"不是"多一行"）。
				p := mdPaths[lc]
				if p == "" {
					// 阶段① 的 md 没写成（磁盘/权限）：现场补一次，宁可晚到也不能让该语言颗粒无收。
					if p2, perr := writeLangMD(lc, tr, notes); perr == nil {
						p = p2
					}
				}
				if p != "" {
					log.Printf("[file-degrade] %s 写回失败（%v），已降级纯文案交付 %s", config.LangNames[lc], aerr, filepath.Base(p))
					filesOut = append(filesOut, p)
					langArtifact[lc] = p // 降级后的 .md 才是该语言真实交付物，保真比对应读它
					degraded = append(degraded, lc)
					artStage.emitOne(ArtifactKindMD, lc, p, false, true)
					continue
				}
				return &FileTranslateResult{Skill: "translation", Error: fmt.Sprintf("%s 译文写回失败（已自动重试3次，纯文案兜底亦生成失败）：%s", config.LangNames[lc], aerr.Error())}
			}
			if mdPaths[lc] == "" && ext == ".md" {
				// 阶段① 的先行写回失败过、这里重试成功：补发 md（同一路径 ⇒ 服务层收敛成一行）。
				appendUntranslatedNotes(lc, outPath, notes)
				mdPaths[lc] = outPath
				artStage.emitOne(ArtifactKindMD, lc, outPath, false, false)
			}
			filesOut = append(filesOut, outPath)
			langArtifact[lc] = outPath
			// ★ 两段式阶段②：原格式写回件即该语言主产物。
			artStage.emitOne(ArtifactKindOriginal, lc, outPath, true, false)
		}
	}

	// ★ 还原模式纯文案旁路产物（2026-09-13 双模式层次1）：还原成功时也逐语言生成 .md
	//   附加交付物——用户对还原不满意可直接取文案；降级语言的主产物已是 .md，不重复生成。
	// ★ 2026-10-30 组5：md 已在阶段①（md 先行）写好了，这一段改为**搬运已有路径**，
	//   不再重写（重写会重复追加「未译出内容」那一节，且与 dedupe 的事件流打架）。
	//   只有一种例外走补写：阶段① 的 md 失败过（磁盘/权限，极少见）⇒ 末尾补一次，
	//   别让"两段式"变成"少了那一段"。按路径去重后进 TextFiles（B6/B10 的去重腿）。
	var textOut []string
	if !deliveryText {
		seenText := map[string]bool{}
		for _, lc := range finalLangs {
			if len(langTranslations[lc]) == 0 || degradedHit(lc) {
				continue
			}
			p := mdPaths[lc]
			if p == "" {
				tr := deliveryTranslations(lc, langTranslations[lc])
				p2, perr := writeLangMD(lc, tr, untranslatedNotesFromSegments(texts, tr))
				if perr != nil {
					// ★ G18：旁路产物失败不判死，但必须进 GateWarnings（否则客户以为一直都有 .md 可读）。
					gateWarnings = append(gateWarnings, fmt.Sprintf("%s 纯文案旁路产物生成失败（不影响主交付）：%s", config.LangNames[lc], perr.Error()))
					observability.Warn(ctx, "纯文案旁路产物生成失败（不影响主交付）", "lang", lc, "error", perr.Error())
					continue
				}
				p = p2
			}
			if p == langArtifact[lc] || seenText[p] {
				continue // 同一份文件（.md 输入件）或已被别的语言引用：只登记一次
			}
			seenText[p] = true
			textOut = append(textOut, p)
		}
	}
	prog("第3步/3：完成", 3, 3)

	// ★ P1 整改（漏译与格式破坏静默通过）：产物落地后再做一次**结构指纹保真**比对。
	//   既有质量闸门全部只看「中文有没有变少」，没有任何一道看「格式有没有丢」，
	//   所以 RC-5（加粗标记成批消失）、表格降级、围栏被吞都能一路潜伏到交付物。
	//   警告并入 GateWarnings（与既有闸门同一口径进工单轨迹/审批台），只提示不判死——
	//   结构变化既可能是真丢失也可能是合法重排（PDF 版式重建），交由人工判断。
	fidelityWarnings := collectFidelityWarnings(filePath, ext, texts, langTranslations, langArtifact, finalLangs)
	if len(fidelityWarnings) > 0 {
		// ★ G18：append 与 warn 同块——「出声」与「记进工单」必须成对，
		//   否则日志里看得见、工单轨迹里查不到（反之亦然）。
		gateWarnings = append(gateWarnings, fidelityWarnings...)
		// 结构化日志（AGENTS 二：新代码走 observability，不加 log.Printf 存量）
		observability.Warn(ctx, "文件产物结构保真闸门发出警告（不判失败，明细见工单 gate_warnings）",
			"count", len(fidelityWarnings), "langs", strings.Join(finalLangs, ","), "file", filepath.Base(filePath))
	}

	// ★ 2026-10-25 修法 C（方案 §10.6-C）：**一个可用交付物都没有时不许报「✅ 完成」**。
	//   两条调用路径的零产物处置此前不一致：多文件路径在 service 侧把 `len(res.Files)==0` 判失败，
	//   而单文件路径只看 `res.Error`——于是「译文全缺/写回与纯文案兜底都失败」那一单会以
	//   「✅ 文件翻译完成：共 N 段文本，输出 0 个文件」的**成功脸**收尾（客户以为交付了）。
	//   判据收在引擎这一个出口：0 个主产物且 0 个纯文案旁路 ⇒ 明确失败并给出可执行文案。
	if fileNoDeliverables(filesOut, textOut) {
		// 失败也要把「为什么」带出去：未译段清单/闸门告警随 Data 一并返回，
		// 否则客户与审批台只看到一句失败、拿不到"缺哪几段"（与 E5 那条"残缺必须如实浮出"同一取向）。
		tp, tc := e.UsageTokens(ctx)
		return &FileTranslateResult{
			Skill: "translation",
			Error: fmt.Sprintf(
				"文件翻译未产出任何可用交付物（共 %d 段文本，目标语言 %s）：译文缺失或写回/纯文案兜底均失败，请稍后重新发起或联系支持",
				len(texts), strings.Join(finalLangs, "/")),
			Data: FileTranslateData{
				TotalTexts:           len(texts),
				TargetLangs:          finalLangs,
				LangNames:            langNames,
				KBHits:               kbHits,
				ModelHits:            modelHits,
				TMHits:               int(tmHitsCounter.Load()),
				Untranslated:         untranslated,
				UntranslatedSegments: untranslatedSegments,
				SourceSegments:       texts,
				GateWarnings:         gateWarnings,
				DegradedLangs:        degraded,
			},
			TokensUsed: tp + tc,
			PointsUsed: e.PointsOfTokens(e.UsageDisplayTokens(ctx)),
		}
	}
	reply := fileCompletionReply(len(texts), filesOut, finalLangs, untranslated, gateWarnings, degraded)
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
			TMHits:       int(tmHitsCounter.Load()), // 本单命中段数（只记数，见字段注释）
			Untranslated: untranslated,              // 语言→未译出段数（>0 时审批台可见）
			Translations: langTranslations,          // 原文→译文（不序列化），工单执行器回写 TM
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

// fileNoDeliverables 判定「这一单没有产出任何可用交付物」（★ 2026-10-25 修法 C 的唯一判据）。
//
// 主产物（原位写回/版式重建/纯文案主交付/图片/音频/字幕）与**纯文案旁路**（textOut）两边都空才算零交付；
// 只要有一边落了盘，客户就仍有可用的东西——那时按失败收尾会白扣一次失败并让客户重跑。
func fileNoDeliverables(filesOut, textOut []string) bool {
	return len(filesOut) == 0 && len(textOut) == 0
}

// fileCompletionReply 生成文件链的完成话术（唯一出口：改口径只改这里，也便于单测）。
//
// ★ 2026-10-25 修法 A：漏译提示里的数字是**段数总和**，不是「有漏译的语种个数」。
// 旧写法把 `len(parts)`（语种数）套进「有 %d 段」的句子：
//
//	· en 缺 3 段 + ja 缺 1 段 ⇒ 文案说「有 2 段」（真值 4 段）；
//	· 只缺一个语种时更离谱：en 缺 5 段 ⇒ 「有 1 段未能译出（en×5）」——同一句自相矛盾。
//
// 括号里保留分语种分解（en×3,ja×1），让客户/审批能同时看到"总量"与"缺在哪个语种"。
func fileCompletionReply(totalTexts int, filesOut, finalLangs []string, untranslated map[string]int, gateWarnings, degraded []string) string {
	reply := fmt.Sprintf("✅ 文件翻译完成：共 %d 段文本，输出 %d 个文件（%s）",
		totalTexts, len(filesOut), strings.Join(finalLangs, "/"))
	if len(untranslated) > 0 {
		parts := make([]string, 0, len(untranslated))
		total := 0
		for _, lc := range finalLangs {
			if n := untranslated[lc]; n > 0 {
				parts = append(parts, fmt.Sprintf("%s×%d", lc, n))
				total += n
			}
		}
		// 只有 0 值（map 里全是 0）时不出这句——否则会印「有 0 段未能译出（）」这种纯噪声。
		if total > 0 {
			reply += fmt.Sprintf("；⚠️ 有 %d 段未能译出已保留原文（%s），具体段落见返回的 untranslated_segments，请人工补译",
				total, strings.Join(parts, ","))
		}
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
	return reply
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

// reviewBatchSafe 批量审校安全包装：把这一批交给**校对模型**过一遍；
// 审校结果不改变成功/失败判定（审校输出为空时保留原译文）。
//
// ★ 2026-10-27（工单 T20261027152356CMY 复盘）：**空槽必须一起送审校**。
// 旧形态 `if tr == "" || tr == "[翻译失败]" { continue }` 把空槽从清单里剔掉，
// 于是"初翻模型没吐出来的那几行"在校对腿上**物理不存在**——而校对腿恰恰是链路上
// 唯一一个可能由**另一族模型**（初翻 Hy、校对 GPT）经手的环节。
// 现网实证（图片工单 136，`已实现=加权成本重放；浮动=市值-成本×数量；30s刷新`）：
// 初翻 Hy 在 `<sN>` 批量形态下对这一槽吐空 → 校对腿跳过它 → 硬闸 3 轮 + 逐行补译 3 轮
// 仍然全是 Hy 在打同一句话 → 最终按原文回落并在交付文档里点名。
// 而同一句话走**单条腿**（试用接口实测）Hy 能给出
// "Realized = Weighted Cost Replay; Floating = Market Value - Cost × Quantity; Refresh every 30s"。
// ⇒ 结论：不是"这行译不出来"，是"译得出来的那个模型从未被问到"。
// 代价口径：只多送**空槽**那几条给校对模型（已译出的行仍照旧送，审校语义不变），
// 换来的是一条"初翻漏槽"的确定性救回通道——比后面 8 轮同模型重拨便宜得多。
func (e *Engine) reviewBatchSafe(ctx context.Context, sources, translations []string, lang string, onDone func(done, total int)) []string {
	idxs := make([]int, 0, len(sources))
	var srcs, tgts []string
	emptyN := 0
	for i, tr := range translations {
		if i >= len(sources) {
			break // 两张表不等长时只处理有源文的那部分（下标越界会 panic，而这是非关键路径）
		}
		if tr == translationFailureMark {
			// 失败占位在清单里按"未译出"呈现：**绝不**把 `[翻译失败]` 这串字面量喂给审校模型
			// （它会当成一条真译文去"润色"，输出一句带着占位符的垃圾）。
			tr = ""
		}
		if strings.TrimSpace(tr) == "" {
			emptyN++
		}
		idxs = append(idxs, i)
		srcs = append(srcs, sources[i])
		tgts = append(tgts, tr)
	}
	// 单段且本来就有译文 ⇒ 走常规逐段审校收益低，跳过（旧口径不变）。
	// ★ 但只要存在空槽，哪怕整批只有 1 段也必须送——那是这一行唯一的换模型机会。
	if len(idxs) < 2 && emptyN == 0 {
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

// fileRepairMaxRounds 5% 触发后的追加补译轮数（env > system_config > 默认 3）。
func (e *Engine) fileRepairMaxRounds() int {
	if v := strings.TrimSpace(os.Getenv("FILE_REPAIR_MAX_ROUNDS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if e != nil && e.St != nil {
		if v, _ := e.St.GetConfig("file_repair_max_rounds"); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				return n
			}
		}
	}
	return 3
}

// fileRepairMaxCalls 单文件补译的最大 LLM 调用次数（防止 5% 阈值把成本放大；
// env > system_config > 默认 40）。
func (e *Engine) fileRepairMaxCalls() int {
	if v := strings.TrimSpace(os.Getenv("FILE_HARDGATE_MAX_CALLS")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if e != nil && e.St != nil {
		if v, _ := e.St.GetConfig("file_hardgate_max_calls"); strings.TrimSpace(v) != "" {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				return n
			}
		}
	}
	return 40
}

// writeMultiSheetXlsx xlsx 多 Sheet 模式：复制原始文件后，每个目标语言新增一个 Sheet，
// 遍历该语言译文逐格替换。Sheet 名=语言代码。
func writeMultiSheetXlsx(srcPath, outputDir, baseName string, langs []string, langTranslations map[string]map[string]string) error {
	outPath := filepath.Join(outputDir, baseName+"-multi.xlsx")
	f, err := excelize.OpenFile(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, lc := range langs {
		tr := deliveryTranslations(lc, langTranslations[lc])
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
