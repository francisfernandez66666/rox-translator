// ============ chat_image.go · 职责说明 ============
// 即时翻译（聊天窗）的**图片翻译**能力（★ 2026-10-17 用户令推翻 #36 旧决策）：
//
//	POST /api/chat/image —— 上传一张图片 → OCR 识别文字 → 走既有翻译链路 → 回传
//	                        ① 各目标语译文文本 ② 在**原图上按原坐标回填译文**的译后图。
//
// ★ 与 chat_audio.go 的口径差异（一句话）：语音两件套**不做翻译**（上游适配器），
//
//	本端点**要做翻译**——因为用户要的产出就是"译好的图"。因此它必须完整走
//	配额闸门（gateUsage）→ 翻译引擎（Engine.HandleText）→ 计费与奖励，与 /api/chat 同权同责，
//	绝不能像 asr/tts 那样绕开计费（那会把"上传图片免费翻译"变成一条薅额度的口子）。
//
// ★ 能力不可用 = 这一次没有产出，不是故障：OCR 依赖/权重缺失时返回 HTTP 200 + {error, reason}，
//
//	前端据此隐藏入口（voiceCapability 的 ocr 门）并给一句本地化提示，主翻译功能零影响。
//
// ★ 译文第一：**译后图是择优产出**。OCR 成功、翻译成功，但原位回填失败（缺中文字体/坐标没缓存/
//
//	行数对不上）时，只把译后图这一件降级（image_error 字段如实说明），译文文本照常返回——
//	与 fileproc/ocr_image.go 头注同一条纪律。
//
// ★ 行↔译文对齐（原位回填的关键）：ocr_translate_image.py 按"框里的原文"取译文（tr[原文行]），
//
//	所以这里要给出「原文行 → 该行译文」的映射。做法是先整段翻一次（与 /api/chat 同构、只发一枪），
//	译文按 \n 拆行；行数与 OCR 行数**严格相等**才配对回填。
//
// ★ 多行分腿（2026-10-19 修）：整段一枪是**快路径**，但部分模型（现网 tencent/Hunyuan-MT-7B）
//
//	在多行输入上会丢行（实测 4 行稳定只回 1 行 "and"、2 行偶发丢首行）。因此行数对不上的那个
//	目标语种**分腿重译**：按行二分，每一腿仍要求"返回行数 == 输入行数"，不满足就再拆，
//	直到单行（单行是终点）。只对坏掉的语种付这份钱——行数已对齐的语种一次额外调用都不发。
//	分腿后仍对不上（单行也回空/多行）才宁可不画（保留原文，image_error=line_mismatch），
//	与 ocr.go 的 parseOcrJSON 同一条"数量不等就不配对"纪律；**绝不拿猜的行去盖框**。
//	分腿产生的真实用量照常计费（每腿都经 Engine.HandleText），并累加进 points_used。
//
// =============================================
package api

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"translator/internal/billing"
	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/fileproc"
	"translator/internal/llm"
	"translator/internal/observability"
	"translator/internal/tenant"
)

// chatImageMaxBytes 聊天窗上传图片的上限（20MiB）。
// 取值依据：即时翻译的图片是"截图/拍照"级语料；20MiB 够放 4K 截图与手机原图，
// 又挡得住"误传一张几百 MB 的扫描件把 OCR 与内存打满"。
const chatImageMaxBytes = 20 << 20

// chatImageResp /api/chat/image 的成功出参。
type chatImageResp struct {
	Text         string            `json:"text"`         // OCR 识别出的原文（多行，\n 分隔）
	Translations map[string]string `json:"translations"` // 目标语种 → 译文（整段）
	ImageLang    string            `json:"image_lang"`   // 译后图采用的目标语种（首个）
	Image        string            `json:"image"`        // 译后图 data URL（PNG）；降级时为空串
	ImageError   string            `json:"image_error"`  // 译后图降级原因（空＝正常产出）
	LineCount    int               `json:"line_count"`   // OCR 行数（前端可据此提示"识别到 N 行"）
	PointsUsed   int64             `json:"points_used"`  // 本次消耗积分（与 /api/chat 同口径）
	Mode         string            `json:"mode"`         // 命中模式描述（前端模式徽标吃这个字段）
	TargetLangs  []string          `json:"target_langs"` // 全部目标语种
}

