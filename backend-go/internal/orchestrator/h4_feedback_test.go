// ============================================================================
// H4 工单反馈 TM 自动审核集成测试：高分直写 tm_segments、低分入 tm_review 池、
// 工单转 completed、不重复投稿。
// ============================================================================
package orchestrator

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	db2 "translator/internal/db"
	"translator/internal/kb"
	"translator/internal/store"
)

func h4Setup(t *testing.T) (*Workflow, *store.Store, *kb.KBDatabase) {
	t.Helper()
	// 生产形态：store 与 kb 共用同一 SQLite 文件（tm_segments/kb_packages 同库）
	dbPath := filepath.Join(t.TempDir(), "app.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	kbdb, err := kb.Open(dbPath)
	if err != nil {
		t.Fatalf("kb.Open: %v", err)
	}
	// FindExact 的 sharedFilterSQL 依赖 tenants/kb_packages 辅表（生产同库；测试补最小集，同 D2 模式）
	for _, ddl := range []string{
		"CREATE TABLE IF NOT EXISTS tenants (id INTEGER PRIMARY KEY, industry TEXT DEFAULT '')",
		"INSERT INTO tenants (id, industry) VALUES (1,'general')",
	} {
		if _, e := db2.Exec(kbdb.RawDB(), db2.CurrentDialect(), ddl); e != nil {
			t.Fatalf("辅表准备失败: %v", e)
		}
	}
	w := NewWorkflow(st, nil, nil, kbdb)
	return w, st, kbdb
}

func h4Ticket(t *testing.T, st *store.Store) *store.Ticket {
	t.Helper()
	p := ticketPayload{
		SourceText:  "设备温度不得超过 45 ℃",
		TargetLangs: []string{"en", "de"},
		Translations: map[string]string{
			"en": "The device temperature must not exceed 99 ℃",         // 数字 45→99：低分
			"de": "Die Gerätetemperatur darf 45 ℃ nicht überschreiten.", // 干净：高分
		},
	}
	b, _ := json.Marshal(p)
	tk, err := st.CreateTicket(1, 1, "H4 预筛", p.SourceText, "", "en,de")
	if err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	tk.Status = store.TicketApproved
	tk.FinalResult = string(b)
	if err := st.UpdateTicket(tk); err != nil {
		t.Fatalf("UpdateTicket: %v", err)
	}
	return tk
}

func TestH4RunFeedbackScreening(t *testing.T) {
	old := config.C
	defer func() { config.C = old }()
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 本测试固定 SQLite 文件（UAT PG 矩阵下 env DB_DRIVER=postgres 会误导方言判定）
	config.C = cfg

	w, st, kbdb := h4Setup(t)
	tk := h4Ticket(t, st)

	if err := w.runFeedback(nil, tk); err != nil {
		t.Fatalf("runFeedback: %v", err)
	}

	// 工单应转 completed
	got, err := st.GetTicket(tk.ID, 1)
	if err != nil || got == nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.Status != store.TicketCompleted {
		t.Fatalf("工单状态应为 completed，实际 %s", got.Status)
	}

	// 高分 de 应入正式 TM（SaveBack 写 tm_segments）
	row, err := kbdb.FindExact(tk.SourceText, 1)
	if err != nil || row == nil {
		t.Fatalf("TM 未写入: %v", err)
	}
	if row.Langs["de"] == "" {
		t.Fatalf("de 高分译文应入 TM: %+v", row.Langs)
	}
	if row.Langs["en"] != "" {
		t.Fatalf("en 低分译文不应直接入 TM: %+v", row.Langs)
	}

	// 低分 en 应入 tm_review 人审池
	reviews, err := st.ListTmReviews("pending")
	if err != nil {
		t.Fatalf("ListTmReviews: %v", err)
	}
	hit := 0
	for _, r := range reviews {
		if r.Lang == "en" && r.Source == "feedback" && r.RefType == "ticket" {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("低分句应产生 1 条人审候选，实际 %d", hit)
	}

	// 幂等：重跑不重复投稿（HasActiveTmReview 去重）
	tk.Status = store.TicketApproved
	if err := w.runFeedback(nil, tk); err != nil {
		t.Fatalf("runFeedback#2: %v", err)
	}
	reviews2, _ := st.ListTmReviews("pending")
	if len(reviews2) != len(reviews) {
		t.Fatalf("重跑不应新增人审候选: %d → %d", len(reviews), len(reviews2))
	}
}
