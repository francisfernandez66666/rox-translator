// ============ 本文件职责中文说明 ============
// kbTenant 回归测试：修复「误调用自身导致无限递归栈溢出」的历史事故（2026-08-21~23）。
// ★ 2026-09-04 权限澄清后：行业包/语言文化包宿主为租户0（平台上下文 SharedHostTenant）。
// 断言：超管平台上下文映射租户 0；已切换企业租户的超管映射该租户；普通用户返回自身租户；绝不发生自递归。
// =============================================
package api

import (
	"net/http/httptest"
	"testing"

	"translator/internal/store"
	"translator/internal/tenant"
)

// TestKBTenantSuperAdminPlatformContext 超管 tid=0 → 平台共享包宿主租户 0。
func TestKBTenantSuperAdminPlatformContext(t *testing.T) {
	s := &Server{}
	u := &store.User{Role: "admin", TenantID: 0}
	got := s.kbTenant(httptest.NewRequest("GET", "/x", nil), u)
	if got != store.SharedHostTenant {
		t.Fatalf("超管平台上下文应映射租户 0（SharedHostTenant），实得 %d", got)
	}
}

// TestKBTenantSuperAdminSwitchedTenant 超管已切换到企业租户 → 返回该租户。
func TestKBTenantSuperAdminSwitchedTenant(t *testing.T) {
	s := &Server{}
	u := &store.User{Role: "admin", TenantID: 0}
	r := httptest.NewRequest("GET", "/x", nil)
	r = r.WithContext(tenant.WithTenant(r.Context(), 3))
	got := s.kbTenant(r, u)
	if got != 3 {
		t.Fatalf("超管已切换企业租户应返回租户 3，实得 %d", got)
	}
}

// TestKBTenantNormalUser 普通租户用户 → 返回自身租户。
func TestKBTenantNormalUser(t *testing.T) {
	s := &Server{}
	u := &store.User{Role: "user", TenantID: 5}
	got := s.kbTenant(httptest.NewRequest("GET", "/x", nil), u)
	if got != 5 {
		t.Fatalf("应返回自身租户 5，实得 %d", got)
	}
}