// ============ trial_auto_test.go · 职责说明 ============
// 账号级体验额度自动发放的 API 端到端锁（★ 2026-10-21 M3）：
//   - 注册成功即到账（不是只改展示字段）；
//   - 同一账号再次登录不重复发放；
//   - 迁移后新建、但注册时未发的自助账号，首次登录补发一次。
//
// 反证：把 autoGrantUserTrial 从 login 路径摘掉，第三段必红。
// =============================================
package api

import (
	"testing"

	"translator/internal/auth"
	"translator/internal/db"
)

// TestAccountTrialAutoGrantRegisterAndLogin 注册/首登各一次，重复登录不再发。
func TestAccountTrialAutoGrantRegisterAndLogin(t *testing.T) {
	s := f17RegisterServer(t)

	// ① 注册即发：响应里的 user.id 对应的账号拿到一笔体验台账。
	out := callF17Register(t, s, "m3_auto_reg", "", "")
	userObj, _ := out["user"].(map[string]interface{})
	uid := int64(userObj["id"].(float64))
	tid := int64(out["tenant_id"].(float64))
	sum1 := registeredTrialGrantSum(t, s, tid, uid)
	if sum1 <= 0 {
		t.Fatalf("注册后账号 %d 应自动获得体验额度，实际台账合计=%d", uid, sum1)
	}

	// ② 再次登录不重复发。
	if code, body := p0Login(t, s, "m3_auto_reg", "pw123456"); code != 200 {
		t.Fatalf("注册账号登录应成功，实际 %d：%v", code, body)
	}
	if sum2 := registeredTrialGrantSum(t, s, tid, uid); sum2 != sum1 {
		t.Fatalf("同一账号再次登录不应重复发放：%d → %d", sum1, sum2)
	}

	// ③ 迁移后新建、但未走注册发放的账号：首登补发一次。
	u, err := s.Store.CreateUser(1, "m3_auto_login", auth.PasswordHash("pw123456"), "m3_auto_login", "user", 0, 0)
	if err != nil {
		t.Fatalf("创建待首登账号失败: %v", err)
	}
	before := registeredTrialGrantSum(t, s, 1, u.ID)
	if code, body := p0Login(t, s, "m3_auto_login", "pw123456"); code != 200 {
		t.Fatalf("待补发账号登录应成功，实际 %d：%v", code, body)
	}
	after := registeredTrialGrantSum(t, s, 1, u.ID)
	if after <= before {
		t.Fatalf("从未发放过的自助账号首登应补发体验额度：%d → %d (uid=%d)", before, after, u.ID)
	}
	if code, body := p0Login(t, s, "m3_auto_login", "pw123456"); code != 200 {
		t.Fatalf("待补发账号二次登录应成功，实际 %d：%v", code, body)
	}
	if again := registeredTrialGrantSum(t, s, 1, u.ID); again != after {
		t.Fatalf("补发账号二次登录不应重复发放：%d → %d", after, again)
	}
}

// registeredTrialGrantSum 只统计本账号的“注册自动发放”台账，避开登录任务等其他入账。
func registeredTrialGrantSum(t *testing.T, s *Server, tid, uid int64) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(s.Store.DB(), db.CurrentDialect(),
		"SELECT COALESCE(SUM(total),0) FROM quota_grants WHERE tenant_id=? AND kind='trial' AND source='register' AND ref_id=?",
		tid, uid).Scan(&n); err != nil {
		t.Fatalf("读取体验台账失败: %v", err)
	}
	return n
}
