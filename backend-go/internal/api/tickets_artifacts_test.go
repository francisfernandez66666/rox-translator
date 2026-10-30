// ============ tickets_artifacts_test.go · 职责说明 ============
// 两段式交付的**接口面**锁（★ 2026-10-30 三事合一批 · 组5，B2/B3/B4/B5）。
//
// 四件事，全部走真 handler（不直接调内部函数）：
//
//	B2 详情：进行中只有 md 行、没有 original 行；完成后两组行齐全（这是客户看到的"两段"）；
//	B3 越权：未登录 401 / 他人 403 / 跨租户 404 / aid 不属于该单 404（不泄露存在性）；
//	B4 预览：只认 md；超 512KB 截断且 X-Truncated: 1；Content-Type 正确；
//	B5 单件下载：aid 不属于该单 404；行在文件不在 ⇒ 404 + 可行动文案（不是 200 空体）。
//
// 夹具：内存 SQLite + 真 user/token + 临时目录里的真产物文件（不碰真实上传目录）。
// 方言自钉 sqlite（AGENTS §一·4）。
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// artProbe 接口面夹具：一个租户 1 的创建者、一个同租户他人、一个租户 2 的用户。
type artProbe struct {
	s        *Server
	st       *store.Store
	dir      string
	owner    *store.User
	tok      string
	otherTok string
	t2Tok    string
}

// newArtProbe 建最小服务栈（真 auth：登录态走 JWT 校验，不 mock）。
func newArtProbe(t *testing.T) *artProbe {
	t.Helper()
	pinSqliteDialect(t)
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	st, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New 失败: %v", err)
	}
	p := &artProbe{s: &Server{Store: st}, st: st, dir: t.TempDir()}
	mk := func(tid int64, name string) (*store.User, string) {
		u, cerr := st.CreateUser(tid, name, auth.PasswordHash("pw123456"), name, store.RoleTenantAdmin, 0, 0)
		if cerr != nil {
			t.Fatalf("建用户 %s 失败: %v", name, cerr)
		}
		tok, serr := auth.Sign(u, time.Hour)
		if serr != nil {
			t.Fatalf("签 token 失败: %v", serr)
		}
		return u, tok
	}
	p.owner, p.tok = mk(1, "art_owner")
	_, p.otherTok = mk(1, "art_other")
	_, p.t2Tok = mk(2, "art_tenant2")
	return p
}

// mkTicket 建一张工单并置指定状态；filePath 非空表示文件工单。
func (p *artProbe) mkTicket(t *testing.T, status, filePath string) *store.Ticket {
	t.Helper()
	tk, err := p.st.CreateTicket(1, p.owner.ID, "两段式接口闸工单", "第一段。\n第二段。", filePath, "en")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	tk.Status = status
	if filePath != "" {
		tk.FilePath = filePath
	}
	if err := p.st.UpdateTicket(tk); err != nil {
		t.Fatalf("置状态失败: %v", err)
	}
	return tk
}

// addFile 挂一条子文件行（多文件工单）。
func (p *artProbe) addFile(t *testing.T, tk *store.Ticket, name string) *store.TicketFile {
	t.Helper()
	tf, err := p.st.AddTicketFile(&store.TicketFile{TenantID: 1, TicketID: tk.ID, FileName: name, FilePath: filepath.Join(p.dir, name)})
	if err != nil {
		t.Fatalf("挂文件行失败: %v", err)
	}
	return tf
}

// writeFile 在临时目录落一份真文件并返回路径（download 走 os.Stat/os.Open，必须是真文件）。
func (p *artProbe) writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写产物失败: %v", err)
	}
	return path
}

// addArtifact 登记一件产物（name 与磁盘 basename 一致，模拟服务层 sink 的写法）。
func (p *artProbe) addArtifact(t *testing.T, tk *store.Ticket, fileID int64, lang, kind, path string, primary, degraded bool) *store.TicketArtifact {
	t.Helper()
	a, err := p.st.UpsertTicketArtifact(&store.TicketArtifact{
		TenantID: 1, TicketID: tk.ID, FileID: fileID, Lang: lang, Kind: kind,
		Path: path, Name: filepath.Base(path), Primary: primary, Degraded: degraded,
	})
	if err != nil {
		t.Fatalf("登记产物失败: %v", err)
	}
	return a
}

