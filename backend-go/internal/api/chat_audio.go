// ============ chat_audio.go · 职责说明 ============
// 即时翻译（聊天窗）的**语音两件套**（P3，2026-10-14）：
//
//	POST /api/chat/asr —— 麦克风录音 → 转写文本（multipart: file）
//	POST /api/chat/tts —— 译文文本 → 可播放音频（JSON: text / lang）
//
// ★ 上游适配器定位（贯穿 OCR/ASR/TTS 的同一条原则）：这两个端点**不做翻译**。
//
//	asr 只把录音变成文字交回前端（前端填进输入框、用户照常点发送，走既有 /api/chat 全链路）；
//	tts 只把已经译好的文字变成声音。翻译引擎、KB、计费、SSE 一条都不碰——
//	这样做的收益是：语音能力整体不可用时，翻译主功能**零影响**（删掉本文件即逐字节回退）。
//
// ★ 入口分工（用户令）：即时翻译只收**麦克风录音**，永不接文件；文件/录音的小伙伴（工单音频）
//
//	在 TicketsPage 那条腿上（见 fileproc/asr.go 的 TranscribeAudio 注释）。
//	★ 试用态不开放：这两个端点与 /api/chat 同口径要求登录（authUser），
//	  免登录试用端点（/api/trial/translate）只有文本一条腿，天然碰不到语音。
//
// ★ 译文第一：tts 失败只影响"有没有声音"，绝不能把译文判死——因此这里一律返回
//
//	明确的错误体**而不触碰任何翻译状态**；前端据此隐藏播放钮并在必要时提示一次。
//
// =============================================
package api

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	apierrors "translator/internal/errors"
	"translator/internal/fileproc"
)

// chatAudioMaxBytes 麦克风录音的上限（25MiB）。
// 取值依据：浏览器 MediaRecorder 出的 opus/webm 约 0.5~1 MB/分钟，25MiB ≈ 25~50 分钟录音；
// 即时翻译的实际语料是几句话，这个上限只是防「误传一个长音频把 CPU 占满整根管道」。
const chatAudioMaxBytes = 25 << 20

// chatVoiceFailure 语音两件套的"这一次没有产出"出参（HTTP 200）。
//
// ★ 两个字段各有各的读者，缺一不可：
//   - Error＝给人看的那一句（运维日志、排障时一眼知道是"依赖没装"还是"语种越界"）；
//   - Reason＝给机器的档位（前端按它取 12 语种词典，**禁止**把上面那句中文投给非中文界面）。
type chatVoiceFailure struct {
	Error   string `json:"error"`   // 面向运维/日志的人话句子
	Reason  string `json:"reason"`  // 分档名：见 fileproc.TtsReason* 与下面 asrReason 的档位
	Suggest string `json:"suggest"` // "ticket"＝引导去翻译工单（带这个文件跳转，§4.3 C-5）
}

// asrReason ASR 侧的档位（与 TTS 同族但形态不同：ASR 只有"这台机器没这项能力"与"这次没听出来"两档，
// 脚本侧的 ERR:<Variant> 细分只落在那句人话里，不进档位——前端两种情况用的是同一句词典文案）。
func asrReason(err error) string {
	if err == nil {
		return ""
	}
	// 能力档判据只问 fileproc 自己给出的那一句开头（同一包内单源，别在前端猜字符串）
	if msg := err.Error(); strings.HasPrefix(msg, "音频语音识别（ASR）不可用") ||
		strings.Contains(msg, "ASR 模型资产缺失") || strings.Contains(msg, "库未安装") {
		return "capability_missing"
	}
	// ★ 静音/未授权档单列（2026-10-19 G11）：判据取自 fileproc 的同一个常量，
	//   前端按 reason==='no_speech' 出「请靠近麦克风重试」而不是笼统的失败话术。
	if strings.HasPrefix(err.Error(), fileproc.AsrNoSpeechMessage) {
		return fileproc.AsrNoSpeechReason
	}
	return "failed"
}

