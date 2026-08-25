// ============ api_openapi_tasks.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 开放 API 异步任务化（API Key 鉴权）：翻译统一走「工单 ID + 轮询」模型。
//   - POST /openapi/v1/tasks          创建任务：JSON=文本任务；multipart=文件批量任务
//   - GET  /openapi/v1/tasks?id=      轮询状态：未完成返回 processing 出参；
//                                     文本完成内联返回译文；文件完成返回产物清单+下载地址
//   - GET  /openapi/v1/tasks/download 文件产物下载（file_id 可选，缺省 zip 全部）
//   - GET  /openapi/v1/balance        查询租户 token 余额与 ≈句数
// 安全要点：
//   - API 任务工单 CreatedBy=0、标题 [API] 前缀；仅开放接口可访问（内部用户不可见/不可操作）
//   - 租户隔离：GetTicket(id, ak.TenantID)，跨租户查询一律 404 不泄露存在性
//   - 余额不足：创建预检与 worker 快速失败均返回独立错误码 insufficient_balance，
//     message 明确提示充值或升级套餐
// ========================================

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"translator/internal/store"
)


// pollIntervalSec 按任务类型给出建议轮询间隔（秒）：文本 15s、文件 60s
func pollIntervalSec(isFile bool) int {
	if isFile {
		return 60
	}
	return 15
}

// writeTaskError 统一任务错误响应：独立 error_code 出参 + 提示语。
func writeTaskError(w http.ResponseWriter, code, message string) {
	writeJSON(w, 200, map[string]interface{}{
		"success": false, "error_code": code, "message": message,
	})
}

// balanceOut 组装余额出参字段（token 主单位 + ≈句数换算）。
func (s *Server) balanceOut(tid int64) map[string]interface{} {
	tokens, approx := s.balancePayload(tid)
	return map[string]interface{}{"balance_tokens": tokens, "balance_sentences_approx": approx}
}

// normalizeTaskMode 归一化模式参数："fast"=快速；其余一律 pro 专业校对。
func normalizeTaskMode(m string) string {
	if strings.ToLower(strings.TrimSpace(m)) == "fast" {
		return "fast"
	}
	return "pro"
}

// handleOpenAPITaskCreate 创建翻译任务（文本 JSON / 文件 multipart 二选一）。
func (s *Server) handleOpenAPITaskCreate(w http.ResponseWriter, r *http.Request) {
	ak, authErr := s.authenticateAPIKey(r)
	if authErr != "" {
		if authErr == "key_quota_exceeded" {
			writeJSON(w, 429, map[string]interface{}{"success": false, "error_code": authErr,
				"message": "该 API Key 今日调用次数已达上限，请调整限额或明日再试"})
			return
		}
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "translate" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "error_code": "forbidden", "message": "API Key 无翻译权限"})
		return
	}
	// 配额闸门：QPS/并发/每日上限/token 余额校验（错误码单独出参）
	tid, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeTaskError(w, gateErrorCode(gateErr), gateErr.Error()+"。如余额不足请充值或升级套餐")
		return
	}
	ct := r.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/json") {
		s.openAPITaskCreateText(w, r, tid, ak)
		return
	}
	s.openAPITaskCreateFiles(w, r, tid, ak)
}

// gateErrorCode 将闸门错误映射为稳定错误码（OpenAPI 出参）。
// errInsufficientCode 余额不足错误码（与 service 包同值：worker 失败原因前缀匹配用）
const errInsufficientCode = "insufficient_balance"

// openAPITaskMaxFiles 单次文件任务数量上限
const openAPITaskMaxFiles = 20

// openAPITaskMaxBytes 单次文件任务总体积上限（30MB；Caddy 该路径放宽至 35MB）
const openAPITaskMaxBytes = 30 << 20

// openAPITaskExtWhitelist 文件任务扩展名白名单（与内部文件工单一致）
var openAPITaskExtWhitelist = map[string]bool{
	".docx": true, ".xlsx": true, ".pptx": true, ".pdf": true,
	".txt": true, ".csv": true, ".srt": true, ".vtt": true,
	".md": true, ".json": true, ".yaml": true, ".yml": true,
}

