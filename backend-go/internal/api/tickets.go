// ============ tickets.go · 职责说明 ============
// api 包内部实现文件。
// =============================================
package api

// ============ 本文件职责中文说明 ============
// 本文件实现工单流程与审批相关接口：
//   - 工单管理（tenant_admin+）：列表/创建/运行/详情（handleTickets / handleTicketCreate / handleTicketRun / handleTicketDetail）
//   - 审批（approver + admin）：待审批列表 / 审批操作（handleApproveList / handleApproveAction）
// 业务要点：
//   - handleTicketRun 执行前先过配额闸门（gateUsage），成功后按源文本字符数计量，失败计入指标
//   - 驳回重翻循环：重翻成功后清空驳回意见，避免下次运行重复重翻
//   - 批准审批后触发自迭代（feedback 步骤）；驳回时记录原因与建议
//   - 工单操作均写入审计；工单查询全部限定生效租户（租户隔离）

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"github.com/xuri/excelize/v2"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/fileproc"
	"translator/internal/orchestrator"
	"translator/internal/qa"
	"translator/internal/store"
)

// ============ 工单 ============

// handleTickets 工单列表接口（tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 mine=1 仅显示本人创建的工单）。
// 返回: success=true 时携带 tickets 数组。
func (s *Server) handleTickets(w http.ResponseWriter, r *http.Request) {
	// 鉴权：登录用户即可查看工单（隐私：非超管强制仅自己的工单）
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 隐私隔离：除超管外，一律只返回当前用户创建的工单（涉及用户隐私）
	onlyMine := !auth.IsSuperAdmin(u)
	// 租户隔离：仅查询生效租户下的工单
	tickets, err := s.Store.ListTickets(s.effTenant(r, u), u.ID, onlyMine)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// ★ 运行中工单附实时进度百分比（后端唯一真源；仅运行中才查状态，避免全量 N+1）
	type ticketWithProgress struct {
		*store.Ticket
		Progress int `json:"progress"`
	}
	out := make([]ticketWithProgress, 0, len(tickets))
	for _, t := range tickets {
		if t.Status != "queued" && t.Status != "in_progress" {
			p := 0
			if t.Status == "completed" || t.Status == "rejected" {
				p = 100
			}
			out = append(out, ticketWithProgress{t, p})
			continue
		}
		states, _ := s.Store.TicketStates(t.ID)
		tfiles, _ := s.Store.TicketFiles(t.ID)
		pct := 10
		if t.Status == "in_progress" {
			pct = ticketProgressPct(t, states, tfiles)
		}
		out = append(out, ticketWithProgress{t, pct})
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": out})
}

// handleTicketCreate 创建工单接口（tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 title/source_text/target_langs）。
// 返回: success=true 时携带新工单对象。
func (s *Server) handleTicketCreate(w http.ResponseWriter, r *http.Request) {
	// 鉴权：登录用户即可创建工单（隐私隔离：仅创建者可见）
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		Title       string `json:"title"`        // 工单标题
		SourceText  string `json:"source_text"`  // 待翻译源文本（必填）
		TargetLangs string `json:"target_langs"` // 目标语言列表（逗号分隔，默认 en）
		Mode        string `json:"mode"`         // 翻译模式：fast | pro（默认 pro）
		MaxLength   int64  `json:"max_length"`   // ★ 缩翻最长字符限制（0=未启用）
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.SourceText == "" {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请提供源文本"})
		return
	}
	// 默认目标语言 en
	if req.TargetLangs == "" {
		req.TargetLangs = "en"
	}
	// ★ 整改 C6：建单即自动执行，必须先过配额闸门（此前闸门只在手动 run 存在，
	//   自动入队路径使 QPS/日额/余额闸对建单入口形同虚设）
	if _, release, gerr := s.gateUsage(r); gerr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": gerr.Error()})
		return
	} else {
		release()
	}
	// 创建工单（归属生效租户）
	t, err := s.Store.CreateTicket(s.effTenant(r, u), u.ID, req.Title, req.SourceText, "", req.TargetLangs)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// ★ 翻译模式随创建请求落库（fast=快速 / pro=专业校对；空值归一化为 pro）
	t.Mode = normalizeTaskMode(req.Mode)
	// ★ 缩翻（任务7）：最长字符限制随建单落库（0=未启用缩翻）
	t.MaxLength = req.MaxLength
	if t.MaxLength > 0 {
		_ = s.Store.UpdateTicket(t)
	}
	// 创建工单审计 + 自动入队执行（★ 整改 C6：入队用 Background——请求 ctx 在响应返回后
	// 即取消，异步任务不得绑定其生命周期）
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_create", "tickets", t.TicketNo)
	if s.TicketSvc != nil {
		t.Status = store.TicketQueued
		_ = s.Store.UpdateTicket(t)
		_, _ = s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// handleTicketCreateFile 文件工单创建（multipart：file/title/target_langs）。
// 文件保存至上传目录 tickets/ 子目录；创建后自动入队，worker 走原格式回写流水线。
func (s *Server) handleTicketCreateFile(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 上传上限与 multipart 解析（与即时翻译对齐：translateUploadMax=40MB，多文件共享总上限）
	r.Body = http.MaxBytesReader(w, r.Body, translateUploadMax)
	if err := r.ParseMultipartForm(translateUploadMax); err != nil {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": fmt.Sprintf("文件解析失败或超过大小上限（%dMB）", translateUploadMax/1024/1024)})
		return
	}
	// 多文件：优先取 "files" 字段（可重复）；兼容旧单文件字段 "file"
	var headers []*multipart.FileHeader
	if r.MultipartForm != nil {
		headers = append(headers, r.MultipartForm.File["files"]...)
		if len(headers) == 0 {
			headers = append(headers, r.MultipartForm.File["file"]...)
		}
	}
	if len(headers) == 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少文件"})
		return
	}
	// 扩展名白名单（与 fileproc.ExtractTexts 支持的格式一致；逐文件校验）
	allowed := map[string]bool{
		".docx": true, ".xlsx": true, ".pptx": true, ".pdf": true,
		".txt": true, ".csv": true, ".srt": true, ".vtt": true,
		".md": true, ".json": true, ".yaml": true, ".yml": true,
	}
	for _, hdr := range headers {
		ext := strings.ToLower(filepath.Ext(hdr.Filename))
		if !allowed[ext] {
			writeJSON(w, 400, map[string]interface{}{"success": false,
				"message": "不支持的格式: " + hdr.Filename + "（仅支持 docx/xlsx/pptx/pdf/txt/csv/srt/vtt/md/json/yaml）"})
			return
		}
	}
	title := r.FormValue("title")
	targetLangs := r.FormValue("target_langs")
	if targetLangs == "" {
		targetLangs = "en"
	}
	// 逐个保存到 上传目录/tickets/
	dir := filepath.Join(s.Cfg.UploadDir, "tickets")
	_ = os.MkdirAll(dir, 0o755)
	saved := make([]struct{ path, name string }, 0, len(headers))
	for _, hdr := range headers {
		saveName := fmt.Sprintf("%d_%s", time.Now().UnixNano(), filepath.Base(hdr.Filename))
		savePath := filepath.Join(dir, saveName)
		src, ferr := hdr.Open()
		if ferr != nil {
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": "读取文件失败: " + hdr.Filename})
			return
		}
		out, cerr := os.Create(savePath)
		if cerr != nil {
			src.Close()
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": "保存文件失败"})
			return
		}
		if _, cerr = io.Copy(out, src); cerr != nil {
			out.Close()
			src.Close()
			writeJSON(w, 500, map[string]interface{}{"success": false, "message": "写入文件失败"})
			return
		}
		out.Close()
		src.Close()
		saved = append(saved, struct{ path, name string }{savePath, hdr.Filename})
	}
	// ★ 性能优化 Phase A1：PDF 前置拦截（大小/页数），超限直接友好拒绝，避免后台转换卡死
	for _, f := range saved {
		if perr := checkPdfLimits(f.path, f.name); perr != nil {
			for _, cleanup := range saved {
				os.Remove(cleanup.path)
			}
			writeJSON(w, 400, map[string]interface{}{"success": false, "message": perr.Error()})
			return
		}
	}
	if title == "" {
		title = saved[0].name + fmt.Sprintf(" 等 %d 个文件", len(saved))
		if len(saved) == 1 {
			title = saved[0].name
		}
	}
	// ★ 整改 C6：建文件工单同样先过配额闸门（自动入队路径此前完全绕闸）
	if _, release, gerr := s.gateUsage(r); gerr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": gerr.Error()})
		return
	} else {
		release()
	}
	// 创建工单（file_path 记首个文件，兼容旧列表展示；全部文件入 ticket_files 表）
	t, err := s.Store.CreateTicket(s.effTenant(r, u), u.ID, title, "", saved[0].path, targetLangs)
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// ★ 文件任务模式落库（multipart mode 字段；空=pro）
	t.Mode = normalizeTaskMode(r.FormValue("mode"))
	// ★ 缩翻（任务7）：文件工单最长字符限制（multipart max_length；空=0 未启用）
	if n, perr := strconv.ParseInt(r.FormValue("max_length"), 10, 64); perr == nil && n > 0 {
		t.MaxLength = n
	}
	tid := s.effTenant(r, u)
	for _, f := range saved {
		_, _ = s.Store.AddTicketFile(&store.TicketFile{
			TenantID: tid, TicketID: t.ID, FileName: f.name, FilePath: f.path,
		})
		s.Store.RegisterArtifact(f.path, tid, u.ID, t.ID) // ★ 归属登记（评审整改 C1）
	}
	s.Store.LogAudit(tid, u.ID, "ticket_create_file", "tickets", fmt.Sprintf("%s (%d files)", t.TicketNo, len(saved)))
	if s.TicketSvc != nil {
		t.Status = store.TicketQueued
		_ = s.Store.UpdateTicket(t)
		// ★ 整改 C6：入队用 Background（请求 ctx 响应后即取消）
		_, _ = s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID)
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// handleTicketRun 运行工单流程接口（FlowDef 编排，tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id）。
// 返回: success=true 时携带运行后的工单对象（含流程结果）。
// 流程：取工单 → 配额闸门 → 置为进行中 → 执行编排 → 计量 → 清空驳回意见。
func (s *Server) handleTicketRun(w http.ResponseWriter, r *http.Request) {
	// 鉴权：登录用户；隐私：仅创建者或超管可运行
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		ID int64 `json:"id"` // 工单 ID
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	// 隐私校验：非创建者且非超管禁止运行他人工单
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权操作他人工单"})
		return
	}
	// ★ 整改 C5：状态机门槛——仅草稿/排队/被驳回可（重）跑。此前 completed 工单可被
	//   置回 queued 重复执行重复计费，cancelled/approved/pending_approval 重跑语义非法。
	switch t.Status {
	case store.TicketDraft, store.TicketQueued, store.TicketRejected:
		// 可执行状态
	default:
		writeJSON(w, 200, map[string]interface{}{"success": false,
			"message": "当前状态不可执行：仅待处理/排队中/被驳回的工单可以运行"})
		return
	}
	// 配额闸门：QPS/并发/每日上限/余额校验（不通过则拒绝运行）
	_, release, gateErr := s.gateUsage(r)
	defer release()
	if gateErr != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": gateErr.Error()})
		return
	}
	// ★ 异步入队：立即返回 ticket_no，worker 后台执行五步编排（大文件不阻塞 HTTP）
	if s.TicketSvc == nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单服务未初始化"})
		return
	}
	t.Status = store.TicketQueued
	_ = s.Store.UpdateTicket(t)
	// ★ 整改 C6：手动 run 的入队同样用 Background（与建单/审批路径口径统一）
	if _, err := s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "入队失败: " + err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_enqueue", "tickets", t.TicketNo)
	resp := map[string]interface{}{"success": true, "ticket": t, "queued": true}
	if strings.EqualFold(filepath.Ext(t.FilePath), ".pdf") && fileproc.PdfImageHeavy(t.FilePath) {
		resp["image_heavy"] = true
	}
	writeJSON(w, 200, resp)
}

