// ============ dispatch_chunks.go · 工单块级派发编排（engine 侧） ============
// 2026-11-26，方案 §四·C-8：把"决策→算边界→取 Key→派发→回执计费"收在一处。
//
// 硬约定：决策只调用一次并整单锁定；Key 只从 llmsource.Current 取（唯一读点），
// 随 payload 传给广州 worker，用完即弃、不落盘。
// =============================================
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/llmsource"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/store"
)

// DispatchChunkOutcome 派发单的编排结果（供 HandleFile 决定是否继续拼回写回）。
type DispatchChunkOutcome struct {
	Dispatched bool
	Reason     string
	Result     fileproc.ChunkRunResult
}

// TryDispatchChunks 工单开工时调用一次：动态决策 + 派发整单。
//
// 参数：
//   localAvailPct 由调用方注入的主站可用内存比例
//   rc 广州机动态读数（由调用方从 probe 现读现填）
//   st/cfg 供 llmsource.Current 取当前生效 Key（唯一读点）
func (e *Engine) TryDispatchChunks(ctx context.Context, st *store.Store, cfg *config.Config,
	localAvailPct float64, rc fileproc.RemoteCapacitySnapshot,
	inputs []string, pages int, payloadLen int64, lang string,
	sessionID, inputPDF, zipPath, textsPath string, terms map[string]string) (DispatchChunkOutcome, error) {

	var out DispatchChunkOutcome
	slots := fileproc.RemoteSlotsFree()
	decision := fileproc.DecideTicketDispatch(localAvailPct, inputs, pages, payloadLen, slots, rc.ToInternal())
	out.Reason = decision.Reason
	fileproc.NotePolicyReason(decision.Reason) // 健康面只出状态词
	if !decision.Dispatch {
		return out, nil // 非派发单：主站整单做完，不切广州
	}
	out.Dispatched = true

	// 从唯一读点取当前生效配置（Key 只在内存流转，绝不写日志/文件）
	snap := llmsource.Current(ctx, st, cfg)
	if snap == nil || snap.OnlineAPIKey == "" {
		return out, fmt.Errorf("llmsource 当前快照无可用 Key（source=%s）", snap.From)
	}
	observability.Info(ctx, "工单块级派发开始", "reason", decision.Reason, "lang", lang, "source", snap.From)

	// 算块边界：复用 adaptiveBatchSize（40 段 / 6000 token），主站唯一尺子
	texts, err := fileproc.ExtractTextsPdfOverlay(ctx, inputPDF)
	if err != nil || len(texts) == 0 {
		return out, fmt.Errorf("主站提取失败，无法算块边界: %w", err)
	}
	batch := adaptiveBatchSize(texts, 40, 6000)
	bounds := make([][2]int, 0, (len(texts)+batch-1)/batch)
	for lo := 0; lo < len(texts); lo += batch {
		hi := lo + batch
		if hi > len(texts) {
			hi = len(texts)
		}
		bounds = append(bounds, [2]int{lo, hi})
	}

	// 产物路径由调用方传入；空则按统一口径推导（与 artifactOutputDir 同一把尺子）
	if zipPath == "" || textsPath == "" {
		outDir := artifactOutputDir(inputPDF)
		sid := fileproc.SessionIDFor(inputPDF)
		if zipPath == "" {
			zipPath = filepath.Join(outDir, sid+".chunks.zip")
		}
		if textsPath == "" {
			textsPath = filepath.Join(outDir, sid+".texts.json")
		}
	}
	if err := os.MkdirAll(filepath.Dir(zipPath), 0755); err != nil {
		return out, fmt.Errorf("建产物目录失败: %w", err)
	}
	res, err := fileproc.DispatchChunkRun(ctx, fileproc.ChunkRunRequest{
		SessionID: sessionID,
		InputPDF:  inputPDF,
		Lang:      lang,
		Bounds:    bounds,
		Key:       snap.OnlineAPIKey,
		Base:      snap.OnlineAPIBase,
		Model:     snap.OnlineModel,
		Terms:     terms,
		OutputZip: zipPath,
		TextsOut:  textsPath,
	})
	if err != nil {
		return out, err // 上层按既定降级链处理（排队重试 / 超限接手）
	}
	out.Result = res
	// E-3：把远端 tokens_used 写回主站 usage 收集器 ⇒ PointsOfTokens/UsageDisplayTokens 能读到，
	// 工单 points_billed 才反映派发单真实用量（口径 3：主站 Key 主站计费）。
	if uc := llm.CollectorFrom(ctx); uc != nil && res.TokensUsed > 0 {
		uc.AddBilled(res.TokensUsed)
	}
	observability.Info(ctx, "工单块级派发回执", "chunks", res.ChunkCount, "tokens", res.TokensUsed)
	return out, nil
}

// MaybeDispatchTicket 在 HandleFile 入口调用一次：按两侧资源决定是否整单派发。
//
// 返回 (result, handled)：
//   handled=false → 调用方走原有主站链路（非派发单，逐字不变）
//   handled=true  → 整单已在广州做完，result 可直接返回；派发失败也 handled=true，
//                   但 result.Error 非空，调用方不得再回主站做一半（整单锁定）。
//
// 只接 PDF 还原模式：其它格式与非 PDF 本就不在派发白名单内（DispatchEligible 判）。
func (e *Engine) MaybeDispatchTicket(ctx context.Context, filePath string,
	options map[string]interface{}) (*FileTranslateResult, bool) {

	if !fileproc.DispatchEnabled() {
		return nil, false
	}
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext != ".pdf" {
		return nil, false
	}
	// 页面数：与上传校验同一把尺子（PdfPageCount），流式为大件不整读
	pages, err := fileproc.PdfPageCount(filePath)
	if err != nil || pages < 0 {
		pages = 0
	}
	fi, statErr := os.Stat(filePath)
	if statErr != nil {
		return nil, false
	}
	rc := fileproc.RemoteCapacityNow()
	// 口径 13：cpu.stat 采样窗 200ms；读不到 -1 ⇒ 规则 6b 不派（fail-closed）
	rc.CPUQuotaUsed = fileproc.SampleFPDSliceCPUQuota()
	// 决策 + 派发（Key 走 llmsource.Current 唯一读点）
	out, err := e.TryDispatchChunks(ctx, e.St, e.Cfg, fileproc.LocalAvailPct(), rc,
		[]string{filePath}, pages, fi.Size(),
		firstLang(options), fileproc.SessionIDFor(filePath), filePath, "", "", nil)
	if err != nil || !out.Dispatched {
		return nil, false // 判 local 或派发失败 ⇒ 主站整单做完（不混合）
	}

	// 构造工单结果：派发单的译文映射来自远端回执，产物由主站落库时另按 outputDir 命名。
	// 这里只返回⭐翻译数据与用量；Files 由上层工单执行器按 zip 落点写回（见 result.FileContext）。
	r := &FileTranslateResult{
		Skill:      "translation",
		Reply:      "已由派发机完成块级翻译",
		TokensUsed: out.Result.TokensUsed,
		PointsUsed: e.PointsOfTokens(e.UsageDisplayTokens(ctx)),
		Data: FileTranslateData{
			TargetLangs:  []string{firstLang(options)},
			Translations: map[string]map[string]string{firstLang(options): out.Result.Translations},
			SourceSegments: out.Result.Texts,
		},
	}
	return r, true
}

// firstLang 从 options 取首个目标语言（派发编排只需要一个代表语言；完整多语由上层循环）。
func firstLang(options map[string]interface{}) string {
	langs := TargetLangsFromOptions(options)
	if len(langs) > 0 {
		return langs[0]
	}
	return "en"
}