// imageReason 把 fileproc 抛出的 OCR 错误归到前端可用的档位（与 asrReason 同族）。
// 档位名单：capability_missing（这台机器没这项能力）/ no_text（图里没识别出字）/ failed（其余）。
// ★ 只问 fileproc 自己那句话的开头（同包内单源），不在前端按字符串猜。
func imageReason(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "不可用") || strings.Contains(msg, "脚本缺失") ||
		strings.Contains(msg, "模型资产") {
		return "capability_missing"
	}
	if strings.Contains(msg, "未识别出文字") {
		return "no_text"
	}
	return "failed"
}

// handleChatImage POST /api/chat/image：图片 → OCR → 翻译 → 译后图＋译文。
//
// 入参（multipart/form-data）：
//
//	file         必填，图片（png/jpg/jpeg/bmp/tif/tiff/webp/heic）
//	target_langs 必填，逗号分隔的目标语种（首个用于译后图）
//	lang         可选，界面语言（决定提示词中/英那一套与作答语言）
//	mode         可选，"fast"/"pro"；缺省按专业档（与 /api/chat 同口径）
func (s *Server) handleChatImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	// ★ 与 /api/chat 同口径要求登录（免登录试用只有文本一条腿，用户令：试用态不开放多模态）。
	if s.authUser(r) == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录或登录已过期"))
		return
	}
	// ★ 能力闸在最前：这台机器没有 OCR（缺依赖/权重）时连解析请求都不做，
	//   直接回一档"这一次没有产出"，前端按 reason 隐藏入口并提示。
	if !fileproc.OcrAvailable() {
		writeJSON(w, 200, chatVoiceFailure{
			Error:  "图片文字识别（OCR）不可用：服务器未部署 OCR 依赖或模型资产",
			Reason: "capability_missing",
		})
		return
	}
	if ct := r.Header.Get("Content-Type"); !strings.Contains(ct, "multipart/") {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请以 multipart/form-data 提交图片"))
		return
	}
	if err := r.ParseMultipartForm(chatImageMaxBytes); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "图片数据格式无法解析（或超出上限）"))
		return
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		f, hdr, err = r.FormFile("image")
	}
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少图片文件"))
		return
	}
	defer f.Close()

	targets := parseChatImageLangs(r.FormValue("target_langs"))
	if len(targets) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少目标语种"))
		return
	}

	ext := ".png"
	if v := strings.ToLower(filepath.Ext(hdr.Filename)); v != "" && fileproc.IsImageFormat(v) {
		ext = v
	}
	// 落盘到临时文件：OCR 脚本与回填脚本都要**文件路径**（不是字节流）。
	tmpPath, cleanup, terr := stageChatImage(f, ext)
	if terr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "图片暂存失败"))
		return
	}
	defer cleanup()

	// ★ 2026-10-30 组4（C-1／P-2／G10）：免费档判定——**尺寸先定档**（第 0 步），再判身份。
	//   · 免费档 ≤ free_image_max_mb（默认 1 MiB）：不扣积分；
	//   · 付费即时档 ≤ image_max_mb（默认 3 MiB）：按量计费（客户自己压不到 1 MiB 时给的选择）；
	//   · 超硬顶：不在这里硬扛，出 use_ticket 引导去翻译工单（上限 40 MB，还得还原原格式）。
	//   体积判据吃 multipart 头里的真实字节数（hdr.Size），不靠 Content-Length 自报。
	imgTid := s.currentTenant(r)
	imgUID := int64(0)
	if u := s.authUser(r); u != nil {
		imgUID = u.ID
	}
	imgRoute := s.instantDecide(r, imgTid, imgUID, instantInput{Kind: "image", SizeBytes: hdr.Size, LangCount: len(targets)})
	if imgRoute.SuggestTicket {
		writeJSON(w, 200, map[string]interface{}{
			"error":   "图片超出即时翻译上限（3 MB）。交给翻译工单：上限 40 MB，译完还原原格式",
			"reason":  imgRoute.Reason,
			"suggest": "ticket",
			"file":    map[string]interface{}{"name": hdr.Filename, "size": hdr.Size},
		})
		return
	}
	// 配额闸门：图片翻译是一次真实算力调用（OCR + LLM）。免费档走免费闸（跳过余额与预算墙），
	// 其余仍走原闸门（余额/预算墙照旧）。
	var tid int64
	var release func()
	var gateErr error
	if imgRoute.Free {
		tid, release, gateErr = s.gateUsageInstant(r)
	} else {
		tid, release, gateErr = s.gateUsage(r)
	}
	defer release()
	if gateErr != nil {
		s.writeGateError(w, r, gateErr)
		return
	}
	_ = imgRoute

	// ★ 2026-10-26 修「即时翻译图片上传像失败」的真因：本链路 OCR＋整段翻译＋分腿对齐
	//   在现网实测 125 s（生产日志 trace 87c3727a…：duration_ms=125157），而跨境边缘
	//   （Cloudflare 约 100 s 空闲/首字节上限）会先把这一跳掐成 524 HTML，客户端只看到失败。
	//   下面在过闸之后立即写出响应头，并周期写空白字节保活；空白是合法 JSON 前导字符，
	//   最终 JSON 仍能被前端 JSON.parse 正常解析（不改成 SSE，前端零改动）。
	stopBeat := startImageKeepAlive(w, 10*time.Second)
	// 所有后续出口都必须先停心跳再写 JSON：同一 ResponseWriter 上的并发写会把 JSON 撕开。
	writeOut := func(v interface{}) {
		stopBeat()
		writeJSON(w, http.StatusOK, v)
	}
	defer stopBeat()

	uiLang := strings.TrimSpace(r.FormValue("lang"))
	mode := engine.ModeFromOptions(map[string]interface{}{"mode": r.FormValue("mode")})
	options := map[string]interface{}{
		"target_langs": toIfaceSlice(targets),
		"mode":         mode,
	}
	if uiLang != "" {
		options["lang"] = uiLang
	}
	// ★ 2026-10-28：ctx 提前构造——OCR 的 AI 判定补偿（ocr_fix）要在提取之后、翻译之前跑，
	//   而它必须带用量收集器（否则这条腿的 token 不计费，预估与实扣又变成两把尺子）。
	//   WithUsageRecorder 是幂等的：随后 HandleText 内部再注入会复用这一只（F-49②）。
	ctx := llm.WithInteractive(tenant.WithLang(tenant.WithMode(s.userOrgCtx(r), mode), uiLang))
	// ★ 组4：免费档标记必须装在**真正传进引擎的那一份 ctx** 上（ChargeUsageRealtime 从这里读）。
	if imgRoute.Free {
		ctx = instantMarkCtx(ctx)
	}
	ctx = s.Engine.WithUsageRecorder(ctx)

	// OCR：识别行文本（同时把「行↔坐标」写进 P4 回填缓存，供 RenderTranslatedImage 取用）。
	lines, oerr := fileproc.ExtractTexts(tmpPath)
	if oerr != nil {
		// ★ OCR 出错＝"这一次没识别出来"，不是请求处理出错：HTTP 200 + {error, reason}，
		//   不碰任何翻译状态（口径同 chat_audio.go「译文第一」）。
		writeOut(chatVoiceFailure{Error: oerr.Error(), Reason: imageReason(oerr)})
		return
	}
	if len(lines) == 0 {
		writeOut(chatVoiceFailure{Error: "图片里没有可翻译的文字", Reason: "no_text"})
		return
	}

	// ★ 2026-10-28（主方案 §9）：② 清洗已在 ExtractTexts 内完成；这里跑 ③ 并重建 ④ 映射。
	//   fail-open：③ 不可用时全部按原识别交付（绝不因 AI 掉线丢内容）。
	if doc, ok := fileproc.PendingOcrDoc(tmpPath); ok {
		s.Engine.ReconcileOcrDoc(ctx, doc)
		if t2, cst := fileproc.CommitOcrDoc(tmpPath, doc, lines); !cst.Stale {
			lines = t2
		}
		defer fileproc.ReleaseOcrDoc(tmpPath)
		// 既有小缺口同批收口：这条链路此前从没释放过坐标缓存的在途钉。
		defer fileproc.ReleaseImageBoxes(tmpPath)
	}

	// ★ 整段一次翻（与 /api/chat 同构、只发一枪），避免逐行多次调用把即时翻译拖慢。
	//   注意这里**保留换行**：joined 里的 \n 是"行↔译文"对齐的唯一线索。
	//   行数对不上的语种随后由 chatImageAlignTranslations 分腿重译（见文件头注）。
	joined := strings.Join(lines, "\n")
	res := s.Engine.HandleText(ctx, joined, options, nil)

	resp := chatImageResp{
		Text:        joined,
		LineCount:   len(lines),
		TargetLangs: targets,
		ImageLang:   targets[0],
	}
	if res.Error != "" {
		// 翻译失败：与 /api/chat 同口径，但本端点主产出是图，这里如实回错误体（HTTP 200 带 error 字段，
		// 因为前端已在界面上，走统一错误体反而要多判一条分支）。
		resp.Translations = map[string]string{}
		resp.ImageError = "translate_failed"
		s.metrics.countTranslate("text", false)
		billing.Flush()
		writeOut(resp)
		return
	}
	s.metrics.countTranslate("text", true)

	// ★ 多行分腿：整段结果与 OCR 行数对不上的语种重译到逐行对齐（快路径失败才付这份钱）。
	//   放在 webhook 之前：外部系统拿到的译文必须与响应/回填图是同一份。
	aligned, splitLangs, legPoints, legErr := s.chatImageAlignTranslations(ctx, options, lines, targets, res.Data.Translations)
	if len(splitLangs) > 0 {
		observability.Info(ctx, "chat_image_leg_split",
			"langs", strings.Join(splitLangs, ","), "lines", len(lines), "points", legPoints)
	}
	if legErr != nil {
		// 分腿仍对不齐＝该语种这一次出不了图（renderChatImage 会如实回 line_mismatch），
		// 但**译文文本照常返回**（分腿成功的语种不受影响）。
		observability.Warn(ctx, "chat_image_leg_split_failed", "err", legErr.Error())
	}
	res.Data.Translations = aligned
	res.PointsUsed += legPoints

	s.grantTranslateTask(r, tid)
	s.dispatchTranslateWebhook(tid, "text", joined, res)

	resp.Translations = res.Data.Translations
	resp.Mode = res.Data.Mode
	resp.PointsUsed = res.PointsUsed

	// 译后图：只为**首个目标语种**出图（多目标语种各出一张会让接口语义与体积都失控；
	// 其余语种的译文走 translations 文本，用户要图可对单语种再来一次）。
	renderChatImage(r, tmpPath, targets[0], lines, res.Data.Translations[targets[0]], &resp)

	// ★ 同步冲刷计量缓冲，余额/台账即时可见（与 /api/chat 的 P3 修复同口径）。
	billing.Flush()
	writeOut(resp)
}

