package api

// ============ 本文件职责中文说明 ============
// 本文件实现 SSE（Server-Sent Events）流式翻译与文件翻译、非流式兼容接口、文件下载：
//   - SSE 工具：sseEvent（构造 data: 事件帧）/ sseHeaders（设置流式响应头）
//   - 流式文本翻译（handleChatStream /api/chat/stream）与流式文件翻译（handleTranslateFileStream /api/translate/stream）
//   - 非流式兼容接口（handleChat / handleTranslateFile）
//   - 文件下载（handleDownload /api/download/），按扩展名推断 Content-Type
// 业务要点：
//   - 所有翻译入口先过配额闸门（gateUsage），成功后按用量计量（meterUsage + countTranslate 指标）
//   - 进度事件 progress 逐步推送（step/done/total/percent），结束推送 done/error
//   - 上传文件保存到 UploadDir（uniqueName 保证文件名唯一），处理完成后删除

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ============ SSE 工具 ============

// sseEvent 构造一个 SSE 事件帧（data: {type, ...payload}）。
// 参数 eventType: 事件类型（如 progress/done/error）；payload: 事件负载字段。
// 返回: 符合 SSE 协议的事件文本（以 "data: " 开头、空行结尾）。
func sseEvent(eventType string, payload map[string]interface{}) string {
	full := map[string]interface{}{"type": eventType}
	for k, v := range payload {
		full[k] = v
	}
	data, _ := json.Marshal(full)
	return "data: " + string(data) + "\n\n"
}

// sseHeaders 设置 SSE 响应头（text/event-stream 及禁用缓冲/代理缓冲）。
// 参数 w: HTTP 响应写入器。无返回。
func sseHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	// 禁用 Nginx 等反向代理的缓冲，保证事件实时推送
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")
}

// ============ 流式文本翻译 ============

