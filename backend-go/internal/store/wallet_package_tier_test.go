// ============ wallet_package_tier_test.go · 职责说明 ============
// 组3 W10 的写侧锁（★ 2026-10-31 补）。
//
// 缺口背景：`packages.tier` 列有读点（txPackageTier，发放 plan 台账时钉档位），
// 但新建/改档套餐此前**没有写路径** ⇒ 运营新配的月包在下次重启回填前一直落 legacy
// （第 ⑥ 档，排最后扣）、年包同样失效。本文件钉两件事：
//
//	① PinPackageTier 的写下归一（显式值只放白名单；空值按有效期钉一次）；
//	② 经 CreatePackage/UpdatePackage 真落库、GetPackage 真读回（列与扫描必须同源）。
//
// =============================================
package store

import "testing"

// TestPinPackageTier 白名单与按期推导。
func TestPinPackageTier(t *testing.T) {
	cases := []struct {
		in   string
		days int
		want string
	}{
		{"", 30, TierPlanMonthly},
		{"", 31, TierPlanMonthly},
		{"", 32, TierPlanYearly},
		{"", 365, TierPlanYearly},
		{"", 366, TierPlanYearly},
		{"", 367, TierPlanPermanent},
		{"", 0, TierPlanPermanent}, // 0＝不限期
		{TierPlanYearly, 30, TierPlanYearly},
		{TierPlanPermanent, 30, TierPlanPermanent},
		{"bogus", 30, TierPlanMonthly}, // 名单外按有效期重钉，不把脏值写进库
	}
	for _, c := range cases {
		if got := PinPackageTier(c.in, c.days); got != c.want {
			t.Fatalf("PinPackageTier(%q,%d)=%q，期望 %q", c.in, c.days, got, c.want)
		}
	}
}

// TestPackageTierPersists 新建即钉档、读回等值；整行更新后仍等值（扫描列与 INSERT/UPDATE 同源）。
func TestPackageTierPersists(t *testing.T) {
	s := newTestStore(t)
	pkg, err := s.CreatePackage(&Package{
		Code: "w10_year", Name: "年包", PType: PackagePaid, Points: 1000,
		DurationDays: 365, Enabled: 1, Tier: PinPackageTier("", 365),
	})
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if pkg.Tier != TierPlanYearly {
		t.Fatalf("W10：新建后读回 tier=%q，期望 %q（列没进 packageCols 或扫描漏了）", pkg.Tier, TierPlanYearly)
	}
	// 整行更新走 UpdatePackage：档位显式改成长期包必须落库。
	pkg.Tier = PinPackageTier(TierPlanPermanent, pkg.DurationDays)
	if err := s.UpdatePackage(pkg); err != nil {
		t.Fatalf("UpdatePackage: %v", err)
	}
	got, err := s.GetPackage(pkg.ID)
	if err != nil {
		t.Fatalf("GetPackage: %v", err)
	}
	if got.Tier != TierPlanPermanent {
		t.Fatalf("W10：更新后 tier=%q，期望 %q", got.Tier, TierPlanPermanent)
	}
}
