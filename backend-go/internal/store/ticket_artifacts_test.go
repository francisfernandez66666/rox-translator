// ============ ticket_artifacts_test.go · 职责说明 ============
// ticket_artifacts（工单交付产物）数据层单元测试（★ 2026-10-30 组5 两段式交付，B1）。
//
// 钉死的口径：
//  1. **同键 upsert 幂等**：同一 (ticket, file, lang, kind, path) 重发只更新、不长第二行
//     （引擎在"写回失败把 md 标 degraded"时会重发同一件，服务层靠它更新而不是加行）；
//  2. **md 与 original 是两行**（kind 不同）：同一语种的两件产物互不顶掉；
//  3. **顺序稳定**：List 按 (file_id, lang, id) 升序——详情面与按需打包共用这一份顺序，
//     两处各排一遍迟早不一致；
//  4. **归属强校验**：GetTicketArtifact 必须带 ticket_id（aid 不属于该单＝查不到 ⇒ 上层 404）；
//  5. **删行干净**：DeleteTicketArtifactsForTicket 之后该单零行，别的单不受影响。
//
// 方言：本文件钉内存 SQLite（AGENTS §一·4 的模板；config.C 自钉并在 Cleanup 恢复）。
// PG 侧同一把尺子由 ticketartifacts_pg_test.go 在真库上再跑一遍（无 PG 即 Skip）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TicketArtifact
// =============================================
package store

import (
	"testing"

	"translator/internal/config"
)

// newArtifactStore 建内存 SQLite Store（自钉方言，防 UAT 矩阵的 PG env 泄漏进本文件）。
func newArtifactStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	s := newTestStore(t)
	return s
}

// artFixture 一件产物的构造简写（测试里只关心键与两档标记）。
func artFixture(ticketID, fileID int64, lang, kind, path string) *TicketArtifact {
	return &TicketArtifact{
		TenantID: 1, TicketID: ticketID, FileID: fileID,
		Lang: lang, Kind: kind, Path: path, Name: path,
	}
}

