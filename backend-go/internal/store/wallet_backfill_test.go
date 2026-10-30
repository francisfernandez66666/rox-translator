// ============ wallet_backfill_test.go · 职责说明 ============
// 存量归档回填的四条锁（★ 2026-10-30 三事合一批 组2b，W7／W8／W9／W10）：
//
//	W7 幂等：第一次跑有改动，第二次启动零改动（Pending=0、Done=0）
//	W8 守卫判据是**数据层面**："列已存在但 tier 全空"的存量库必须回填；
//	   判据若写成"列在不在"，补列语句自己就会把它满足 ⇒ 回填永不执行（AGENTS §三 现网事故同形）
//	W9 task 行拿不到唯一领奖人 ⇒ 留 user_id=0 ＋ 计数，**不许**拿 ref_id 硬猜
//	W10 包档位以 packages.tier 列为准：列为空时按天数区间生成一次；
//	    列与天数不一致时只计 Disagree、**不改写**（覆盖＝把运营那次保存抹掉）
//
// 夹具手法：用裸 SQL 插 tier=” 的行，模拟"补列之后、回填之前"的存量库形态。
// =============================================
package store

import (
	"testing"
	"time"

	"translator/internal/db"
)

// insertLegacyGrant 插一条"未归档"的台账行（tier 留空＝补列之后的存量形态）。
func insertLegacyGrant(t *testing.T, st *Store, tid, refID int64, kind, source string, total int64, expires time.Time) int64 {
	t.Helper()
	id, err := db.InsertID(st.db, db.CurrentDialect(), "id",
		`INSERT INTO quota_grants (tenant_id, user_id, tier, kind, total, "left", expires_at, source, ref_id, created_at)
		 VALUES (?,0,'',?,?,?,?,?,?,?)`,
		tid, kind, total, total, expires.UTC().Format(time.RFC3339), source, refID, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("插存量台账行失败: %v", err)
	}
	return id
}

// W8／W7：数据层面的守卫 + 二次运行零改动。
func TestWalletBackfill_W8DataLevelAndW7Idempotent(t *testing.T) {
	st := newTestStore(t)
	tid := int64(501)
	now := time.Now().UTC()
	gReg := insertLegacyGrant(t, st, tid, 0, "trial", "register", 100, now.Add(7*24*time.Hour))
	gTask := insertLegacyGrant(t, st, tid, 0, "task", "task_task_x", 50, now.Add(30*24*time.Hour))
	gPerm := insertLegacyGrant(t, st, tid, 0, "plan", "order", 900, now.AddDate(100, 0, 0))
	if _, err := db.Exec(st.db, db.CurrentDialect(),
		`UPDATE quota_grants SET expires_at=? WHERE id=?`, "9999-12-31T23:59:59Z", gPerm); err != nil {
		t.Fatalf("构造永久哨兵失败: %v", err)
	}

	r1 := st.WalletBackfill()
	if r1.Pending != 3 || r1.Done != 3 {
		t.Fatalf("W8：存量库必须回填 3 行，实得 pending=%d done=%d", r1.Pending, r1.Done)
	}
	tierOf := func(id int64) string {
		var tier string
		if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT tier FROM quota_grants WHERE id=?", id).Scan(&tier); err != nil {
			t.Fatalf("读 tier 失败: %v", err)
		}
		return tier
	}
	if got := tierOf(gReg); got != TierRegister {
		t.Fatalf("W8：register 行应归档为 %s，实得 %s", TierRegister, got)
	}
	if got := tierOf(gTask); got != TierTask {
		t.Fatalf("W8：task 行应归档为 %s，实得 %s", TierTask, got)
	}
	if got := tierOf(gPerm); got != TierPlanPermanent {
		t.Fatalf("W10：永久哨兵行应归档为 %s，实得 %s", TierPlanPermanent, got)
	}

	// W7：第二次启动零改动
	r2 := st.WalletBackfill()
	if r2.Pending != 0 || r2.Done != 0 {
		t.Fatalf("W7：二次运行必须零改动，实得 pending=%d done=%d", r2.Pending, r2.Done)
	}
}