// startImageKeepAlive 图片翻译的「长请求保活」：写出 200 响应头后周期追加空白字节。
//
// 为什么需要：这条链是 OCR ＋ 整段翻译 ＋（坏语种）分腿对齐，现网实测单次 125 s；
// 跨境边缘（Cloudflare）约 100 s 无字节就回 524 HTML，用户侧表现成"上传图片没反应/失败"。
// 空白（\n）是合法 JSON 前导字符，最终 JSON 仍是合法响应体，前端 JSON.parse 零改动。
//
// 约束：返回的 stop 必须在写最终 JSON **之前**调用（它会等心跳 goroutine 退出），
// 否则同一 ResponseWriter 上的并发写会把 JSON 撕开。可重复调用（第二次是 no-op）。
func startImageKeepAlive(w http.ResponseWriter, interval time.Duration) (stop func()) {
	if interval <= 0 {
		interval = 10 * time.Second
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// 让 Caddy/nginx 这类反代不要在整包完成前缓冲，否则心跳到了代理就被扣住。
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if flusher != nil {
		flusher.Flush()
	}
	var mu sync.Mutex
	done := make(chan struct{})
	var once sync.Once
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				mu.Lock()
				fmt.Fprint(w, "\n")
				if flusher != nil {
					flusher.Flush()
				}
				mu.Unlock()
			case <-done:
				return
			}
		}
	}()
	stop = func() {
		once.Do(func() {
			close(done)
			wg.Wait()
			mu.Lock() // 与最后可能正在写的心跳字节形成先后关系
			mu.Unlock()
		})
	}
	return stop
}