// chatAsrResp /api/chat/asr 的出参。
type chatAsrResp struct {
	Text string `json:"text"` // 转写结果（多段按换行拼好；前端直接填进输入框）
}

// handleChatASR POST /api/chat/asr：把麦克风录音转写成文本。
// ★ 只做识别，不做翻译：识别出的文本交回前端由用户确认后照常走翻译链路
//
//	（"识别完就自动翻"会剥夺用户改错字的机会，而 ASR 把专有名词听错的情形并不罕见）。
func (s *Server) handleChatASR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	if s.authUser(r) == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录或登录已过期"))
		return
	}
	ext := ".webm"
	if ct := r.Header.Get("Content-Type"); strings.Contains(ct, "multipart/") {
		if err := r.ParseMultipartForm(chatAudioMaxBytes); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "录音数据格式无法解析（或超出上限）"))
			return
		}
	} else {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请以 multipart/form-data 提交录音"))
		return
	}
	var f multipart.File
	var hdr *multipart.FileHeader
	f, hdr, err := r.FormFile("file")
	if err != nil {
		f, hdr, err = r.FormFile("audio")
	}
	if err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少录音文件"))
		return
	}
	defer f.Close()
	if v := strings.ToLower(filepath.Ext(hdr.Filename)); v != "" {
		ext = v
	}

	tmp, terr := os.CreateTemp("", "chat-asr-*"+ext)
	if terr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "录音暂存失败"))
		return
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	// ★ 落盘必须限流：MaxBytesReader 管的是请求体大小，COPY 阶段不加这道墙，
	//   一个伪造的 Content-Length 就能把磁盘写满（ parseUpload 同款教训）。
	if _, cerr := io.Copy(tmp, io.LimitReader(f, chatAudioMaxBytes)); cerr != nil {
		tmp.Close()
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "录音暂存失败"))
		return
	}
	tmp.Close()

	// ★ 2026-10-30 组4（C-3／G11）：免费档判定——时长先定档，再判身份。
	//   ASR 是本地模型、没有 LLM 成本，所以「30 秒内」这一档不扣积分（只占免费额度/IP 次数）；
	//   30–60 秒走原 gateUsage（只判付费身份与余额墙，**不扣分**——听自己的翻译还要按秒付钱是灾难）。
	//   超过硬顶 / 探测失败 ⇒ 引导工单或明确说"这段录音我听不了"，绝不静默放行。
	asrTid := s.currentTenant(r)
	asrUID := int64(0)
	if u := s.authUser(r); u != nil {
		asrUID = u.ID
	}
	asrFree := false
	if dur, derr := fileproc.AudioDuration(tmpPath); derr != nil {
		writeJSON(w, 200, chatVoiceFailure{Error: "无法读取这段录音的时长，请换一段录音或交给翻译工单", Reason: instantReasonUnreadable, Suggest: "ticket"})
		return
	} else {
		route := s.instantDecide(r, asrTid, asrUID, instantInput{Kind: "audio", SizeBytes: hdr.Size, DurationSec: dur, LangCount: 1})
		if route.SuggestTicket {
			writeJSON(w, 200, chatVoiceFailure{Error: "录音超出即时翻译上限，请交给翻译工单（上限 40 MB）", Reason: route.Reason, Suggest: "ticket"})
			return
		}
		asrFree = route.Free
		if !asrFree {
			// 付费档（含 30–60 秒）：走原闸门只判身份/余额，不产生积分扣减
			if _, release, gateErr := s.gateUsage(r); gateErr != nil {
				release()
				s.writeGateError(w, r, gateErr)
				return
			} else {
				release()
			}
		} else {
			if _, release, gateErr := s.gateUsageInstant(r); gateErr != nil {
				release()
				s.writeGateError(w, r, gateErr)
				return
			} else {
				release()
			}
		}
	}

	cues, aerr := fileproc.TranscribeAudio(r.Context(), tmpPath)
	if aerr != nil {
		// ★ ASR 的错误文案由 fileproc 单源产出（含"依赖未安装"这类排障指向），这里原样透出，
		//   不要二次包装成笼统的"识别失败"——运维看日志时最需要的正是那句 actionable 的话。
		// ★ 为什么是 200 而不是 5xx：识别不出来是"这一条语音能力的结果"，不是请求处理出错；
		//   本端点不动任何翻译状态，前端拿到的是一条"没有文字"的读数（口径见文件头「译文第一」）。
		//   仍带 error 句子给运维与日志，另带 **reason 档位**给前端按界面语言取词典文案。
		writeJSON(w, 200, chatVoiceFailure{Error: aerr.Error(), Reason: asrReason(aerr)})
		return
	}
	var b strings.Builder
	for i, c := range cues {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(c.Text)
	}
	writeJSON(w, 200, chatAsrResp{Text: b.String()})
}