func gateErrorCode(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "余额"):
		return "insufficient_balance"
	case strings.Contains(msg, "频繁"), strings.Contains(msg, "并发"):
		return "rate_limited"
	case strings.Contains(msg, "上限"):
		return "daily_quota_exceeded"
	case strings.Contains(msg, "额度"):
		return "insufficient_balance"
	default:
		return "rejected"
	}
}

// openAPITaskCreateText 创建文本任务（JSON body）。
func (s *Server) openAPITaskCreateText(w http.ResponseWriter, r *http.Request, tid int64, ak *store.APIKey) {
	var req struct {
		Text        string   `json:"text"`         // 待翻译源文本（必填）
		TargetLangs []string `json:"target_langs"` // 目标语言列表（默认 ["en"]）
		Title       string   `json:"title"`        // 自定义标题（可选）
		Mode        string   `json:"mode"`         // fast | pro（默认 pro）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		writeTaskError(w, "bad_request", "text 不能为空")
		return
	}
	if len(req.TargetLangs) == 0 {
		req.TargetLangs = []string{"en"}
	}
	// 余额预检：强制计费时零余额直接拒绝（worker 内还有二次快速失败兜底）
	if s.Bill.Enabled() {
		if b, err := s.Store.GetBalance(tid); err == nil && b.Balance <= 0 {
			writeTaskError(w, "insufficient_balance", "余额不足，请充值或升级套餐")
			return
		}
	}
	mode := normalizeTaskMode(req.Mode)
	title := req.Title
	if title == "" {
		title = "[API] 文本翻译"
	}
	t, err := s.Store.CreateTicket(tid, 0, title, req.Text, "", strings.Join(req.TargetLangs, ","))
	if err != nil {
		writeTaskError(w, "internal", err.Error())
		return
	}
	s.enqueueAPITask(t, mode, ak)
	writeJSON(w, 202, map[string]interface{}{
		"task_id": t.ID, "mode": mode,
		"type": "text", "status": "queued",
	})
}

// enqueueAPITask 置模式/入队并写审计（文本与文件任务共用收尾）。
func (s *Server) enqueueAPITask(t *store.Ticket, mode string, ak *store.APIKey) {
	t.Mode = mode
	t.Status = store.TicketQueued
	if ak != nil && ak.UserID > 0 {
		_ = s.Store.StampTicketAPIUser(t.ID, ak.UserID) // ★ 归属盖印：回读时校验 Key→用户
	}
	_ = s.Store.UpdateTicket(t)
	if s.TicketSvc != nil {
		_, _ = s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID)
	}
	s.Store.LogAudit(t.TenantID, 0, "api_task_create", "tickets", t.TicketNo+" mode="+mode)
}

