// ============ 本文件职责中文说明 ============
// 订阅到期数据层单元测试：付费包发放写入 PackageExpires（DurationDays 换算）、
// 增量包不改订阅态、ExpirePackage 摘除订阅身份但保留句数余额。
package store

import (
	"strings"
	"testing"
	"time"

	"translator/internal/tenant"
)

// TestGrantPaidPackageExpiry 付费包发放：写入到期时间并清空提醒标记。
func TestGrantPaidPackageExpiry(t *testing.T) {
	s := newTestStoreWithTenants(t)
	pkg, err := s.CreatePackage(&Package{Code: "m30", Name: "包月", PType: PackagePaid, Sentences: 100, DurationDays: 30, Enabled: 1})
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if _, err := s.GrantPackageSentences(1, pkg); err != nil {
		t.Fatalf("GrantPackageSentences: %v", err)
	}
	perms, _ := s.GetTenantPerms(1)
	if perms.PackageCode != "m30" || perms.PackageExpires == "" {
		t.Fatalf("应写入 package_code 与 package_expires_at: %+v", perms)
	}
	exp, perr := time.Parse(time.RFC3339, perms.PackageExpires)
	if perr != nil || time.Until(exp) < 29*24*time.Hour {
		t.Fatalf("到期时间应为 ~30 天后: %v (%v)", perms.PackageExpires, perr)
	}
}

// TestGrantIncrementNoSubChange 增量包：只加句数，不动订阅身份与到期。
func TestGrantIncrementNoSubChange(t *testing.T) {
	s := newTestStoreWithTenants(t)
	before := &tenant.Perms{SentenceBalance: 10}
	_ = s.SaveTenantPerms(1, before)
	pkg, _ := s.CreatePackage(&Package{Code: "inc50", Name: "增量 50", PType: PackageIncrement, Sentences: 50, Enabled: 1})
	bal, err := s.GrantPackageSentences(1, pkg)
	if err != nil || bal != 60 {
		t.Fatalf("增量发放应得 60，实际 %d (%v)", bal, err)
	}
	perms, _ := s.GetTenantPerms(1)
	if perms.PackageCode != "" || perms.PackageExpires != "" {
		t.Fatalf("增量包不应改订阅态: %+v", perms)
	}
}

// TestExpirePackage 到期摘除：订阅字段清空、句数余额保留、提醒标记复位。
func TestExpirePackage(t *testing.T) {
	s := newTestStoreWithTenants(t)
	pkg, _ := s.CreatePackage(&Package{Code: "m1", Name: "月包", PType: PackagePaid, Sentences: 30, DurationDays: 30, Enabled: 1})
	_, _ = s.GrantPackageSentences(1, pkg)
	code, err := s.ExpirePackage(1)
	if err != nil || code != "m1" {
		t.Fatalf("ExpirePackage 应返回被摘除包编码 m1: %q %v", code, err)
	}
	perms, _ := s.GetTenantPerms(1)
	if perms.PackageCode != "" || perms.PackageExpires != "" {
		t.Fatalf("摘除后不应残留订阅字段: %+v", perms)
	}
	if perms.SentenceBalance != 30 {
		t.Fatalf("句数余额是买断资产应保留 30，实际 %d", perms.SentenceBalance)
	}
}

// TestPermsJSONRoundTrip Perms 新字段的 JSON 序列化往返（防 tag 笔误）。
func TestPermsJSONRoundTrip(t *testing.T) {
	p := tenant.ParsePerms(`{"package_code":"x","package_expires_at":"2026-09-01T00:00:00Z","notified_exp7":true}`)
	if p.PackageExpires == "" || !p.NotifiedExp7 || strings.Contains(p.PackageExpires, `"`) {
		t.Fatalf("新字段解析异常: %+v", p)
	}
}
