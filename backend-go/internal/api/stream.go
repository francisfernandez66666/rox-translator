package api

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

func sseEvent(eventType string, payload map[string]interface{}) string {
	full := map[string]interface{}{"type": eventType}
	for k, v := range payload {
		full[k] = v
	}
	data, _ := json.Marshal(full)
	return "data: " + string(data) + "\n\n"
}

func sseHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")
}

// ============ 流式文本翻译 ============

func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求格式错误"})
		return
	}
	sseHeaders(w)
	flusher, _ := w.(http.Flusher)

	if strings.TrimSpace(req.Message) == "" {
		result := map[string]interface{}{"skill": "system", "reply": "你好！我是翻译助手，可以帮你翻译多语言文本和文件。"}
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": result}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// ★ 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝本次翻译）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": gateErr.Error()}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

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

	res := s.Engine.HandleText(r.Context(), req.Message, req.Options, prog)
	fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": "完成", "done": 1, "total": 1, "percent": 100}))
	if flusher != nil {
		flusher.Flush()
	}
	if res.Error != "" {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": res.Error}))
		s.metrics.countTranslate("text", false)
	} else {
		// ★ 计量：按源文本字符数记入用量（强制计费模式会扣余额）
		s.meterUsage(r, tid, "translate", int64(len([]rune(req.Message))))
		s.metrics.countTranslate("text", true)
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": res}))
	}
}

// ============ 流式文件翻译 ============

func (s *Server) handleTranslateFileStream(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		writeJSON(w, 400, map[string]string{"error": "文件上传失败"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "缺少文件"})
		return
	}
	defer file.Close()

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

	// ★ 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝本次文件翻译）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": gateErr.Error()}))
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

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

	res := s.Engine.HandleFile(r.Context(), savePath, options, prog)
	os.Remove(savePath)

	fmt.Fprint(w, sseEvent("progress", map[string]interface{}{"step": "完成", "done": 1, "total": 1, "percent": 100}))
	if flusher != nil {
		flusher.Flush()
	}
	if res.Error != "" {
		fmt.Fprint(w, sseEvent("error", map[string]interface{}{"error": res.Error}))
		s.metrics.countTranslate("file", false)
	} else {
		// ★ 计量：按提取段数计量文件翻译用量（强制计费模式会扣余额）
		s.meterUsage(r, tid, "translate", int64(res.Data.TotalTexts))
		s.metrics.countTranslate("file", true)
		fmt.Fprint(w, sseEvent("done", map[string]interface{}{"result": res}))
	}
}

// ============ 非流式兼容接口 ============

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]string{"error": "请求格式错误"})
		return
	}
	// ★ 配额闸门
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": gateErr.Error()})
		return
	}
	res := s.Engine.HandleText(r.Context(), req.Message, req.Options, nil)
	if res.Error != "" {
		res.Skill = "translation"
		res.Reply = "❌ 处理出错: " + res.Error
		s.metrics.countTranslate("text", false)
	} else {
		// ★ 计量
		s.meterUsage(r, tid, "translate", int64(len([]rune(req.Message))))
		s.metrics.countTranslate("text", true)
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleTranslateFile(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		writeJSON(w, 400, map[string]string{"error": "文件上传失败"})
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "缺少文件"})
		return
	}
	defer file.Close()

	targetLangs := r.FormValue("target_langs")
	message := r.FormValue("message")

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
	// ★ 配额闸门
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "error": gateErr.Error()})
		return
	}
	res := s.Engine.HandleFile(r.Context(), savePath, options, nil)
	os.Remove(savePath)
	if res.Error == "" {
		// ★ 计量：按提取段数计量文件翻译用量
		s.meterUsage(r, tid, "translate", int64(res.Data.TotalTexts))
		s.metrics.countTranslate("file", true)
	} else {
		s.metrics.countTranslate("file", false)
	}
	writeJSON(w, 200, res)
}

// ============ 文件下载 ============

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request) {
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		filePath = strings.TrimPrefix(r.URL.Path, "/api/download/")
	}
	if filePath == "" {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	f, err := os.Open(filePath)
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		writeJSON(w, 404, map[string]string{"error": "文件不存在"})
		return
	}
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
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Disposition", "attachment; filename=\""+filepath.Base(filePath)+"\"")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
func uniqueName(name string) string {
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	return fmt.Sprintf("%s_%d%s", base, timeNow(), ext)
}

func timeNow() int64 {
	return time.Now().UnixNano()
}