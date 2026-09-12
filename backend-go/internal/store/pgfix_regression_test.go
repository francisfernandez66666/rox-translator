// 职责：2026-09-12 PG 方言重研修复项的回归测试（SQLite 真实执行路径）——
// 句数余额守卫、欠费清零结算、任务返回自增 ID、卡死工单自动重排、个人标记落库。
// PG 形态由 internal/db/jsonops_test.go 与 UAT 双方言矩阵覆盖。
package store

import (
	"strconv"
	"testing"
	"time"

	"translator/internal/db"
	"translator/internal/tenant"
)

// TestSentenceBalanceGuard 增量包句数：自增/守卫自减/余额不足拦截（JSONNumAdd/JSONNumGE 回归）。
func TestSentenceBalanceGuard(t *testing.T) {
	st, kdb := newKBEnv(t)
	if _, err := st.db.Exec("INSERT INTO tenants (id, code, name, status, permissions) VALUES (9,'t9','句数测试','active','')"); err != nil {
		t.Fatalf("种租户失败: %v", err)
	}
	if got, err := st.AddSentences(9, 100); err != nil || got != 100 {
		t.Fatalf("AddSentences 应得 100，实得 %d (err=%v)", got, err)
	}
	if got, err := st.DeductSentences(9, 30); err != nil || got != 70 {
		t.Fatalf("DeductSentences 应得 70，实得 %d (err=%v)", got, err)
	}
	if _, err := st.DeductSentences(9, 150); err != ErrSentenceExhausted {
		t.Fatalf("超额扣减应返回 ErrSentenceExhausted，实得 %v", err)
	}
	if got, _ := st.GetSentenceBalance(9); got != 70 {
		t.Fatalf("失败扣减不应改变余额，实得 %d", got)
	}
	_ = kdb
}

// TestSettleExhausted 欠费停用结算：双桶清零 + 幂等（决策 2026-09-12）。
func TestSettleExhausted(t *testing.T) {
	st, _ := newKBEnv(t)
	if _, err := st.db.Exec("INSERT INTO tenants (id, code, name, status) VALUES (7,'t7','欠费测试','active')"); err != nil {
		t.Fatalf("种租户失败: %v", err)
	}
	if _, err := st.db.Exec("INSERT INTO balance_accounts (tenant_id, balance, updated_at) VALUES (7, 500, ?)", time.Now().Format(time.RFC3339)); err != nil {
		t.Fatalf("种余额失败: %v", err)
	}
	if err := st.CreateQuotaGrant(7, "trial", 300, time.Now().Add(24*time.Hour), "test", 0); err != nil {
		t.Fatalf("发放额度失败: %v", err)
	}
	if err := st.SettleExhausted(7); err != nil {
		t.Fatalf("SettleExhausted 失败: %v", err)
	}
	var bal, left int64
	if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT balance FROM balance_accounts WHERE tenant_id=7").Scan(&bal); err != nil || bal != 0 {
		t.Fatalf("永久余额应清零，实得 %d (err=%v)", bal, err)
	}
	if err := db.QueryRow(st.db, db.CurrentDialect(), `SELECT COALESCE(SUM("left"),0) FROM quota_grants WHERE tenant_id=7`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("发放台账应清零，实得 %d (err=%v)", left, err)
	}
	if err := st.SettleExhausted(7); err != nil {
		t.Fatalf("SettleExhausted 应幂等，二次调用失败: %v", err)
	}
}

// TestSaveUserTaskReturnsID 新建任务必须返回自增 ID（PG 下 LastInsertId 恒 0 的回归）。
func TestSaveUserTaskReturnsID(t *testing.T) {
	st, _ := newKBEnv(t)
	id, err := st.SaveUserTask(&UserTask{TaskType: "once", Title: "完善资料", RewardTokens: 10, Enabled: 1})
	if err != nil || id <= 0 {
		t.Fatalf("SaveUserTask 应返回正 ID，实得 %d (err=%v)", id, err)
	}
	var title string
	if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT title FROM user_tasks WHERE id=?", id).Scan(&title); err != nil || title != "完善资料" {
		t.Fatalf("按返回 ID 应能查回任务，实得 %q (err=%v)", title, err)
	}
	// 更新路径：ID 原样返回且不新建行
	if got, err := st.SaveUserTask(&UserTask{ID: id, TaskType: "daily", Title: "完善资料2", RewardTokens: 20, Enabled: 1}); err != nil || got != id {
		t.Fatalf("更新应返回原 ID %d，实得 %d (err=%v)", id, got, err)
	}
}

