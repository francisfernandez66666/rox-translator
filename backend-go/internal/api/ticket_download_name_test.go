// ============ ticket_download_name_test.go · 职责说明 ============
// ★ O-03（2026-10-24 云端 UAT 修复批）「译文产物文件名统一带 -<lang>」的**下载面**行为锁。
//
// 命名函数本身已由 engine/artifact_naming_test.go 钉住，但那一支只问纯函数；
// AGENTS §三 那条「判据级断言代替不了守卫级」在这里成立得很具体：
// 谁把 tickets.go 下载分支里的 `engine.DeliverableBaseName(f.ResultPath, …)`
// 换回 `f.FileName`（原始上传名），纯函数测试一字不动地全绿，
// 而现网形态立刻回到 UAT 报的那个样子——
//
//	① 多语言工单打进同一个 zip 时**每个条目录入名都相同** ⇒ 后写的覆盖先写的，
//	   客户解压只拿到一种语言，且零报错（zip 条目同名是合法输入，不是失败）；
//	② 单文件下载存回磁盘叫 `合同.docx`，与原件同名，本地一覆盖就把原文丢了。
//
// 因此本文件走**真 handler**（handleTicketDownload 的三条出栈口）：
//
//	DN-1 多产物 zip：条目名必须逐语言互不相同、且各含自己的 `-<lang>`；
//	DN-2 ?file_id= 单文件：Content-Disposition 必须带产物真实 basename（含 -en）；
//	DN-3 ?fmt=text&file_id= 纯文案旁路：同一口径，不许退回 `_译文` 那类旧名。
//
// 反向对照写在 DN-1 的断言里（两条名称若塌成一条，条目数就少于产物数）。
//
// 方言自钉 sqlite（AGENTS §一·4）；不连生产库、不碰真实上传目录，产物落 t.TempDir()。
// =============================================
package api