// handleChatTTS POST /api/chat/tts：把一段已译好的文本合成语音。
// ★ 只做朗读，不做翻译：前端传进来的是**译文原文**（含目标语种），本端点不判断语言、
//
//	不校对付账、不写 SSE —— 合成不出来就如实报错，**译文照常在界面上**。
func (s *Server) handleChatTTS(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, r, apierrors.New(apierrors.ErrMethodNotAllowed, "仅支持 POST"))
		return
	}
	if s.authUser(r) == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录或登录已过期"))
		return
	}
	var req struct {
		Text string `json:"text"`
		Lang string `json:"lang"`
		ID   string `json:"id"` // 前端本地播放器的缓存键（播放器用于中止上一轮播放，服务端只原样过，不入业务逻辑）
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "没有可朗读的内容"))
		return
	}
	// 配额闸门与 /api/chat 同口径：合成也是一次真实算力调用（CPU 是成本，
	//   且在"未开启计费"的部署形态下这道墙就是唯一的护栏，不能因为"只是朗读"就绕开）。
	// ★ 2026-10-30 组4：朗读同样没有 LLM 成本 ⇒ 免费档内走免费闸（不扣分、不够余额也能听）；
	//   超出免费档才回落原 gateUsage（只判付费身份）。判据与文本同一条 instantDecide，
	//   LangCount=1（朗读是单语种）。
	ttsTid := s.currentTenant(r)
	ttsUID := int64(0)
	if u := s.authUser(r); u != nil {
		ttsUID = u.ID
	}
	ttsRoute := s.instantDecide(r, ttsTid, ttsUID, instantInput{Kind: "text", LangCount: 1})
	tid, release, gateErr := s.gateUsage(r)
	if ttsRoute.Free {
		tid, release, gateErr = s.gateUsageInstant(r)
	}
	defer release()
	if gateErr != nil {
		s.writeGateError(w, r, gateErr)
		return
	}
	_ = tid
	_ = ttsRoute

	outPath := filepath.Join(os.TempDir(), "")
	tmp, oerr := os.CreateTemp(outPath, "chat-tts-*.wav")
	if oerr != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "语音产物暂存失败"))
		return
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	// ★ 2026-10-21 UAT 收口 M8：合成必须跟请求同生共死（r.Context()，而不是 Background()）。
	//   旧写法在客户端断开／浏览器放弃之后，这个 Python 子进程仍会继续吃掉一整个 CPU 核
	//   直到 60s 预算烧完——同一台上每个"点了朗读又关掉"的访客都会留下一条这样的尾巴，
	//   而 CPU 正是 TTS 最贵的资源。runSubprocess 认 ctx 取消（见 fileproc 侧），
	//   这里换上下文是零语义改动的资源收口；合成预算本身仍由 ttsTimeout() 那道墙负责。
	if serr := fileproc.SpeakToWav(r.Context(), text, req.Lang, tmpPath); serr != nil {
		// 同上：朗读不出来＝"这一次没有语音"，HTTP 200 ＋ reason 档位，译文与主链一个字节都不动。
		writeJSON(w, 200, chatVoiceFailure{Error: serr.Error(), Reason: fileproc.TtsReasonOf(serr)})
		return
	}
	data, rerr := os.ReadFile(tmpPath)
	if rerr != nil || len(data) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "语音产物读取失败"))
		return
	}
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(200)
	_, _ = w.Write(data)
}