// TestRequeueStalledTickets 卡死工单重排：无活跃 job 的重排；有 running job 的受保护（JSONTicketIDExpr 回归）。
func TestRequeueStalledTickets(t *testing.T) {
	st, _ := newKBEnv(t)
	if _, err := st.db.Exec("INSERT INTO tenants (id, code, name, status) VALUES (5,'t5','工单测试','active')"); err != nil {
		t.Fatalf("种租户失败: %v", err)
	}
	old := time.Now().Add(-2 * time.Hour).Format(time.RFC3339)
	// 工单 A：in_progress 且超时、无 running job → 应重排
	tkA, err := st.CreateTicket(5, 1, "卡死工单A", "hello", "", "en")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE tickets SET status='in_progress', updated_at=? WHERE id=?", old, tkA.ID); err != nil {
		t.Fatalf("置 in_progress 失败: %v", err)
	}
	// 工单 B：in_progress 且超时、但有 running job → 不得重排（双副本防线 R3）
	tkB, err := st.CreateTicket(5, 1, "活跃工单B", "hello", "", "en")
	if err != nil {
		t.Fatalf("建工单失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE tickets SET status='in_progress', updated_at=? WHERE id=?", old, tkB.ID); err != nil {
		t.Fatalf("置 in_progress 失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		"INSERT INTO jobs (type, payload, status) VALUES ('ticket_run', ?, 'running')",
		`{"ticket_id":`+strconv.FormatInt(tkB.ID, 10)+`}`); err != nil {
		t.Fatalf("种 running job 失败: %v", err)
	}
	n, err := st.RequeueStalledTickets(time.Hour)
	if err != nil {
		t.Fatalf("RequeueStalledTickets 失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("应恰好重排 1 单（A），实得 %d", n)
	}
	var sA, sB string
	_ = db.QueryRow(st.db, db.CurrentDialect(), "SELECT status FROM tickets WHERE id=?", tkA.ID).Scan(&sA)
	_ = db.QueryRow(st.db, db.CurrentDialect(), "SELECT status FROM tickets WHERE id=?", tkB.ID).Scan(&sB)
	if sA != "queued" || sB != "in_progress" {
		t.Fatalf("重排结果异常：A=%s（应 queued）B=%s（应 in_progress）", sA, sB)
	}
}

// TestTenantPersonalFlag 个人标记/邀请开关：bool→INTEGER 列写入（PG P0 回归）。
func TestTenantPersonalFlag(t *testing.T) {
	_, kdb := newKBEnv(t)
	ts, err := tenant.NewStore(kdb.RawDB())
	if err != nil {
		t.Fatalf("创建租户存储失败: %v", err)
	}
	tt, err := ts.Create("tp1", "个人标记测试", "", "")
	if err != nil {
		t.Fatalf("建租户失败: %v", err)
	}
	if err := ts.SetPersonal(tt.ID, true); err != nil {
		t.Fatalf("SetPersonal(true) 失败: %v", err)
	}
	if err := ts.SetInviteEnabled(tt.ID, false); err != nil {
		t.Fatalf("SetInviteEnabled(false) 失败: %v", err)
	}
	got, err := ts.GetByID(tt.ID)
	if err != nil {
		t.Fatalf("读租户失败: %v", err)
	}
	if !got.IsPersonal {
		t.Fatal("is_personal 应为 true")
	}
	if got.InviteEnabled {
		t.Fatal("invite_enabled 应为 false")
	}
	// 反向翻转再读一次（确保不是默认值巧合）
	if err := ts.SetPersonal(tt.ID, false); err != nil {
		t.Fatalf("SetPersonal(false) 失败: %v", err)
	}
	if got, _ = ts.GetByID(tt.ID); got.IsPersonal {
		t.Fatal("is_personal 翻转后应为 false")
	}
}
