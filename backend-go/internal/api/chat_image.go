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
//	译文按 \n 拆行；行数与 OCR 行数**严格相等**才配对回填。数量对不上说明模型没保持行结构，
//	这时宁可不画（保留原文，image_error=line_mismatch）也不把译文盖到错误的框上——
//	与 ocr.go 的 parseOcrJSON 同一条"数量不等就不配对"纪律。
//
// =============================================
package api

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"translator/internal/billing"
	"translator/internal/engine"
	apierrors "translator/internal/errors"
	"translator/internal/fileproc"
	"translator/internal/llm"
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
	tmp, terr := os.CreateTemp("", "chat-image-*"+ext)
	if terr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "图片暂存失败"))
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)
	// ★ 限流 COPY：MaxBytesReader/ParseMultipartForm 管的是请求体，COPY 阶段不加这道墙，
	//   一个伪造的 Content-Length 就能把磁盘写满（与 chat_audio.go 同款教训）。
	if _, cerr := io.Copy(tmp, io.LimitReader(f, chatImageMaxBytes)); cerr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "图片暂存失败"))
		return
	}

	// 配额闸门：图片翻译是一次真实算力调用（OCR + LLM），与 /api/chat 同口径先过闸再干活。
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		s.writeGateError(w, r, gateErr)
		return
	}

	// OCR：识别行文本（同时把「行↔坐标」写进 P4 回填缓存，供 RenderTranslatedImage 取用）。
	lines, oerr := fileproc.ExtractTexts(tmpPath)
	if oerr != nil {
		// ★ OCR 出错＝"这一次没识别出来"，不是请求处理出错：HTTP 200 + {error, reason}，
		//   不碰任何翻译状态（口径同 chat_audio.go「译文第一」）。
		writeJSON(w, 200, chatVoiceFailure{Error: oerr.Error(), Reason: imageReason(oerr)})
		return
	}
	if len(lines) == 0 {
		writeJSON(w, 200, chatVoiceFailure{Error: "图片里没有可翻译的文字", Reason: "no_text"})
		return
	}

	uiLang := strings.TrimSpace(r.FormValue("lang"))
	mode := engine.ModeFromOptions(map[string]interface{}{"mode": r.FormValue("mode")})
	options := map[string]interface{}{
		"target_langs": toIfaceSlice(targets),
		"mode":         mode,
	}
	if uiLang != "" {
		options["lang"] = uiLang
	}
	// ★ 整段一次翻（与 /api/chat 同构、只发一枪），避免逐行多次调用把即时翻译拖慢。
	//   注意这里**保留换行**：joined 里的 \n 是"行↔译文"对齐的唯一线索。
	joined := strings.Join(lines, "\n")
	ctx := llm.WithInteractive(tenant.WithLang(tenant.WithMode(s.userOrgCtx(r), mode), uiLang))
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
		writeJSON(w, 200, resp)
		return
	}
	s.metrics.countTranslate("text", true)
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
	writeJSON(w, 200, resp)
}

// renderChatImage 生成译后图并把 data URL（或降级原因）写进 resp。
// 参数：r＝请求（取 ctx）；srcPath＝原图；lang＝目标语种；lines＝OCR 行；
// translated＝该语种整段译文；resp＝出参（就地填 Image/ImageError）。
//
// ★ 一切失败都只降级译后图这一件，绝不返回错误把整单判死（译文文本已在 resp 里）。
func renderChatImage(r *http.Request, srcPath, lang string, lines []string, translated string, resp *chatImageResp) {
	translated = strings.TrimSpace(translated)
	if translated == "" {
		resp.ImageError = "no_translation"
		return
	}
	trLines := strings.Split(translated, "\n")
	if len(trLines) != len(lines) {
		// 模型没保持行结构：宁可不画也不把译文盖到错误的框上（保留原文更安全）。
		resp.ImageError = "line_mismatch"
		return
	}
	tr := make(map[string]string, len(lines))
	for i, ln := range lines {
		tr[ln] = strings.TrimSpace(trLines[i])
	}
	outPath := srcPath + ".translated.png"
	defer os.Remove(outPath)
	if err := fileproc.RenderTranslatedImage(r.Context(), srcPath, outPath, tr); err != nil {
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
