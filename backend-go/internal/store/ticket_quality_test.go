// ============================================================================
// ★ 改造 4/5（2026-09-17）质检透出列测试：
//
//	tickets.quality_flagged / qa_errors / qa_warnings 幂等补列、写入与列表回读。
//
// 背景：此前评估分与 QA 报告落 payload 后即「死数据」，前端零透出（仅 xlsx 下载可见）。
// 本组用例守住「列存在 + 读写一致 + 列表接口可零成本透出」这条链。
// ============================================================================
package store

import (
	"testing"

	"translator/internal/db"
)

// TestTicketQualityColumnsDefaultZero 新库经 New() 补列后，新建工单三列默认 0（不误标）。
func TestTicketQualityColumnsDefaultZero(t *testing.T) {
	s := newTestStoreWithTenants(t)
	tk, err := s.CreateTicket(1, 1, "质检列默认值", "你好", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	got, err := s.GetTicket(tk.ID, 1)
	if err != nil {
		t.Fatalf("GetTicket 失败: %v", err)
	}
	if got.QualityFlagged != 0 || got.QAErrors != 0 || got.QAWarnings != 0 {
		t.Fatalf("新工单质检列应全为 0，实际 flagged=%d errs=%d warns=%d",
			got.QualityFlagged, got.QAErrors, got.QAWarnings)
	}
}

// TestTicketQualityMigrateIdempotent 补列幂等：重复调用不报错（老库反复启动场景）。
func TestTicketQualityMigrateIdempotent(t *testing.T) {
	s := newTestStoreWithTenants(t)
	for i := 0; i < 3; i++ {
		s.TicketQualityMigrate()
	}
	// 列仍然只存在一份且可写
	tk, err := s.CreateTicket(1, 1, "幂等", "你好", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if err := s.SetTicketQualityFlagged(tk.ID); err != nil {
		t.Fatalf("幂等迁移后打标失败: %v", err)
	}
}

// TestSetTicketQualityFlaggedReadBack 打标回读：GetTicket 与 ListTickets 均透出 quality_flagged=1。
func TestSetTicketQualityFlaggedReadBack(t *testing.T) {
	s := newTestStoreWithTenants(t)
	tk, err := s.CreateTicket(1, 1, "打标", "你好", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if err := s.SetTicketQualityFlagged(tk.ID); err != nil {
		t.Fatalf("SetTicketQualityFlagged: %v", err)
	}
	got, err := s.GetTicket(tk.ID, 1)
	if err != nil {
		t.Fatalf("GetTicket 失败: %v", err)
	}
	if got.QualityFlagged != 1 {
		t.Fatalf("详情应透出 quality_flagged=1，实际 %d", got.QualityFlagged)
	}
	// 列表接口数据源（handleTickets 直接序列化 store.Ticket）
	list, err := s.ListTickets(1, 1, true)
	if err != nil {
		t.Fatalf("ListTickets 失败: %v", err)
	}
	found := false
	for _, x := range list {
		if x.ID == tk.ID {
			found = true
			if x.QualityFlagged != 1 {
				t.Fatalf("列表应透出 quality_flagged=1，实际 %d", x.QualityFlagged)
			}
		}
	}
	if !found {
		t.Fatal("ListTickets 未返回新建工单")
	}
}

// TestSetTicketQualityFlaggedMonotonic 打标单向性：置 1 后再次写入不改回 0（人工复核提示不应自动撤销）。
func TestSetTicketQualityFlaggedMonotonic(t *testing.T) {
	s := newTestStoreWithTenants(t)
	tk, _ := s.CreateTicket(1, 1, "单向", "你好", "", "en")
	if err := s.SetTicketQualityFlagged(tk.ID); err != nil {
		t.Fatalf("首次打标失败: %v", err)
	}
	if err := s.SetTicketQualityFlagged(tk.ID); err != nil {
		t.Fatalf("重复打标应幂等: %v", err)
	}
	got, _ := s.GetTicket(tk.ID, 1)
	if got.QualityFlagged != 1 {
		t.Fatalf("重复打标后应仍为 1，实际 %d", got.QualityFlagged)
	}
}

// TestSetTicketQASummaryReadBack 质检摘要回读：qa_errors/qa_warnings 落列，详情与列表一致透出。
func TestSetTicketQASummaryReadBack(t *testing.T) {
	s := newTestStoreWithTenants(t)
	tk, err := s.CreateTicket(1, 1, "质检摘要", "你好", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if err := s.SetTicketQASummary(tk.ID, 2, 5); err != nil {
		t.Fatalf("SetTicketQASummary: %v", err)
	}
	got, err := s.GetTicket(tk.ID, 1)
	if err != nil {
		t.Fatalf("GetTicket 失败: %v", err)
	}
	if got.QAErrors != 2 || got.QAWarnings != 5 {
		t.Fatalf("质检摘要应为 errs=2 warns=5，实际 errs=%d warns=%d", got.QAErrors, got.QAWarnings)
	}
	// 覆盖更新：同工单重跑质检应按最新报告覆盖，而非累加
	if err := s.SetTicketQASummary(tk.ID, 0, 1); err != nil {
		t.Fatalf("覆盖质检摘要失败: %v", err)
	}
	got2, _ := s.GetTicket(tk.ID, 1)
	if got2.QAErrors != 0 || got2.QAWarnings != 1 {
		t.Fatalf("质检摘要应被覆盖为 errs=0 warns=1，实际 errs=%d warns=%d", got2.QAErrors, got2.QAWarnings)
	}
}

// TestTicketQualityColumnsExistInSchema 断言三列确实落库（防止 SELECT/Scan 与建列脱节）。
func TestTicketQualityColumnsExistInSchema(t *testing.T) {
	s := newTestStoreWithTenants(t)
	d := db.CurrentDialect()
	rows, err := db.Query(s.db, d, "PRAGMA table_info(tickets)")
	if err != nil {
		t.Skipf("非 SQLite 方言跳过列枚举: %v", err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			continue
		}
		have[name] = true
	}
	for _, col := range []string{"quality_flagged", "qa_errors", "qa_warnings"} {
		if !have[col] {
			t.Fatalf("tickets 缺少质检透出列 %s", col)
		}
	}
}
