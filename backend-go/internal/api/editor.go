// ============ editor.go · 职责说明 ============
// 对照编辑器接口（工作流 D，新 feature）：
//   - GET  /api/tickets/segments?id=&lang=   读取工单的源文/译文逐段对照 + 术语表（供前端双栏编辑器）
//   - POST /api/tickets/segments?id=&lang=   保存逐段编辑/通过/驳回批注到 translation_edits
//
// 文本工单解析 FinalResult；文件工单解析产物（xlsx/csv 对照表），docx/pdf 等二进制暂不支持在线逐段编辑。
// 所有接口要求登录且工单归属当前租户（超管可跨租户）。
// =============================================
package api

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"translator/internal/auth"

	"github.com/xuri/excelize/v2"
	"translator/internal/doc"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// EditorSegment 单段对照（前端双栏编辑器的一行）。
type EditorSegment struct {
	Index      int    `json:"index"`
	Source     string `json:"source"`
	Target     string `json:"target"`
	EditedText string `json:"edited_text"`
	Status     string `json:"status"`
	Note       string `json:"note"`
}

// routesEditor 注册对照编辑器相关路由。
func (s *Server) routesEditor() {
	s.mux.HandleFunc("/api/tickets/segments", s.handleTicketSegments)
	s.mux.HandleFunc("/api/tickets/segments/save", s.handleSaveSegments)
	s.mux.HandleFunc("/api/tickets/segments/export", s.handleExportSegments)
	s.mux.HandleFunc("/api/editor/export/download", s.handleEditorExportDownload)
}

// canEditTicket ★ A4 授权口径（2026-09-12，替代旧「同租户任意用户可读写」）：
// 创建者 ∪ 同租户审校及以上（role 等级 ≥2：approver/租管，与审批工作台口径一致）
// ∪ 超管。旧实现只比对 TenantID，同租户普通成员可读写他人工单全部译文段，
// 与 tickets.go「隐私=创建者或超管」冲突——在线编辑作为审批工具向 ≥2 级放开。
func canEditTicket(u *store.User, t *store.Ticket) bool {
	if u == nil || t == nil {
		return false
	}
	if auth.IsSuperAdmin(u) {
		return true
	}
	if u.TenantID != t.TenantID {
		return false
	}
	return t.CreatedBy == u.ID || auth.RoleLevel(u.Role) >= 2
}

// editorDeny 统一拒绝响应（读接口按 403；不泄漏工单存在性由上层 GetTicketGlobal 已兜底）。
func (s *Server) editorDeny(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权访问该工单"))
}

// handleTicketSegments 读取工单逐段对照 + 术语表。
func (s *Server) handleTicketSegments(w http.ResponseWriter, r *http.Request) {
	// 用户鉴权
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录或登录已失效"))
		return
	}
	// 解析工单 ID 和目标语言
	id, lang := s.parseTicketIDLang(r)
	// 获取工单信息
	t, err := s.Store.GetTicketGlobal(id)
	if err != nil || t == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	// ★ A4：创建者 ∪ 同租户审校及以上 ∪ 超管（旧版仅租户级，同租户可越权读写他人工单）
	if !canEditTicket(u, t) {
		s.editorDeny(w, r)
		return
	}

	// 提取工单的源文/译文逐段对照
	base, supported := s.extractSegments(t, lang)
	// 叠加已保存的编辑（edited_text/status/note）
	edits, _ := s.Store.GetTranslationEdits(id, lang)
	editMap := map[int]*store.TranslationEdit{}
	for i := range edits {
		editMap[edits[i].SegIndex] = &edits[i]
	}
	// 组装最终输出：基础段落 + 编辑记录
	out := make([]EditorSegment, 0, len(base))
	for _, b := range base {
		seg := EditorSegment{Index: b.Index, Source: b.Source, Target: b.Target}
		if e, ok := editMap[b.Index]; ok {
			seg.EditedText = e.EditedText
			seg.Status = e.Status
			seg.Note = e.Note
		}
		out = append(out, seg)
	}

	// 判断工单类型：文本/文件/不支持在线编辑
	typ := "text"
	if t.FilePath != "" {
		if supported {
			typ = "file"
		} else {
			typ = "unsupported"
		}
	}

	// 获取术语表（供前端编辑器展示）
	terms, _ := s.Store.ListKBTerms(t.TenantID, lang, 200)

	// 解析目标语言列表
	langs := []string{}
	for _, l := range strings.Split(t.TargetLangs, ",") {
		if l = strings.TrimSpace(l); l != "" {
			langs = append(langs, l)
		}
	}

	writeJSON(w, 200, map[string]interface{}{
		"success":   true,
		"ticket_id": id,
		"lang":      lang,
		"langs":     langs,
		"type":      typ,
		"segments":  out,
		"terms":     terms,
	})
}

