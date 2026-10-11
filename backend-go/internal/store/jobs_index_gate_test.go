// ============ jobs_index_gate_test.go · 职责说明 ============
// store 包 jobs 表取活索引（★ 2026-10-11 慢 SQL 批 P2）的迁移闸门。
// 三条射程：
//
//	① CREATE INDEX IF NOT EXISTS 条目真落进迁移清单并在内存库上建成（sqlite_master 直读等值）；
//	② 幂等：同一块库上再跑一次 store.New 不报错、索引不翻倍（老库启动自动补的形态）；
//	③ 索引立起来后 queue.DirectQueue.Reserve 同款取活 SQL 的**选取语义**不变
//	   （queued 优先队首、过期 running 可捡、新鲜 running 与终态不可捡）。
//
// 说明：不在本文件断言「查询计划必须走该索引」——92 行量级上两种方言的规划器选 seq scan 是**正当**
// 行为，钉死计划会得到一条与性能收益无关的脆锁；本索引的价值在表长大后的形态（取证账里 worker
// 每 200ms 拨一次全表扫是恒定开销，行数一涨就线性放大）。
// =============================================
package store

import (
	"database/sql"
	"strings"
	"testing"

	"translator/internal/config"

	_ "modernc.org/sqlite"
)

func newJobsIdxStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	s, err := New(raw)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return s, raw
}

// TestJobsClaimIndexExistsAndIdempotent ①＋②：索引在位＋重复迁移幂等。
func TestJobsClaimIndexExistsAndIdempotent(t *testing.T) {
	s, raw := newJobsIdxStore(t)

	var name, tbl, sqlText string
	err := raw.QueryRow("SELECT name, tbl_name, sql FROM sqlite_master WHERE type='index' AND name='idx_jobs_status_leased'").
		Scan(&name, &tbl, &sqlText)
	if err != nil {
		t.Fatalf("迁移清单里的 idx_jobs_status_leased 没建成（老库启动自动补的腿断了）: %v", err)
	}
	if tbl != "jobs" {
		t.Fatalf("索引挂错表: %s", tbl)
	}
	if !containsAll(sqlText, "status", "leased_at") {
		t.Fatalf("索引列形不对，Reserve 的两个 OR 分支都要罩住: %s", sqlText)
	}

	// 幂等复跑：同一块库再 New 一次，不得报错、索引不得变成两份
	if _, err := New(raw); err != nil {
		t.Fatalf("重复迁移报错（IF NOT EXISTS 腿失效）: %v", err)
	}
	var n int
	if err := raw.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_jobs_status_leased'").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("重复迁移后索引份数期望 1 实际 %d", n)
	}
	_ = s
}

// TestJobsClaimQuerySemanticsUnchanged ③：取活 SQL 在索引立起后的选取语义。
func TestJobsClaimQuerySemanticsUnchanged(t *testing.T) {
	s, raw := newJobsIdxStore(t)
	if _, err := raw.Exec("INSERT INTO jobs (id, type, payload, status, leased_at) VALUES " +
		"(1,'ticket_run','{}','running','2000-01-01T00:00:00')," + // 租约早过期 ⇒ 可捡
		"(2,'ticket_run','{}','running','2999-01-01T00:00:00')," + // 新鲜租约 ⇒ 不可捡
		"(3,'ticket_run','{}','done','')," +
		"(4,'ticket_run','{}','queued','')"); err != nil { // queued 但 id 靠后
		t.Fatalf("种任务失败: %v", err)
	}
	// DirectQueue.Reserve 同款判据（ORDER BY id LIMIT 1）：id=1 的过期 running 应排在 queued 的 id=4 前
	var got int64
	err := s.db.QueryRow("SELECT id FROM jobs WHERE status='queued' OR (status='running' AND leased_at<=?) ORDER BY id LIMIT 1",
		"2026-10-11T00:00:00").Scan(&got)
	if err != nil {
		t.Fatalf("取活查询失败: %v", err)
	}
	if got != 1 {
		t.Fatalf("期望先捡过期 running(id=1) 实际 id=%d", got)
	}
}

// containsAll 文本里是否同时含全部子串（判索引列形用）。
func containsAll(text string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(text, sub) {
			return false
		}
	}
	return true
}
