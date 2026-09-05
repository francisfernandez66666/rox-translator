// ============ 本文件职责中文说明 ============
// packages_platform_test.go · 平台包（tenant_id=0）订阅回归测试
// 回归锁定：/api/plans 会列出全部 enabled 商业包（含平台包），而 handlePackageSubscribe
// 走 GetPackageByCode——修复前按 tenant_id 严格匹配，平台包订阅必然报「套餐不存在或已下架」。
// 本用例锁定：租户可经 GetPackageByCode 命中平台包；同 code 下租户自有包优先。
// =============================================
package store

import (
	"fmt"
	"testing"
)

// TestPlatformPackageSubscribable 平台包可被任意租户命中订阅。
func TestPlatformPackageSubscribable(t *testing.T) {
	env := newScopeEnv(t)
	// 平台包：tenant_id=0，付费包
	plat, err := env.st.CreatePackage(&Package{
		TenantID: 0, Code: "plat_paid", Name: "平台付费包",
		PType: PackagePaid, Sentences: 100000, PriceMoney: 99, DurationDays: 30, Enabled: 1,
	})
	if err != nil {
		t.Fatalf("建平台包失败: %v", err)
	}
	// 任意租户（env 拓扑里的租户2 已存在企业包）应能命中平台包
	pkg, err := env.st.GetPackageByCode(2, "plat_paid")
	if err != nil || pkg == nil {
		t.Fatalf("租户2 应能命中平台包 plat_paid: err=%v pkg=%v", err, pkg)
	}
	if pkg.TenantID != 0 {
		t.Fatalf("命中包宿主应为平台0, 实得 %d", pkg.TenantID)
	}
	if pkg.Enabled != 1 || pkg.PType != PackagePaid {
		t.Fatalf("平台包属性不符: enabled=%d ptype=%s", pkg.Enabled, pkg.PType)
	}
	// 下架包：store 层不判 enabled（由 handler 层 plans_api.go:147 拦截），仅验证不误伤同 code 命中的生效包
	if _, err := env.st.CreatePackage(&Package{
		TenantID: 0, Code: "plat_off", Name: "下架平台包",
		PType: PackagePaid, Sentences: 1, PriceMoney: 1, DurationDays: 1, Enabled: 0,
	}); err != nil {
		t.Fatalf("建下架包失败: %v", err)
	}
	off, err := env.st.GetPackageByCode(2, "plat_off")
	if err != nil || off == nil {
		t.Fatal("store 层应返回下架包（enabled 过滤在 handler），err=" + fmt.Sprint(err))
	}
	if off.Enabled != 0 {
		t.Fatalf("下架包 enabled 应为 0, 实得 %d", off.Enabled)
	}
	_ = plat
}

// TestGetPackageByCodeTenantPriority 同 code 下租户自有包优先于平台包。
func TestGetPackageByCodeTenantPriority(t *testing.T) {
	env := newScopeEnv(t)
	// 平台包与租户3 自有包共用 code "same_code"
	if _, err := env.st.CreatePackage(&Package{
		TenantID: 0, Code: "same_code", Name: "平台版", PType: PackagePaid, Sentences: 1000, PriceMoney: 1, DurationDays: 1, Enabled: 1,
	}); err != nil {
		t.Fatalf("建平台包失败: %v", err)
	}
	if _, err := env.st.CreatePackage(&Package{
		TenantID: 3, Code: "same_code", Name: "租户定制版", PType: PackagePaid, Sentences: 2000, PriceMoney: 2, DurationDays: 2, Enabled: 1,
	}); err != nil {
		t.Fatalf("建租户包失败: %v", err)
	}
	pkg, err := env.st.GetPackageByCode(3, "same_code")
	if err != nil || pkg == nil {
		t.Fatalf("租户3 应命中包: err=%v", err)
	}
	if pkg.TenantID != 3 || pkg.Name != "租户定制版" {
		t.Fatalf("应优先命中租户自有包(3), 实得 tenant=%d name=%s", pkg.TenantID, pkg.Name)
	}
}
