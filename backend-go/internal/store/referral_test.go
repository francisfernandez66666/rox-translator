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
	// 邀请人与被邀人均在租户1（ref_code 全局唯一，跨租户裂变亦可解析）
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
	u2, err := s.CreateUser(1, "invitee", "x", "被邀人", RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("创建被邀人失败: %v", err)
	}
	// 首绑绑定
	iUID, iTID, ok := s.BindReferral(u2.ID, 1, code)
	if !ok || iUID != u1.ID || iTID != 1 {
		t.Fatalf("首绑应成功且指向邀请人: ok=%v uid=%d tid=%d", ok, iUID, iTID)
	}
	// 首绑闸门：再次绑定不同码无效
	if _, _, ok := s.BindReferral(u2.ID, 1, "zzzz"); ok {
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
	if err := s.RewardPaidPermanent(u2.ID, 500000, 0); err != nil {
		t.Fatalf("RewardPaidPermanent 失败: %v", err)
	}
	b, _ := s.GetBalance(1)
	if b == nil || b.Balance != before+500000 {
		t.Fatalf("邀请人永久余额应为 %d，实际 %+v", before+500000, b)
	}
	// 仅首笔：重复确认不再发奖
	_ = s.RewardPaidPermanent(u2.ID, 500000, 0)
	if b2, _ := s.GetBalance(1); b2.Balance != before+500000 {
		t.Fatalf("付费奖励应按对去重，余额异常: %d", b2.Balance)
	}
	// 记录查询：两条（trial_stack + paid_perm）
	recs := s.ListReferrals(u1.ID)
	if len(recs) != 2 {
		t.Fatalf("邀请记录应为 2 条，实际 %d", len(recs))
	}
	// 非邀请来源静默跳过
	if err := s.RewardPaidPermanent(u1.ID, 100, 0); err != nil {
		t.Fatalf("非邀请来源应返回 nil，实际 %v", err)
	}
}

// TestReferralOneidDualUnique 奖励双唯一回归（2026-08-26 修正定稿）：
// 账户层 id 主键不可变、email 同一时刻唯一可换绑；奖励层 invitee_uid 与
// invitee_email 快照任一历史碰撞即永久拒绝——换绑流转无法二次领取。
func TestReferralOneidDualUnique(t *testing.T) {
	st := newConcurrentTestStore(t)
	x, err := st.CreateUser(1, "oneid_inv_x", "h", "X", "tenant_admin", 0, 0)
	if err != nil {
		t.Fatalf("建邀请人X: %v", err)
	}
	y, _ := st.CreateUser(1, "oneid_inv_y", "h", "Y", "user", 0, 0)
	a, _ := st.CreateUser(1, "oneid_inv_a", "h", "A", "user", 0, 0)
	b, _ := st.CreateUser(1, "oneid_inv_b", "h", "B", "user", 0, 0)
	if err := st.SetUserEmail(a.ID, 1, "swap@t.com"); err != nil {
		t.Fatalf("绑定邮箱A: %v", err)
	}
	if err := st.SetUserEmail(b.ID, 1, "b@t.com"); err != nil {
		t.Fatalf("绑定邮箱B: %v", err)
	}
	codeX, codeY := st.EnsureRefCode(x.ID), st.EnsureRefCode(y.ID)

	// ① A 被 X 邀请：首绑 + 体验叠加正常发放
	if _, _, ok := st.BindReferral(a.ID, 1, codeX); !ok {
		t.Fatal("A 首绑应成功")
	}
	if err := st.GrantTrialStack(x.ID, 1, a.ID, 300000, 14); err != nil {
		t.Fatalf("首次体验叠加: %v", err)
	}
	first := st.SumActiveGrants(1)
	if first != 300000 {
		t.Fatalf("台账应为 300000，实际 %d", first)
	}

	// ② 邮箱流转模拟：A 解绑 swap@t.com → B 接手该邮箱；B 被 Y 邀请
	//    绑定本身可成立（绑定不等于奖励），但奖励因邮箱快照撞库被永久拒绝
	if err := st.SetUserEmail(a.ID, 1, ""); err != nil {
		t.Fatalf("A 解绑: %v", err)
	}
	if err := st.SetUserEmail(b.ID, 1, "swap@t.com"); err != nil {
		t.Fatalf("B 接手邮箱: %v", err)
	}
	if _, _, ok := st.BindReferral(b.ID, 1, codeY); !ok {
		t.Fatal("B 首绑应成功（绑定与奖励分离）")
	}
	if err := st.GrantTrialStack(y.ID, 1, b.ID, 300000, 14); err != nil {
		t.Fatalf("撞库调用不应报错（静默跳过）: %v", err)
	}
	if g := st.SumActiveGrants(1); g != first {
		t.Fatalf("邮箱撞库必须拒绝发放: %d → %d", first, g)
	}
}