// W9：task 行拿不到唯一领奖人 ⇒ 留 0 ＋ 计数（不许硬猜）。
func TestWalletBackfill_W9TaskOwnerNotGuessed(t *testing.T) {
	st := newTestStore(t)
	tid := int64(502)
	now := time.Now().UTC()
	// ref_id=0：没有任务定义号，任何"猜"都不成立
	g := insertLegacyGrant(t, st, tid, 0, "task", "task_unknown", 80, now.Add(10*24*time.Hour))
	r := st.WalletBackfill()
	if r.Pending != 1 {
		t.Fatalf("前置：应有一条待归档，实得 %d", r.Pending)
	}
	if r.Unresolved != 1 {
		t.Fatalf("W9：拿不到领奖人必须计 Unresolved=1，实得 %d", r.Unresolved)
	}
	var uid int64
	var tier string
	if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT user_id, tier FROM quota_grants WHERE id=?", g).Scan(&uid, &tier); err != nil {
		t.Fatalf("读回填结果失败: %v", err)
	}
	if uid != 0 {
		t.Fatalf("W9：拿不到归属时必须留 0（＝租户共享），实得 user_id=%d", uid)
	}
	if tier != TierTask {
		t.Fatalf("W9：档位仍应归档为 task，实得 %s", tier)
	}
}

// W10：packages 档位＝列为准（空列按天数区间生成；显式列与天数不一致只报不改）。
func TestWalletBackfill_W10PackageTierColumnWins(t *testing.T) {
	st := newTestStore(t)
	pkg := &Package{TenantID: 0, Code: "uat_bf_month", Name: "月包", PType: "paid", Points: 1000, PriceMoney: 99, DurationDays: 30, Enabled: 1}
	created, err := st.CreatePackage(pkg)
	if err != nil {
		t.Fatalf("建包失败: %v", err)
	}
	pkg = created // CreatePackage 回填 ID 在返回值上，入参那份不动（踩过：用入参 ID=0 去 SELECT 会读到空行）
	// 模拟存量：档位列还空着
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE packages SET tier='' WHERE id=?", pkg.ID); err != nil {
		t.Fatalf("清档位列失败: %v", err)
	}
	r := st.WalletBackfill()
	if r.PackagesTier < 1 {
		t.Fatalf("W10：应至少生成 1 条包档位，实得 %d", r.PackagesTier)
	}
	var tier string
	if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT tier FROM packages WHERE id=?", pkg.ID).Scan(&tier); err != nil {
		t.Fatalf("读包档位失败: %v", err)
	}
	if tier != TierPlanMonthly {
		t.Fatalf("W10：30 天包应生成 %s，实得 %s", TierPlanMonthly, tier)
	}
	// 运营把它显式改成 plan_year（与天数区间不一致）⇒ 只计 Disagree，**不许**覆写回 plan_month
	if _, err := db.Exec(st.db, db.CurrentDialect(), "UPDATE packages SET tier=? WHERE id=?", TierPlanYearly, pkg.ID); err != nil {
		t.Fatalf("改包档位失败: %v", err)
	}
	r2 := st.WalletBackfill()
	if r2.Disagree < 1 {
		t.Fatalf("W10：列与天数不一致必须计数，实得 disagree=%d", r2.Disagree)
	}
	var tier2 string
	if err := db.QueryRow(st.db, db.CurrentDialect(), "SELECT tier FROM packages WHERE id=?", pkg.ID).Scan(&tier2); err != nil {
		t.Fatalf("读包档位失败: %v", err)
	}
	if tier2 != TierPlanYearly {
		t.Fatalf("W10：显式配置不许被天数近似值覆盖，实得 %s（期望 %s）", tier2, TierPlanYearly)
	}
}