// stageChatImage 把上传的图片字节落盘到临时文件，返回路径与清理函数。
//
// ★ 句柄顺序是这条链唯一的硬约束：必须**先 io.Copy 写完、再 Close**。
// 对已关闭的 *os.File 写入必退 "file already closed"，而那条错误分支是**每次都走到**的
// （2026-10-19 修复：此前 Close 排在 Copy 之前，图片即时翻译从上线起恒 500——这是
// "有锁而射程不足"的典型：老用例从不喂真件，那段写盘代码在 CI 里从没执行过）。
// 形态与 chat_audio.go 同款；失败分支也要 Close，防句柄泄漏。
//
// ★ 限流 COPY 一字不动：MaxBytesReader/ParseMultipartForm 管的是请求体，COPY 阶段不加这道墙，
// 一个伪造的 Content-Length 就能把磁盘写满（与 chat_audio.go 同款教训）。
func stageChatImage(f io.Reader, ext string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "chat-image-*"+ext)
	if err != nil {
		return "", nil, err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, cerr := io.Copy(tmp, io.LimitReader(f, chatImageMaxBytes)); cerr != nil {
		tmp.Close()
		cleanup()
		return "", nil, cerr
	}
	if cerr := tmp.Close(); cerr != nil {
		cleanup()
		return "", nil, cerr
	}
	return tmpPath, cleanup, nil
}

