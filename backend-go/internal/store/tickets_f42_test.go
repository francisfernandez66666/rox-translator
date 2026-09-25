// ============ tickets_f42_test.go · 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 C（F-42 假 completed 专项）store 层回归断言，钉三条：
//
//	① F-42-b：reject_source 列的读写往返（UpdateTicket / FinishTicket / GetTicket /
//	   ListTickets 四条通道都必须把来源带回来）——判据侧靠它区分「人工驳回意见」与
//	   「系统失败原因」，任何一条通道漏带都会让收窄失效；
//	② F-42-c：ClaimTicketForRun 认领收窄——queued/draft 照旧可认领；rejected 只有
//	   reject_source='human'（审批台明确写了意见）才自动认领；系统类 rejected
//	   （'system'）与历史空串行一律 0 行（旧实现无差别放行，与队列自动回队合成
//	   无限重跑并最终刷成假 completed，生产任务 90 即此型）；
//	③ 幂等迁移：老库（先建不含 reject_source 的表）经 Store.New 自动补列，
//	   老行默认空串、读取不报错（AGENTS §一·1 第 4 条：新增列必须走幂等补列）。
//
// 方言：显式钉死 SQLite 内存库（AGENTS.md §一·4，防 run_uat 的 PG env 泄漏方言）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TestUATBatchC
// =============================================
package store

import (
	"database/sql"
	"testing"

	"translator/internal/config"

	_ "modernc.org/sqlite"
)

// batchCEnv 批 C 测试环境：钉 SQLite 方言 + 独立内存库 Store。
// 参数：t。返回：已完成迁移（含 reject_source 列）的 *Store。
func batchCEnv(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	s, err := New(conn)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return s
}

// setStatusCol 测试辅助：绕开业务方法直改工单状态列（模拟队列回队/历史脏行等外部形态）。
func setStatusCol(t *testing.T, s *Store, id int64, status, source string) {
	t.Helper()
	if _, err := s.db.Exec("UPDATE tickets SET status=?, reject_source=? WHERE id=?", status, source, id); err != nil {
		t.Fatalf("直改工单 %d 状态失败: %v", id, err)
	}
}

// TestUATBatchC_RejectSourceRoundTrip ①：四条读写通道都要带回 reject_source。
func TestUATBatchC_RejectSourceRoundTrip(t *testing.T) {
	s := batchCEnv(t)
	tk, err := s.CreateTicket(11, 3, "来源往返单", "原文一句话", "", "en")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	tk.Status = TicketRejected
	tk.RejectReason = "术语不一致，请重翻"
	tk.RejectSource = RejectSourceHuman
	if err := s.UpdateTicket(tk); err != nil {
		t.Fatalf("UpdateTicket 失败: %v", err)
	}
	got, err := s.GetTicket(tk.ID, 11)
	if err != nil {
		t.Fatalf("GetTicket 失败: %v", err)
	}
	if got.RejectSource != RejectSourceHuman {
		t.Errorf("GetTicket 未带回 reject_source，实得 %q", got.RejectSource)
	}
	list, err := s.ListTickets(11, 3, false)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTickets 异常: len=%d err=%v", len(list), err)
	}
	if list[0].RejectSource != RejectSourceHuman {
		t.Errorf("ListTickets 未带回 reject_source，实得 %q", list[0].RejectSource)
	}
	// FinishTicket 事务通道：内存对象改成 system，落库必须是 system
	// （旧 SQL 不含该列 ⇒ 收尾写入会把刚标的来源静默清掉）
	tk.RejectSource = RejectSourceSystem
	tk.RejectReason = "insufficient_balance: 余额不足，请充值或升级套餐"
	applied, err := s.FinishTicket(&TicketFinishInput{Ticket: tk, ExcludeStatuses: []string{"cancelled"}})
	if err != nil || !applied {
		t.Fatalf("FinishTicket 失败: applied=%v err=%v", applied, err)
	}
	got2, _ := s.GetTicketGlobal(tk.ID)
	if got2 == nil || got2.RejectSource != RejectSourceSystem {
		t.Errorf("FinishTicket 未落 reject_source，实得 %q", got2.RejectSource)
	}
	if got2.RejectReason != "insufficient_balance: 余额不足，请充值或升级套餐" {
		t.Errorf("收尾原因被改写: %q", got2.RejectReason)
	}
}

