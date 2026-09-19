// ============================================================================
// engine/scope_persona_test.go — CJK 缓存键的角色隔离断言（2026-09-19 角色功能）
// 口径：cjkCacheScopeKey 必须包含 scope.JobRole——两个同租户同组织链但不同职业角色的
// 用户可见域不同（角色包按用户装配），键相同即串缓存（前端开发命中销售的术语）。
// ============================================================================
package engine

import (
	"testing"

	"translator/internal/kb"
)

func TestCJKCacheScopeKeyJobRoleIsolation(t *testing.T) {
	base := func(jobRole string) *kb.PackScope {
		return &kb.PackScope{
			TenantID: 2, Chain: []int64{7, 3}, AllowCrossDept: true,
			JobRole: jobRole,
		}
	}
	if k1, k2 := cjkCacheScopeKey(base("frontend")), cjkCacheScopeKey(base("sales")); k1 == k2 {
		t.Fatalf("不同 job_role 缓存键相同 → 跨用户串缓存: %s", k1)
	}
	if k1, k2 := cjkCacheScopeKey(base("frontend")), cjkCacheScopeKey(base("frontend")); k1 != k2 {
		t.Fatalf("相同作用域缓存键不稳定: %s != %s", k1, k2)
	}
	if k1, k2 := cjkCacheScopeKey(base("")), cjkCacheScopeKey(base("frontend")); k1 == k2 {
		t.Fatal("未选角色与已选角色必须分区（装配集不同）")
	}
}

func TestUserJobRoleContext(t *testing.T) {
	ctx := t.Context()
	if got := UserJobRoleFromContext(ctx); got != "" {
		t.Fatalf("未注入时 job_role 应为空（安全默认：不见角色包），实际 %q", got)
	}
	ctx = WithUserJobRole(ctx, "pm")
	if got := UserJobRoleFromContext(ctx); got != "pm" {
		t.Fatalf("ctx 回读失败: %q", got)
	}
}
