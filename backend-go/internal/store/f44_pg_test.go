// ============ f44_pg_test.go · 职责说明 ============
// ★ F-44（〇-U 批 I-1）PostgreSQL 真库读侧回归锁（2026-09-26）。
// 为什么必须单独有这份 PG 用例：对照编辑器三个读函数
// （GetTranslationEdits / ListKBTerms / GetTicketSegments）曾裸用 *sql.DB + `?` 占位符，
// 而 `?`→`$n` 的改写只在 internal/db 包装器里做、lib/pq 不会代做。结果：
// SQLite（本地快跑/CI 单测）**全绿**，PG（生产）语法报错，调用方又写成 `edits, _ :=`
// 把错误当「无修订」吞掉 ⇒ 客户侧表现为「保存成功、读回永远空、导出静默丢修订」，
// 零日志零红。ListKBTerms 还叠了一个 PG 专属错：`SELECT DISTINCT source_text … ORDER BY id`
// 在 PG 里非法（ORDER BY 表达式必须出现在选择列表里），SQLite 却照收。
// 本文件在真实 PG 上跑「写一遍 → 读回来等值」，是 SQLite 快跑与 internal/db 静态守卫
// （guard_test.go 的裸连接扫描）都替代不了的一条腿（AGENTS §一·4）。
// 依赖：PG_TEST_DSN（默认 postgres://$USER@127.0.0.1:5432/pgtest），无 PG 则跳过
// （与 coupons_pg_concurrency_test.go 同一口径；闸门侧的 PG 覆盖由 run_uat.sh 主矩阵承担）。
// =============================================
package store_test

import (
	"os"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"

	_ "github.com/lib/pq"
)

// newPGStoreForF44 建立真实 PG 上的 Store（跑完整幂等迁移链）。无 PG 即 Skip，不造恒绿断言。
func newPGStoreForF44(t *testing.T) *store.Store {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://" + os.Getenv("USER") + "@127.0.0.1:5432/pgtest?sslmode=disable"
	}
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PostgreSQL，跳过：%v", err)
	}
	t.Cleanup(func() { conn.Close() })
	// 方言钉死 postgres 并在结束时恢复：config.C 是全局的，不恢复会把 PG 方言
	// 漏给同包后续内存 SQLite 用例（AGENTS §一·4 记的两次整包假红）。
	prevC := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	config.C = cfg
	t.Cleanup(func() { config.C = prevC })

	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New(PG) 失败: %v", err)
	}
	return s
}