// TestTicketArtifactUpsertIsIdempotent B1：同键重发只更新一行（含 degraded 改写）。
func TestTicketArtifactUpsertIsIdempotent(t *testing.T) {
	s := newArtifactStore(t)

	a := artFixture(42, 7, "en", TicketArtifactKindMD, "/out/a_en-text.md")
	a.Size = 10
	first, err := s.UpsertTicketArtifact(a)
	if err != nil {
		t.Fatalf("首次登记失败: %v", err)
	}
	if first.ID <= 0 || first.CreatedAt == "" {
		t.Fatalf("首次登记应回填 ID 与 created_at：%+v", first)
	}
	// 同一件重发：改名 + 标降级（引擎「写回失败把 md 标 degraded」就是这一形态）。
	a2 := artFixture(42, 7, "en", TicketArtifactKindMD, "/out/a_en-text.md")
	a2.Name = "a_en.md"
	a2.Size = 11
	a2.Degraded = true
	second, err := s.UpsertTicketArtifact(a2)
	if err != nil {
		t.Fatalf("重发失败: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("同键重发必须更新同一行：first=%d second=%d", first.ID, second.ID)
	}
	if !second.Degraded || second.Name != "a_en.md" || second.Size != 11 {
		t.Fatalf("重发应覆盖 name/size/degraded：%+v", second)
	}
	if second.CreatedAt != first.CreatedAt {
		t.Errorf("created_at 不该被重发改写：%q → %q", first.CreatedAt, second.CreatedAt)
	}
	list, _ := s.ListTicketArtifacts(42)
	if len(list) != 1 {
		t.Fatalf("同键应只留一行，实得 %d 行（唯一索引没生效？）", len(list))
	}
}

// TestTicketArtifactKindsAreSeparateRows B1：md 与 original 是两行，同 path 也不合并。
func TestTicketArtifactKindsAreSeparateRows(t *testing.T) {
	s := newArtifactStore(t)
	if _, err := s.UpsertTicketArtifact(artFixture(43, 0, "en", TicketArtifactKindMD, "/out/a_en.md")); err != nil {
		t.Fatal(err)
	}
	orig := artFixture(43, 0, "en", TicketArtifactKindOriginal, "/out/a_en.md") // .md 输入件：同路径
	orig.Primary = true
	if _, err := s.UpsertTicketArtifact(orig); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListTicketArtifacts(43)
	if len(list) != 2 {
		t.Fatalf("md 与 original 应是两行（kind 在唯一键里），实得 %d 行", len(list))
	}
	// 同路径收敛的落点：md 行可以被单独置主产物，且不影响 original 行。
	if err := s.SetTicketArtifactPrimary(list[0].ID); err != nil {
		t.Fatalf("置主产物失败: %v", err)
	}
	md, _ := s.GetTicketArtifact(list[0].ID, 43)
	if md == nil || !md.Primary {
		t.Fatalf("md 行应已置 is_primary=1：%+v", md)
	}
	found, _ := s.FindTicketArtifact(43, 0, "en", TicketArtifactKindMD, "/out/a_en.md")
	if found == nil || found.ID != md.ID {
		t.Fatalf("FindTicketArtifact 应按唯一键命中同一行：%+v", found)
	}
}

// TestTicketArtifactListOrderAndOwnership B1：顺序稳定 + 归属强校验。
func TestTicketArtifactListOrderAndOwnership(t *testing.T) {
	s := newArtifactStore(t)
	// 故意乱序写入：file 2 的 md、file 1 的 original/md、file 2 的 original。
	rows := []*TicketArtifact{
		artFixture(44, 2, "en", TicketArtifactKindMD, "/out/f2_en-text.md"),
		artFixture(44, 1, "en", TicketArtifactKindOriginal, "/out/f1_en.docx"),
		artFixture(44, 1, "en", TicketArtifactKindMD, "/out/f1_en-text.md"),
		artFixture(44, 2, "en", TicketArtifactKindOriginal, "/out/f2_en.docx"),
	}
	for _, r := range rows {
		if _, err := s.UpsertTicketArtifact(r); err != nil {
			t.Fatalf("写入失败: %v", err)
		}
	}
	list, err := s.ListTicketArtifacts(44)
	if err != nil {
		t.Fatalf("列表失败: %v", err)
	}
	if len(list) != 4 {
		t.Fatalf("应有 4 行，实得 %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].FileID > list[i].FileID {
			t.Fatalf("List 必须按 file_id 升序：第 %d 行 file_id=%d 大于第 %d 行 %d", i-1, list[i-1].FileID, i, list[i].FileID)
		}
	}
	// 归属：拿 aid 去查别单必须查不到（上层据此回 404，不泄露存在性）。
	if got, _ := s.GetTicketArtifact(list[0].ID, 999); got != nil {
		t.Fatalf("aid 不属于该单时必须查不到，实得 %+v", got)
	}
	if got, _ := s.GetTicketArtifact(list[0].ID, 44); got == nil {
		t.Fatal("归属正确的 aid 必须查得到")
	}
}

// TestTicketArtifactDeleteAndPaths B1：删行干净、路径集合去重（清理/擦除的输入）。
func TestTicketArtifactDeleteAndPaths(t *testing.T) {
	s := newArtifactStore(t)
	// 同一份 xlsx 被两个语种引用（多语言合并交付）⇒ 路径集合只应出现一次。
	if _, err := s.UpsertTicketArtifact(artFixture(45, 0, "en", TicketArtifactKindOriginal, "/out/a_multi.xlsx")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertTicketArtifact(artFixture(45, 0, "ja", TicketArtifactKindOriginal, "/out/a_multi.xlsx")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertTicketArtifact(artFixture(45, 0, "en", TicketArtifactKindMD, "/out/a_en-text.md")); err != nil {
		t.Fatal(err)
	}
	// 别的单的行不受影响（删单只删自己）。
	if _, err := s.UpsertTicketArtifact(artFixture(46, 0, "en", TicketArtifactKindMD, "/out/b_en-text.md")); err != nil {
		t.Fatal(err)
	}
	paths, err := s.TicketArtifactPaths(45)
	if err != nil {
		t.Fatalf("路径集合失败: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("路径集合应去重成 2 条，实得 %d: %v", len(paths), paths)
	}
	if err := s.DeleteTicketArtifactsForTicket(45); err != nil {
		t.Fatalf("删行失败: %v", err)
	}
	if list, _ := s.ListTicketArtifacts(45); len(list) != 0 {
		t.Fatalf("删行后该单应零行，实得 %d", len(list))
	}
	if list, _ := s.ListTicketArtifacts(46); len(list) != 1 {
		t.Fatalf("删单 45 不该动到单 46，实得 %d 行", len(list))
	}
}

// TestTicketArtifactUniqueIndexExists B1 反向对照：唯一索引必须在（否则幂等腿是空转的）。
func TestTicketArtifactUniqueIndexExists(t *testing.T) {
	s := newArtifactStore(t)
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_ticket_artifacts_uniq'").Scan(&n); err != nil {
		t.Fatalf("查索引失败: %v", err)
	}
	if n != 1 {
		t.Fatal("缺唯一索引 idx_ticket_artifacts_uniq：同键 upsert 会退化成多行")
	}
}