// openAPITaskCreateFiles 创建文件批量任务（multipart）。
func (s *Server) openAPITaskCreateFiles(w http.ResponseWriter, r *http.Request, tid int64, ak *store.APIKey) {
	r.Body = http.MaxBytesReader(w, r.Body, openAPITaskMaxBytes)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeTaskError(w, "bad_request", "文件解析失败或超过 30MB 总量上限")
		return
	}
	var headers []*multipart.FileHeader
	if r.MultipartForm != nil {
		headers = append(headers, r.MultipartForm.File["files"]...)
		if len(headers) == 0 {
			headers = append(headers, r.MultipartForm.File["file"]...)
		}
	}
	if len(headers) == 0 {
		writeTaskError(w, "bad_request", "缺少文件：multipart 字段名 files，curl 请使用 -F \"files=@本地路径\"（注意 @ 前缀）")
		return
	}
	if len(headers) > openAPITaskMaxFiles {
		writeTaskError(w, "bad_request", fmt.Sprintf("单次最多 %d 个文件", openAPITaskMaxFiles))
		return
	}
	for _, hdr := range headers {
		ext := strings.ToLower(filepath.Ext(hdr.Filename))
		if !openAPITaskExtWhitelist[ext] {
			writeTaskError(w, "bad_request", "不支持的格式: "+hdr.Filename+"（仅支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml）")
			return
		}
	}
	targetLangs := r.FormValue("target_langs")
	if targetLangs == "" {
		targetLangs = "en"
	}
	mode := normalizeTaskMode(r.FormValue("mode"))
	title := r.FormValue("title")

	// 余额预检（同文本任务）
	if s.Bill.Enabled() {
		if b, err := s.Store.GetBalance(tid); err == nil && b.Balance <= 0 {
			writeTaskError(w, "insufficient_balance", "余额不足，请充值或升级套餐")
			return
		}
	}

	// 保存到上传目录 tickets/ 子目录（与内部文件工单同目录约定）
	dir := filepath.Join(s.Cfg.UploadDir, "tickets")
	_ = os.MkdirAll(dir, 0o755)
	var saved []struct{ path, name string }
	for _, hdr := range headers {
		saveName := fmt.Sprintf("%d_%s", time.Now().UnixNano(), filepath.Base(hdr.Filename))
		savePath := filepath.Join(dir, saveName)
		src, ferr := hdr.Open()
		if ferr != nil {
			writeTaskError(w, "internal", "读取文件失败: "+hdr.Filename)
			return
		}
		out, cerr := os.Create(savePath)
		if cerr != nil {
			src.Close()
			writeTaskError(w, "internal", "保存文件失败")
			return
		}
		if _, cerr = io.Copy(out, src); cerr != nil {
			out.Close()
			src.Close()
			writeTaskError(w, "internal", "写入文件失败")
			return
		}
		out.Close()
		src.Close()
		saved = append(saved, struct{ path, name string }{savePath, hdr.Filename})
	}
	if title == "" {
		title = "[API] " + saved[0].name
		if len(saved) > 1 {
			title += fmt.Sprintf(" 等 %d 个文件", len(saved))
		}
	}
	t, err := s.Store.CreateTicket(tid, 0, title, "", saved[0].path, targetLangs)
	if err != nil {
		writeTaskError(w, "internal", err.Error())
		return
	}
	tid2 := t.TenantID
	for _, f := range saved {
		_, _ = s.Store.AddTicketFile(&store.TicketFile{
			TenantID: tid2, TicketID: t.ID, FileName: f.name, FilePath: f.path,
		})
	}
	s.enqueueAPITask(t, mode, ak)
	writeJSON(w, 202, map[string]interface{}{
		"task_id": t.ID, "mode": mode,
		"type": "files", "status": "queued",
		"file_count": len(saved),
	})
}