// TestEditorReadersOnPG F-44 本体：三个读函数在 PG 方言下写一遍、读回来必须等值。
// 断言按「先证明写侧真的落库、再证明读侧真的读得到」分开做——旧缺陷正是写侧好、读侧坏，
// 只断「保存返回成功」会永远绿（2026-09-25 轮那条 PASS 就是这么来的）。
func TestEditorReadersOnPG(t *testing.T) {
	s := newPGStoreForF44(t)
	// 随机化归属 ID，避免多次运行互踩（唯一键 ticket_id+lang+seg_index）。
	// 上限压在 int32 内：PG 侧 ticket_id/tenant_id 建的是 INTEGER 列，给到 Unix 纳秒会
	// 直接「value out of range for type integer」（首跑实测），那是一条与本缺陷无关的红。
	ticketID := time.Now().Unix()%1_400_000_000 + 500_000_000
	tenantID := ticketID%900_000 + 100_000
	filePath := "/uat/f44/in.pdf"
	defer func() {
		_, _ = db.Exec(s.DB(), db.CurrentDialect(), `DELETE FROM translation_edits WHERE ticket_id=?`, ticketID)
		_, _ = db.Exec(s.DB(), db.CurrentDialect(), `DELETE FROM ticket_segments WHERE ticket_id=?`, ticketID)
		_, _ = db.Exec(s.DB(), db.CurrentDialect(), `DELETE FROM kb_entries WHERE tenant_id=? AND source_text LIKE 'UATF44PG%'`, tenantID)
	}()

	// ① translation_edits：写一行修订，读回等值（旧写法在 PG 下这里直接报错）
	if err := s.UpsertTranslationEdit(tenantID, ticketID, "en", 0,
		"源文第一段", "机翻第一段", "人工修订第一段", "approved", "批注：术语按品牌表", 1); err != nil {
		t.Fatalf("PG 写修订失败: %v", err)
	}
	edits, err := s.GetTranslationEdits(ticketID, "en")
	if err != nil {
		t.Fatalf("★ F-44 复现：PG 下 GetTranslationEdits 报错（旧写法裸 `?` 未被方言改写）: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("GetTranslationEdits 应读回 1 行，实得 %d（读侧被吞成「无数据」就是这个形态）", len(edits))
	}
	e := edits[0]
	if e.SegIndex != 0 || e.EditedText != "人工修订第一段" || e.Status != "approved" || e.Note != "批注：术语按品牌表" {
		t.Errorf("修订行读回不等值: seg=%d edited=%q status=%q note=%q", e.SegIndex, e.EditedText, e.Status, e.Note)
	}
	if e.SourceText != "源文第一段" || e.TargetText != "机翻第一段" {
		t.Errorf("源文/系统译文未随修订一起回读: src=%q tgt=%q", e.SourceText, e.TargetText)
	}

	// ② ticket_segments：逐段真值表（PDF 工单的对照口径），同一段写侧走包装器、读侧曾裸用
	segs := []store.TicketSegment{
		{SegIndex: 0, Source: "第一段源文", Target: "first segment"},
		{SegIndex: 1, Source: "第二段源文", Target: "second segment"},
		{SegIndex: 2, Source: "第三段源文", Target: ""}, // 未译出：target 允许为空
	}
	if err := s.SaveTicketSegments(tenantID, ticketID, filePath, "en", segs); err != nil {
		t.Fatalf("PG 写逐段真值失败: %v", err)
	}
	got, err := s.GetTicketSegments(ticketID, filePath, "en")
	if err != nil {
		t.Fatalf("★ F-44 复现：PG 下 GetTicketSegments 报错: %v", err)
	}
	if len(got) != len(segs) {
		t.Fatalf("逐段真值应读回 %d 行，实得 %d", len(segs), len(got))
	}
	for i, g := range got {
		if g.SegIndex != i || g.Source != segs[i].Source || g.Target != segs[i].Target {
			t.Errorf("第%d段读回不等值: idx=%d src=%q tgt=%q", i, g.SegIndex, g.Source, g.Target)
		}
	}

	// ③ kb_entries 术语表：旧写法在 PG 下是 DISTINCT + ORDER BY 未选择列的语法错
	for _, term := range []string{"UATF44PG术语一", "UATF44PG术语二"} {
		if _, err := db.Exec(s.DB(), db.CurrentDialect(),
			`INSERT INTO kb_entries (tenant_id, source_text, target_lang, created_at) VALUES (?, ?, ?, ?)`,
			tenantID, term, "en", time.Now().Format(time.RFC3339)); err != nil {
			t.Fatalf("PG 种术语失败: %v", err)
		}
	}
	terms, err := s.ListKBTerms(tenantID, "en", 50)
	if err != nil {
		t.Fatalf("★ F-44 复现：PG 下 ListKBTerms 报错（DISTINCT/ORDER BY 口径）: %v", err)
	}
	if len(terms) != 2 {
		t.Fatalf("术语表应读回 2 条，实得 %d: %v", len(terms), terms)
	}
	if terms[0] != "UATF44PG术语二" {
		t.Errorf("术语须按 id 倒序（新词在前）返回，首条实得 %q", terms[0])
	}
	// 空串 lang=不限语种也要能在 PG 下跑通（编辑器按语种取，别的路径取全量）
	if all, err := s.ListKBTerms(tenantID, "", 50); err != nil || len(all) < 2 {
		t.Errorf("不限语种术语读取失败: len=%d err=%v", len(all), err)
	}
}
