// ============ rolemap_gate_test.go · 职责说明 ============
// 三期 W3 / A09（按二期 §8.5 原义）：SCRM 双读单写迁移期角色映射的行为锁。
// R1：admin → tenant_admin(3)，改掉映射必红（M0220）；
// R2：readonly 不许降成 user(1)，降档必红（M0221）。
// 两处各配反向对照；sites==0（映射表少于 2 项）判射程失明，直接 Fatal。
// 靶是 store/sales_role.go 里的具名常量与 SCRMRoleMap（生产代码，非锁自身），
// 反证一律用「内存副本」跑同一个 ValidateSCRMRoleMap 判定函数，不碰锁本体。
// ================================================
package salesflow

import (
	"testing"

	"translator/internal/iam"
	"translator/internal/store"
)

// TestRoleMapAdminToTenantAdmin R1：admin 必须映射到等级 3，改掉即红。
func TestRoleMapAdminToTenantAdmin(t *testing.T) {
	m := store.SCRMRoleMap
	if len(m) < 2 {
		t.Fatalf("A09 射程自证失败：SCRM 角色映射少于 2 项（sites==0），实得 %d", len(m))
	}
	if err := store.ValidateSCRMRoleMap(m); err != nil {
		t.Fatalf("A09 R1：生产映射必须守住 admin→tenant_admin(3): %v", err)
	}
	if got := store.SCRMRoleLevel("admin"); got != 3 {
		t.Fatalf("A09 R1：admin 等级必须是 3，实得 %d", got)
	}
	if m["admin"] != iam.RoleTenantAdmin {
		t.Fatalf("A09 R1：admin 必须映射到 tenant_admin，实得 %s", m["admin"])
	}
	// 反向对照：只改 admin 一处映射，判定函数必须红（不许「一律判绿」偷懒）。
	bad := map[string]string{"admin": iam.RoleUser, "readonly": iam.RoleDeptAdmin}
	if err := store.ValidateSCRMRoleMap(bad); err == nil {
		t.Fatal("A09 R1 反证：admin 被改成 user(1) 必须判红（M0220 靶）")
	}
}

// TestRoleMapReadonlyNotDemotedToUser R2：readonly 等级 ≥2，不许降成 user(1)。
func TestRoleMapReadonlyNotDemotedToUser(t *testing.T) {
	if got := store.SCRMRoleLevel("readonly"); got < 2 {
		t.Fatalf("A09 R2：readonly 不许降成 user(1)，实得 %d", got)
	}
	// 反向对照：readonly 降成 user 必须红（M0221 靶）。
	bad := map[string]string{"admin": iam.RoleTenantAdmin, "readonly": iam.RoleUser}
	if err := store.ValidateSCRMRoleMap(bad); err == nil {
		t.Fatal("A09 R2 反证：readonly 降成 user(1) 必须判红（M0221 靶）")
	}
	// 正向对照：只把 readonly 升到等价高档的合法副本仍须放行，防「一律判红」。
	ok := map[string]string{"admin": iam.RoleTenantAdmin, "readonly": iam.RoleTenantAdmin}
	if err := store.ValidateSCRMRoleMap(ok); err != nil {
		t.Fatalf("A09 R2 正向对照：readonly 升到 tenant_admin(3) 应放行: %v", err)
	}
}

// TestRoleMapEmptySetFatal 射程与空集自证：空映射必被判失明。
func TestRoleMapEmptySetFatal(t *testing.T) {
	if err := store.ValidateSCRMRoleMap(map[string]string{}); err == nil {
		t.Fatal("A09 射程自证：空映射必须判红（sites==0 失明）")
	}
}
