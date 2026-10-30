// ============ wallet_refund_test.go · 职责说明 ============
// 按档退款的三条锁（★ 2026-10-30 三事合一批 组3 #8）：
//
//	W13 扣 3 档 → 退款 → 3 档 left 各自逐字复原，且 SUM(tokens)=SUM(refunded_tokens)
//	W14 同一笔退两次 ⇒ 第二次一分不回（幂等），余额不再增长
//	W15 原档已过期 ⇒ 不回补、只记账与 WARN（U-2 口径：不许凭空造已作废额度）
//
// 另加一腿「非订阅单但额度在台账行里」：RefundOrder 必须按台账收回（D-6 之后长期包落
// plan_permanent 台账行，ptype 未必是 paid；旧实现只看 ptype ⇒ 从永久余额收，一分收不到）。
//
// 夹具手法：测试在包内（package store），所以直接调 deductWalletTx ＋ 手工落 usage_ledger，
// 不经过 pricingCost（那会把"扣多少"变成定价表的函数，用例就测不到拆扣本身）。
// =============================================
package store

import (
	"testing"
	"time"

	"translator/internal/db"
)

// mustInsertGrant 直接插一条台账行（显式 user_id/tier，跳过回填），返回 id。
func mustInsertGrant(t *testing.T, st *Store, tid, uid int64, tier string, total int64, expires time.Time) int64 {
	t.Helper()
	id, err := db.InsertID(st.db, db.CurrentDialect(), "id",
		`INSERT INTO quota_grants (tenant_id, user_id, tier, kind, total, "left", expires_at, source, ref_id, created_at)
		 VALUES (?,?,?,?,?,?,?,'test',0,?)`,
		tid, uid, tier, "trial", total, total, expires.UTC().Format(time.RFC3339), time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("插台账行失败: %v", err)
	}
	return id
}

// grantLeftOf 读一条台账行的剩余量。
func grantLeftOf(t *testing.T, st *Store, id int64) int64 {
	t.Helper()
	var left int64
	if err := db.QueryRow(st.db, db.CurrentDialect(), `SELECT "left" FROM quota_grants WHERE id=?`, id).Scan(&left); err != nil {
		t.Fatalf("读台账行 %d 失败: %v", id, err)
	}
	return left
}

// balanceOf 读某租户永久余额。
func balanceOf(t *testing.T, st *Store, tid int64) int64 {
	t.Helper()
	var bal int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		"SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=?", tid).Scan(&bal); err != nil {
		t.Fatalf("读余额失败: %v", err)
	}
	return bal
}

