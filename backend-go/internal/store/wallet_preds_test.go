// ============ wallet_preds_test.go · 职责说明 ============
// 组3 #9 的锁：三把尺子在 `instant_free` 上的落点（W16／W17／W19）＋ 用户可用池（D-9）。
//
//	W16 免费即时用量**不进** usage_daily（否则免费档自己会被日限额拦死）
//	W17 免费即时用量**仍在**客户面射程（CustomerUsagePred），客户看得见自己用了什么
//	W19 三名单逐值点名：instant_free ∈ DailyUsageExcl、∉ 平台名单、∈ CustomerUsagePred 射程
//	另：Go 侧 platformTaskTypes 与 SQL 谓词必须同一份名单（domain_detect 那次漏改的机械防线）
//
// D-9：部门月度预算满 ⇒ 共享 plan 档跳过、个人档照吃（且 fail-open：没部门/没配预算=不限）。
// =============================================
package store

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"translator/internal/db"
)

// containsQuoted 判 SQL 片段里有没有某个带单引号的字面（本文件只用来比名单，不解析 SQL）。
func containsQuoted(sql, quoted string) bool { return strings.Contains(sql, quoted) }

// extractQuoted 取出 SQL 片段里全部 '...' 之间的字面（按单引号切分取奇数位）。
func extractQuoted(sql string) []string {
	parts := strings.Split(sql, "'")
	var out []string
	for i := 1; i < len(parts); i += 2 {
		if v := strings.TrimSpace(parts[i]); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// TestWalletPreds_GoAndSQLLabelListsAgree Go 侧名单与 SQL 谓词逐值等值。
// （2026-10-30 加 domain_detect 时只改了 SQL 一侧，Go 侧 switch 漏掉 ⇒ 判域成本照旧进日额。）
func TestWalletPreds_GoAndSQLLabelListsAgree(t *testing.T) {
	inSQL := map[string]bool{}
	for _, label := range platformTaskTypes {
		if !containsQuoted(PlatformTaskTypeExclPred, "'"+label+"'") {
			t.Fatalf("平台名单里的 %q 不在 SQL 谓词里：%s", label, PlatformTaskTypeExclPred)
		}
		inSQL[label] = true
	}
	// 反向：SQL 里出现的每一个字面都必须在 Go 侧名单里（两边各漏一个都算漂）
	for _, q := range extractQuoted(PlatformTaskTypeExclPred) {
		if !inSQL[q] {
			t.Fatalf("SQL 谓词里的 %q 不在 Go 侧 platformTaskTypes 里（判据会两边不一致）", q)
		}
	}
	// 行为腿：Go 判定必须与名单同值
	for _, label := range platformTaskTypes {
		if !isPlatformTaskType(label) {
			t.Fatalf("isPlatformTaskType(%q) 应为 true", label)
		}
	}
	if isPlatformTaskType(TaskTypeInstantFree) {
		t.Fatal("instant_free 绝不许被归进平台名单（那会把客户面用量整段抹掉，W17）")
	}
}

// W16/W17/W19：免费即时用量在两张名单上的落点。
func TestWalletPreds_InstantFreeFaces(t *testing.T) {
	// ① Go 侧判定
	if !isDailyUsageExcluded(TaskTypeInstantFree) {
		t.Fatal("W16：instant_free 必须不累加 usage_daily")
	}
	if countsTowardDailyUsage(TaskTypeInstantFree) {
		t.Fatal("W16：countsTowardDailyUsage(instant_free) 必须为 false")
	}
	if !countsTowardDailyUsage("translate") {
		t.Fatal("正向对照：translate 必须照常累加（否则这条判据把所有人都豁免了）")
	}
	if countsTowardDailyUsage("kb_embed") {
		t.Fatal("平台标签必须不累加（〇-AD 补丁二的既有口径）")
	}

	// ② SQL 侧：客户面射程（W17）
	st := newTestStore(t)
	tid, uid := int64(201), int64(9201)
	now := time.Now().UTC().Format(time.RFC3339)
	insert := func(taskType, chargeKind string) {
		if _, err := db.Exec(st.db, db.CurrentDialect(),
			`INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, charge_kind, created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			tid, uid, taskType, "test", "m", 100, 1, 100, chargeKind, now); err != nil {
			t.Fatalf("插用量行失败: %v", err)
		}
	}
	insert(TaskTypeInstantFree, "log") // 免费即时：客户看得见
	insert("translate", "charge")      // 正常消费：客户看得见
	insert("kb_embed", "log")          // 平台承担：客户看不见

	var n int
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=? AND `+CustomerUsagePred, tid).Scan(&n); err != nil {
		t.Fatalf("客户面计数失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("W17：客户面应恰好看到 instant_free 与 translate 两行，实得 %d 行", n)
	}
	var inst int
	if err := db.QueryRow(st.db, db.CurrentDialect(),
		`SELECT COUNT(*) FROM usage_ledger WHERE tenant_id=? AND task_type=? AND `+CustomerUsagePred,
		tid, TaskTypeInstantFree).Scan(&inst); err != nil {
		t.Fatalf("instant_free 客户面计数失败: %v", err)
	}
	if inst != 1 {
		t.Fatalf("W17：instant_free 必须在客户面射程内（实得 %d）——把它加进平台名单就会整段抹空", inst)
	}
}

// W16 行为腿：LogUsage 落 instant_free 行后 usage_daily 一字不动；translate 仍照常累加。
func TestLogUsage_InstantFreeDoesNotTouchDailyCounter(t *testing.T) {
	st := newTestStore(t)
	tid := int64(202)
	base, err := st.DailyUsage(tid)
	if err != nil {
		t.Fatalf("读日计数失败: %v", err)
	}
	if err := st.LogUsage(tid, 9202, TaskTypeInstantFree, "test", "m", "en", 1000, "text", "fast"); err != nil {
		t.Fatalf("落免费即时用量失败: %v", err)
	}
	after, err := st.DailyUsage(tid)
	if err != nil {
		t.Fatalf("读日计数失败: %v", err)
	}
	if after != base {
		t.Fatalf("W16：instant_free 不许累加 usage_daily（base=%d after=%d）", base, after)
	}
	// 正向对照：正常消费照旧累加（否则"不累加"可能只是因为整条腿坏了）
	if err := st.LogUsage(tid, 9202, "translate", "test", "m", "en", 1000, "text", "fast"); err != nil {
		t.Fatalf("落正常用量失败: %v", err)
	}
	final, err := st.DailyUsage(tid)
	if err != nil {
		t.Fatalf("读日计数失败: %v", err)
	}
	if final <= after {
		t.Fatalf("正向对照失败：translate 必须累加 usage_daily（after=%d final=%d）", after, final)
	}
}

// W16 批量腿：LogUsageBatch 里的 instant_free 行同样不进日额。
func TestLogUsageBatch_InstantFreeExcludedFromDaily(t *testing.T) {
	st := newTestStore(t)
	tid := int64(203)
	base, _ := st.DailyUsage(tid)
	rows := []UsageBatchRow{
		{UserID: 9203, TaskType: TaskTypeInstantFree, Provider: "test", Model: "m", Quantity: 500},
	}
	if err := st.LogUsageBatch(tid, rows); err != nil {
		t.Fatalf("批量留痕失败: %v", err)
	}
	after, _ := st.DailyUsage(tid)
	if after != base {
		t.Fatalf("W16：批量腿也不许把 instant_free 累进日额（base=%d after=%d）", base, after)
	}
}

// D-9：部门预算满 ⇒ 共享 plan 档跳过、个人档照吃；没部门/没配预算 ⇒ 不限（fail-open）。
func TestUserSharedPlanBlocked_GatesOnlySharedPlanTiers(t *testing.T) {
	st := newTestStore(t)
	tid := int64(204)
	root, err := st.EnsureRootOrg(tid, "公司")
	if err != nil {
		t.Fatalf("建根组织失败: %v", err)
	}
	dept, err := st.CreateOrg(tid, root.ID, "翻译部", "dept")
	if err != nil {
		t.Fatalf("建部门失败: %v", err)
	}
	u, err := st.CreateUser(tid, "wallet_pool_u", "x", "池测试", "member", 0, dept.ID)
	if err != nil {
		t.Fatalf("建用户失败: %v", err)
	}

	// ① 未配预算 ⇒ 不限
	if st.UserSharedPlanBlocked(tid, u.ID) {
		t.Fatal("部门未配预算时必须不限（fail-open 的方向反了会把客户拦死）")
	}
	// ② 配了预算、本月用量未达 ⇒ 不限
	if err := st.SetOrgTokenLimit(dept.ID, 10000); err != nil {
		t.Fatalf("设预算失败: %v", err)
	}
	if st.UserSharedPlanBlocked(tid, u.ID) {
		t.Fatal("用量未达预算时不许拦")
	}
	// ③ 用量达标 ⇒ 共享 plan 档被挡，个人档仍可扣
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		`INSERT INTO usage_ledger (tenant_id, user_id, task_type, provider, model, quantity, unit_price, cost, charge_kind, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		tid, u.ID, "translate", "test", "m", 10000, 1, 10000, "charge", time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("插用量失败: %v", err)
	}
	if !st.UserSharedPlanBlocked(tid, u.ID) {
		t.Fatal("部门预算已用满时必须挡住共享 plan 档")
	}
	now := time.Now().UTC()
	gTask := mustInsertGrant(t, st, tid, u.ID, TierTask, 100, now.Add(24*time.Hour))
	gMonth := mustInsertGrant(t, st, tid, 0, TierPlanMonthly, 100, now.Add(30*24*time.Hour))
	// 恰好扣满个人档：成功，且共享 plan 档一分不动
	if err := st.DeductWithGrants(tid, u.ID, 100); err != nil {
		t.Fatalf("拆扣失败: %v", err)
	}
	if l := grantLeftOf(t, st, gMonth); l != 100 {
		t.Fatalf("D-9：预算满后不许再吃共享 plan 档，实得 left=%d", l)
	}
	if l := grantLeftOf(t, st, gTask); l != 0 {
		t.Fatalf("D-9：个人档必须照吃，实得 left=%d", l)
	}
	// 个人档空了、共享档被挡、又没有永久余额 ⇒ 欠费（不是静默成功，也不是去扣共享档）
	if err := st.DeductWithGrants(tid, u.ID, 10); err == nil {
		rows, _ := db.Query(st.db, db.CurrentDialect(),
			`SELECT id, tier, user_id, kind, total, "left" FROM quota_grants WHERE tenant_id=?`, tid)
		var dump []string
		if rows != nil {
			for rows.Next() {
				var id, owner, total, left int64
				var tier, kind string
				if rows.Scan(&id, &tier, &owner, &kind, &total, &left) == nil {
					dump = append(dump, fmt.Sprintf("{id=%d tier=%q owner=%d kind=%s total=%d left=%d}", id, tier, owner, kind, total, left))
				}
			}
			rows.Close()
		}
		blocked := st.UserSharedPlanBlocked(tid, u.ID)
		t.Fatalf("D-9：全部可用档都不够时必须报欠费（W12：第一档不够但后面够才算正常拆扣）；blocked=%v 台账=%v", blocked, dump)
	}
	if l := grantLeftOf(t, st, gMonth); l != 100 {
		t.Fatalf("D-9：欠费路径不许留下半截扣减，plan 档 left=%d", l)
	}
}
