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

// TestCleanupTicketResults 清理收集主产物、多文件产物、text_result_path 与 artifacts 路径
// 并清空字段（保留 expire_notify 历史）。
//
// ★ 2026-10-30 组5（B11/W25）：两段式之后「交付过什么」的事实源是 ticket_artifacts，
// 只清 result_path 会把 md/原格式产物留在磁盘上、行却被删掉＝永久孤儿。
// 本用例同时钉两件事：**行与文件路径两边都进收集集**，且清理后 artifacts 行真的不在。
func TestCleanupTicketResults(t *testing.T) {
	s := newTestStoreWithTenants(t)
	id := seedCompletedTicket(t, s, "清理工单")
	tf, _ := s.AddTicketFile(&TicketFile{TenantID: 1, TicketID: id, FileName: "b.pdf", FilePath: "/tmp/x/b.pdf"})
	_ = s.SetTicketFileResult(tf.ID, "/tmp/out/b_en.pdf")                      // 产物由执行器回填
	_ = s.SetTicketFileTextResult(tf.ID, "/tmp/out/b_en-text.md")              // ★ 纯文案旁路（R-3 老缺口）
	_ = s.SetTicketExpiry(id, time.Now().Add(-time.Hour).Format(time.RFC3339)) // 已过期
	// ★ 两段式的产物行：media 路径只存在于 artifacts 表（result_path 只记首件）。
	if _, err := s.UpsertTicketArtifact(&TicketArtifact{TenantID: 1, TicketID: id, FileID: tf.ID,
		Lang: "en", Kind: TicketArtifactKindMD, Path: "/tmp/out/b_en-text.md", Name: "b_en-text.md"}); err != nil {
		t.Fatalf("登记产物行失败: %v", err)
	}
	if _, err := s.UpsertTicketArtifact(&TicketArtifact{TenantID: 1, TicketID: id, FileID: tf.ID,
		Lang: "en", Kind: TicketArtifactKindOriginal, Path: "/tmp/out/b_en.pdf", Name: "b_en.pdf", Primary: true}); err != nil {
		t.Fatalf("登记产物行失败: %v", err)
	}

	paths, err := s.CleanupTicketResults(id)
	if err != nil {
		t.Fatalf("CleanupTicketResults: %v", err)
	}
	// 主产物 + 多文件产物 + 多文件纯文案 = 3 条（artifacts 的两条与上面同路径，去重后不新增）。
	if len(paths) != 3 {
		t.Fatalf("应收集 3 个产物路径（主+多文件+纯文案旁路），实际 %d: %v", len(paths), paths)
	}
	var rp string
	_ = s.db.QueryRow("SELECT COALESCE(result_path,'') FROM tickets WHERE id=?", id).Scan(&rp)
	if rp != "" {
		t.Fatalf("主产物路径应已清空，实际 %q", rp)
	}
	tfs, _ := s.TicketFiles(id)
	for _, f := range tfs {
		if f.ResultPath != "" || f.TextResultPath != "" {
			t.Fatalf("多文件产物/纯文案路径应已清空: %+v", f)
		}
	}
	// ★ 行与文件是两件事，判据要问两件：路径进了返回集（上一段已断）＋行真的被删了。
	if list, _ := s.ListTicketArtifacts(id); len(list) != 0 {
		t.Fatalf("清理后 artifacts 行应全部删除，实得 %d 行（只删文件不删行/或反之都是缺陷）", len(list))
	}
}
