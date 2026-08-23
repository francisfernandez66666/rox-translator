// ============ 本文件职责中文说明 ============
// 部门预算单元测试：子树消耗聚合（JOIN users）、部门墙触发与首次提醒去重、
// 总预算（∑部门预算）组织墙判定。
package store

import (
	"testing"
	"time"
)

// seedBudgetFixture 造数：根组织→部门「研发部」(预算100)←用户77/78；本月账本 60+30=90。
func seedBudgetFixture(t *testing.T, s *Store) []int64 {
	t.Helper()
	now := time.Now().Format(time.RFC3339)
	mustExec(t, s, "INSERT INTO orgs (tenant_id,parent_id,name,type,token_limit,created_at,updated_at) VALUES (1,0,'根','root',0,?,?)", now, now)
	var rootID int64
	if err := s.db.QueryRow("SELECT id FROM orgs WHERE tenant_id=1 AND type='root'").Scan(&rootID); err != nil {
		t.Fatal(err)
	}
	mustExec(t, s, "INSERT INTO orgs (tenant_id,parent_id,name,type,token_limit,created_at,updated_at) VALUES (1,?,'研发部','dept',100,?,?)", rootID, now, now)
	var deptID int64
	if err := s.db.QueryRow("SELECT id FROM orgs WHERE name='研发部'").Scan(&deptID); err != nil {
		t.Fatal(err)
	}
	realIDs := []int64{}
	for _, name := range []string{"u77", "u78"} {
		res, err := s.db.Exec("INSERT INTO users (tenant_id,username,password_hash,role,org_id,status,created_at,updated_at) VALUES (1,?,'x','user',?,'active',?,?)",
			name, deptID, now, now)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		realIDs = append(realIDs, id)
	}
	for i, uid := range realIDs {
		qty := int64(60)
		if i == 1 {
			qty = 30
		}
		mustExec(t, s, "INSERT INTO usage_ledger (tenant_id,user_id,task_type,provider,model,quantity,unit_price,cost,created_at) VALUES (1,?,'translate','t','m',?,?,?,?)",
			uid, qty, 0, qty, now)
	}
	return realIDs
}

// mustExec 执行种子语句，失败即终止测试。
func mustExec(t *testing.T, s *Store, q string, args ...interface{}) {
	t.Helper()
	if _, err := s.db.Exec(q, args...); err != nil {
		t.Fatalf("seed exec: %v (sql=%s)", err, q)
	}
}

// TestOrgTokensUsedThisMonth 子树聚合：两成员合计 90。
func TestOrgTokensUsedThisMonth(t *testing.T) {
	s := newTestStoreWithTenants(t)
	seedBudgetFixture(t, s)
	var deptID int64
	if err := s.db.QueryRow("SELECT id FROM orgs WHERE name='研发部'").Scan(&deptID); err != nil {
		t.Fatal(err)
	}
	used, err := s.OrgTokensUsedThisMonth(1, deptID)
	if err != nil || used != 90 {
		t.Fatalf("子树月度消耗应为 90，实得 %d (err=%v)", used, err)
	}
}

// TestCheckBudgetWallsDeptHit 超 100 后命中部门墙；重复撞墙告警去重。
func TestCheckBudgetWallsDeptHit(t *testing.T) {
	s := newTestStoreWithTenants(t)
	ids := seedBudgetFixture(t, s)
	var deptID int64
	_ = s.db.QueryRow("SELECT id FROM orgs WHERE name='研发部'").Scan(&deptID)
	mustExec(t, s, "INSERT INTO usage_ledger (tenant_id,user_id,task_type,quantity,cost,created_at) VALUES (1,?,'translate',20,20,?)",
		ids[0], time.Now().Format(time.RFC3339)) // 110 ≥ 100 → 部门墙
	hit := s.CheckBudgetWalls(1, ids[0])
	if hit == nil || hit.Wall != "dept" || hit.OrgID != deptID {
		t.Fatalf("应命中部门墙，实得 %+v", hit)
	}
	if hit.Msg == "" {
		t.Fatal("缺少前台文案")
	}
	n1 := countOpenQuotaAlerts(s)
	if hit2 := s.CheckBudgetWalls(1, ids[1]); hit2 == nil {
		t.Fatal("仍应拦截")
	}
	if countOpenQuotaAlerts(s) != n1 {
		t.Fatal("重复撞墙不应重复建告警（去重失效）")
	}
}

// TestTenantWallViaSum 组织墙：用户所在部门未启用预算时仅组织墙生效
// （总预算=∑部门预算=80，全租户月耗 110 ≥ 80）。
func TestTenantWallViaSum(t *testing.T) {
	s := newTestStoreWithTenants(t)
	ids := seedBudgetFixture(t, s)
	// 场景：直属用户（org_id=0，不占部门预算）撞租户总预算墙
	// 总预算=∑部门预算=80；全租户本月消耗 110 ≥ 80 → 组织墙拦截并提醒租管
	mustExec(t, s, "UPDATE users SET org_id=0 WHERE id=?", ids[0])
	mustExec(t, s, "UPDATE orgs SET token_limit=80 WHERE name='研发部'")
	hit := s.CheckBudgetWalls(1, ids[0])
	if hit == nil || hit.Wall != "tenant" {
		t.Fatalf("直属用户应命中组织墙，实得 %+v", hit)
	}
}

// countOpenQuotaAlerts 统计 open 状态的 quota 告警数（去重断言用）。
func countOpenQuotaAlerts(s *Store) int {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM alerts WHERE status='open' AND kind='quota'`).Scan(&n)
	return n
}