// mustDebitLedger 走真实拆扣并落一条 usage_ledger，返回该流水 id（退款按它找明细）。
func mustDebitLedger(t *testing.T, st *Store, tid, uid, tokens int64) int64 {
	t.Helper()
	tx, err := st.db.Begin()
	if err != nil {
		t.Fatalf("开事务失败: %v", err)
	}
	defer tx.Rollback()
	lines, err := deductWalletTx(tx, tid, uid, tokens, false)
	if err != nil {
		t.Fatalf("拆扣失败: %v", err)
	}
	id, err := db.InsertID(tx, db.CurrentDialect(), "id",
		"INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, charge_kind, created_at) VALUES (?,?,?,?,?,?,?,?, 'charge', ?)",
		tid, uid, "translate", "test", "test-model", tokens, 1, tokens, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("落流水失败: %v", err)
	}
	if err := recordDebitLines(tx, tid, id, lines); err != nil {
		t.Fatalf("落扣档明细失败: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("提交失败: %v", err)
	}
	return id
}

// W13：三档各扣一部分，退款后三档 left 逐字复原，且明细的 SUM(tokens)=SUM(refunded_tokens)。
func TestWalletRefund_ByTierRestoresEveryBucket(t *testing.T) {
	st := newTestStore(t)
	tid, uid := int64(101), int64(9001)
	now := time.Now().UTC()
	gRegister := mustInsertGrant(t, st, tid, uid, TierRegister, 100, now.Add(10*24*time.Hour))
	gTask := mustInsertGrant(t, st, tid, uid, TierTask, 100, now.Add(5*24*time.Hour)) // 更早到期 ⇒ 先核销
	gMonth := mustInsertGrant(t, st, tid, 0, TierPlanMonthly, 100, now.Add(30*24*time.Hour))

	ledgerID := mustDebitLedger(t, st, tid, uid, 250)
	// 桶1 内部先到期先花（task 5 天 → register 10 天），再吃桶2 的月包
	if l := grantLeftOf(t, st, gTask); l != 0 {
		t.Fatalf("W13 前置：任务档应先被扣满，实得 left=%d", l)
	}
	if l := grantLeftOf(t, st, gRegister); l != 0 {
		t.Fatalf("W13 前置：注册赠送应其次被扣满，实得 left=%d", l)
	}
	if l := grantLeftOf(t, st, gMonth); l != 50 {
		t.Fatalf("W13 前置：月包应只被扣 50，实得 left=%d", l)
	}

	res, err := st.RefundUsageByLedger(ledgerID)
	if err != nil {
		t.Fatalf("按档退款失败: %v", err)
	}
	if res.Refunded != 250 || res.Lines != 3 {
		t.Fatalf("退款读数不对：refunded=%d lines=%d（期望 250/3）", res.Refunded, res.Lines)
	}
	for _, c := range []struct {
		name string
		id   int64
	}{{"register", gRegister}, {"task", gTask}, {"plan_month", gMonth}} {
		if l := grantLeftOf(t, st, c.id); l != 100 {
			t.Fatalf("W13：%s 档未逐字复原，left=%d（期望 100）", c.name, l)
		}
	}
	var debitSum, refundSum int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COALESCE(SUM(tokens),0), COALESCE(SUM(refunded_tokens),0) FROM usage_debit_lines WHERE ledger_id=?`,
		ledgerID).Scan(&debitSum, &refundSum); err != nil {
		t.Fatalf("读明细合计失败: %v", err)
	}
	if debitSum != refundSum || debitSum != 250 {
		t.Fatalf("W13 对账等式不成立：SUM(tokens)=%d SUM(refunded_tokens)=%d（应等值 250）", debitSum, refundSum)
	}
}

// W14：同一笔退两次 ⇒ 第二次一分不回；客户退完又花掉也不许把余额顶穿 total。
func TestWalletRefund_IdempotentAndCapped(t *testing.T) {
	st := newTestStore(t)
	tid, uid := int64(102), int64(9002)
	now := time.Now().UTC()
	g := mustInsertGrant(t, st, tid, uid, TierTask, 100, now.Add(5*24*time.Hour))

	ledgerID := mustDebitLedger(t, st, tid, uid, 80)
	if _, err := st.RefundUsageByLedger(ledgerID); err != nil {
		t.Fatalf("首次退款失败: %v", err)
	}
	if l := grantLeftOf(t, st, g); l != 100 {
		t.Fatalf("首次退款后应复原为 100，实得 %d", l)
	}
	// 幂等标记必须真推进：它是"这一条已经退过"的唯一持久事实
	//（只靠容量守卫挡重复退是第二层网，挡不住"客户退完又花掉、再退一次"那一档）。
	var refundedMark int64
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COALESCE(SUM(refunded_tokens),0) FROM usage_debit_lines WHERE ledger_id=?`, ledgerID).Scan(&refundedMark); err != nil {
		t.Fatalf("读幂等标记失败: %v", err)
	}
	if refundedMark != 80 {
		t.Fatalf("W14：幂等标记必须推进到 80，实得 %d（标记不推进＝同一笔能退两次）", refundedMark)
	}
	// 第二次退款：refunded_tokens 已写满 ⇒ 一分不回
	res2, err := st.RefundUsageByLedger(ledgerID)
	if err != nil {
		t.Fatalf("二次退款失败: %v", err)
	}
	if res2.Refunded != 0 {
		t.Fatalf("W14：二次退款不许再回补，实得 %d", res2.Refunded)
	}
	if l := grantLeftOf(t, st, g); l != 100 {
		t.Fatalf("W14：二次退款把余额顶穿 total，left=%d（期望 100）", l)
	}

	// 容量守卫：客户退完又花掉一大截，这一行只剩 95（total=100）。
	// 再退 80 只该补回剩下的 5，其余 75 记 CappedSkipped——**不许**把 left 顶穿 total，
	// 也不许"按 total 补满"（那会把客户这段时间正常花掉的额度也退回来）。
	if _, err := db.Exec(st.db, db.CurrentDialect(), `UPDATE quota_grants SET "left"=95 WHERE id=?`, g); err != nil {
		t.Fatalf("构造低余额失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		`UPDATE usage_debit_lines SET refunded_tokens=0 WHERE ledger_id=?`, ledgerID); err != nil {
		t.Fatalf("重置幂等标记失败: %v", err)
	}
	res3, err := st.RefundUsageByLedger(ledgerID)
	if err != nil {
		t.Fatalf("容量守卫退款失败: %v", err)
	}
	if l := grantLeftOf(t, st, g); l != 100 {
		t.Fatalf("W14：容量守卫失效，left=%d（期望 100，不许超 total）", l)
	}
	if res3.Refunded != 5 || res3.CappedSkipped != 75 {
		t.Fatalf("W14：容量守卫读数不对，实得 %+v（期望 refunded=5 capped=75）", res3)
	}
}

// W15：原档在扣款之后过期 ⇒ 不回补，只记账；其余档照常回补。
func TestWalletRefund_ExpiredBucketNotReplenished(t *testing.T) {
	st := newTestStore(t)
	tid, uid := int64(103), int64(9003)
	now := time.Now().UTC()
	gKeep := mustInsertGrant(t, st, tid, uid, TierRegister, 100, now.Add(10*24*time.Hour))
	gExpire := mustInsertGrant(t, st, tid, uid, TierTask, 100, now.Add(5*24*time.Hour)) // 先吃它

	ledgerID := mustDebitLedger(t, st, tid, uid, 150) // task 100 + register 50
	// 扣完之后让任务档过期（这就是"已作废的那一桶"）
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		`UPDATE quota_grants SET expires_at=? WHERE id=?`,
		now.Add(-time.Hour).Format(time.RFC3339), gExpire); err != nil {
		t.Fatalf("构造过期失败: %v", err)
	}
	res, err := st.RefundUsageByLedger(ledgerID)
	if err != nil {
		t.Fatalf("退款失败: %v", err)
	}
	if res.ExpiredSkipped != 100 {
		t.Fatalf("W15：过期档必须记 100 未回补，实得 %d", res.ExpiredSkipped)
	}
	if res.Refunded != 50 {
		t.Fatalf("W15：未过期那一档应回补 50，实得 %d", res.Refunded)
	}
	if l := grantLeftOf(t, st, gExpire); l != 0 {
		t.Fatalf("W15：已过期档不许被回补，left=%d（期望 0）", l)
	}
	if l := grantLeftOf(t, st, gKeep); l != 100 {
		t.Fatalf("W15：未过期档应复原为 100，实得 %d", l)
	}
}