// ticketProgressPct 工单进度百分比（步骤锚点阶梯，唯一真源）：
// 排队10 → 提取20 → 初翻40 → 校对60 → 回写80 → 完成100。
func ticketProgressPct(t *store.Ticket, states []*store.TicketState, files []*store.TicketFile) int {
	if t == nil {
		return 0
	}
	switch t.Status {
	case "completed":
		return 100
	case "rejected":
		return 100
	case "", "draft", "queued", "pending_approval":
		if t.Status == "queued" {
			return 10
		}
		return 5
	}
	pct := 5
	W := map[string]int{
		"upload": 20, "file_extract": 20, "extract": 20,
		"translate": 40, "file_translate": 40, "init_translation": 40,
		"proofread": 60, "qa": 60, "file_qa": 60, "quality_check": 60,
		"writeback": 80, "file_writeback": 80, "package": 80,
	}
	for _, x := range states {
		w, ok := W[x.Step]
		if !ok {
			continue
		}
		if x.Status == "success" || x.Status == "skipped" {
			if w > pct {
				pct = w
			}
		} else if x.Status == "running" {
			r := w - 10
			if r < w && r > pct {
				pct = r
			} else if r <= 10 && w > pct {
				pct = w
			}
		}
	}
	for _, f := range files {
		if f.ResultPath != "" || f.Error != "" {
			if pct < 80 {
				pct = 80
			}
			break
		}
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// handleTicketDetail 工单详情接口（含状态轨迹，tenant_admin 及以上）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（查询参数 id）。
// 返回: success=true 时携带 ticket（工单）与 states（状态轨迹数组）。
func (s *Server) handleTicketDetail(w http.ResponseWriter, r *http.Request) {
	// 鉴权：登录用户；隐私=创建者或超管
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 解析工单 ID（来自查询参数）
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(id, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	// 隐私校验：非创建者且非超管不可见他人工单详情
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权查看他人工单"})
		return
	}
	// 附带状态轨迹（流程步骤执行历史）
	states, _ := s.Store.TicketStates(id)
	// ★ 文件工单附带各文件处理状态（前端进度面板渲染用）
	tfiles, _ := s.Store.TicketFiles(id)
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t, "states": states, "files": tfiles, "progress": ticketProgressPct(t, states, tfiles)})
}