// handleChatStream 流式文本翻译接口（/api/chat/stream，SSE）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 ChatRequest）。
// 事件流：progress 进度事件 → done（携带 result）或 error 事件。
// 流程：解码请求 → 配额闸门 → 引擎流式处理 → 进度推送 → 计量 → 结果/错误事件。
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求格式错误"})
		return
	}
	sseHeaders(w)
	flusher, _ := w.(http.Flusher)

	// 空消息：返回系统问候语（不消耗配额）
	if strings.TrimSpace(req.Message) == "" {
		result := map[string]interface{}{"skill": "system", "reply": "你好！我是翻译助手，可以帮你翻译多语言文本和文件。"}
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": result}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝本次翻译）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		// 限流/余额不足：推送 error 事件并结束
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": gateErr.Error()}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// 进度回调：计算百分比（封顶 99%，完成时单独发 100%）并推送 progress 事件
	prog := func(step string, done, total int) {
		percent := 0
		if total > 0 {
			percent = done * 100 / total
			if percent > 99 {
				percent = 99
			}
		}
		fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": step, "done": done, "total": total, "percent": percent}))
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 调用引擎处理文本翻译（流式回调进度）
	res := s.Engine.HandleText(r.Context(), req.Message, req.Options, prog)
	// 推送完成进度
	fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": "完成", "done": 1, "total": 1, "percent": 100}))
	if flusher != nil {
		flusher.Flush()
	}
	if res.Error != "" {
		// 翻译失败：推送 error 事件并计入失败指标
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": res.Error}))
		s.metrics.countTranslate("text", false)
	} else {
		// ★ Token 实费计费：聚合本次全链路真实 token × 均摊系数（强制计费时扣余额）
		s.chargeTaskTokens(r, tid, "translate")
		s.metrics.countTranslate("text", true)
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": res}))
		// Webhook：翻译完成事件回调租户配置的 URL（异步投递，不阻塞 SSE 返回）
		s.dispatchTranslateWebhook(tid, "text", req.Message, res)
	}
}

// dispatchTranslateWebhook 投递翻译完成 webhook 事件（text/file 通用）。
// 参数：tid=租户 ID，kind=任务类型（text/file），source=源文本或文件名，res=引擎结果。
func (s *Server) dispatchTranslateWebhook(tid int64, kind, source string, res interface{}) {
	if s.Store == nil {
		return
	}
	s.Store.DispatchWebhook(tid, "translation.completed", map[string]interface{}{
		"event":     "translation.completed",
		"tenant_id": tid,
		"type":      kind,
		"source":    source,
		"result":    res,
		"time":      nowRFC3339(),
	})
}

// ============ 流式文件翻译 ============

// handleTranslateFileStream 流式文件翻译接口（/api/translate/stream，SSE）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart：file + target_langs + message）。
// 事件流：progress 进度事件 → done（携带 result）或 error 事件。
// 流程：保存上传文件 → 解析语言参数 → 配额闸门 → 引擎流式处理 → 计量 → 清理文件。
func (s *Server) handleTranslateFileStream(w http.ResponseWriter, r *http.Request) {
	// 解析 multipart 表单（上限 50MB，仅允许 docx/pptx/xlsx/pdf）
	if err := parseUpload(r, translateUploadMax, translateExtWhitelist); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	// 取上传文件
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "缺少文件"})
		return
	}
	defer file.Close()

	// 读取目标语言与提示语参数
	targetLangs := r.FormValue("target_langs")
	message := r.FormValue("message")

	// 保存上传文件到 UploadDir（唯一文件名避免冲突）
	os.MkdirAll(s.Cfg.UploadDir, 0o755)
	savePath := filepath.Join(s.Cfg.UploadDir, uniqueName(header.Filename))
	f, err := os.Create(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法保存文件"})
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		f.Close()
		writeJSON(w, 500, map[string]string{"error": "写入失败"})
		return
	}
	f.Close()

	// 解析目标语言列表（逗号分隔，去空白）
	langs := strings.Split(targetLangs, ",")
	clean := []string{}
	for _, l := range langs {
		l = strings.TrimSpace(l)
		if l != "" {
			clean = append(clean, l)
		}
	}
	// 不在此默认 en：由 HandleFile 内部解析 message 语言，再兜底 en
	options := map[string]interface{}{"target_langs": clean}
	if message != "" {
		options["message"] = message
		options["_prompt"] = message
	}

	sseHeaders(w)
	flusher, _ := w.(http.Flusher)

	// 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝本次文件翻译）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": gateErr.Error()}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// 进度回调：推送 progress 事件（与文本翻译一致，封顶 99%）
	prog := func(step string, done, total int) {
		percent := 0
		if total > 0 {
			percent = done * 100 / total
			if percent > 99 {
				percent = 99
			}
		}
		fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": step, "done": done, "total": total, "percent": percent}))
		if flusher != nil {
			flusher.Flush()
		}
	}

	// 调用引擎处理文件翻译
	res := s.Engine.HandleFile(r.Context(), savePath, options, prog)
	// 处理完成后删除临时文件
	os.Remove(savePath)

	// 推送完成进度
	fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": "完成", "done": 1, "total": 1, "percent": 100}))
	if flusher != nil {
		flusher.Flush()
	}
	if res.Error != "" {
		// 失败：推送 error 事件并计入失败指标
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": res.Error}))
		s.metrics.countTranslate("file", false)
	} else {
		// ★ Token 实费计费：聚合本次全链路真实 token × 均摊系数（强制计费时扣余额）
		s.chargeTaskTokens(r, tid, "translate")
		s.metrics.countTranslate("file", true)
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": res}))
		// Webhook：翻译完成事件回调（异步投递）
		s.dispatchTranslateWebhook(tid, "file", header.Filename, res)
	}
}

// ============ 非流式兼容接口 ============

// handleChat 非流式文本翻译接口（/api/chat，JSON 返回）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 为 ChatRequest）。
// 返回: 引擎处理结果对象（JSON）；成功时已计量。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求格式错误"})
		return
	}
	// 配额闸门：QPS/并发/每日上限/余额校验
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": gateErr.Error()})
		return
	}
	// 调用引擎处理文本翻译（非流式，无进度回调）
	res := s.Engine.HandleText(r.Context(), req.Message, req.Options, nil)
	if res.Error != "" {
		// 失败：填充错误回复并计入失败指标
		res.Skill = "translation"
		res.Reply = "❌ 处理出错: " + res.Error
		s.metrics.countTranslate("text", false)
	} else {
		// ★ Token 实费计费：聚合本次全链路真实 token × 均摊系数（强制计费时扣余额）
		s.chargeTaskTokens(r, tid, "translate")
		s.metrics.countTranslate("text", true)
		// Webhook：翻译完成事件回调（异步投递）
		s.dispatchTranslateWebhook(tid, "text", req.Message, res)
	}
	writeJSON(w, 200, res)
}

