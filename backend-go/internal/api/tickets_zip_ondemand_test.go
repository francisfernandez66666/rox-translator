// ============ tickets_zip_ondemand_test.go · 职责说明 ============
// 「旧下载端点改为**按需流式打包**」的接口锁（★ 2026-10-30 三事合一批 · 组5，B9）。
//
// 两件事，缺一不可：
//  1. **新单**：下载体是 zip，条目名逐一等于 ticket_artifacts.name（= 落盘 basename）——
//     条目名不许另起一套（T76-2 的判据：zip 条目名与 artifact.name 逐字一致）；
//     `fmt=text` 只给 md 那几件；
//  2. **历史单**（无 artifacts 行、只有 ticket_files.result_path）：download 仍可用，
//     条目名沿用旧口径（产物 basename）——旧客户端零改动，这是"不迁移只回落"的落点。
//
// 判据刻意现读（不写死件数/名字）：件数 = 夹具登记了几件，名字 = 夹具里的 basename。
// =============================================
package api

import (
	"archive/zip"
	"bytes"
	"net/http"
	"sort"
	"testing"

	"translator/internal/store"
)

// zipEntryNames 把下载体当 zip 读，返回条目名（排序后便于等值比较）。
func zipEntryNames(t *testing.T, body []byte) []string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("下载体应是合法 zip（实得 %d 字节）: %v", len(body), err)
	}
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names
}

// TestTicketDownloadOnDemandZipFromArtifacts B9（新单）：条目名 = artifacts.name，件数 = 登记件数。
func TestTicketDownloadOnDemandZipFromArtifacts(t *testing.T) {
	p := newArtProbe(t)
	tk := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "a.docx", "src"))
	tf := p.addFile(t, tk, "a.docx")
	if err := p.st.SetTicketResultPath(tk.ID, ""); err != nil {
		t.Fatalf("清空 result_path 失败: %v", err)
	}
	mdPath := p.writeFile(t, "合同-en-text.md", "md-en")
	origPath := p.writeFile(t, "合同-en.docx", "docx-en")
	mdJa := p.writeFile(t, "合同-ja-text.md", "md-ja")
	origJa := p.writeFile(t, "合同-ja.docx", "docx-ja")
	p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindMD, mdPath, false, false)
	p.addArtifact(t, tk, tf.ID, "en", store.TicketArtifactKindOriginal, origPath, true, false)
	p.addArtifact(t, tk, tf.ID, "ja", store.TicketArtifactKindMD, mdJa, false, false)
	p.addArtifact(t, tk, tf.ID, "ja", store.TicketArtifactKindOriginal, origJa, true, false)

	rec := p.call(p.s.handleTicketDownload, p.tok, "id="+artID(tk.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("下载应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	// 每组取一件（original 优先）⇒ 2 件；条目名与 artifact.name（= basename）逐字一致。
	got := zipEntryNames(t, rec.Body.Bytes())
	want := []string{"合同-en.docx", "合同-ja.docx"}
	if len(got) != len(want) {
		t.Fatalf("zip 条目数应等于主产物件数 %d，实得 %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("zip 条目名必须与 artifact.name 逐字一致：want %v got %v", want, got)
		}
	}

	// fmt=text ⇒ 只给 md 两件（且件名带 -text）。
	recText := p.call(p.s.handleTicketDownload, p.tok, "id="+artID(tk.ID)+"&fmt=text")
	if recText.Code != http.StatusOK {
		t.Fatalf("fmt=text 应 200，实得 %d: %s", recText.Code, recText.Body.String())
	}
	gotText := zipEntryNames(t, recText.Body.Bytes())
	wantText := []string{"合同-en-text.md", "合同-ja-text.md"}
	if len(gotText) != len(wantText) || gotText[0] != wantText[0] || gotText[1] != wantText[1] {
		t.Fatalf("fmt=text 只应给 md 两件：want %v got %v", wantText, gotText)
	}
}

// TestTicketDownloadOnDemandZipLegacyFallback B9（历史单）：无 artifacts 行时仍按旧口径打包。
func TestTicketDownloadOnDemandZipLegacyFallback(t *testing.T) {
	p := newArtProbe(t)
	tk := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "a.docx", "src"))
	f1 := p.addFile(t, tk, "a.docx")
	f2 := p.addFile(t, tk, "b.docx")
	p1 := p.writeFile(t, "a-en.txt", "a")
	p2 := p.writeFile(t, "b-en.txt", "b")
	if err := p.st.SetTicketFileResult(f1.ID, p1); err != nil {
		t.Fatal(err)
	}
	if err := p.st.SetTicketFileResult(f2.ID, p2); err != nil {
		t.Fatal(err)
	}
	// 反向对照：确认这张单**没有** artifacts 行（否则本用例测的是新单那条路）。
	if list, _ := p.st.ListTicketArtifacts(tk.ID); len(list) != 0 {
		t.Fatalf("夹具不该有 artifacts 行，实得 %d", len(list))
	}
	rec := p.call(p.s.handleTicketDownload, p.tok, "id="+artID(tk.ID))
	if rec.Code != http.StatusOK {
		t.Fatalf("历史单下载应 200，实得 %d: %s", rec.Code, rec.Body.String())
	}
	got := zipEntryNames(t, rec.Body.Bytes())
	want := []string{"a-en.txt", "b-en.txt"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("历史单 zip 条目名应与旧口径一致：want %v got %v", want, got)
	}
}
