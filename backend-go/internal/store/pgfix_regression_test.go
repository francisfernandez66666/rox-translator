// 职责：2026-09-12 PG 方言重研修复项的回归测试（SQLite 真实执行路径）——
// 句数余额守卫、欠费清零结算、任务返回自增 ID、卡死工单自动重排、个人标记落库。
// PG 形态由 internal/db/jsonops_test.go 与 UAT 双方言矩阵覆盖。
package store

import (
	"errors"
	"strconv"
	"testing"
	"time"

	"translator/internal/db"
	"translator/internal/tenant"
)

// TestSentenceBalanceGuard 句数镜像自增回归（JSONNumAdd；★ C26 删除守卫自减语义）。
func TestSentenceBalanceGuard(t *testing.T) {
	st, kdb := newKBEnv(t)
	if _, err := st.db.Exec("INSERT INTO tenants (id, code, name, status, permissions) VALUES (9,'t9','句数测试','active','')"); err != nil {
		t.Fatalf("种租户失败: %v", err)
	}
	if got, err := st.AddSentences(9, 100); err != nil || got != 100 {
		t.Fatalf("AddSentences 应得 100，实得 %d (err=%v)", got, err)
	}
	if got, err := st.AddSentences(9, 30); err != nil || got != 130 {
		t.Fatalf("镜像再自增应得 130，实得 %d (err=%v)", got, err)
	}
	if got, _ := st.GetSentenceBalance(9); got != 130 {
		t.Fatalf("镜像回读应为 130，实得 %d", got)
	}
	_ = kdb
}

// TestSettleExhausted 欠费停用结算（★ P0-1 修复 2026-09-14）：
// 事务内复核 + 有界清零（消耗 ≤ owed）+ 调整流水 + 幂等。
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
	// ① 复核防线：余额（800）≥ owed（100）→ 拒绝结算（修复 TOCTOU 误清零）
	if _, err := st.SettleExhausted(7, 100); !errors.Is(err, ErrSettleNotNeeded) {
		t.Fatalf("余额足够时应返回 ErrSettleNotNeeded，实得 %v", err)
	}
	// ② 欠费（owed=2000 > 可用 800）→ 有界清零且消耗量=可用量
	consumed, err := st.SettleExhausted(7, 2000)
	if err != nil {
		t.Fatalf("SettleExhausted 失败: %v", err)
	}
	if consumed != 800 {
		t.Fatalf("应消耗 800（trial 300 + 余额 500），实得 %d", consumed)
	}
	var bal, left int64
	if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT balance FROM balance_accounts WHERE tenant_id=7").Scan(&bal); err != nil || bal != 0 {
		t.Fatalf("永久余额应清零，实得 %d (err=%v)", bal, err)
	}
	if err := db.QueryRow(st.db, db.CurrentDialect(), `SELECT COALESCE(SUM("left"),0) FROM quota_grants WHERE tenant_id=7`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("发放台账应清零，实得 %d (err=%v)", left, err)
	}
	// ③ 调整流水：清零量必须落 ledger（charge_kind='settle'），可追偿可审计
	var settleCost int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=7 AND charge_kind='settle'`).Scan(&settleCost); err != nil || settleCost != 800 {
		t.Fatalf("欠费结算应落调整流水 800，实得 %d (err=%v)", settleCost, err)
	}
	// ④ 幂等：再次调用消耗 0
	if c2, err := st.SettleExhausted(7, 2000); err != nil || c2 != 0 {
		t.Fatalf("SettleExhausted 应幂等，二次调用实得 (%d, %v)", c2, err)
	}
}

// TestSettleExhaustedNoPermAccount 永久余额行缺失时的结算（★ 缺陷核实修复 D1 · 2026-09-16）：
// 兜底分支只应容忍 sql.ErrNoRows（无账户=无永久余额可清），其余 DB 错误必须上抛——
// 旧实现误判陈旧变量 qerr（恒 nil），真实错误会被静默吞掉导致部分欠费无痕消失。
// 本用例锁死「缺账户 + 台账不足」路径：消耗=台账量、无错误、调整流水同额。
func TestSettleExhaustedNoPermAccount(t *testing.T) {
	st, _ := newKBEnv(t)
	if _, err := st.db.Exec("INSERT INTO tenants (id, code, name, status) VALUES (8,'t8','缺账户结算','active')"); err != nil {
		t.Fatalf("种租户失败: %v", err)
	}
	// 只发台账 400，不建 balance_accounts 行（QueryRow 必然 ErrNoRows）
	if err := st.CreateQuotaGrant(8, "plan", 400, time.Now().Add(24*time.Hour), "test", 0); err != nil {
		t.Fatalf("发放额度失败: %v", err)
	}
	consumed, err := st.SettleExhausted(8, 1000)
	if err != nil {
		t.Fatalf("缺永久账户应容忍（ErrNoRows 路径），实得错误: %v", err)
	}
	if consumed != 400 {
		t.Fatalf("应消耗台账 400，实得 %d", consumed)
	}
	var settleCost int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=8 AND charge_kind='settle'`).Scan(&settleCost); err != nil || settleCost != 400 {
		t.Fatalf("调整流水应为 400，实得 %d (err=%v)", settleCost, err)
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