// renderChatImage 生成译后图并把 data URL（或降级原因）写进 resp。
// 参数：r＝请求（取 ctx）；srcPath＝原图；lang＝目标语种；lines＝OCR 行；
// translated＝该语种整段译文；resp＝出参（就地填 Image/ImageError）。
//
// ★ 一切失败都只降级译后图这一件，绝不返回错误把整单判死（译文文本已在 resp 里）。
func renderChatImage(r *http.Request, srcPath, lang string, lines []string, translated string, resp *chatImageResp) {
	tr, ok := chatImageLinePairs(lines, translated)
	if !ok {
		// 模型没保持行结构（且分腿也没救回来）：宁可不画也不把译文盖到错误的框上（保留原文更安全）。
		if strings.TrimSpace(translated) == "" {
			resp.ImageError = "no_translation"
		} else {
			resp.ImageError = "line_mismatch"
		}
		return
	}
	outPath := srcPath + ".translated.png"
	defer os.Remove(outPath)
	if _, err := fileproc.RenderTranslatedImage(r.Context(), srcPath, outPath, engine.RenderSafeTranslations(tr)); err != nil {
		// 原位回填失败（缺中文字体/坐标没缓存住/绘制超时）只跳过该产物，译文照常在。
		resp.ImageError = "render_failed"
		return
	}
	data, rerr := os.ReadFile(outPath)
	if rerr != nil || len(data) == 0 {
		resp.ImageError = "render_failed"
		return
	}
	resp.Image = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	_ = lang // 语种只用于排障日志，当前不再加入出参（ImageLang 已表达）
}

// chatImageLinesOf 把一段译文按行切开。
// ★ 行数判据的唯一实现：对齐（chatImageLinePairs）、分腿（chatImageLegs）、快路径判定
// 都必须吃这一个函数——三处各写一遍切法，迟早出现"这里算 4 行、那里算 3 行"。
func chatImageLinesOf(s string) []string {
	return strings.Split(strings.TrimSpace(s), "\n")
}

// chatImageLinePairs 把「OCR 行 → 译文行」按**位置**配对。
// ok=false 表示行数对不上或整段译文为空——调用方必须放弃绘图，不能猜（猜＝把译文盖到错误的框上）。
func chatImageLinePairs(lines []string, translated string) (map[string]string, bool) {
	translated = strings.TrimSpace(translated)
	if translated == "" {
		return nil, false
	}
	trLines := chatImageLinesOf(translated)
	if len(trLines) != len(lines) {
		return nil, false
	}
	tr := make(map[string]string, len(lines))
	for i, ln := range lines {
		tr[ln] = strings.TrimSpace(trLines[i])
	}
	return tr, true
}

// chatImageLegMaxCalls **单次请求**分腿的调用上限（成本/延迟闸，多语种共用这一份）。
// 命中即放弃该语种的出图（如实回 line_mismatch，其余语种的译文照常返回）——绝不把一次
// "即时翻译"拖成几十枪（主站 LLM_CHAT_CONCURRENT=1，串行）。
// 24 枪足够修复常见的 ≤12 行整段丢行；再大的图请改用文件翻译入口。
const chatImageLegMaxCalls = 24

