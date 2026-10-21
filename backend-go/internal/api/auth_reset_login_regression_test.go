package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
)

// TestResetPasswordEmailThenLogin 端到端回归：注册（带邮箱）→ 发码 → 邮箱重置 → 邮箱+新密码登录。
// 这条锁用户 2026-10-21 报障「前端重置密码后仍然无法登录」。
func TestResetPasswordEmailThenLogin(t *testing.T) {
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	const email = "reset_login@example.com"
	const oldPwd = "oldpass123"
	const newPwd = "newpass123"
	if _, m := p0Register(t, s, "reset_login_user", oldPwd, email); m["success"] != true {
		t.Fatalf("注册失败: %v", m)
	}
	u, err := s.Store.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("按邮箱反查账号失败: %v", err)
	}
	f20SeedCode(t, u.ID, resetCode{Code: "246810", ExpiresAt: time.Now().Add(10 * time.Minute)})
	body, _ := json.Marshal(map[string]string{"email": email, "code": "246810", "new_password": newPwd})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.230:40000"
	w := httptest.NewRecorder()
	s.handleResetPassword(w, r)
	if w.Code != 200 {
		t.Fatalf("邮箱重置应成功: code=%d body=%s", w.Code, w.Body.String())
	}
	var rm map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &rm)
	if rm["login_account"] != email {
		t.Fatalf("重置出参 login_account 必须是邮箱: %v", rm)
	}
	if code, m := p0Login(t, s, email, newPwd); code != 200 {
		t.Fatalf("重置后用邮箱+新密码登录失败: code=%d body=%v", code, m)
	}
	if code, _ := p0Login(t, s, email, oldPwd); code == 200 {
		t.Fatalf("旧密码仍然能登录")
	}
	// 反查库内哈希确认真被改写（不是"登录接口恰好放行"）
	u2, _ := s.Store.GetUserByEmail(email)
	if u2 == nil || !auth.CheckPassword(u2.PasswordHash, newPwd) || auth.CheckPassword(u2.PasswordHash, oldPwd) {
		t.Fatalf("库内密码哈希未被正确改写")
	}
}