// call 直调 handler（带可选 Bearer）。
func (p *artProbe) call(handler http.HandlerFunc, tok, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/tickets/x?"+query, nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// artID 整数转字符串。
func artID(v int64) string { return strconv.FormatInt(v, 10) }

// detailArtifacts 调详情接口并取 artifacts 数组（B2/B3 的读数）。
func (p *artProbe) detailArtifacts(t *testing.T, tok string, ticketID int64) (int, []struct {
	ID       int64  `json:"id"`
	FileID   int64  `json:"file_id"`
	Lang     string `json:"lang"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Primary  bool   `json:"primary"`
	Degraded bool   `json:"degraded"`
}) {
	t.Helper()
	rec := p.call(p.s.handleTicketDetail, tok, "id="+artID(ticketID))
	if rec.Code != http.StatusOK {
		t.Fatalf("详情应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Artifacts []struct {
			ID       int64  `json:"id"`
			FileID   int64  `json:"file_id"`
			Lang     string `json:"lang"`
			Kind     string `json:"kind"`
			Name     string `json:"name"`
			Size     int64  `json:"size"`
			Primary  bool   `json:"primary"`
			Degraded bool   `json:"degraded"`
		} `json:"artifacts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("详情响应解析失败: %v %s", err, rec.Body.String())
	}
	return len(resp.Artifacts), resp.Artifacts
}

// TestTicketArtifactsDetailTwoStages B2：进行中只有 md；完成后 md+original 齐全。
func TestTicketArtifactsDetailTwoStages(t *testing.T) {
	p := newArtProbe(t)
	srcPath := p.writeFile(t, "a.docx", "src")
	tk := p.mkTicket(t, store.TicketInProgress, srcPath)
	tf := p.addFile(t, tk, "a.docx")
	mdPath := p.writeFile(t, "a_en-text.md", "# 译文\n\n第一段\n")
	p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindMD, mdPath, false, false)

	n, list := p.detailArtifacts(t, p.tok, tk.ID)
	if n != 1 || list[0].Kind != store.TicketArtifactKindMD {
		t.Fatalf("进行中应只有 1 条 md 行，实得 %d 条: %+v", n, list)
	}
	for _, a := range list {
		if a.Kind == store.TicketArtifactKindOriginal {
			t.Fatalf("进行中不该出现 original 行（原格式件还在生成）：%+v", a)
		}
	}

	// 完成 + original 落地 ⇒ 两组行齐全，且 original 带 primary。
	tk.Status = store.TicketCompleted
	if err := p.st.UpdateTicket(tk); err != nil {
		t.Fatal(err)
	}
	origPath := p.writeFile(t, "a_en.docx", "docx")
	p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindOriginal, origPath, true, false)
	n2, list2 := p.detailArtifacts(t, p.tok, tk.ID)
	if n2 != 2 {
		t.Fatalf("完成后应有 md + original 两行，实得 %d: %+v", n2, list2)
	}
	var md, orig int
	for _, a := range list2 {
		if a.Kind == store.TicketArtifactKindMD {
			md++
		}
		if a.Kind == store.TicketArtifactKindOriginal {
			orig++
			if !a.Primary {
				t.Errorf("original 必须是该语言主产物（primary=1）：%+v", a)
			}
			if a.FileID != tf.ID {
				t.Errorf("file_id 必须指回 ticket_files 行：%+v", a)
			}
		}
	}
	if md != 1 || orig != 1 {
		t.Fatalf("两档各一件，实得 md=%d original=%d", md, orig)
	}
}

