// ============ 本文件职责中文说明 ============
// #40②（2026-09-21 评审缺陷）工单收尾事务单测：
//
//	① 一次事务内同时落「状态/计费 + 产物到期时间 + 站内信」；
//	② 站内信写入失败必须整体回滚（不留「已完成的工单却没通知」的半收尾状态）；
//	③ 状态守卫命中（工单已被取消/被其他副本重排）时一行都不写。
//
// ★ 方言自钉：见 AGENTS.md 一.4——config.Default() 会写全局 config.C，
//
//	必须先存旧值并 Cleanup 还原，否则 run_uat 的 PG 模式会污染本包内存 SQLite 用例。
package store

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
)

// newTestStoreTicketFinish 建一张内存 SQLite，只建本测试涉及的 tickets / notifications 表。
func newTestStoreTicketFinish(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	schema := []string{
		`CREATE TABLE tickets (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			ticket_no TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			source_text TEXT NOT NULL DEFAULT '',
			file_path TEXT NOT NULL DEFAULT '',
			target_langs TEXT NOT NULL DEFAULT '',
			created_by INTEGER NOT NULL DEFAULT 0,
			approver_id INTEGER NOT NULL DEFAULT 0,
			reviewer_id INTEGER NOT NULL DEFAULT 0,
			reject_reason TEXT NOT NULL DEFAULT '',
			final_result TEXT NOT NULL DEFAULT '',
			result_path TEXT NOT NULL DEFAULT '',
			mode TEXT NOT NULL DEFAULT '',
			tokens_billed INTEGER NOT NULL DEFAULT 0,
			api_user_id INTEGER NOT NULL DEFAULT 0,
			max_length INTEGER NOT NULL DEFAULT 0,
			delivery TEXT NOT NULL DEFAULT '',
			text_result_path TEXT NOT NULL DEFAULT '',
			result_expires_at TEXT NOT NULL DEFAULT '',
			expire_notify TEXT NOT NULL DEFAULT '',
			created_at TEXT,
			updated_at TEXT
		)`,
		`CREATE TABLE notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL DEFAULT 0,
			title TEXT NOT NULL DEFAULT '',
			body TEXT NOT NULL DEFAULT '',
			ref_type TEXT NOT NULL DEFAULT '',
			ref_id INTEGER NOT NULL DEFAULT 0,
			read_at TEXT NOT NULL DEFAULT '',
			created_at TEXT
		)`,
	}
	for _, ddl := range schema {
		if _, err := raw.Exec(ddl); err != nil {
			t.Fatalf("建表失败: %v", err)
		}
	}
	if _, err := raw.Exec(`INSERT INTO tickets (id, tenant_id, ticket_no, title, status, created_by, mode, target_langs)
		VALUES (1, 7, 'T-1', '测试工单', 'in_progress', 42, 'pro', '["en"]')`); err != nil {
		t.Fatalf("插入测试工单失败: %v", err)
	}
	s, err := New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	t.Cleanup(func() { raw.Close() })
	return s
}

// finishTicket 取工单收尾入参的公共骨架（调用方按需覆盖状态/守卫）。
func finishTicket(status string) *TicketFinishInput {
	return &TicketFinishInput{
		Ticket: &Ticket{
			ID:           1,
			TenantID:     7,
			TicketNo:     "T-1",
			Title:        "测试工单",
			Status:       status,
			TargetLangs:  `["en"]`,
			Mode:         "pro",
			TokensBilled: 1234,
		},
		ExpiresAt: time.Now().AddDate(0, 0, 14).Format(time.RFC3339),
		Notify: &Notification{
			UserID:  42,
			Title:   "翻译工单完成：测试工单",
			Body:    "工单号 T-1 翻译完成。",
			RefType: "ticket",
			RefID:   1,
		},
	}
}

