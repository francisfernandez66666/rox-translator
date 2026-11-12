// ============ sales_role_gate_test.go · 职责说明 ============
// 三期 W3 销售角色权限表的行为锁（store 侧）：
// ① SalesRoleMigrate() 幂等（连跑两次不报错、表与列齐全、唯一键防重复）；
// ② 行式三列读写：落库行覆盖出厂缺省、跨租户不串、未落库项回退出厂缺省；
// ③ 新表尚未挂进 New() 域链时 SalesRoleAllowed 仍按缺省判（主 agent 装配前的过渡态，不静默放行）；
// ④ PermForEndpoint 四端点口径：读 ≥2、转正审批 ≥3。
// 只用内存 SQLite，不碰共享库；db.CurrentDialect 默认回退 SQLite（rewrite.go:129）。
// ================================================
package store

import (
	"database/sql"
	"testing"
)

// newSalesRoleTestStore 起一张内存库并直接构造 Store（本批方法只吃 s.db，零其它字段依赖）。
func newSalesRoleTestStore(t *testing.T) *Store {
	t.Helper()
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("内存库起不来: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &Store{db: conn}
}

// TestSalesRoleMigrateIdempotent 迁移幂等 + 表/列齐全 + 唯一键防重复。
func TestSalesRoleMigrateIdempotent(t *testing.T) {
	s := newSalesRoleTestStore(t)
	for i := 0; i < 2; i++ {
		if err := s.SalesRoleMigrate(); err != nil {
			t.Fatalf("第 %d 次 SalesRoleMigrate 失败: %v", i+1, err)
		}
	}
	// 列形状：role_level / perm_key / allowed / updated_at 四列齐全（唯一键前三列）。
	rows, err := s.db.Query(`PRAGMA table_info(sales_role_perms)`)
	if err != nil {
		t.Fatalf("读表结构失败: %v", err)
	}
	cols := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt interface{}
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			t.Fatalf("扫表结构失败: %v", err)
		}
		cols[name] = true
	}
	_ = rows.Close()
	for _, c := range []string{"tenant_id", "role_level", "perm_key", "allowed", "updated_at"} {
		if !cols[c] {
			t.Fatalf("sales_role_perms 缺列 %s", c)
		}
	}
	// 唯一键前三列：同一 (tid, level, perm) 连写两次只留一行。
	if err := s.SaveSalesRolePerm(1, 2, PermLeadView, false); err != nil {
		t.Fatalf("首写失败: %v", err)
	}
	if err := s.SaveSalesRolePerm(1, 2, PermLeadView, true); err != nil {
		t.Fatalf("覆盖写失败: %v", err)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM sales_role_perms WHERE tenant_id=1 AND role_level=2 AND perm_key=?`, PermLeadView).Scan(&n); err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if n != 1 {
		t.Fatalf("唯一键必须防重复，实得 %d 行", n)
	}
}

// TestSalesRoleRowMatrixOverridesDefaults 落库行覆盖缺省、跨租户不串、未落库项回退缺省。
func TestSalesRoleRowMatrixOverridesDefaults(t *testing.T) {
	s := newSalesRoleTestStore(t)
	if err := s.SalesRoleMigrate(); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	// 出厂缺省：dept_admin(2) 可读草稿；落库一行显式关掉后必须优先用落库值。
	if !s.SalesRoleAllowed(1, 2, PermLeadView) {
		t.Fatal("过渡态：未落库项应回退出厂缺省（dept_admin 可读）")
	}
	if err := s.SaveSalesRolePerm(1, 2, PermLeadView, false); err != nil {
		t.Fatalf("落库关掉读权限失败: %v", err)
	}
	if s.SalesRoleAllowed(1, 2, PermLeadView) {
		t.Fatal("落库行必须覆盖出厂缺省（显式关掉后不许仍放行）")
	}
	// 跨租户：租户 2 不许读到租户 1 的关闭行。
	if !s.SalesRoleAllowed(2, 2, PermLeadView) {
		t.Fatal("跨租户隔离失败：租户 2 应回退自己的出厂缺省")
	}
	// 审批类 ≥3：tenant_admin 放行、dept_admin 不放行。
	if !s.SalesRoleAllowed(1, 3, PermQuoteApprove) || s.SalesRoleAllowed(1, 2, PermQuoteApprove) {
		t.Fatal("quote_approve 必须 ≥3")
	}
	// 未登记权限项一律不许放行（fail-closed）。
	if s.SalesRoleAllowed(1, 4, "not_a_perm") {
		t.Fatal("未登记权限项必须拒")
	}
}

// TestSalesRoleGateBeforeMountFallsBackToDefaults 表未挂进域链时仍按缺省判，不报错、不静默放行。
func TestSalesRoleGateBeforeMountFallsBackToDefaults(t *testing.T) {
	s := newSalesRoleTestStore(t) // 故意不调 SalesRoleMigrate
	if !s.SalesRoleAllowed(1, 3, PermLeadView) {
		t.Fatal("表未建时 tenant_admin 读类应按缺省放行")
	}
	if s.SalesRoleAllowed(1, 1, PermQuoteApprove) {
		t.Fatal("表未建时普通用户转正审批仍须拒（缺省矩阵不静默放行）")
	}
}

// TestSalesRoleEndpointPermMatrix 四端点口径：读 ≥2、approve ≥3。
func TestSalesRoleEndpointPermMatrix(t *testing.T) {
	for _, p := range []string{"/api/sales/capability", "/api/sales/drafts", "/api/sales/draft"} {
		perm := PermForEndpoint(p)
		if perm == "" {
			t.Fatalf("%s 未登记权限项", p)
		}
		if DefaultSalesRoleAllowed(1, perm) || !DefaultSalesRoleAllowed(2, perm) {
			t.Fatalf("%s 必须是读 ≥2，实得 perm=%s", p, perm)
		}
	}
	approve := PermForEndpoint("/api/sales/draft/approve")
	if DefaultSalesRoleAllowed(2, approve) || !DefaultSalesRoleAllowed(3, approve) {
		t.Fatalf("/api/sales/draft/approve 必须 ≥3，实得 perm=%s", approve)
	}
	if got := PermForEndpoint("/api/sales/draft?id=1"); got != PermForEndpoint("/api/sales/draft") {
		t.Fatal("带 query 的路径必须与裸路径同判")
	}
	if PermForEndpoint("/api/sales/not-registered") != "" {
		t.Fatal("未登记端点必须返回空串（调用方 fail-closed）")
	}
}

// TestSalesRoleSCRMRoleMap production 映射自检（A09 不变式在 store 侧同样守住）。
func TestSalesRoleSCRMRoleMap(t *testing.T) {
	if err := ValidateSCRMRoleMap(SCRMRoleMap); err != nil {
		t.Fatalf("生产 SCRM 角色映射自检失败: %v", err)
	}
}