// TestTicketArtifactAccessControl B3：401 / 403 / 跨租户 404 / aid 不属于该单 404。
func TestTicketArtifactAccessControl(t *testing.T) {
	p := newArtProbe(t)
	tk := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "a.docx", "src"))
	tf := p.addFile(t, tk, "a.docx")
	mdPath := p.writeFile(t, "a_en-text.md", "译文")
	a := p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindMD, mdPath, true, false)

	if rec := p.call(p.s.handleTicketArtifact, "", "id="+artID(tk.ID)+"&aid="+artID(a.ID)); rec.Code != http.StatusUnauthorized {
		t.Errorf("未登录应 401，实得 %d", rec.Code)
	}
	if rec := p.call(p.s.handleTicketArtifact, p.otherTok, "id="+artID(tk.ID)+"&aid="+artID(a.ID)); rec.Code != http.StatusForbidden {
		t.Errorf("同租户他人应 403，实得 %d", rec.Code)
	}
	if rec := p.call(p.s.handleTicketArtifact, p.t2Tok, "id="+artID(tk.ID)+"&aid="+artID(a.ID)); rec.Code != http.StatusNotFound {
		t.Errorf("跨租户应 404（不泄露存在性），实得 %d: %s", rec.Code, rec.Body.String())
	}
	// aid 属于**别的单**（同一创建者）：必须 404，否则拿别单的 aid 就能越单取件。
	tk2 := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "b.docx", "src"))
	a2 := p.addArtifact(t, tk2, 0, "en", store.TicketArtifactKindMD, p.writeFile(t, "b_en-text.md", "x"), true, false)
	if rec := p.call(p.s.handleTicketArtifact, p.tok, "id="+artID(tk.ID)+"&aid="+artID(a2.ID)); rec.Code != http.StatusNotFound {
		t.Errorf("aid 不属于该单应 404，实得 %d: %s", rec.Code, rec.Body.String())
	}
	// 正向对照：自己的 aid 必须 200（否则上面三条"全 404"可能只是路由整体坏了）。
	rec := p.call(p.s.handleTicketArtifact, p.tok, "id="+artID(tk.ID)+"&aid="+artID(a.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("正向对照失败（自己的 md 应可下载）：%d %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != "译文" {
		t.Fatalf("下载体应与磁盘内容逐字一致，实得 %q", rec.Body.String())
	}
}

// TestTicketArtifactPreview B4：只认 md；超上限截断；Content-Type 正确。
func TestTicketArtifactPreview(t *testing.T) {
	p := newArtProbe(t)
	tk := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "a.docx", "src"))
	tf := p.addFile(t, tk, "a.docx")

	small := p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindMD,
		p.writeFile(t, "small.md", "## 标题\n\n正文"), false, false)
	orig := p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindOriginal,
		p.writeFile(t, "a_en.docx", "docx"), true, false)
	// 600KB 纯 ASCII：截断点不会落在多字节字符中间，长度可精确等值。
	bigBody := strings.Repeat("a", 600<<10)
	big := p.addArtifact(t, tk, tf.ID, "ja", store.TicketArtifactKindMD, p.writeFile(t, "big.md", bigBody), false, false)

	// ① 原格式件不能预览（前端只画 md 的预览）。
	if rec := p.call(p.s.handleTicketArtifactPreview, p.tok, "id="+artID(tk.ID)+"&aid="+artID(orig.ID)); rec.Code != http.StatusBadRequest {
		t.Errorf("original 预览应 400，实得 %d: %s", rec.Code, rec.Body.String())
	}
	// ② 正常 md：Content-Type 正确、无 X-Truncated。
	rec := p.call(p.s.handleTicketArtifactPreview, p.tok, "id="+artID(tk.ID)+"&aid="+artID(small.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("md 预览应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type 应为 text/markdown; charset=utf-8，实得 %q", ct)
	}
	if rec.Header().Get("X-Truncated") != "" {
		t.Error("未超上限时不该带 X-Truncated")
	}
	if rec.Body.String() != "## 标题\n\n正文" {
		t.Errorf("预览正文应与 md 内容一致，实得 %q", rec.Body.String())
	}
	// ③ 超 512KB：截断到上限、带 X-Truncated: 1。
	recBig := p.call(p.s.handleTicketArtifactPreview, p.tok, "id="+artID(tk.ID)+"&aid="+artID(big.ID))
	if recBig.Code != http.StatusOK {
		t.Fatalf("大 md 预览应 200，实得 %d", recBig.Code)
	}
	if recBig.Header().Get("X-Truncated") != "1" {
		t.Errorf("超上限必须带 X-Truncated: 1，实得 %q", recBig.Header().Get("X-Truncated"))
	}
	if got := recBig.Body.Len(); got != ticketArtifactPreviewMax {
		t.Errorf("截断后长度应恰为上限 %d，实得 %d", ticketArtifactPreviewMax, got)
	}
}

// TestTicketArtifactDownloadMissingFile B5：行在、文件不在 ⇒ 404 + 可行动文案。
func TestTicketArtifactDownloadMissingFile(t *testing.T) {
	p := newArtProbe(t)
	tk := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "a.docx", "src"))
	tf := p.addFile(t, tk, "a.docx")
	path := p.writeFile(t, "gone.md", "x")
	a := p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindMD, path, true, false)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	rec := p.call(p.s.handleTicketArtifact, p.tok, "id="+artID(tk.ID)+"&aid="+artID(a.ID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("文件丢失应 404（不是 200 空体），实得 %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "保留期") {
		t.Errorf("404 文案应可行动（点名过期/保留期），实得 %s", rec.Body.String())
	}
}
