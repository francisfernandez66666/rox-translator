// ============ gdpr_test.go · 职责说明 ============
// GDPR 租户擦除的**表清单与磁盘路径**闸（★ 2026-10-30 组5，B11/W25）。
//
// 缺陷形态（如果没有这条锁）：EraseTenantData 的表清单是手写的，新表（ticket_artifacts /
// ticket_segments）加进来以后若忘了登记，租户注销后**客户原文与译文产物路径仍留在库里**；
// collectTenantArtifactPaths 若忘了 text_result_path / artifacts.path，磁盘上的译文文件
// 永远没人删。两种都是"行没了、文件还在"或"文件该删没删"的静默残留，
// 界面上零症状、日志里零线索（AGENTS §一·19 那条"有降级链的静默失败"同族）。
//
// 判据按**两件事**分别问（方案 §6 W25 的口径）：
//  1. 行：ticket_artifacts / ticket_segments 里的行必须不在；
//  2. 文件：产物路径（含 text_result_path 与 artifacts.path）必须进收集集并被删掉。
//
// 方言：内存 SQLite（自钉 config.C，Cleanup 恢复）。
// =============================================
package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestEraseTenantDataCoversArtifactsAndSegments W25：新表进清单 + 文件真的被删。
func TestEraseTenantDataCoversArtifactsAndSegments(t *testing.T) {
	s := newTestStore(t)
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("造文件失败: %v", err)
		}
		return p
	}
	artifactPath := mk("a_en.docx")
	textPath := mk("a_en-text.md")

	tk, err := s.CreateTicket(9, 42, "GDPR 擦除单", "源文", "/tmp/src/a.docx", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	tf, err := s.AddTicketFile(&TicketFile{TenantID: 9, TicketID: tk.ID, FileName: "a.docx", FilePath: "/tmp/src/a.docx"})
	if err != nil {
		t.Fatalf("挂文件行失败: %v", err)
	}
	if err := s.SetTicketFileTextResult(tf.ID, textPath); err != nil {
		t.Fatalf("写纯文案路径失败: %v", err)
	}
	if _, err := s.UpsertTicketArtifact(&TicketArtifact{TenantID: 9, TicketID: tk.ID, FileID: tf.ID,
		Lang: "en", Kind: TicketArtifactKindOriginal, Path: artifactPath, Name: "a_en.docx", Primary: true}); err != nil {
		t.Fatalf("登记产物行失败: %v", err)
	}
	if err := s.SaveTicketSegments(9, tk.ID, "/tmp/src/a.docx", "en", []TicketSegment{{SegIndex: 0, Source: "源文", Target: "source"}}); err != nil {
		t.Fatalf("写逐段真值失败: %v", err)
	}

	// 收集集必须含 artifacts 与 text_result_path 两条路径（去重）。
	paths := map[string]bool{}
	for _, p := range s.collectTenantArtifactPaths(9) {
		paths[p] = true
	}
	if !paths[artifactPath] {
		t.Errorf("收集集缺 ticket_artifacts.path（%s）：磁盘上的译文产物将永远没人删", artifactPath)
	}
	if !paths[textPath] {
		t.Errorf("收集集缺 ticket_files.text_result_path（%s）：纯文案旁路产物将永远没人删", textPath)
	}

	if err := s.EraseTenantDataFull(9); err != nil {
		t.Fatalf("擦除失败: %v", err)
	}

	// ① 行不在。
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ticket_artifacts WHERE tenant_id=9").Scan(&n); err != nil || n != 0 {
		t.Errorf("ticket_artifacts 行应被擦除，实得 %d 行 (err=%v)", n, err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM ticket_segments WHERE tenant_id=9").Scan(&n); err != nil || n != 0 {
		t.Errorf("ticket_segments 行应被擦除，实得 %d 行 (err=%v)", n, err)
	}
	// ② 文件不在（只有行没了、文件还在＝永久孤儿）。
	for _, p := range []string{artifactPath, textPath} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("磁盘产物应被删掉：%s", p)
		}
	}
	// 反向对照：别的租户的行一动不动（擦除按 tenant_id 收口）。
	if _, err := s.UpsertTicketArtifact(&TicketArtifact{TenantID: 10, TicketID: 100, Lang: "en",
		Kind: TicketArtifactKindMD, Path: "/tmp/other.md", Name: "other.md"}); err != nil {
		t.Fatal(err)
	}
	if err := s.EraseTenantData(9); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.ListTicketArtifacts(100); len(list) != 1 {
		t.Fatalf("擦除租户 9 不该动到租户 10 的产物行，实得 %d 行", len(list))
	}
}
