// ============ 本文件职责中文说明 ============
// KB 包类型权限校验单元测试：租户管理员仅可管理企业包/部门包，超管可管理全部类型。
// =============================================
package api

import (
	"testing"

	"translator/internal/store"
)

func TestCanManagePackType(t *testing.T) {
	super := &store.User{Role: "super_admin"}
	tenantAdmin := &store.User{Role: "tenant_admin"}

	cases := []struct {
		u        *store.User
		packType string
		want     bool
	}{
		{super, store.PackTenant, true},
		{super, store.PackDepartment, true},
		{super, store.PackIndustry, true},
		{super, store.PackLocale, true},
		{tenantAdmin, store.PackTenant, true},
		{tenantAdmin, store.PackDepartment, true},
		{tenantAdmin, store.PackIndustry, false},
		{tenantAdmin, store.PackLocale, false},
	}
	for _, c := range cases {
		if got := canManagePackType(c.u, c.packType); got != c.want {
			t.Errorf("canManagePackType(role=%v, type=%s) = %v, want %v", c.u, c.packType, got, c.want)
		}
	}
}