// chatImageLegTranslate 分腿翻译一枪（入参是被 \n 拼成一段的若干 OCR 行）。
type chatImageLegTranslate func(text string) (string, int64, error)

// chatImageLegs 把一段**行数对不上**的译文重译到逐行对齐。
//
// 算法：整段先翻一枪；返回行数 == 输入行数就采用，否则按行二分（前半/后半各自递归），
// 直到单行。单行是终点（再拆没有意义）——单行仍回空或多行即判失败，由调用方降级（不出图）。
//
// 为什么二分而不是逐行：整段一枪是快路径；只有模型丢行的语种才付这份钱。二分让「局部坏」
// 最多付 O(k log n) 次而不是 N 次（95 行图逐行 = 95 次往返，主站 LLM_CHAT_CONCURRENT=1 下不可接受）。
// ★ 判据只有一条：**每一腿返回的行数必须等于该腿输入的行数**。不依赖模型自觉——
// 现网 Hunyuan-MT-7B 在 4 行输入上稳定只回 1 行 "and"，且质检把它标成"长度合理"。
//
// 空行不送模型（原文空行不需要翻译），但输出必须与 lines 等长（下标一一对应）。
func chatImageLegs(lines []string, translate chatImageLegTranslate) ([]string, int64, error) {
	budget := chatImageLegMaxCalls
	return chatImageLegsWithin(lines, translate, &budget)
}

// chatImageLegsWithin 同 chatImageLegs，但调用预算由调用方持有——多语种请求共用一份，
// 否则 N 个坏语种会各花 N×24 枪（上限按"每张图"成立才算真的封住了延迟）。
func chatImageLegsWithin(lines []string, translate chatImageLegTranslate, budget *int) ([]string, int64, error) {
	out := make([]string, len(lines))
	var points int64
	idx := make([]int, 0, len(lines))
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		// ★ 2026-10-21 UAT 收口 M9：非语言行（数字/金额/日期/符号/箭头）**根本不送模型**，
		//   直接由 Go 侧回填原文。判据与文件主链同一个入口（engine.NeedsIdentityDelivery），
		//   两处各写一份"什么算符号"正是本仓反复出事的形态。
		//   不这么做的话，分腿重译会把 `<` 也拨一枪，模型换回 "and" 后行数照样对齐 ⇒
		//   回填门拿到 text != 原文，图标就被一个英文单词盖掉。
		if engine.NeedsIdentityDelivery(ln) {
			out[i] = strings.TrimSpace(ln)
			continue
		}
		idx = append(idx, i)
	}
	var solve func([]int) error
	solve = func(idxs []int) error {
		if len(idxs) == 0 {
			return nil
		}
		if *budget <= 0 {
			return fmt.Errorf("分腿重译超过上限（%d 次）", chatImageLegMaxCalls)
		}
		*budget--
		parts := make([]string, len(idxs))
		for k, i := range idxs {
			parts[k] = lines[i]
		}
		text, p, err := translate(strings.Join(parts, "\n"))
		points += p
		if err != nil {
			return err
		}
		tr := chatImageLinesOf(text)
		usable := len(tr) == len(idxs)
		if usable {
			for k := range idxs {
				if strings.TrimSpace(tr[k]) == "" {
					usable = false // 非空原文行译成空行＝这一腿有行没给出译文，继续拆
					break
				}
			}
		}
		if usable {
			for k, i := range idxs {
				out[i] = strings.TrimSpace(tr[k])
			}
			return nil
		}
		if len(idxs) == 1 {
			return fmt.Errorf("分腿译文不可用（输入 1 行，返回 %d 行）", len(tr))
		}
		mid := len(idxs) / 2
		if err := solve(idxs[:mid]); err != nil {
			return err
		}
		return solve(idxs[mid:])
	}
	if err := solve(idx); err != nil {
		return out, points, err
	}
	return out, points, nil
}

