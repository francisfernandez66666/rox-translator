// ============ 本文件职责中文说明 ============
// 三期 W3 把销售域端点收紧为「读类 ≥2、草稿转正 ≥3」，权限真值是
// store.PermForEndpoint + salesRoleGate。adminGateServer 刻意签的是**普通会员**
// （等级 1，供「已登录但权限不足」的反向对照用），不能再拿它当业务正向 token。
//
// 本文件只补一枚租户管理员（等级 3）token：不改 adminGateServer 的语义，
// 不给权限闸开口子，也不改任何产品代码。业务用例用它做正向，
// 同时必须显式判一次普通会员 403，防「夹具升级」把权限锁洗成空转。
// ================================================
package api

import (
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/iam"
)

// salesGateAdminToken 在同一台 srv 的库里建并签一个租户管理员（等级 3）。
// 参数：t=测试句柄；srv=adminGateServer 返回的整机。返回：Bearer 用 token。
func salesGateAdminToken(t *testing.T, srv *Server) string {
	t.Helper()
	u, err := srv.Store.CreateUser(1, "sales_gate_admin", auth.PasswordHash("pw123456"),
		"销售闸门管理员", iam.RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("建租户管理员失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签管理员 token 失败: %v", err)
	}
	return tok
}
