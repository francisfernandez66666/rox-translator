// ============ editor.go · 职责说明 ============
// 对照编辑器接口（工作流 D，新 feature）：
//   - GET  /api/tickets/segments?id=&lang=   读取工单的源文/译文逐段对照 + 术语表（供前端双栏编辑器）
//   - POST /api/tickets/segments?id=&lang=   保存逐段编辑/通过/驳回批注到 translation_edits
// 文本工单解析 FinalResult；文件工单解析产物（xlsx/csv 对照表），docx/pdf 等二进制暂不支持在线逐段编辑。
// 所有接口要求登录且工单归属当前租户（超管可跨租户）。
// =============================================
package api

import (
	"encoding/csv"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
	apierrors "translator/internal/errors"
	"translator/internal/store"
)

// EditorSegment 单段对照（前端双栏编辑器的一行）。
type EditorSegment struct {
	Index       int    `json:"index"`
	Source      string `json:"source"`
	Target      string `json:"target"`
	EditedText  string `json:"edited_text"`
	Status      string `json:"status"`
	Note        string `json:"note"`
}

// routesEditor 注册对照编辑器相关路由。
func (s *Server) routesEditor() {
	s.mux.HandleFunc("/api/tickets/segments", s.handleTicketSegments)
	s.mux.HandleFunc("/api/tickets/segments/save", s.handleSaveSegments)
}

// handleTicketSegments 读取工单逐段对照 + 术语表。
func (s *Server) handleTicketSegments(w http.ResponseWriter, r *http.Request) {
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
	if u.TenantID != t.TenantID && u.TenantID != 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权访问该工单"))
		return
	}

	base, supported := s.extractSegments(t, lang)
	// 叠加已保存的编辑（edited_text/status/note）
	edits, _ := s.Store.GetTranslationEdits(id, lang)
	editMap := map[int]*store.TranslationEdit{}
	for i := range edits {
		editMap[edits[i].SegIndex] = &edits[i]
	}
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

	typ := "text"
	if t.FilePath != "" {
		if supported {
			typ = "file"
		} else {
			typ = "unsupported"
		}
	}

	terms, _ := s.Store.ListKBTerms(t.TenantID, lang, 200)

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
	if u.TenantID != t.TenantID && u.TenantID != 0 {
		s.writeError(w, r, apierrors.New(apierrors.ErrForbidden, "无权访问该工单"))
		return
	}

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

type baseSeg struct {
	Index  int
	Source string
	Target string
}

// extractSegments 从工单抽取源文/译文逐段对照。
// 返回 (段落列表, 是否支持在线编辑)。文件工单仅 xlsx/csv 对照表支持；其余返回 supported=false。
func (s *Server) extractSegments(t *store.Ticket, lang string) ([]baseSeg, bool) {
	if t.FilePath == "" {
		return extractTextSegments(t, lang), true
	}
	// 文件工单：取主产物路径
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

// parseTicketIDLang 从查询参数解析工单 ID 与语言（lang 缺省取工单首目标语言）。
func (s *Server) parseTicketIDLang(r *http.Request) (int64, string) {
	id, _ := parseInt64(r.URL.Query().Get("id"))
	lang := r.URL.Query().Get("lang")
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