// handleTicketDownload 下载工单翻译结果（创建者或超管）。
// 文件工单：流式返回原格式回写产物（docx/xlsx/pptx）；
// 纯文本工单：动态生成 xlsx 对照表（源文+各语言列）。
func (s *Server) handleTicketDownload(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	idStr := r.URL.Query().Get("id")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	if id <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	t, err := s.Store.GetTicket(id, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权下载他人工单结果"})
		return
	}
	if t.Status != store.TicketCompleted {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单尚未完成"})
		return
	}
	baseName := t.TicketNo
	if baseName == "" {
		baseName = fmt.Sprintf("ticket_%d", t.ID)
	}
	// ⓪ 多文件工单：?file_id= 取单个文件产物；否则把全部产物打包 zip 返回
	tfiles, _ := s.Store.TicketFiles(t.ID)
	if len(tfiles) > 0 {
		// 单文件下载（可选 file_id 指定）
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
			writeJSON(w, 404, map[string]interface{}{"success": false, "message": "该文件的产物不存在"})
			return
		}
		// 全部产物打包 zip（跳过未生成/失败的文件）
		var paths []string
		var names []string
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
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "暂无已生成的产物（部分文件可能处理失败）"})
		return
	}
	// ① 原格式回写产物直接流式返回（旧单文件工单）
	if t.ResultPath != "" {
		f, ferr := os.Open(t.ResultPath)
		if ferr == nil {
			defer f.Close()
			w.Header().Set("Content-Disposition", `attachment; filename="`+baseName+filepath.Ext(t.ResultPath)+`"`)
			http.ServeContent(w, r, filepath.Base(t.ResultPath), time.Now(), f)
			return
		}
		// 结果文件丢失 → 落入 xlsx 对照表兜底
	}
	// ② 纯文本工单：从 FinalResult 解析译文生成 xlsx 对照表
	var payload struct {
		Translations map[string]string `json:"translations"`
		QAReport     *qa.Report        `json:"qa_report"`
	}
	_ = json.Unmarshal([]byte(t.FinalResult), &payload)
	if len(payload.Translations) == 0 {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "暂无可下载的翻译结果"})
		return
	}
	f := excelize.NewFile()
	sheet := "Sheet1"
	langs := make([]string, 0, len(payload.Translations))
	for lc := range payload.Translations {
		langs = append(langs, lc)
	}
	sort.Strings(langs)
	_ = f.SetSheetName(sheet, sheet)
	headers := []string{"source_text"}
	for _, lc := range langs {
		// ★ 需求5：表头语言码 → 语言中文名（en→英文、fr→法语），无映射时回退语言码
		name := config.LangNames[lc]
		if name == "" {
			name = lc
		}
		headers = append(headers, name)
	}
	// QA 列：存在质检报告时追加（error 级前缀 ✖，warning 级 ⚠）
	hasQA := payload.QAReport != nil && len(payload.QAReport.Issues) > 0
	if hasQA {
		headers = append(headers, "QA")
	}
	for i, h := range headers {
		cell, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, cell, h)
	}
	// 源文可能多段（换行分隔），逐段成行；单段则一行
	srcLines := strings.Split(t.SourceText, "\n")
	row := 2
	for _, line := range srcLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		_ = f.SetCellValue(sheet, cellName(1, row), line)
		for i, lc := range langs {
			_ = f.SetCellValue(sheet, cellName(i+2, row), payload.Translations[lc])
		}
		row++
	}
	if row == 2 {
		_ = f.SetCellValue(sheet, cellName(1, 2), t.SourceText)
		for i, lc := range langs {
			_ = f.SetCellValue(sheet, cellName(i+2, 2), payload.Translations[lc])
		}
	}
	// QA 汇总写入 QA 列首行数据行（整单一份报告，写在第 2 行末列）
	if hasQA {
		q := payload.QAReport
		var b strings.Builder
		fmt.Fprintf(&b, "pass=%v errors=%d warnings=%d", q.Pass, q.Errors, q.Warnings)
		for _, iss := range q.Issues {
			mark := "⚠"
			if iss.Level == "error" {
				mark = "✖"
			}
			fmt.Fprintf(&b, "\n%s [%s/%s] %s", mark, iss.Lang, iss.Rule, iss.Detail)
		}
		_ = f.SetCellValue(sheet, cellName(len(headers), 2), b.String())
	}
	buf, err := f.WriteToBuffer()
	if err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": "生成 xlsx 失败"})
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+baseName+`.xlsx"`)
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	_, _ = w.Write(buf.Bytes())
}