// handleTranslateFile 非流式文件翻译接口（/api/translate，JSON 返回）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（multipart：file + target_langs + message）。
// 返回: 引擎处理结果对象（JSON）；成功时已计量。
func (s *Server) handleTranslateFile(w http.ResponseWriter, r *http.Request) {
	// 解析 multipart 表单（上限 50MB，仅允许 docx/pptx/xlsx/pdf）
	if err := parseUpload(r, translateUploadMax, translateExtWhitelist); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "缺少文件"})
		return
	}
	defer file.Close()

	// 读取目标语言与提示语参数
	targetLangs := r.FormValue("target_langs")
	message := r.FormValue("message")

	// 保存上传文件
	os.MkdirAll(s.Cfg.UploadDir, 0o755)
	savePath := filepath.Join(s.Cfg.UploadDir, uniqueName(header.Filename))
	f, err := os.Create(savePath)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "无法保存文件"})
		return
	}
	if _, err := io.Copy(f, file); err != nil {
		f.Close()
		writeJSON(w, 500, map[string]string{"error": "写入失败"})
		return
	}
	f.Close()

	// 解析目标语言列表
	langs := strings.Split(targetLangs, ",")
	clean := []string{}
	for _, l := range langs {
		l = strings.TrimSpace(l)
		if l != "" {
			clean = append(clean, l)
		}
	}
	// 不在此默认 en：由 HandleFile 内部解析 message 语言，再兜底 en
	options := map[string]interface{}{"target_langs": clean}
	if message != "" {
		options["message"] = message
		options["_prompt"] = message
	}
	// 配额闸门：QPS/并发/每日上限/余额校验
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": gateErr.Error()})
		return
	}
	// 调用引擎处理文件翻译（非流式）
	res := s.Engine.HandleFile(r.Context(), savePath, options, nil)
	// 处理完成后删除临时文件
	os.Remove(savePath)
	if res.Error == "" {
		// ★ Token 实费计费：聚合本次全链路真实 token × 均摊系数（强制计费时扣余额）
		s.chargeTaskTokens(r, tid, "translate")
		s.metrics.countTranslate("file", true)
		// Webhook：翻译完成事件回调（异步投递）
		s.dispatchTranslateWebhook(tid, "file", header.Filename, res)
	} else {
		s.metrics.countTranslate("file", false)
	}
	writeJSON(w, 200, res)
}

// ============ 文件下载 ============

// handleDownload 文件下载接口（/api/download/，按扩展名推断 Content-Type）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 path 或路径中 /api/download/ 之后的文件名）。
// 返回: 文件内容（attachment 下载），支持图片/Office/PDF 等类型。
func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	// 解析文件路径：优先取查询参数 path，否则从 URL 路径提取
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		filePath = strings.TrimPrefix(r.URL.Path, "/api/download/")
	}
	if filePath == "" {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	// 打开文件并校验存在性
	f, err := os.Open(filePath)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	defer f.Close()
	// 校验为普通文件（非目录）
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	// 按扩展名推断 Content-Type（Office/图片/PDF 等）
	ext := strings.ToLower(filepath.Ext(filePath))
	contentTypes := map[string]string{
		".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
		".gif": "image/gif", ".webp": "image/webp",
		".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
		".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		".pdf":  "application/pdf",
	}
	ct, ok := contentTypes[ext]
	if !ok {
		// 未知类型回退二进制流
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	// 附件下载（保留原始文件名）
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(filePath)+"\"")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

// uniqueName 生成带时间戳的唯一文件名（避免上传同名冲突）。
// 参数 name: 原始文件名。返回: "<base>_<纳秒时间戳><ext>" 格式的唯一文件名。
func uniqueName(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s_%d%s", base, timeNow(), ext)
}

// timeNow 返回当前纳秒时间戳（用于唯一文件名）。
// 返回: UnixNano 纳秒时间戳。
func timeNow() int64 {
	return time.Now().UnixNano()
}
