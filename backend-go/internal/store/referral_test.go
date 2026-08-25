// ============ 本文件职责说明 ============
// 邀请裂变数据层测试：首绑闸门、体验叠加去重、付费永久奖励入邀请人租户、记录查询。
package store

import (
	"testing"
)

// TestReferralFlow 全链路：建邀请人/被邀人 → 首绑绑定 → 体验叠加（重复调用不叠加奖励行）→
// 付费永久奖励入「邀请人」租户余额（幂等）→ 记录查询。
func TestReferralFlow(t *testing.T) {
	s := newTestStoreWithTenants(t)
	// 第二个租户：被邀人注册后归属的独立试用租户
	mustExec(t, s, `INSERT INTO tenants (code, name, status, expires_at, permissions) VALUES ('t2','被邀租户','active','','{}')`)
	_ = s.EnsureBalance(2)
	// 邀请人：租户1 管理员；被邀人：租户2 用户
	u1, err := s.CreateUser(1, "inviter", "x", "邀请人", RoleTenantAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建邀请人失败: %v", err)
	}
	code := s.EnsureRefCode(u1.ID)
	if code == "" {
		t.Fatalf("EnsureRefCode 应生成个人码")
	}
	if again := s.EnsureRefCode(u1.ID); again != code {
		t.Fatalf("个人码应稳定: %s vs %s", code, again)
	}
	u2, err := s.CreateUser(2, "invitee", "x", "被邀人", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("创建被邀人失败: %v", err)
	}
	// 首绑绑定
	iUID, iTID, ok := s.BindReferral(u2.ID, code)
	if !ok || iUID != u1.ID || iTID != 1 {
		t.Fatalf("首绑应成功且指向邀请人: ok=%v uid=%d tid=%d", ok, iUID, iTID)
	}
	// 首绑闸门：再次绑定不同码无效
	if _, _, ok := s.BindReferral(u2.ID, "zzzz"); ok {
		t.Fatalf("已绑定的被邀人不应被二次绑定")
	}
	// 体验叠加：+10万/+14天，写入 trial_stack 奖励
	if err := s.GrantTrialStack(u1.ID, 1, u2.ID, 100000, 14); err != nil {
		t.Fatalf("GrantTrialStack 失败: %v", err)
	}
	// 同对去重：重复发放无副作用（仍只有一条 trial_stack）
	_ = s.GrantTrialStack(u1.ID, 1, u2.ID, 100000, 14)
	if g := s.SumActiveGrants(1); g != 100000 {
		t.Fatalf("体验叠加台账应为 100000，实际 %d", g)
	}
	// 付费永久奖励：+50万 入邀请人租户1 永久余额
	before := int64(0)
	if b, _ := s.GetBalance(1); b != nil {
		before = b.Balance
	}
	if err := s.RewardPaidPermanent(u2.ID, 500000); err != nil {
		t.Fatalf("RewardPaidPermanent 失败: %v", err)
	}
	b, _ := s.GetBalance(1)
	if b == nil || b.Balance != before+500000 {
		t.Fatalf("邀请人永久余额应为 %d，实际 %+v", before+500000, b)
	}
	// 仅首笔：重复确认不再发奖
	_ = s.RewardPaidPermanent(u2.ID, 500000)
	if b2, _ := s.GetBalance(1); b2.Balance != before+500000 {
		t.Fatalf("付费奖励应按对去重，余额异常: %d", b2.Balance)
	}
	// 记录查询：两条（trial_stack + paid_perm）
	recs := s.ListReferrals(u1.ID)
	if len(recs) != 2 {
		t.Fatalf("邀请记录应为 2 条，实际 %d", len(recs))
	}
	// 非邀请来源静默跳过
	if err := s.RewardPaidPermanent(u1.ID, 100); err != nil {
		t.Fatalf("非邀请来源应返回 nil，实际 %v", err)
	}
}