// cellName 行列号转单元格名（excelize.CoordinatesToCellName 的本地简写）。
func cellName(col, row int) string {
	n, _ := excelize.CoordinatesToCellName(col, row)
	return n
}

// ============ 审批（approver + admin） ============

// handleApproveList 待审批列表接口（approver + admin 角色，权限等级 >=2）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求。返回: success=true 时携带待审批 tickets 数组。
func (s *Server) handleApproveList(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 权限校验：需角色等级 >= 2（approver/admin/tenant_admin/super_admin）
	if err := auth.RequireRole(u, 2); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 租户隔离：仅列出生效租户下待审批工单
	tickets, err := s.Store.ListPendingApproval(s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "tickets": tickets})
}

// handleApproveAction 审批操作接口：approve（批准）/ reject（驳回，带意见）。
// 参数 w: HTTP 响应写入器；r: HTTP 请求（body 含 id/action/reason/suggestion/approved_text）。
// 返回: success=true 表示审批完成；批准会触发自迭代（feedback 步骤）。
func (s *Server) handleApproveAction(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	// 权限校验：需角色等级 >= 2
	if err := auth.RequireRole(u, 2); err != nil {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	var req struct {
		ID           int64  `json:"id"`            // 工单 ID
		Action       string `json:"action"`        // approve / reject
		Reason       string `json:"reason"`        // 驳回原因
		Suggestion   string `json:"suggestion"`    // 修改建议（附加到驳回原因）
		ApprovedText string `json:"approved_text"` // 审批员可编辑终稿
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	// 租户隔离：仅可取生效租户下的工单
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	// 状态机约束：仅待审批状态的工单可审批
	if t.Status != store.TicketPendingAppr {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不在待审批状态"})
		return
	}
	// 根据action 更新工单状态与审批字段
	switch req.Action {
	case "approve":
		t.Status = store.TicketApproved
		t.ApproverID = u.ID
		// ★ 整改 C4：终稿修订写回载荷 JSON（按语言定位合并覆盖），不再用裸文本整体替换
		//   FinalResult——旧实现撑破 JSON 结构，重跑流水线时被新建载荷静默丢弃。
		if req.ApprovedText != "" {
			if merged := applyApprovedTextOverride(t.FinalResult, req.ApprovedText); merged != "" {
				t.FinalResult = merged
				s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_edit", "tickets", t.TicketNo+" 审批终稿已并入载荷")
			} else {
				s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_edit_skip", "tickets", t.TicketNo+" 终稿无法定位语言，保留原载荷")
			}
		}
	case "reject":
		t.Status = store.TicketRejected
		t.ApproverID = u.ID
		t.RejectReason = req.Reason
		if req.Suggestion != "" {
			t.RejectReason = strings.TrimSpace(req.Reason + "；建议: " + req.Suggestion)
		}
	default:
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "action 仅支持 approve/reject"})
		return
	}
	if err := s.Store.UpdateTicket(t); err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	// 审批审计
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "approve_"+req.Action, "tickets", t.TicketNo)

	// 批准 → 异步触发自迭代（feedback 步骤）。
	// ★ 整改 C4：改为入队后台执行——此前在 HTTP 请求内同步重跑整条流水线，
	//   客户端断连会让工单永久卡在 approved；配合 flow 层「approved 态仅 QA+feedback」
	//   短路（见 orchestrator.applyModeOverride），二次质检失败也不会翻案人工结论。
	if req.Action == "approve" && s.TicketSvc != nil {
		if _, enqErr := s.TicketSvc.EnqueueTicketRun(context.Background(), t.ID); enqErr == nil {
			writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t, "queued": true})
			return
		}
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "ticket": t})
}