// TestUATBatchC_ClaimTicketForRunNarrowed ②：认领收窄的四态判据（含「系统类不得认领」负向）。
func TestUATBatchC_ClaimTicketForRunNarrowed(t *testing.T) {
	s := batchCEnv(t)
	cases := []struct {
		name   string
		status string
		source string
		want   int64 // 期望受影响行数
	}{
		{"排队单可认领", TicketQueued, "", 1},
		{"草稿单可认领", TicketDraft, "", 1},
		{"人工驳回可重翻", TicketRejected, RejectSourceHuman, 1},
		{"系统驳回不得自动认领", TicketRejected, RejectSourceSystem, 0},
		{"历史空串驳回不得自动认领", TicketRejected, "", 0},
		{"已完成不得认领", TicketCompleted, "", 0},
	}
	for i, c := range cases {
		tk, err := s.CreateTicket(12, int64(20+i), c.name, "原文", "", "en")
		if err != nil {
			t.Fatalf("建单失败: %v", err)
		}
		setStatusCol(t, s, tk.ID, c.status, c.source)
		n, err := s.ClaimTicketForRun(tk.ID)
		if err != nil {
			t.Fatalf("%s: 认领出错 %v", c.name, err)
		}
		if n != c.want {
			t.Errorf("%s（status=%s source=%q）期望认领 %d 行，实得 %d", c.name, c.status, c.source, c.want, n)
		}
		if c.want == 0 {
			fresh, _ := s.GetTicketGlobal(tk.ID)
			if fresh != nil && fresh.Status == TicketInProgress {
				t.Errorf("%s 未被认领却翻到 in_progress", c.name)
			}
		}
	}
}

// TestUATBatchC_OldDBGetsColumn ③：不含 reject_source 的老库经 Store.New 自动补列，
// 老行读出空串且不报错（幂等迁移判据；漏挂迁移链会让生产启动后第一条列表查询直接红灯）。
func TestUATBatchC_OldDBGetsColumn(t *testing.T) {
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(`CREATE TABLE tickets (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 1,
		ticket_no TEXT NOT NULL DEFAULT '',
		title TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'draft',
		source_text TEXT NOT NULL DEFAULT '',
		file_path TEXT NOT NULL DEFAULT '',
		target_langs TEXT NOT NULL DEFAULT '',
		created_by INTEGER NOT NULL DEFAULT 0,
		approver_id INTEGER NOT NULL DEFAULT 0,
		reviewer_id INTEGER NOT NULL DEFAULT 0,
		reject_reason TEXT NOT NULL DEFAULT '',
		final_result TEXT NOT NULL DEFAULT '',
		created_at TEXT,
		updated_at TEXT
	)`); err != nil {
		t.Fatalf("建老表失败: %v", err)
	}
	if _, err := conn.Exec(`INSERT INTO tickets (tenant_id, ticket_no, title, status, reject_reason, created_at, updated_at)
		VALUES (1,'T_OLD','历史驳回单','rejected','旧数据没有来源列','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatalf("灌老行失败: %v", err)
	}
	s, err := New(conn)
	if err != nil {
		t.Fatalf("老库 Store.New 失败（补列未生效？）: %v", err)
	}
	tk, err := s.GetTicket(1, 1)
	if err != nil {
		t.Fatalf("老库读单失败: %v", err)
	}
	if tk.RejectSource != "" {
		t.Errorf("老行 reject_source 应为空串，实得 %q", tk.RejectSource)
	}
	// 空串行不得被自动认领（保守口径）
	if n, _ := s.ClaimTicketForRun(1); n != 0 {
		t.Error("历史 rejected 空串源不得自动认领")
	}
}
