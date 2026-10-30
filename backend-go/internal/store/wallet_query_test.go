// ============ wallet_query_test.go · 职责说明 ============
// 组3 #10 的两条锁：
//
//	W20 免费档**永不**出栈（哪怕库里真有一行 tier='free'），逐档字段只出 tier/points_left/expires_at
//	W21 同一时刻 逐档之和 == points_grants_left（TenantRemainTotal 的 grants，同一谓词）
//
// 另带一条「未知档位不许被吞」：客户账上有余额、面板上却找不到那一行，是对账时最难查的形态。
// =============================================
package store

import (
	"strings"
	"testing"
	"time"

	"translator/internal/db"
)

// W21：逐档之和必须等于台账合计（同一时刻、同一谓词）。
func TestWalletTiers_SumEqualsGrantsLeft(t *testing.T) {
	st := newTestStore(t)
	tid := int64(301)
	now := time.Now().UTC()
	mustInsertGrant(t, st, tid, 9001, TierRegister, 100, now.Add(10*24*time.Hour))
	mustInsertGrant(t, st, tid, 9001, TierTask, 200, now.Add(5*24*time.Hour))
	mustInsertGrant(t, st, tid, 0, TierPlanMonthly, 300, now.Add(30*24*time.Hour))
	mustInsertGrant(t, st, tid, 0, TierPlanPermanent, 400, now.AddDate(50, 0, 0))
	// 两条干扰行：已过期、已花光——都不许出现在逐档里，也不许进合计
	mustInsertGrant(t, st, tid, 9001, TierTask, 50, now.Add(-time.Hour))
	gone := mustInsertGrant(t, st, tid, 9001, TierRegister, 70, now.Add(24*time.Hour))
	if _, err := db.Exec(st.db, db.CurrentDialect(), `UPDATE quota_grants SET "left"=0 WHERE id=?`, gone); err != nil {
		t.Fatalf("清零失败: %v", err)
	}

	tiers, err := st.WalletTiers(tid)
	if err != nil {
		t.Fatalf("WalletTiers 失败: %v", err)
	}
	var sum, grants, permanent int64
	for _, tr := range tiers {
		sum += tr.Tokens
		if tr.Tier == TierFree {
			t.Fatal("W20：免费档绝不许出栈")
		}
	}
	grants, permanent, err = st.TenantRemainTotal(tid)
	if err != nil {
		t.Fatalf("TenantRemainTotal 失败: %v", err)
	}
	if sum != grants {
		t.Fatalf("W21：逐档之和 %d ≠ points_grants_left %d（两处各算，必须等值）", sum, grants)
	}
	if grants != 1000 {
		t.Fatalf("W21：前置口径不对，grants=%d（期望 100+200+300+400=1000）", grants)
	}
	if permanent != 0 {
		t.Fatalf("前置：永久余额应为 0，实得 %d", permanent)
	}
	// 次序：按扣减次序出栈（register → task → plan_month → plan_permanent）
	want := []string{TierRegister, TierTask, TierPlanMonthly, TierPlanPermanent}
	if len(tiers) != len(want) {
		t.Fatalf("逐档行数不对：%d（期望 %d）", len(tiers), len(want))
	}
	for i, w := range want {
		if tiers[i].Tier != w {
			t.Fatalf("第 %d 档应为 %s，实得 %s（次序必须来自 TierNamesInOrder）", i, w, tiers[i].Tier)
		}
	}
}

// W20：库里真有 tier='free' 的行时也必须被排除（防日后把免费额度落成台账行）。
func TestWalletTiers_NeverEmitsFreeTier(t *testing.T) {
	st := newTestStore(t)
	tid := int64(302)
	now := time.Now().UTC()
	mustInsertGrant(t, st, tid, 9002, TierFree, 999, now.Add(24*time.Hour))
	mustInsertGrant(t, st, tid, 9002, TierRegister, 100, now.Add(24*time.Hour))
	tiers, err := st.WalletTiers(tid)
	if err != nil {
		t.Fatalf("WalletTiers 失败: %v", err)
	}
	if len(tiers) != 1 || tiers[0].Tier != TierRegister {
		t.Fatalf("W20：只该出 register 一档，实得 %+v", tiers)
	}
	// 第二道防线（源码级）：SQL 自己也必须排除 free。
	// 只有循环里那句 continue 的话，将来有人把 SELECT 改成"直出"就会漏；
	// 只有 SQL 那句的话，将来有人把 tier 归一化改坏同样会漏 ⇒ 两道都要在位。
	body := stripGoComments(mustReadGateSource(t, "internal/store/wallet_query.go"))
	if !strings.Contains(body, `expires_at>? AND COALESCE(tier,'')<>?`) {
		t.Fatal("W20：wallet_query.go 的 SQL 必须自带 tier<>free 的排除条件（不能只靠循环里的 continue）")
	}
	if !strings.Contains(body, "if tier == TierFree {") {
		t.Fatal("W20：wallet_query.go 的循环必须自带 free 档跳过（不能只靠 SQL 条件）")
	}
	// 反向对照：TenantRemainTotal 是**合计真值**（它会把免费行算进 grants 吗？）
	// 这里刻意不合并两处口径——W21 的等值锁要求两处同谓词；免费行若真的落账，
	// 两条腿会一起变红，而那正是"有人把免费额度落成台账"这件事故要暴露的时刻。
	grants, _, _ := st.TenantRemainTotal(tid)
	if grants == 0 {
		t.Fatal("TenantRemainTotal 不应为空（正向前置）")
	}
}

// 未知档位不许被吞：名单外的 tier 仍要出栈（排最后），否则"账上有、面板上没有"。
func TestWalletTiers_UnknownTierStillEmitted(t *testing.T) {
	st := newTestStore(t)
	tid := int64(303)
	mustInsertGrant(t, st, tid, 9003, "future_tier_x", 66, time.Now().UTC().Add(24*time.Hour))
	tiers, err := st.WalletTiers(tid)
	if err != nil {
		t.Fatalf("WalletTiers 失败: %v", err)
	}
	if len(tiers) != 1 || tiers[0].Tier != "future_tier_x" || tiers[0].Tokens != 66 {
		t.Fatalf("未知档位必须照常出栈，实得 %+v", tiers)
	}
}