// applyApprovedTextOverride 把审批员修订的终稿合并进工单载荷 JSON。
// 语言定位规则：① 与某语言现有译文完全一致 → 覆盖该语言；② 载荷仅一个语言 → 直接覆盖；
// 无法定位时返回空串（调用方保持原载荷不动并留痕审计）。返回合并后的 FinalResult。
func applyApprovedTextOverride(finalResult, approvedText string) string {
	var p struct {
		SourceText   string            `json:"source_text"`
		TargetLangs  []string          `json:"target_langs"`
		Translations map[string]string `json:"translations"`
		Sources      map[string]string `json:"sources"`
	}
	if json.Unmarshal([]byte(finalResult), &p) != nil || len(p.Translations) == 0 {
		return ""
	}
	hit := ""
	for lc, v := range p.Translations {
		if v == approvedText {
			hit = lc
			break
		}
	}
	if hit == "" && len(p.Translations) == 1 {
		for lc := range p.Translations {
			hit = lc
		}
	}
	if hit == "" {
		return ""
	}
	p.Translations[hit] = approvedText
	if p.Sources == nil {
		p.Sources = map[string]string{}
	}
	p.Sources[hit] = "approved"
	b, err := json.Marshal(p)
	if err != nil {
		return ""
	}
	return string(b)
}

