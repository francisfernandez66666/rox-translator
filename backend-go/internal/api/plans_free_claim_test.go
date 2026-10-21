// ============ 本文件职责中文说明 ============
// 免费体验包手动领取的拒绝锁（★ 2026-10-21 M3 用户令：注册/首登自动发放，不需要手动领）。
//
// 旧形态（上一批）在 /api/package/subscribe 对免费包真发 1000 积分，导致同一账号可反复领取；
// 本锁钉住：手动领取一律 409 CONFLICT，余额与订阅身份一分不动。
// =============================================
package api

import (
	"encoding/json"
	"testing"

	"translator/internal/store"
)

// TestPackageSubscribeFreeIsAutoOnly 免费包不允许手动领取，且不产生任何余额变化。
func TestPackageSubscribeFreeIsAutoOnly(t *testing.T) {
	s, token, _ := newUpgradeTestServer(t)
	if _, err := s.Store.CreatePackage(&store.Package{
		Code: "trial_1000", Name: "体验包（新用户）", PType: store.PackageFree,
		Points: 1000, Sentences: 0, DurationDays: 14, Enabled: 1,
	}); err != nil {
		t.Fatalf("创建免费体验包失败: %v", err)
	}
	beforePerms, _ := s.Store.GetTenantPerms(1)
	beforeBal, _ := s.Store.GetBalance(1)

	r := postUpgrade(t, s, "/api/package/subscribe", token, map[string]string{"code": "trial_1000"})
	if r.Code != 409 {
		t.Fatalf("手动领取免费包应 409，实际 %d：%s", r.Code, r.Body.String())
	}
	var resp struct {
		Success bool   `json:"success"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(r.Body.Bytes(), &resp)
	if resp.Success || resp.Code != "CONFLICT" {
		t.Fatalf("409 响应体应为 success=false code=CONFLICT，实际 %s", r.Body.String())
	}

	afterPerms, _ := s.Store.GetTenantPerms(1)
	if afterPerms.PackageCode != beforePerms.PackageCode || afterPerms.PackageExpires != beforePerms.PackageExpires {
		t.Fatalf("被拒的手动领取改写了订阅身份：%s/%s → %s/%s",
			beforePerms.PackageCode, beforePerms.PackageExpires, afterPerms.PackageCode, afterPerms.PackageExpires)
	}
	afterBal, _ := s.Store.GetBalance(1)
	if afterBal.Balance != beforeBal.Balance {
		t.Fatalf("被拒的手动领取动了余额：%d → %d", beforeBal.Balance, afterBal.Balance)
	}
}