// handleSaveSegments 保存逐段编辑/通过/驳回批注。
func (s *Server) handleSaveSegments(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录或登录已失效"))
		return
	}
	id, lang := s.parseTicketIDLang(r)
	t, err := s.Store.GetTicketGlobal(id)
	if err != nil || t == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	if !canEditTicket(u, t) {
		s.editorDeny(w, r)
		return
	}

	// 守门链：登录 → 工单存在 → 可编辑权限；body 为批量 edits（index/edited_text/status/note）
	var req struct {
		Edits []struct {
			Index      int    `json:"index"`
			EditedText string `json:"edited_text"`
			Status     string `json:"status"` // approved / rejected / pending
			Note       string `json:"note"`
		} `json:"edits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "请求格式错误"))
		return
	}

	// 重新提取基础段落，以取回 source/target 用于落库（edited 仅存修订译文）
	base, _ := s.extractSegments(t, lang)
	baseMap := map[int]baseSeg{}
	for _, b := range base {
		baseMap[b.Index] = b
	}

	for _, e := range req.Edits {
		status := e.Status
		if status == "" {
			status = "pending"
		}
		bs := baseMap[e.Index]
		if err := s.Store.UpsertTranslationEdit(t.TenantID, id, lang, e.Index,
			bs.Source, bs.Target, e.EditedText, status, e.Note, u.ID); err != nil {
			s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "保存失败: "+err.Error()))
			return
		}
	}

	writeJSON(w, 200, map[string]interface{}{"success": true, "saved": len(req.Edits)})
}

// ---- 段落提取 ----

// baseSeg 基础段落（段落提取的中间产物，尚未叠加编辑记录）。
type baseSeg struct {
	Index  int
	Source string
	Target string
}

// extractSegments 从工单抽取源文/译文逐段对照。
// 返回 (段落列表, 是否支持在线编辑)。文本/xlsx/csv/docx/pdf 均支持；其余返回 supported=false。
func (s *Server) extractSegments(t *store.Ticket, lang string) ([]baseSeg, bool) {
	if t.FilePath == "" {
		return extractTextSegments(t, lang), true
	}
	// 文件工单：docx/pdf 走 Office 段落抽取（源文取原文件、译文取结果文件，按段对齐）
	if doc.IsOfficeDoc(t.FilePath) || doc.IsPDF(t.FilePath) {
		return s.extractOfficeSegments(t, lang)
	}
	// xlsx/csv 对照表
	path := t.ResultPath
	if path == "" {
		if files, _ := s.Store.TicketFiles(t.ID); len(files) > 0 {
			path = files[0].ResultPath
		}
	}
	if path == "" {
		return nil, false
	}
	segs, ok := parseAlignedFile(path, lang)
	return segs, ok
}

// extractOfficeSegments 抽取 docx/pdf 工单的源文/译文段落对照。
// 源文取原始上传文件段落；译文取翻译结果文件（docx/pdf）段落，按段落索引对齐。
// 未生成结果文件时返回 supported=false（前端提示「翻译完成后可在线编辑」）。
//
// ★ 2026-09-18 PDF 分支改走真值表：PDF 工单的源文与译本是**两次独立**的 pdf2docx 转换产物，
// 段落切分粒度必然不同（实测同一工单源 504 段 / 译本 578 段），按下标 min() 硬对齐会让双栏
// 编辑器显示成「大量块不匹配」（抽查 20 对 100% 错位）。翻译时手上本就有
// texts[i] ↔ translations[texts[i]] 的精确配对，已落进 ticket_segments（独立表，前端无感知），
// 这里优先读它。读不到（历史工单 / 落库失败 / 非文件工单）时静默回退原有口径。
// docx 分支保持原样：写回在同一份文件上进行，两侧段落结构一致，按下标对齐本来就是对的。
func (s *Server) extractOfficeSegments(t *store.Ticket, lang string) ([]baseSeg, bool) {
	var srcParas []string
	var err error
	switch {
	case doc.IsOfficeDoc(t.FilePath):
		srcParas, err = doc.DocxParagraphs(t.FilePath)
	case doc.IsPDF(t.FilePath):
		// 真值优先：命中即返回，段落数与段序号都来自翻译当时的提取顺序
		if segs, ok := s.segmentsFromStore(t, lang); ok {
			return segs, true
		}
		srcParas, err = doc.PDFToParagraphs(t.FilePath)
	default:
		return nil, false
	}
	if err != nil || len(srcParas) == 0 {
		return nil, false
	}
	// 译文：取结果文件段落（与源文段数未必一致，按短者对齐）
	tgtPath := t.ResultPath
	if tgtPath == "" {
		if files, _ := s.Store.TicketFiles(t.ID); len(files) > 0 {
			tgtPath = files[0].ResultPath
		}
	}
	var tgtParas []string
	if tgtPath != "" {
		switch {
		case doc.IsOfficeDoc(tgtPath):
			tgtParas, _ = doc.DocxParagraphs(tgtPath)
		case doc.IsPDF(tgtPath):
			tgtParas, _ = doc.PDFToParagraphs(tgtPath)
		}
	}
	n := len(srcParas)
	if len(tgtParas) < n {
		n = len(tgtParas)
	}
	segs := make([]baseSeg, 0, n)
	// 按段落下标一一对齐源文与目标文，构成可编辑片段
	for i := 0; i < n; i++ {
		segs = append(segs, baseSeg{Index: i, Source: srcParas[i], Target: tgtParas[i]})
	}
	return segs, true
}

// segmentsFromStore 从 ticket_segments 真值表取「源文段→译文段」逐段对照。
// 参数：t=工单（用 ID + FilePath 定位）；lang=目标语言。
// 返回：(段列表, 是否命中)。表里没有该工单该文件该语言的记录时返回 (nil, false)，
// 调用方据此回退旧口径——「没有数据」不是错误，不要把历史工单变成不可用。
// Index 直接用落库时的 seg_index（写入时按提取顺序 0..n-1 连续，故对外仍是连续下标）。
//
// ⚠️ 段序号口径切换的连带影响（已知取舍）：translation_edits 也按 seg_index 挂段，
// 历史 PDF 工单若在旧口径（源/译两份独立转换结果按下标 min() 硬对齐）下存过编辑记录，
// 切到真值口径后段号会整体重排（两侧段数本就不同），旧批注可能落到别的段上。
// 两种口径无法同时对齐，选择以「当前真值口径」为准；旧数据段数更少/错位本就不可信。
func (s *Server) segmentsFromStore(t *store.Ticket, lang string) ([]baseSeg, bool) {
	rows, err := s.Store.GetTicketSegments(t.ID, t.FilePath, lang)
	if err != nil || len(rows) == 0 {
		// 查询失败也一律当「未命中」走回退：真值表是附加数据，不能因为它抖动就让
		// 对照编辑器整页 500（回退用的旧口径本就可用）。
		return nil, false
	}
	segs := make([]baseSeg, 0, len(rows))
	for _, r := range rows {
		segs = append(segs, baseSeg{Index: r.SegIndex, Source: r.Source, Target: r.Target})
	}
	return segs, true
}

// extractTextSegments 文本工单：按行对齐 FinalResult.translations 与 SourceText。
func extractTextSegments(t *store.Ticket, lang string) []baseSeg {
	var payload struct {
		Translations map[string]string `json:"translations"`
	}
	_ = json.Unmarshal([]byte(t.FinalResult), &payload)
	tgt := payload.Translations[lang]
	srcLines := splitLines(t.SourceText)
	tgtLines := splitLines(tgt)
	segs := make([]baseSeg, 0, len(srcLines))
	for i, src := range srcLines {
		target := ""
		if i < len(tgtLines) {
			target = tgtLines[i]
		}
		segs = append(segs, baseSeg{Index: i, Source: src, Target: target})
	}
	return segs
}

// alignTicketRow 计算 xlsx 对照表第 rowIdx 行（源文行）、目标语言 lang 的单元格译文。
// 参数：t=工单对象；srcLines=splitLines(t.SourceText)（源文各行）；lang=目标语言；rowIdx=源文行下标。
// 返回：该单元格内容。对齐口径：
//   - 译文行数与源文行数一致 → 逐行对应（extractTextSegments 的按行映射，杜绝整段译文重复填每行）；
//   - 译文行数不一致（译文更少/更多）→ 首行填整段译文、其余行留空，保证每格只含一段、不丢信息。
func alignTicketRow(t *store.Ticket, srcLines []string, lang string, rowIdx int) string {
	segs := extractTextSegments(t, lang)
	if len(segs) == len(srcLines) && rowIdx < len(segs) {
		return segs[rowIdx].Target
	}
	if rowIdx == 0 {
		var payload struct {
			Translations map[string]string `json:"translations"`
		}
		_ = json.Unmarshal([]byte(t.FinalResult), &payload)
		return payload.Translations[lang]
	}
	return ""
}

// parseAlignedFile 解析 xlsx/csv 对照表为逐段对照（首列源文，目标语言列为译文）。
func parseAlignedFile(path, lang string) ([]baseSeg, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".xlsx", ".xlsm":
		return parseXLSX(path, lang)
	case ".csv":
		return parseCSVFile(path, lang)
	default:
		return nil, false
	}
}

// parseXLSX 读取首个工作表：以表头定位 source_text（或首列）与目标语言列，逐数据行成段。
func parseXLSX(path, lang string) ([]baseSeg, bool) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	sheet := f.GetSheetName(0)
	rows, err := f.GetRows(sheet)
	if err != nil || len(rows) == 0 {
		return nil, false
	}
	srcIdx, tgtIdx := locateColumns(rows[0], lang)
	if srcIdx < 0 {
		return nil, false
	}
	var segs []baseSeg
	// 跳过表头逐行成段：源文为空的行丢弃，Index 按有效段落连续编号
	for i := 1; i < len(rows); i++ {
		row := rows[i]
		src := cell(row, srcIdx)
		if strings.TrimSpace(src) == "" {
			continue
		}
		tgt := ""
		if tgtIdx >= 0 {
			tgt = cell(row, tgtIdx)
		}
		segs = append(segs, baseSeg{Index: len(segs), Source: src, Target: tgt})
	}
	return segs, true
}

// parseCSVFile 解析 CSV 对照表（同 xlsx 列定位逻辑）。
func parseCSVFile(path, lang string) ([]baseSeg, bool) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer file.Close()
	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	records, err := reader.ReadAll()
	if err != nil || len(records) == 0 {
		return nil, false
	}
	srcIdx, tgtIdx := locateColumns(records[0], lang)
	if srcIdx < 0 {
		return nil, false
	}
	var segs []baseSeg
	// 同 xlsx 口径：FieldsPerRecord=-1 容忍不等宽行，空源文行丢弃
	for i := 1; i < len(records); i++ {
		row := records[i]
		src := cell(row, srcIdx)
		if strings.TrimSpace(src) == "" {
			continue
		}
		tgt := ""
		if tgtIdx >= 0 {
			tgt = cell(row, tgtIdx)
		}
		segs = append(segs, baseSeg{Index: len(segs), Source: src, Target: tgt})
	}
	return segs, true
}

// locateColumns 依据表头定位源文列与目标语言列。
// 返回 (sourceIdx, targetIdx)；找不到源文列时 sourceIdx=-1。
func locateColumns(header []string, lang string) (int, int) {
	srcIdx := -1
	tgtIdx := -1
	for i, h := range header {
		h = strings.TrimSpace(strings.ToLower(h))
		if h == "source_text" || h == "source" || h == "源文" || h == "原文" {
			srcIdx = i
		}
		if h == strings.ToLower(lang) {
			tgtIdx = i
		}
	}
	if srcIdx < 0 {
		// 未显式标 source_text：首列当作源文
		srcIdx = 0
	}
	if tgtIdx < 0 {
		// 未找到目标语言列：取源文列之后第一列
		if srcIdx+1 < len(header) {
			tgtIdx = srcIdx + 1
		}
	}
	return srcIdx, tgtIdx
}

// cell 安全取行内第 i 列（越界返回空串）。
func cell(row []string, i int) string {
	if i >= 0 && i < len(row) {
		return row[i]
	}
	return ""
}

// splitLines 按换行切分为非空段落。
func splitLines(s string) []string {
	parts := strings.Split(s, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{s}
	}
	return out
}

// handleExportSegments 审批后回写：按翻译编辑（edited_text）逐段重写结果 docx，导出修订稿。
// 仅支持 docx 结果文件的回写（pdf 结果先转 docx 的成本较高，MVP 限定 docx）。
func (s *Server) handleExportSegments(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录或登录已失效"))
		return
	}
	id, lang := s.parseTicketIDLang(r)
	t, err := s.Store.GetTicketGlobal(id)
	if err != nil || t == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrTicketNotFound, "工单不存在"))
		return
	}
	if !canEditTicket(u, t) {
		s.editorDeny(w, r)
		return
	}
	// 守门通过后取基础段落，装配逐段覆盖文本表 repl（有编辑译文则优先编辑稿）
	base, ok := s.extractSegments(t, lang)
	if !ok || len(base) == 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "该工单不支持在线回写（需 docx 结果文件）"))
		return
	}
	edits, _ := s.Store.GetTranslationEdits(id, lang)
	editMap := map[int]*store.TranslationEdit{}
	for i := range edits {
		editMap[edits[i].SegIndex] = &edits[i]
	}
	repl := make([]string, len(base))
	for i, b := range base {
		rep := b.Target
		if e, ok := editMap[i]; ok && e.EditedText != "" {
			rep = e.EditedText
		}
		repl[i] = rep
	}
	// 结果 docx 路径
	tgtPath := t.ResultPath
	if tgtPath == "" {
		if files, _ := s.Store.TicketFiles(t.ID); len(files) > 0 {
			tgtPath = files[0].ResultPath
		}
	}
	if tgtPath == "" || !doc.IsOfficeDoc(tgtPath) {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "回写仅支持 docx 结果文件（当前结果非 docx）"))
		return
	}
	dir := filepath.Dir(tgtPath)
	base2 := strings.TrimSuffix(filepath.Base(tgtPath), filepath.Ext(tgtPath))
	outPath := filepath.Join(dir, fmt.Sprintf("%s_%s_edited_%d.docx", base2, lang, time.Now().Unix()))
	if err := doc.DocxRewrite(tgtPath, outPath, repl); err != nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrInternal, "回写失败: "+err.Error()))
		return
	}
	// ★ B8：产物归属登记（同评审整改 C1 口径），下载侧凭登记行做越权拦截
	s.Store.RegisterArtifact(outPath, t.TenantID, u.ID, t.ID)
	rel, _ := filepath.Rel(s.Cfg.UploadDir, outPath)
	writeJSON(w, 200, map[string]interface{}{
		"success":  true,
		"download": "/api/editor/export/download?file=" + url.PathEscape(rel),
	})
}

// handleEditorExportDownload 鉴权后安全下载回写产物（限定在 UploadDir 内，防路径穿越）。
func (s *Server) handleEditorExportDownload(w http.ResponseWriter, r *http.Request) {
	u := s.authUser(r)
	if u == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrUnauthorized, "未登录或登录已失效"))
		return
	}
	file := r.URL.Query().Get("file")
	if file == "" {
		s.writeError(w, r, apierrors.New(apierrors.ErrValidation, "缺少 file 参数"))
		return
	}
	decoded, err := url.QueryUnescape(file)
	if err != nil {
		decoded = file
	}
	abs, ok := resolveSafePath([]string{s.Cfg.UploadDir}, decoded)
	if !ok {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "非法文件路径"))
		return
	}
	// ★ B8（2026-09-12）：归属校验——凡已登记产物必须命中「同租户 + 本人/租管以上/超管」；
	//   未登记文件一律 404（本端点只服务编辑器导出产物，无历史存量，不留灰度口子）。
	art, aerr := s.Store.GetArtifactByPath(abs)
	if aerr != nil || art == nil {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "文件不存在"))
		return
	}
	if !auth.IsSuperAdmin(u) && !(art.TenantID == u.TenantID &&
		(art.UserID == u.ID || auth.IsTenantAdmin(u))) {
		s.writeError(w, r, apierrors.New(apierrors.ErrNotFound, "文件不存在"))
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filepath.Base(abs)))
	http.ServeFile(w, r, abs)
}

// parseTicketIDLang 从查询参数解析工单标识与语言（lang 缺省取工单首目标语言）。
// ★ 兼容双标识（2026-09 修复）：前端可能粘贴数字 ID 或「工单号 T20260902...」，
//
//	后者经 id 数值解析得 0，需回退按 ticket_no 精确查行再取其 ID。
func (s *Server) parseTicketIDLang(r *http.Request) (int64, string) {
	raw := r.URL.Query().Get("id")
	id, _ := parseInt64(raw)
	lang := r.URL.Query().Get("lang")
	// 数字解析失败（粘贴工单号）→ 回退按工单号精确查找
	if id <= 0 && strings.TrimSpace(raw) != "" {
		if t, err := s.Store.GetTicketByNo(strings.TrimSpace(raw)); err == nil && t != nil {
			id = t.ID
		}
	}
	if lang == "" {
		if t, err := s.Store.GetTicketGlobal(id); err == nil && t != nil {
			if langs := strings.Split(t.TargetLangs, ","); len(langs) > 0 {
				lang = strings.TrimSpace(langs[0])
			}
		}
	}
	if lang == "" {
		lang = "en"
	}
	return id, lang
}