// workflow 构建工作流（惰性，需 Store+Engine+Tenant 齐备）。
// 返回: 编排工作流实例；存储或引擎未初始化时返回 nil。
func (s *Server) workflow() *orchestrator.Workflow {
	if s.Store == nil || s.Engine == nil {
		return nil
	}
	return orchestrator.NewWorkflow(s.Store, s.Engine, s.Ten, s.DB)
}

// resultFileName 生成多文件工单产物在 zip 中的文件名（原文件名 + 语言后缀 + 原产物扩展名）。
// 参数 f: 工单文件行；返回形如 "报告_zh.docx"。
func resultFileName(f *store.TicketFile) string {
	base := f.FileName
	if base == "" {
		base = fmt.Sprintf("file_%d", f.ID)
	}
	ext := filepath.Ext(f.ResultPath)
	if ext == "" {
		ext = filepath.Ext(base)
	}
	return strings.TrimSuffix(base, filepath.Ext(base)) + ext
}

// mimeEscape Content-Disposition 文件名兜底转义（非 ASCII 场景由前端 zip 名承担）。
func mimeEscape(name string) string {
	return strings.ReplaceAll(name, `"`, `_`)
}

// handleTicketDelete 删除已完成工单及其关联文件（创建者或超管；弹窗确认由前端负责）。
func (s *Server) handleTicketDelete(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "请求格式错误"})
		return
	}
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 404, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权删除他人工单"})
		return
	}
	if t.Status == store.TicketInProgress || t.Status == store.TicketQueued {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "工单正在翻译中，无法删除"})
		return
	}
	if err := s.Store.DeleteTicketWithFiles(req.ID, s.effTenant(r, u)); err != nil {
		writeJSON(w, 500, map[string]interface{}{"success": false, "message": err.Error()})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_delete", "tickets", t.TicketNo)
	writeJSON(w, 200, map[string]interface{}{"success": true})
}

