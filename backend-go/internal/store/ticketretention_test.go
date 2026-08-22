// ============ 本文件职责中文说明 ============
// 工单产物保留期单元测试：到期打点、扫描清单、档位标记去重、到期清理（文件路径收集+字段清空）。
package store

import (
	"testing"
	"time"
)

// seedCompletedTicket 造一个 completed 且带主产物路径的工单。
func seedCompletedTicket(t *testing.T, s *Store, title string) int64 {
	t.Helper()
	tk, err := s.CreateTicket(1, 7, title, "", "/tmp/x/"+title+".docx", "en")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	tk.Status = TicketCompleted
	if err := s.UpdateTicket(tk); err != nil {
		t.Fatal(err)
	}
	_ = s.SetTicketResultPath(tk.ID, "/tmp/out/"+title+"_en.docx")
	return tk.ID
}

// TestRetentionScanAndMarks 到期打点进入扫描清单；7/3/1 档标记幂等。
func TestRetentionScanAndMarks(t *testing.T) {
	s := newTestStoreWithTenants(t)
	id := seedCompletedTicket(t, s, "留存工单")
	exp := time.Now().AddDate(0, 0, 6).Format(time.RFC3339)
	if err := s.SetTicketExpiry(id, exp); err != nil {
		t.Fatalf("SetTicketExpiry: %v", err)
	}
	rows, err := s.ListTicketsForRetention()
	if err != nil || len(rows) == 0 {
		t.Fatalf("扫描清单应包含该工单: %d (%v)", len(rows), err)
	}
	found := false
	for _, r := range rows {
		if r.ID == id {
			found = true
		}
	}
	if !found {
		t.Fatal("扫描清单缺少目标工单")
	}
	// 档位标记：首标生效、重复标幂等
	if s.TicketExpireMarked(id, "7") {
		t.Fatal("未标记前不应返回已标记")
	}
	_ = s.MarkTicketExpireNotify(id, "7")
	_ = s.MarkTicketExpireNotify(id, "7") // 幂等
	if !s.TicketExpireMarked(id, "7") || s.TicketExpireMarked(id, "3") {
		t.Fatalf("档位标记状态异常")
	}
}

// TestCleanupTicketResults 清理收集主产物与多文件产物路径并清空字段（保留 expire_notify 历史）。
func TestCleanupTicketResults(t *testing.T) {
	s := newTestStoreWithTenants(t)
	id := seedCompletedTicket(t, s, "清理工单")
	tf, _ := s.AddTicketFile(&TicketFile{TenantID: 1, TicketID: id, FileName: "b.pdf", FilePath: "/tmp/x/b.pdf"})
	_ = s.SetTicketFileResult(tf.ID, "/tmp/out/b_en.pdf") // 产物由执行器回填
	_ = s.SetTicketExpiry(id, time.Now().Add(-time.Hour).Format(time.RFC3339)) // 已过期

	paths, err := s.CleanupTicketResults(id)
	if err != nil {
		t.Fatalf("CleanupTicketResults: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("应收集 2 个产物路径（主+多文件），实际 %d: %v", len(paths), paths)
	}
	var rp string
	_ = s.db.QueryRow("SELECT COALESCE(result_path,'') FROM tickets WHERE id=?", id).Scan(&rp)
	if rp != "" {
		t.Fatalf("主产物路径应已清空，实际 %q", rp)
	}
	tfs, _ := s.TicketFiles(id)
	for _, f := range tfs {
		if f.ResultPath != "" {
			t.Fatalf("多文件产物路径应已清空: %+v", f)
		}
	}
}
