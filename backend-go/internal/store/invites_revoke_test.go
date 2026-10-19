// ============ invites_revoke_test.go · 职责说明 ============
// G9（2026-10-19）：邀请码撤销（revoked 状态位，不删行）。
//
// 缺陷形态（D10）：邀请码只能一次性使用、无法撤销——发错/泄露的码只能眼巴巴等它被用掉或
// 造一枚新码；且没有任何「谁用了这枚码」的界面追溯（码用完就成死数据）。
//
// 判据：
//   - 撤销后 used_by/used_at 原样保留（追溯资产不许随撤销丢失），行仍在库里；
//   - 撤销后注册口拒收（MarkInviteCodeUsed 返回 claimed=false）；
//   - 已使用的码不可撤销（used=true 档明确区分于「不存在/已撤销」）；
//   - 重复撤销幂等（不报错）；
//   - 派生 Status：active/used/revoked。
//
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TestInviteRevoke
// =============================================
package store

import "testing"

// TestInviteRevokeLifecycle G9 主判据：撤销→拒用→保追溯→幂等。
func TestInviteRevokeLifecycle(t *testing.T) {
	s := newTestStoreWithTenants(t)

	c, err := s.CreateInviteCode("INV-REVOKE-1", 1)
	if err != nil {
		t.Fatalf("建码失败: %v", err)
	}
	if c.Status != "active" {
		t.Fatalf("新码 Status 应为 active，实际 %q", c.Status)
	}

	// 撤销：ok=true
	ok, used, rerr := s.RevokeInviteCode(c.ID)
	if rerr != nil || !ok || used {
		t.Fatalf("撤销应成功(ok=true,used=false)，实际 ok=%v used=%v err=%v", ok, used, rerr)
	}

	// 行仍在、状态位正确、追溯字段保留
	got, err := s.GetInviteCodeByCode("INV-REVOKE-1")
	if err != nil {
		t.Fatalf("撤销后码应仍在库（不删行），查询报错: %v", err)
	}
	if got.Revoked != 1 || got.Status != "revoked" {
		t.Fatalf("撤销后应 revoked=1/status=revoked，实际 revoked=%d status=%q", got.Revoked, got.Status)
	}
	if got.RevokedAt == "" {
		t.Fatalf("撤销时间应落库，实际为空")
	}
	if got.Used != 0 || got.UsedBy != "" {
		t.Fatalf("未使用的码撤销后 used/used_by 应保持 0/空，实际 used=%d used_by=%q", got.Used, got.UsedBy)
	}

	// 注册口：撤销后必须拒用（claimed=false）
	claimed, merr := s.MarkInviteCodeUsed(c.ID, "someone")
	if merr != nil {
		t.Fatalf("MarkInviteCodeUsed 不应报错: %v", merr)
	}
	if claimed {
		t.Fatalf("已撤销的码必须拒用（claimed=false），实际 claimed=true")
	}

	// 重复撤销幂等：不报错、不改变结果
	ok2, used2, err2 := s.RevokeInviteCode(c.ID)
	if err2 != nil || ok2 || used2 {
		t.Fatalf("重复撤销应幂等(ok=false,used=false,err=nil)，实际 ok=%v used=%v err=%v", ok2, used2, err2)
	}

	// 列表回读带状态
	list, lerr := s.ListInviteCodes()
	if lerr != nil {
		t.Fatalf("列表失败: %v", lerr)
	}
	found := false
	for _, it := range list {
		if it.Code == "INV-REVOKE-1" {
			found = true
			if it.Status != "revoked" {
				t.Fatalf("列表中撤销码 Status 应为 revoked，实际 %q", it.Status)
			}
		}
	}
	if !found {
		t.Fatalf("列表应含被撤销的码（撤销不等于删除）")
	}
}

// TestInviteRevokeRejectsUsedCode 已使用的码不可撤销，且与「不存在/已撤销」档分得开。
func TestInviteRevokeRejectsUsedCode(t *testing.T) {
	s := newTestStoreWithTenants(t)

	c, err := s.CreateInviteCode("INV-USED-1", 1)
	if err != nil {
		t.Fatalf("建码失败: %v", err)
	}
	if claimed, _ := s.MarkInviteCodeUsed(c.ID, "alice"); !claimed {
		t.Fatalf("首次占用应成功")
	}
	got, _ := s.GetInviteCodeByCode("INV-USED-1")
	if got.Status != "used" {
		t.Fatalf("已用码 Status 应为 used，实际 %q", got.Status)
	}
	ok, used, rerr := s.RevokeInviteCode(c.ID)
	if rerr != nil {
		t.Fatalf("撤销已用码不应报错: %v", rerr)
	}
	if ok || !used {
		t.Fatalf("已用码应返回 ok=false/used=true（供调用方给明确文案），实际 ok=%v used=%v", ok, used)
	}
	// 追溯字段必须还在（撤销尝试不改动它）
	got2, _ := s.GetInviteCodeByCode("INV-USED-1")
	if got2.UsedBy != "alice" || got2.UsedAt == "" {
		t.Fatalf("已用码追溯字段应保留，实际 used_by=%q used_at=%q", got2.UsedBy, got2.UsedAt)
	}

	// 不存在的 id：ok=false/used=false（不泄露存在性、不报错）
	ok3, used3, err3 := s.RevokeInviteCode(999999)
	if err3 != nil || ok3 || used3 {
		t.Fatalf("不存在 id 应 ok=false/used=false/err=nil，实际 ok=%v used=%v err=%v", ok3, used3, err3)
	}
}

// TestInvitesRevokeMigrateIdempotent 补列幂等（老库反复启动场景）。
func TestInvitesRevokeMigrateIdempotent(t *testing.T) {
	s := newTestStoreWithTenants(t)
	for i := 0; i < 3; i++ {
		s.InvitesRevokeMigrate()
	}
	if _, err := s.CreateInviteCode("INV-MIG-1", 1); err != nil {
		t.Fatalf("补列后建码失败: %v", err)
	}
}
