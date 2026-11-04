// ============ tenants_remain_total_map_test.go · 职责说明 ============
// store 包内部测试文件。
//
// ============ 本文件职责中文说明 ============
// ★ 2026-11-04 体验批 A12：批量版余额聚合（TenantsRemainTotalMap）与逐条版
//
//	（TenantRemainTotal，全系统「可用额度」唯一口径）的**等值锁**。
//
// 这条锁的存在理由：批量口是为了省 N 次往返而新增的第二条读腿，
// 一旦两边谓词分叉（比如批量侧漏了 `left>0` 或 `expires_at>` 那一半），
// 表现不是报错，而是"挽回礼包发给还有额度的客户"——钱的判断，必须逐租户逐键比。
//
//	T1 同夹具下两档读数逐租户逐键等值（含"只有永久余额"“只有未过期台账”“都有一点”三档）
//	T2 已过期台账**两边都不许算进来**（漏了 expires_at 条件就是把作废额度当余额）
//	T3 空入参回空 map 且不发查询（不是 nil——调用方按"在不在读数里"分支，见 watchdog 那段）
//	T4 从没铺过余额行的租户：两个口必须给同一个答案（两条腿都先 EnsureBalance 补行，
//	   正确答案是"补完之后一起是 0"，不是"批量侧看不见它"——判据方向写反会把锁做成空锁）
//
// 方言：自钉内存 SQLite（AGENTS §一·4，newTestStore）。
// =============================================
package store

import (
	"testing"
	"time"

	"translator/internal/db"
)

// seedBalanceAndGrants 给某租户铺一个余额行与若干台账行（grants 可为空串跳过）。
func seedBalanceAndGrants(t *testing.T, s *Store, tid, balance int64, grantLeft int64, grantExpires string) {
	t.Helper()
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO balance_accounts (tenant_id, balance) VALUES (?,?) ON CONFLICT(tenant_id) DO UPDATE SET balance=excluded.balance",
		tid, balance); err != nil {
		t.Fatalf("铺余额行失败: %v", err)
	}
	if grantLeft == 0 {
		return
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO quota_grants (tenant_id, kind, total, \"left\", expires_at, source, created_at) VALUES (?,?,?,?,?,?,?)",
		tid, "trial", grantLeft, grantLeft, grantExpires, "test",
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("铺台账行失败: %v", err)
	}
}

// TestTenantsRemainTotalMapEqualsPerTenant T1＋T2：批量与逐条逐键等值，过期台账两边都不算。
func TestTenantsRemainTotalMapEqualsPerTenant(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	expired := now.Add(-24 * time.Hour).Format(time.RFC3339)
	active := now.Add(24 * time.Hour).Format(time.RFC3339)

	// 三档形态：只有永久余额 / 只有未过期台账 / 两桶都有
	seedBalanceAndGrants(t, s, 11, 900, 0, "")
	seedBalanceAndGrants(t, s, 12, 0, 500, active)
	seedBalanceAndGrants(t, s, 13, 700, 300, active)
	// T2 靶子：台账已过期 ⇒ 只能看到永久余额
	seedBalanceAndGrants(t, s, 14, 111, 888, expired)

	ids := []int64{11, 12, 13, 14}
	grantsMap, permMap, err := s.TenantsRemainTotalMap(ids)
	if err != nil {
		t.Fatalf("批量口报错: %v", err)
	}
	for _, tid := range ids {
		g, p, e := s.TenantRemainTotal(tid)
		if e != nil {
			t.Fatalf("逐条口报错 tid=%d: %v", tid, e)
		}
		if grantsMap[tid] != g || permMap[tid] != p {
			t.Fatalf("tid=%d 两档读数不等值：批量 (%d,%d) vs 逐条 (%d,%d)",
				tid, grantsMap[tid], permMap[tid], g, p)
		}
	}
	// 点名判据（不点名就退化成"两边一起错也算等值"）
	if grantsMap[14] != 0 || permMap[14] != 111 {
		t.Fatalf("已过期台账被算进余额了：grants=%d permanent=%d（应为 0/111）", grantsMap[14], permMap[14])
	}
	if grantsMap[13] != 300 || permMap[13] != 700 {
		t.Fatalf("两桶都有的那档读数异常：(%d,%d) 期望 (300,700)", grantsMap[13], permMap[13])
	}
}

// TestTenantsRemainTotalMapEmptyInput T3：空入参回空 map、不发查询、不报错。
func TestTenantsRemainTotalMapEmptyInput(t *testing.T) {
	s := newTestStore(t)
	g, p, err := s.TenantsRemainTotalMap(nil)
	if err != nil {
		t.Fatalf("空入参不该报错: %v", err)
	}
	if g == nil || p == nil {
		t.Fatalf("空入参必须回**空 map**而不是 nil——调用方按 map[..] 取值与 in 判定，nil 会让两种空同形")
	}
	if len(g) != 0 || len(p) != 0 {
		t.Fatalf("空入参应回空 map，实得 %v / %v", g, p)
	}
}

// TestTenantsRemainTotalMapMissingTenantMatchesSingle T4：从没铺过余额行的租户，
// 两档必须给**同一个答案**。
// ⚠️ 这里刻意不写"缺席才对"：两个口都会先 EnsureBalance（缺行即补一行），
//
//	所以正确答案是"补完之后一起是 0"，而不是"批量侧看不见它"。
//	真正的"缺席"只可能来自 EnsureBalance 失败那一支——api 侧的跳过判据（watchdog 那段）
//	防的是"把读失败当余额 0"，与本条不矛盾，那条的形状由源码锁守。
func TestTenantsRemainTotalMapMissingTenantMatchesSingle(t *testing.T) {
	s := newTestStore(t)
	seedBalanceAndGrants(t, s, 21, 40, 0, "")
	g, p, err := s.TenantsRemainTotalMap([]int64{21, 22}) // 22 从没铺过余额行
	if err != nil {
		t.Fatalf("批量口报错: %v", err)
	}
	if p[21] != 40 {
		t.Fatalf("租户 21 余额应为 40，实得 %d", p[21])
	}
	sg, sp, e := s.TenantRemainTotal(22)
	if e != nil {
		t.Fatalf("逐条口对缺行租户报错: %v", e)
	}
	if g[22] != sg || p[22] != sp {
		t.Fatalf("缺行租户两档答案不一致：批量 (%d,%d) vs 逐条 (%d,%d)——批量口径分叉就是把挽回礼包发错人",
			g[22], p[22], sg, sp)
	}
}
