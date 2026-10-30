// ============ tickets_deliverables_test.go · 职责说明 ============
// B17：「有产物」判据必须并入 ticket_artifacts（★ 2026-10-30 三事合一批 · 组5）。
//
// 缺陷形态（如果不补这条判据）：两段式之后**多文件新单不写 tickets.result_path**
// （只写 ticket_files.result_path），产物事实源是 ticket_artifacts。旧判据只看 result_path ⇒
// 「产物都在、artifacts 也齐」的 completed 单被判成"无产物"，下载端直接 409——
// 客户看着一张完成了的单，点下载回"该工单没有可用译文产物"，而磁盘上文件一个都不少。
//
// 两腿：
//  1. 多文件新单（tickets.result_path 空、子文件 result_path 也空，只有 artifacts 行）⇒
//     ticketHasDeliverable=true 且下载 200；
//  2. 反向：completed 但既无 artifacts 也无 result_path ⇒ 409（判据不是恒真）。
//
// 判据用真 handler + 真 Store（不 mock 判据函数本身，避免"测的是替身"）。
// =============================================
package api

import (
	"net/http"
	"testing"

	"translator/internal/store"
)

// TestTicketDeliverableCountsArtifacts B17：多文件新单靠 artifacts 判「有产物」。
func TestTicketDeliverableCountsArtifacts(t *testing.T) {
	p := newArtProbe(t)
	tk := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "a.docx", "src"))
	tf1 := p.addFile(t, tk, "a.docx")
	tf2 := p.addFile(t, tk, "b.docx")
	// 只登记 artifacts（模拟两段式服务层：result_path 只写首件、这里连首件都不写，
	// 直接把判据逼到"只能靠 artifacts"的那一档）。
	o1 := p.writeFile(t, "a-en.docx", "a")
	o2 := p.writeFile(t, "b-en.docx", "b")
	p.addArtifact(t, tk, tf1.ID, "en", store.TicketArtifactKindOriginal, o1, true, false)
	p.addArtifact(t, tk, tf2.ID, "en", store.TicketArtifactKindOriginal, o2, true, false)

	fresh, err := p.st.GetTicket(tk.ID, 1)
	if err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	// 反向对照的**前提**：这一单的旧判据输入全是空的（否则本用例测不到新判据）。
	if fresh.ResultPath != "" {
		t.Fatalf("夹具应保持 tickets.result_path 为空（多文件新单形态），实得 %q", fresh.ResultPath)
	}
	for _, f := range mustFiles(t, p.st, tk.ID) {
		if f.ResultPath != "" {
			t.Fatalf("夹具应保持子文件 result_path 为空，实得 %q", f.ResultPath)
		}
	}
	if !p.s.ticketHasDeliverable(fresh) {
		t.Fatal("只有 artifacts 行的完成单必须判「有产物」（旧口径只看 result_path 会把它判成 409）")
	}
	if rec := p.call(p.s.handleTicketDownload, p.tok, "id="+artID(tk.ID)); rec.Code != http.StatusOK {
		t.Fatalf("下载应 200（按需打包），实得 %d: %s", rec.Code, rec.Body.String())
	}
}

// TestTicketDeliverableStillBlocksGhostCompleted B17 反向：真无产物仍 409。
func TestTicketDeliverableStillBlocksGhostCompleted(t *testing.T) {
	p := newArtProbe(t)
	tk := p.mkTicket(t, store.TicketCompleted, p.writeFile(t, "a.docx", "src"))
	p.addFile(t, tk, "a.docx")
	fresh, err := p.st.GetTicket(tk.ID, 1)
	if err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	if p.s.ticketHasDeliverable(fresh) {
		t.Fatal("无 artifacts、无 result_path 的鬼 completed 单必须判「无产物」")
	}
	rec := p.call(p.s.handleTicketDownload, p.tok, "id="+artID(tk.ID))
	if rec.Code != http.StatusConflict {
		t.Fatalf("无产物单下载应 409，实得 %d: %s", rec.Code, rec.Body.String())
	}
}

// mustFiles 回读子文件行（夹具断言用）。
func mustFiles(t *testing.T, st *store.Store, ticketID int64) []*store.TicketFile {
	t.Helper()
	files, err := st.TicketFiles(ticketID)
	if err != nil {
		t.Fatalf("回读子文件失败: %v", err)
	}
	return files
}