// handleOpenAPITaskStatus 轮询任务状态（未完成给 processing 出参；完成按类型回结果）。
func (s *Server) handleOpenAPITaskStatus(w http.ResponseWriter, r *http.Request) {
	ak, authErr := s.authenticateAPIKey(r)
	if authErr != "" {
		if authErr == "key_quota_exceeded" {
			writeJSON(w, 429, map[string]interface{}{"success": false, "error_code": authErr,
				"message": "该 API Key 今日调用次数已达上限，请调整限额或明日再试"})
			return
		}
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 {
		writeTaskError(w, "bad_request", "缺少任务 id")
		return
	}
	// 租户隔离 + 仅限 API 任务（CreatedBy=0），跨租户/内部工单一律 404
	t, err := s.Store.GetTicket(id, ak.TenantID)
	if err != nil || t.CreatedBy != 0 {
		writeJSON(w, 404, map[string]interface{}{"success": false, "error_code": "not_found", "message": "任务不存在"})
		return
	}
	// ★ 用户级归属校验（强绑定无旁路）：租户匹配 + Key用户==任务盖印用户，否则 404 不泄露存在性
	if t.APIUserID != ak.UserID {
		writeJSON(w, 404, map[string]interface{}{"success": false, "error_code": "not_found", "message": "任务不存在"})
		return
	}
	isFile := t.FilePath != ""
	resp := map[string]interface{}{
		"task_id": t.ID,
		"type":    map[bool]string{true: "files", false: "text"}[isFile],
		"mode":    normalizeTaskMode(t.Mode),
		"status":  "",
	}
	// 状态映射：queued→queued；in_progress/pending_approval/approved→processing；
	// completed→completed；rejected/failed→failed
	status := "processing"
	errCode := ""
	errMsg := ""
	switch t.Status {
	case store.TicketQueued:
		status = "queued"
	case store.TicketInProgress, store.TicketPendingAppr, store.TicketApproved:
		status = "processing"
	case store.TicketCompleted:
		status = "completed"
	default: // rejected / failed / draft 等异常态
		status = "failed"
		if strings.HasPrefix(t.RejectReason, errInsufficientCode+":") {
			errCode = errInsufficientCode
			errMsg = "余额不足，请充值或升级套餐"
		} else {
			errCode = "task_failed"
			errMsg = t.RejectReason
		}
	}
	resp["status"] = status
	switch status {
	case "completed":
		// ★ 用量出参：本单实费计费 token 数（真实用量×均摊系数，完成时落库 tickets.tokens_billed）
		resp["tokens_used"] = t.TokensBilled
		if isFile {
			tfiles, _ := s.Store.TicketFiles(t.ID)
			files := make([]map[string]interface{}, 0, len(tfiles))
			for _, f := range tfiles {
				files = append(files, map[string]interface{}{
					"file_id": f.ID, "name": f.FileName,
					"result_ready": f.ResultPath != "",
					"error":        f.Error,
				})
			}
			resp["files"] = files
			resp["download"] = "/openapi/v1/tasks/download?id=" + strconv.FormatInt(t.ID, 10)
			// 产物到期时间（保留期提示；读 result_expires_at 列）
			if exp, ok := s.ticketExpiry(t.ID); ok && exp != "" {
				resp["expires_at"] = exp
			}
		} else {
			// 文本任务：解析 FinalResult 内联返回译文
			var payload struct {
				Translations map[string]string `json:"translations"`
				Sources      map[string]string `json:"sources"`
			}
			_ = json.Unmarshal([]byte(t.FinalResult), &payload)
			resp["translations"] = payload.Translations
			resp["sources"] = payload.Sources
		}
	case "failed":
		resp["error_code"] = errCode
		resp["message"] = errMsg
	}
	for k, v := range s.balanceOut(ak.TenantID) {
		resp[k] = v
	}
	writeJSON(w, 200, resp)
}

// handleOpenAPITaskDownload 下载文件任务产物（file_id 可选，缺省单文件直返/多文件打 zip）。
func (s *Server) handleOpenAPITaskDownload(w http.ResponseWriter, r *http.Request) {
	ak, authErr := s.authenticateAPIKey(r)
	if authErr != "" {
		if authErr == "key_quota_exceeded" {
			writeJSON(w, 429, map[string]interface{}{"success": false, "error_code": authErr,
				"message": "该 API Key 今日调用次数已达上限，请调整限额或明日再试"})
			return
		}
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	if ak.Perms != "all" && ak.Perms != "translate" {
		writeJSON(w, 403, map[string]interface{}{"success": false, "error_code": "forbidden", "message": "API Key 无翻译权限"})
		return
	}
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if id <= 0 {
		writeTaskError(w, "bad_request", "缺少任务 id")
		return
	}
	t, err := s.Store.GetTicket(id, ak.TenantID)
	if err != nil || t.CreatedBy != 0 {
		writeJSON(w, 404, map[string]interface{}{"success": false, "error_code": "not_found", "message": "任务不存在"})
		return
	}
	// ★ 用户级归属校验（强绑定无旁路）：租户匹配 + Key用户==任务盖印用户，否则 404 不泄露存在性
	if t.APIUserID != ak.UserID {
		writeJSON(w, 404, map[string]interface{}{"success": false, "error_code": "not_found", "message": "任务不存在"})
		return
	}
	if t.Status != store.TicketCompleted {
		writeTaskError(w, "not_ready", "任务尚未完成，请先轮询至 completed 再下载")
		return
	}
	baseName := t.TicketNo
	// 多文件工单：?file_id= 取单个产物；否则打包 zip（跳过失败文件）
	tfiles, _ := s.Store.TicketFiles(t.ID)
	if len(tfiles) > 0 {
		if fidStr := r.URL.Query().Get("file_id"); fidStr != "" {
			fid, _ := strconv.ParseInt(fidStr, 10, 64)
			for _, f := range tfiles {
				if f.ID == fid && f.ResultPath != "" {
					if fh, oerr := os.Open(f.ResultPath); oerr == nil {
						defer fh.Close()
						w.Header().Set("Content-Disposition", `attachment; filename="`+mimeEscape(f.FileName)+`"`)
						http.ServeContent(w, r, f.FileName, time.Now(), fh)
						return
					}
				}
			}
			writeJSON(w, 404, map[string]interface{}{"success": false, "error_code": "not_found", "message": "该文件的产物不存在"})
			return
		}
		var paths, names []string
		for _, f := range tfiles {
			if f.ResultPath != "" {
				if _, serr := os.Stat(f.ResultPath); serr == nil {
					paths = append(paths, f.ResultPath)
					names = append(names, resultFileName(f))
				}
			}
		}
		if len(paths) == 1 {
			fh, oerr := os.Open(paths[0])
			if oerr == nil {
				defer fh.Close()
				w.Header().Set("Content-Disposition", `attachment; filename="`+names[0]+`"`)
				http.ServeContent(w, r, names[0], time.Now(), fh)
				return
			}
		}
		if len(paths) > 1 {
			w.Header().Set("Content-Disposition", `attachment; filename="`+baseName+`.zip"`)
			w.Header().Set("Content-Type", "application/zip")
			zw := zip.NewWriter(w)
			for i, p := range paths {
				data, rerr := os.ReadFile(p)
				if rerr != nil {
					continue
				}
				fe, _ := zw.Create(names[i])
				_, _ = fe.Write(data)
			}
			_ = zw.Close()
			return
		}
		writeTaskError(w, "no_result", "暂无已生成的产物（部分或全部文件处理失败）")
		return
	}
	// 旧单文件工单：原格式产物直返
	if t.ResultPath != "" {
		if f, ferr := os.Open(t.ResultPath); ferr == nil {
			defer f.Close()
			w.Header().Set("Content-Disposition", `attachment; filename="`+baseName+filepath.Ext(t.ResultPath)+`"`)
			http.ServeContent(w, r, filepath.Base(t.ResultPath), time.Now(), f)
			return
		}
	}
	writeTaskError(w, "no_result", "该任务无可下载的文件产物")
}

// ticketExpiry 读取工单产物到期时间（result_expires_at 列；无值返回 ok=false）。
func (s *Server) ticketExpiry(ticketID int64) (string, bool) {
	var exp string
	if err := s.Store.DB().QueryRow("SELECT COALESCE(result_expires_at,'') FROM tickets WHERE id=?", ticketID).Scan(&exp); err != nil || exp == "" {
		return "", false
	}
	return exp, true
}

// handleOpenAPIBalance 查询租户 token 余额与 ≈句数（任意权限的有效 Key 均可查询）。
func (s *Server) handleOpenAPIBalance(w http.ResponseWriter, r *http.Request) {
	ak, authErr := s.authenticateAPIKey(r)
	if authErr != "" {
		if authErr == "key_quota_exceeded" {
			writeJSON(w, 429, map[string]interface{}{"success": false, "error_code": authErr,
				"message": "该 API Key 今日调用次数已达上限，请调整限额或明日再试"})
			return
		}
		writeJSON(w, 401, map[string]interface{}{"success": false, "error_code": "invalid_api_key", "message": "API Key 无效"})
		return
	}
	resp := map[string]interface{}{
		"success":   true,
		"tenant_id": ak.TenantID,
		"currency":  "tokens",
	}
	for k, v := range s.balanceOut(ak.TenantID) {
		resp[k] = v
	}
	writeJSON(w, 200, resp)
}