// TestFinishTicketWritesAllThree 完成态：状态 + 到期时间 + 站内信三者同事务落库。
func TestFinishTicketWritesAllThree(t *testing.T) {
	s := newTestStoreTicketFinish(t)
	in := finishTicket(TicketCompleted)
	in.ExcludeStatuses = []string{"cancelled", "queued"}
	applied, err := s.FinishTicket(in)
	if err != nil || !applied {
		t.Fatalf("收尾应成功，实际 applied=%v err=%v", applied, err)
	}
	var status, expires string
	var billed int64
	if err := s.db.QueryRow(`SELECT status, result_expires_at, tokens_billed FROM tickets WHERE id=1`).
		Scan(&status, &expires, &billed); err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	if status != TicketCompleted || billed != 1234 {
		t.Fatalf("工单状态/计费未落库: status=%s billed=%d", status, billed)
	}
	if expires == "" {
		t.Fatalf("到期时间未落库（留存扫描将永远漏掉该工单）")
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications WHERE user_id=42 AND ref_id=1`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("站内信未落库: n=%d err=%v", n, err)
	}
}

// TestFinishTicketRollsBackOnNotifyError 站内信写入失败时整体回滚：
// 工单不得停留在「已完成却没通知」的半收尾状态（#40② 的核心动机）。
func TestFinishTicketRollsBackOnNotifyError(t *testing.T) {
	s := newTestStoreTicketFinish(t)
	// 制造第二条写的必然失败：删掉 notifications 表（模拟迁移缺表/权限异常）
	if _, err := s.db.Exec(`DROP TABLE notifications`); err != nil {
		t.Fatalf("DROP 失败: %v", err)
	}
	in := finishTicket(TicketCompleted)
	in.ExcludeStatuses = []string{"cancelled", "queued"}
	applied, err := s.FinishTicket(in)
	if err == nil {
		t.Fatalf("站内信写失败必须返回错误，实际 applied=%v err=<nil>", applied)
	}
	if applied {
		t.Fatalf("事务失败时不得报告已写入")
	}
	var status, expires string
	if err := s.db.QueryRow(`SELECT status, result_expires_at FROM tickets WHERE id=1`).Scan(&status, &expires); err != nil {
		t.Fatalf("回读工单失败: %v", err)
	}
	if status != "in_progress" || expires != "" {
		t.Fatalf("事务未回滚：status=%s expires=%q", status, expires)
	}
}

// TestFinishTicketGuardSkipsCancelled 状态守卫：工单已被取消/被巡检重排时一行都不写。
func TestFinishTicketGuardSkipsCancelled(t *testing.T) {
	for _, cur := range []string{"cancelled", "queued"} {
		s := newTestStoreTicketFinish(t)
		if _, err := s.db.Exec(`UPDATE tickets SET status=? WHERE id=1`, cur); err != nil {
			t.Fatalf("预置状态失败: %v", err)
		}
		in := finishTicket(TicketCompleted)
		in.ExcludeStatuses = []string{"cancelled", "queued"}
		applied, err := s.FinishTicket(in)
		if err != nil {
			t.Fatalf("守卫命中不应报错: %v", err)
		}
		if applied {
			t.Fatalf("当前状态 %s 应被守卫拦下，实际写入", cur)
		}
		var got string
		var n int
		if err := s.db.QueryRow(`SELECT status FROM tickets WHERE id=1`).Scan(&got); err != nil {
			t.Fatalf("回读失败: %v", err)
		}
		if got != cur {
			t.Fatalf("守卫命中却改了状态: %s → %s", cur, got)
		}
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&n); err != nil || n != 0 {
			t.Fatalf("守卫命中仍发了站内信: n=%d err=%v", n, err)
		}
	}
}

// TestFinishTicketWithoutExpiryOrNotify 永久保留（到期空）与不发通知的组合：
// 只更新工单本体，SQL 片段不得残留 result_expires_at / 不得写 notifications。
func TestFinishTicketWithoutExpiryOrNotify(t *testing.T) {
	s := newTestStoreTicketFinish(t)
	in := finishTicket(TicketRejected)
	in.ExpiresAt = ""
	in.Notify = nil
	in.Ticket.RejectReason = "模型超时"
	applied, err := s.FinishTicket(in)
	if err != nil || !applied {
		t.Fatalf("收尾应成功，实际 applied=%v err=%v", applied, err)
	}
	var expires, reject string
	if err := s.db.QueryRow(`SELECT COALESCE(result_expires_at,''), reject_reason FROM tickets WHERE id=1`).
		Scan(&expires, &reject); err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if expires != "" && !strings.EqualFold(expires, "null") {
		t.Fatalf("到期为空时不应写入 result_expires_at，实际 %q", expires)
	}
	if reject != "模型超时" {
		t.Fatalf("失败原因未落库: %q", reject)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM notifications`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("Notify=nil 时不应发站内信: n=%d err=%v", n, err)
	}
}