import (
	"archive/zip"
	"bytes"
	"database/sql"
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

// newDnProbe 建「租户 1 + 一个已完成的多语言文件工单 + 两份逐语言产物」的最小服务栈。
// 返回：Server、创建者 token、工单号、以及 zip 里应出现的两份产物 basename。
func newDnProbe(t *testing.T) (*Server, string, *store.Ticket) {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := st.CreateUser(1, "dn_owner", "hash", "下载名闸门创建者", store.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("建创建者失败: %v", err)
	}
	tok, err := auth.Sign(owner, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	tk, err := st.CreateTicket(1, owner.ID, "O-03 下载名闸门工单", "第一段。\n第二段。", "uat_sample.txt", "en,ja")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	dir := t.TempDir()
	// 两份「同一原件、不同语言」的产物——旧命名形态下这两个路径的 basename 会完全相同，
	// 那正是 zip 覆盖丢语言的现场。
	enPath := filepath.Join(dir, "uat_sample-en.txt")
	jaPath := filepath.Join(dir, "uat_sample-ja.txt")
	mdPath := filepath.Join(dir, "uat_sample-en.md")
	for _, p := range []string{enPath, jaPath, mdPath} {
		if werr := os.WriteFile(p, []byte("译文正文 "+filepath.Base(p)), 0o644); werr != nil {
			t.Fatalf("写产物样张失败: %v", werr)
		}
	}
	tk.Status = store.TicketCompleted
	tk.FilePath = filepath.Join(dir, "uat_sample.txt")
	if werr := os.WriteFile(tk.FilePath, []byte("源文"), 0o644); werr != nil {
		t.Fatalf("写源件失败: %v", werr)
	}
	if uerr := st.UpdateTicket(tk); uerr != nil {
		t.Fatalf("置完成态失败: %v", uerr)
	}
	// 两个子文件行（一份原件对应两种语言产物），result_path 走裸 SQL：
	// AddTicketFile 只登记源件，产物路径在生产是流水线回填的，这里照同一形态写。
	var ids []int64
	for _, f := range []struct {
		name string
		path string
		md   string
	}{{"uat_sample.txt", enPath, mdPath}, {"uat_sample.txt", jaPath, ""}} {
		tf, aerr := st.AddTicketFile(&store.TicketFile{TenantID: 1, TicketID: tk.ID, FileName: f.name, FilePath: tk.FilePath})
		if aerr != nil {
			t.Fatalf("登记子文件失败: %v", aerr)
		}
		if _, uerr := sqlDB.Exec("UPDATE ticket_files SET result_path=? WHERE id=?", f.path, tf.ID); uerr != nil {
			t.Fatalf("回填产物路径失败: %v", uerr)
		}
		if f.md != "" {
			if _, uerr := sqlDB.Exec("UPDATE ticket_files SET text_result_path=? WHERE id=?", f.md, tf.ID); uerr != nil {
				t.Fatalf("回填纯文案路径失败: %v", uerr)
			}
		}
		ids = append(ids, tf.ID)
	}
	_ = ids // 子文件行号由用例用 dnFileIDs 现读，夹具不往外传"编号顺序"这种隐式契约
	return &Server{Store: st}, tok, tk
}

// dnFileIDs 回读本工单的子文件行 ID（升序），供 ?file_id= 精确点单。
func dnFileIDs(t *testing.T, s *Server, ticketID int64) []int64 {
	t.Helper()
	files, err := s.Store.TicketFiles(ticketID)
	if err != nil {
		t.Fatalf("回读子文件失败: %v", err)
	}
	var out []int64
	for _, f := range files {
		out = append(out, f.ID)
	}
	if len(out) != 2 {
		t.Fatalf("夹具应有 2 个子文件行，实得 %d", len(out))
	}
	return out
}

// dnItoa 整数转字符串（拼 query 用）。
func dnItoa(v int64) string { return strconv.FormatInt(v, 10) }

// callDownload 直调 GET /api/tickets/download，返回 recorder。
func callDownload(t *testing.T, s *Server, tok string, query string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/tickets/download?"+query, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handleTicketDownload(rec, req)
	return rec
}

// TestTicketDownloadZipEntriesCarryLangSuffix DN-1：多语言产物打进 zip 时条目名必须逐语言不同。
//
// 这一条是 O-03 的真缺陷本体：同名条目在 zip 里合法，解压时后者覆盖前者，
// 于是「12 语种工单」交回客户手里只剩一种语言，而 HTTP 200、零报错、产物能打开。
func TestTicketDownloadZipEntriesCarryLangSuffix(t *testing.T) {
	s, tok, tk := newDnProbe(t)
	rec := callDownload(t, s, tok, "id="+dnItoa(tk.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("下载应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	zr, zerr := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if zerr != nil {
		t.Fatalf("多产物下载体应是合法 zip（实得 %d 字节）: %v", rec.Body.Len(), zerr)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	if len(names) != 2 {
		t.Fatalf("zip 条目数应等于产物数 2，实得 %d: %v（条目同名会静默覆盖＝客户只拿到一种语言）", len(names), names)
	}
	joined := strings.Join(names, "|")
	if !strings.Contains(joined, "-en.txt") {
		t.Errorf("zip 条目应含英语产物名 uat_sample-en.txt，实得 %v", names)
	}
	if !strings.Contains(joined, "-ja.txt") {
		t.Errorf("zip 条目应含日语产物名 uat_sample-ja.txt，实得 %v", names)
	}
	// 负向锁：条目名不得退回"原始上传名"（那种形态两条完全相同，正是上面条目数那条抓的形态）
	for _, n := range names {
		if n == "uat_sample.txt" {
			t.Errorf("zip 条目名退回原始上传名 %q：逐语言后缀丢了", n)
		}
	}
}

// TestTicketDownloadSingleFileHeaderCarriesLangSuffix DN-2：?file_id= 单文件下载的附件名。
func TestTicketDownloadSingleFileHeaderCarriesLangSuffix(t *testing.T) {
	s, tok, tk := newDnProbe(t)
	ids := dnFileIDs(t, s, tk.ID)
	rec := callDownload(t, s, tok, "id="+dnItoa(tk.ID)+"&file_id="+dnItoa(ids[0]))
	if rec.Code != http.StatusOK {
		t.Fatalf("单文件下载应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "uat_sample-en.txt") {
		t.Errorf("Content-Disposition 应带产物真实 basename（含 -en），实得 %q", cd)
	}
	// 反向对照：第二档（日语）必须带 -ja 而不是复用同一份名——两条都从 f.FileName 取就会同值。
	rec2 := callDownload(t, s, tok, "id="+dnItoa(tk.ID)+"&file_id="+dnItoa(ids[1]))
	cd2 := rec2.Header().Get("Content-Disposition")
	if !strings.Contains(cd2, "uat_sample-ja.txt") {
		t.Errorf("第二个产物的 Content-Disposition 应带 -ja，实得 %q（两条同值＝按源件名出栈）", cd2)
	}
	if cd == cd2 {
		t.Errorf("两份不同语言的产物出栈名完全相同：%q", cd)
	}
}

// TestTicketDownloadTextModeNameCarriesLangSuffix DN-3：?fmt=text 纯文案旁路同一口径。
func TestTicketDownloadTextModeNameCarriesLangSuffix(t *testing.T) {
	s, tok, tk := newDnProbe(t)
	ids := dnFileIDs(t, s, tk.ID)
	rec := callDownload(t, s, tok, "id="+dnItoa(tk.ID)+"&fmt=text&file_id="+dnItoa(ids[0]))
	if rec.Code != http.StatusOK {
		t.Fatalf("纯文案下载应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "uat_sample-en.md") {
		t.Errorf("纯文案旁路的附件名应取产物 basename（uat_sample-en.md），实得 %q", cd)
	}
	if strings.Contains(cd, "_译文") {
		t.Errorf("纯文案旁路仍在拼旧式 `_译文` 名：%q（O-03 已统一为 -<lang>）", cd)
	}
}
