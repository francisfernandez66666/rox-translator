// ============ 本文件职责中文说明 ============
// kbTenant 回归测试：修复「误调用自身导致无限递归栈溢出」的历史事故（2026-08-21~23）。
// 断言：超管平台上下文映射租户 1；普通租户管理员返回自身租户；绝不发生自递归。
// =============================================
package api

import (
	"net/http/httptest"
	"testing"

	"translator/internal/store"
)

// TestKBTenantSuperAdminPlatformContext 超管 tid=0 → 行业包宿主租户 1。
func TestKBTenantSuperAdminPlatformContext(t *testing.T) {
	s := &Server{}
	u := &store.User{Role: "admin", TenantID: 0}
	got := s.kbTenant(httptest.NewRequest("GET", "/x", nil), u)
	if got != 1 {
		t.Fatalf("超管平台上下文应映射租户 1，实得 %d", got)
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
