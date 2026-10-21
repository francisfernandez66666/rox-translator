// ============ trial_grant_test.go · 职责说明 ============
// 账号级体验额度发放锁（★ 2026-10-21 M3）：
//   - 同一账号第一次发放成功、第二次返回 false 且余额不再增加；
//   - 不同账号各自发一次（账号级，不是租户级）；
//   - 启动迁移回填的存量账号不再补发。
//
// 反证：把 user_trial_grants 的 user_id 主键摘掉，第二次发放会让余额再涨一次，本文件必红。
// =============================================
package store

import (
	"testing"
	"time"
)

// TestGrantUserTrialOncePerAccount 同一账号只发一次，不同账号各发一次。
func TestGrantUserTrialOncePerAccount(t *testing.T) {
	s := newTestStoreWithTenants(t)
	u1, err := s.CreateUser(1, "m3_u1", "h", "m3_u1", "user", 0, 0)
	if err != nil {
		t.Fatalf("创建用户1失败: %v", err)
	}
	u2, err := s.CreateUser(1, "m3_u2", "h", "m3_u2", "user", 0, 0)
	if err != nil {
		t.Fatalf("创建用户2失败: %v", err)
	}
	const tokens = int64(400000)
	exp := time.Now().Add(14 * 24 * time.Hour)

	before := s.SumActiveGrants(1)
	granted, err := s.GrantUserTrialOnce(1, u1.ID, tokens, exp, "register")
	if err != nil || !granted {
		t.Fatalf("首次发放应成功，granted=%v err=%v", granted, err)
	}
	mid := s.SumActiveGrants(1)
	if delta := mid - before; delta != tokens {
		t.Fatalf("首次发放台账增量应为 %d，实际 %d", tokens, delta)
	}

	granted, err = s.GrantUserTrialOnce(1, u1.ID, tokens, exp, "register")
	if err != nil {
		t.Fatalf("第二次发放不应报错: %v", err)
	}
	if granted {
		t.Fatalf("同一账号第二次发放必须返回 false（一个账号仅一次）")
	}
	after := s.SumActiveGrants(1)
	if after != mid {
		t.Fatalf("重复发放又动了台账：%d → %d", mid, after)
	}
	if ok, _ := s.userTrialGranted(u1.ID); !ok {
		t.Fatalf("首次发放后应存在 user_trial_grants 行")
	}

	// 不同账号各自一次：证明粒度是 user_id，不是 tenant_id。
	granted, err = s.GrantUserTrialOnce(1, u2.ID, tokens, exp, "register")
	if err != nil || !granted {
		t.Fatalf("第二个账号首次发放应成功，granted=%v err=%v", granted, err)
	}
	after2 := s.SumActiveGrants(1)
	if delta := after2 - after; delta != tokens {
		t.Fatalf("第二个账号应再发 %d，实际增量 %d", tokens, delta)
	}
}

// TestUserTrialGrantMigrateBackfillsExistingAccounts 存量账号只回填占位、不再补发。
func TestUserTrialGrantMigrateBackfillsExistingAccounts(t *testing.T) {
	s := newTestStoreWithTenants(t)
	u, err := s.CreateUser(1, "m3_legacy", "h", "m3_legacy", "user", 0, 0)
	if err != nil {
		t.Fatalf("创建存量用户失败: %v", err)
	}
	s.UserTrialGrantMigrate() // 模拟升级启动时对存量账号的回填

	before := s.SumActiveGrants(1)
	granted, err := s.GrantUserTrialOnce(1, u.ID, 400000, time.Now().Add(24*time.Hour), "register")
	if err != nil {
		t.Fatalf("回填后的发放探测不应报错: %v", err)
	}
	if granted {
		t.Fatalf("迁移回填的存量账号不应再补发体验额度")
	}
	after := s.SumActiveGrants(1)
	if after != before {
		t.Fatalf("回填后的探测动了台账：%d → %d", before, after)
	}
}