// handleTicketCancel 用户取消工单：仅创建者或超管；排队中/翻译中可取消。
// 取消为标记式：worker 收尾前复查状态，已取消则放弃计费/完成态/通知。
func (s *Server) handleTicketCancel(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		writeJSON(w, 401, map[string]interface{}{"success": false, "message": "未登录"})
		return
	}
	var req struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID <= 0 {
		writeJSON(w, 400, map[string]interface{}{"success": false, "message": "缺少工单 id"})
		return
	}
	t, err := s.Store.GetTicket(req.ID, s.effTenant(r, u))
	if err != nil {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "工单不存在"})
		return
	}
	if t.CreatedBy != u.ID && !auth.IsSuperAdmin(u) {
		writeJSON(w, 403, map[string]interface{}{"success": false, "message": "无权取消他人工单"})
		return
	}
	if t.Status != store.TicketQueued && t.Status != store.TicketInProgress {
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": "当前状态不可取消（仅排队中/翻译中）"})
		return
	}
	if err := s.Store.CancelTicket(req.ID); err != nil {
		msg := err.Error()
		// ★ 用户友好映射：底层并发锁冲突对用户表现为「系统繁忙」
		if strings.Contains(msg, "SQLITE_BUSY") || strings.Contains(msg, "database is locked") {
			msg = "系统繁忙，工单取消未成功，请稍候重试"
		}
		writeJSON(w, 200, map[string]interface{}{"success": false, "message": msg})
		return
	}
	s.Store.LogAudit(s.effTenant(r, u), u.ID, "ticket_cancel", "tickets", t.TicketNo)
	s.Store.SetTicketState(t.ID, "cancel", "success", "by user")
	writeJSON(w, 200, map[string]interface{}{"success": true})
}