// pinChatImageIdentityLines 把「非语言行」（数字/金额/日期/符号/箭头）的译位硬钉回原文。
//
// ★ 2026-10-21 UAT 收口 M9：快路径（整段那一枪就对齐了行数）**不等于**每一行都对——
// 模型完全可能把 `<` 译成 "and"、把 `⇅` 译成一句话，而行数照样等于 len(lines)，
// 于是「已对齐」这一档一路放行，英文单词盖住图标。三条口径：
//
//	① 判据与文件主链同一个入口（engine.NeedsIdentityDelivery），不许在这里另写一份"什么算符号"；
//	② 只改这些位置——模型对无字母行的输出只可能是噪声，原文才是唯一正确交付；
//	③ 行数对不上时**原样返回**：对齐本身由 chatImageLinePairs 判（它才是行数契约的守卫），
//	   这里不替它做决定，免得把"对不上"悄悄修成"看起来对上了"。
func pinChatImageIdentityLines(lines []string, translated string) string {
	tr := chatImageLinesOf(translated)
	if len(tr) != len(lines) {
		return translated
	}
	changed := false
	for i, ln := range lines {
		if !engine.NeedsIdentityDelivery(ln) {
			continue
		}
		want := strings.TrimSpace(ln)
		if tr[i] != want {
			tr[i] = want
			changed = true
		}
	}
	if !changed {
		return translated
	}
	return strings.Join(tr, "\n")
}

// alignChatImageTranslations 把每个目标语种的译文对齐到 OCR 行：
// 已对齐的语种原样保留（零额外调用，但非语言行仍要钉回原文）；对不上的语种走 chatImageLegs 重译。
// translate(lang, text) 由调用方注入（生产＝Engine.HandleText，测试＝假函数）。
// 返回对齐后的新 map、发生重译的语种、附加积分、首个错误（某语种失败不拖累其他语种）。
func alignChatImageTranslations(lines []string, targets []string, got map[string]string,
	translate func(lang, text string) (string, int64, error)) (map[string]string, []string, int64, error) {
	out := make(map[string]string, len(got))
	for k, v := range got {
		out[k] = v
	}
	budget := chatImageLegMaxCalls
	var splitLangs []string
	var points int64
	var firstErr error
	for _, lang := range targets {
		if strings.TrimSpace(out[lang]) != "" && len(chatImageLinesOf(out[lang])) == len(lines) {
			// ★ 快路径也要过一遍「非语言行钉回原文」：行数对齐不等于逐行正确，
			//   模型对 `<`/`1` 的输出只可能是噪声（详见 pinChatImageIdentityLines）。
			out[lang] = pinChatImageIdentityLines(lines, out[lang])
			continue
		}
		legs, p, err := chatImageLegsWithin(lines, func(text string) (string, int64, error) {
			return translate(lang, text)
		}, &budget)
		points += p
		if err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("%s: %w", lang, err)
			}
			continue
		}
		out[lang] = strings.Join(legs, "\n")
		splitLangs = append(splitLangs, lang)
	}
	return out, splitLangs, points, firstErr
}

// chatImageAlignTranslations 生产接线：把分腿翻译接到引擎。
// 每一腿只翻**该目标语种**（其余语种已在整段那一枪里出过，不再重复计费/重复调用），
// mode 与界面语言沿用本次请求；用量由 Engine 内部实时记账，这里只把 points_used 累加回去。
func (s *Server) chatImageAlignTranslations(ctx context.Context, options map[string]interface{},
	lines []string, targets []string, got map[string]string) (map[string]string, []string, int64, error) {
	translate := func(lang, text string) (string, int64, error) {
		opts := make(map[string]interface{}, len(options)+1)
		for k, v := range options {
			opts[k] = v
		}
		opts["target_langs"] = []interface{}{lang}
		res := s.Engine.HandleText(ctx, text, opts, nil)
		if res == nil {
			return "", 0, errors.New("引擎未返回结果")
		}
		if res.Error != "" {
			return "", res.PointsUsed, errors.New(res.Error)
		}
		return res.Data.Translations[lang], res.PointsUsed, nil
	}
	return alignChatImageTranslations(lines, targets, got, translate)
}

// parseChatImageLangs 解析逗号分隔的目标语种（去空、去重、保序）。
func parseChatImageLangs(raw string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	return out
}

// toIfaceSlice []string → []interface{}（options 走 map[string]interface{}，与 ChatRequest 同形）。
func toIfaceSlice(in []string) []interface{} {
	out := make([]interface{}, 0, len(in))
	for _, v := range in {
		out = append(out, v)
	}
	return out
}