// 兜底桶：没有台账行、只有永久余额时，退款回补 balance_accounts（TierLegacy，grant_id=0）。
func TestWalletRefund_LegacyBucketGoesToBalance(t *testing.T) {
	st := newTestStore(t)
	tid, uid := int64(104), int64(9004)
	if err := st.EnsureBalance(tid); err != nil {
		t.Fatalf("建余额失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=500 WHERE tenant_id=?", tid); err != nil {
		t.Fatalf("充值余额失败: %v", err)
	}
	ledgerID := mustDebitLedger(t, st, tid, uid, 120)
	if bal := balanceOf(t, st, tid); bal != 380 {
		t.Fatalf("前置：永久余额应被扣到 380，实得 %d", bal)
	}
	res, err := st.RefundUsageByLedger(ledgerID)
	if err != nil {
		t.Fatalf("退款失败: %v", err)
	}
	if res.Refunded != 120 {
		t.Fatalf("兜底桶回补量不对：%d（期望 120）", res.Refunded)
	}
	if bal := balanceOf(t, st, tid); bal != 500 {
		t.Fatalf("兜底桶应按原路回到永久余额 500，实得 %d", bal)
	}
}

// 非订阅单但额度在台账行里：RefundOrder 必须按台账收回，不许去动永久余额。
// （D-6 之后长期包落 plan_permanent 台账行，ptype 未必是 paid —— 旧实现只看 ptype 就会收错桶。）
func TestRefundOrder_NonSubOrderWithGrantRowsClawsLedger(t *testing.T) {
	st := newTestStore(t)
	tid := int64(105)
	pkg, err := st.CreatePackage(&Package{TenantID: 0, Code: "uat_wallet_perm", Name: "长期包", PType: "increment", Points: 1000, PriceMoney: 99, DurationDays: 0, Enabled: 1})
	if err != nil {
		t.Fatalf("建包失败: %v", err)
	}
	o, err := st.CreatePackageOrder(tid, pkg, 5, "mock")
	if err != nil {
		t.Fatalf("建单失败: %v", err)
	}
	if err := st.MarkOrderPaid(o.ID, tid); err != nil {
		t.Fatalf("支付失败: %v", err)
	}
	// increment 走永久余额；这里把它改造成"额度在台账行里"的形态（D-6 之后的长期包）
	if err := st.EnsureBalance(tid); err != nil {
		t.Fatalf("建余额失败: %v", err)
	}
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=777 WHERE tenant_id=?", tid); err != nil {
		t.Fatalf("设余额失败: %v", err)
	}
	grantID, err := db.InsertID(st.db, db.CurrentDialect(), "id",
		`INSERT INTO quota_grants (tenant_id, user_id, tier, kind, total, "left", expires_at, source, ref_id, created_at)
		 VALUES (?,0,?,'plan',?,?,?,'order',?,?)`,
		tid, TierPlanPermanent, 1000, 400, time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339), o.ID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("插台账行失败: %v", err)
	}
	if err := st.RefundOrder(o.ID, tid); err != nil {
		t.Fatalf("退款失败: %v", err)
	}
	if l := grantLeftOf(t, st, grantID); l != 0 {
		t.Fatalf("本单剩余台账应被收回为 0，实得 left=%d", l)
	}
	if bal := balanceOf(t, st, tid); bal != 777 {
		t.Fatalf("有台账行时不许去动永久余额，实得 balance=%d（期望 777）", bal)
	}
}
