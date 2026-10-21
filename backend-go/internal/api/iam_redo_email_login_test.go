// ============================================================================
// iam_redo_email_login_test.go — 2026-10-21 IAM 重做：邮箱验证码登录主流程
//
// 覆盖四条产品口径：
//
//	① 未注册邮箱 + 有效登录码 + 已同意协议 ⇒ 自动建空白个人账号并下发会话；
//	② 验证码错 ⇒ 401 且**不建号**（不能靠错码试出"这个邮箱没注册"）；
//	③ 首登资料提醒可跳过：跳过只关本轮、标记仍为 1 并重签 token；完成才清 0；
//	④ 公开找回/重置已下线 ⇒ 410；密码登录未知账号/错密码统一 404（不泄露存在性）。
//
// ============================================================================
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/iam"
)

var iamEmailSeq int64

func iamEmailFixture(t *testing.T) (*Server, string) {
	t.Helper()
	fx := f77Fixture(t)
	fx.srv.Cfg = config.C
	email := fmt.Sprintf("iam_otp_%d@example.com", atomic.AddInt64(&iamEmailSeq, 1))
	return fx.srv, email
}

func putLoginCode(email, code string) {
	emailCodes.mu.Lock()
	emailCodes.codes[emailCodeKey(email, emailPurposeLogin)] = &emailCode{Code: code, SentAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}
	emailCodes.mu.Unlock()
}

func postEmailLogin(t *testing.T, s *Server, email, code string, agreed bool) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"email": email, "code": code, "agreed": agreed, "device_id": "iamdevice01"})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestIAMRedoEmailLogin_CreatesBlankPersonalAccount(t *testing.T) {
	s, email := iamEmailFixture(t)
	putLoginCode(email, "654321")

	code, resp := postEmailLogin(t, s, email, "654321", true)
	if code != 200 || resp["success"] != true || resp["token"] == "" {
		t.Fatalf("邮箱验证码登录应成功：code=%d body=%v", code, resp)
	}
	u, ok := resp["user"].(map[string]any)
	if !ok || u["email"] != email {
		t.Fatalf("响应缺少账号/邮箱：%v", resp["user"])
	}
	if u["profile_setup_required"] != float64(1) {
		t.Fatalf("自动建号必须带首登资料提醒：%v", u)
	}
	got, err := s.Store.GetUserByEmail(email)
	if err != nil || got == nil {
		t.Fatalf("账号未落库：err=%v", err)
	}
	if got.TenantID <= 0 || got.Role != "tenant_admin" {
		t.Fatalf("应为个人租户 tenant_admin：%+v", got)
	}
	if got.PasswordHash != "" {
		t.Fatalf("空白账号不应有密码：%q", got.PasswordHash)
	}
	// 同一邮箱再来一次必须复用账号，不许重复建号。
	putLoginCode(email, "111111")
	if code, resp := postEmailLogin(t, s, email, "111111", true); code != 200 {
		t.Fatalf("老账号二次登录失败：code=%d body=%v", code, resp)
	}
	all, _ := s.Store.ListUsers(got.TenantID)
	if len(all) != 1 {
		t.Fatalf("同邮箱重复建号：tenant=%d users=%d", got.TenantID, len(all))
	}
}

func TestIAMRedoEmailLogin_WrongCodeCreatesNothing(t *testing.T) {
	s, email := iamEmailFixture(t)
	putLoginCode(email, "222222")
	code, resp := postEmailLogin(t, s, email, "000000", true)
	if code != 401 {
		t.Fatalf("错码应 401：code=%d body=%v", code, resp)
	}
	if _, err := s.Store.GetUserByEmail(email); err == nil {
		t.Fatalf("错码不应留下账号")
	}
}

func TestIAMRedoProfileSetup_SkipKeepsFlagAndReissuesToken(t *testing.T) {
	s, email := iamEmailFixture(t)
	putLoginCode(email, "333333")
	if code, resp := postEmailLogin(t, s, email, "333333", true); code != 200 {
		t.Fatalf("预置登录失败：%d %v", code, resp)
	}
	u, err := s.Store.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("读账号失败：%v", err)
	}
	tok, _ := auth.Sign(u, time.Hour)
	body := bytes.NewBufferString(`{"skip":true}`)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/profile-setup", body)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleProfileSetup(w, r)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != 200 || resp["success"] != true || resp["token"] == "" {
		t.Fatalf("跳过应成功并重签：code=%d body=%v", w.Code, resp)
	}
	after, _ := s.Store.GetUserByEmail(email)
	if after.ProfileSetupRequired != 1 {
		t.Fatalf("暂时跳过必须保留提醒标记：%+v", after)
	}
	// 跳过后不能把无密码账号的邮箱验证码登录弄坏，且下次登录仍应带提醒。
	putLoginCode(email, "444444")
	if code, resp := postEmailLogin(t, s, email, "444444", true); code != 200 || resp["user"].(map[string]any)["profile_setup_required"] != float64(1) {
		t.Fatalf("暂时跳过后二次登录应成功且仍提醒：code=%d body=%v", code, resp)
	}
}

func TestIAMRedoProfileSetup_CompleteClearsFlagAndPersistsIdentity(t *testing.T) {
	s, email := iamEmailFixture(t)
	putLoginCode(email, "777777")
	if code, resp := postEmailLogin(t, s, email, "777777", true); code != 200 {
		t.Fatalf("预置登录失败：%d %v", code, resp)
	}
	u, err := s.Store.GetUserByEmail(email)
	if err != nil {
		t.Fatalf("读账号失败：%v", err)
	}
	tok, _ := auth.Sign(u, time.Hour)
	body := bytes.NewBufferString(`{"username":"资料完成用户","password":"newpass123"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/profile-setup", body)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleProfileSetup(w, r)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if w.Code != 200 || resp["success"] != true || resp["token"] == "" {
		t.Fatalf("完成资料应成功并重签：code=%d body=%v", w.Code, resp)
	}
	after, _ := s.Store.GetUserByEmail(email)
	if after.ProfileSetupRequired != 0 {
		t.Fatalf("完成资料必须清掉提醒标记：%+v", after)
	}
	if after.Username != "资料完成用户" || after.DisplayName != "资料完成用户" {
		t.Fatalf("用户名/展示名未落库：%+v", after)
	}
	if !auth.CheckPassword(after.PasswordHash, "newpass123") {
		t.Fatalf("新密码未写入")
	}
	// 完成后用邮箱 + 新密码走备用密码登录，必须能进——这条锁住「资料页存下的口令」不是摆设。
	loginBody, _ := json.Marshal(map[string]any{"email": email, "password": "newpass123"})
	lr := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(loginBody))
	lr.Header.Set("Content-Type", "application/json")
	lw := httptest.NewRecorder()
	s.handleLogin(lw, lr)
	if lw.Code != 200 || !bytes.Contains(lw.Body.Bytes(), []byte(`"token"`)) {
		t.Fatalf("资料页设置的新密码必须能登录：code=%d body=%s", lw.Code, lw.Body.String())
	}
}

func TestIAMRedoPasswordRecoveryGoneAndLoginNoEnumeration(t *testing.T) {
	s, _ := iamEmailFixture(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/forgot-password", nil)
	s.handlePasswordRecoveryGone(w, r)
	if w.Code != http.StatusGone {
		t.Fatalf("找回密码必须 410：%d %s", w.Code, w.Body.String())
	}
	for _, acct := range []string{"nobody@example.com", "ghost"} {
		body, _ := json.Marshal(map[string]any{"email": acct, "password": "whatever123"})
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		if w.Code != 404 || !bytes.Contains(w.Body.Bytes(), []byte("账号或密码错误")) {
			t.Fatalf("未知账号必须统一 404/同文案：acct=%s code=%d body=%s", acct, w.Code, w.Body.String())
		}
	}
}

func TestIAMRedoUsernameMayRepeatWithinTenant(t *testing.T) {
	fx := f77Fixture(t)
	st := fx.srv.Store
	tid := int64(99)
	if _, err := st.CreateUserWithEmail(tid, "same_name", "", "同名", iam.RoleUser, 0, 0, "same1@example.com", time.Now().Format(time.RFC3339), 1); err != nil {
		t.Fatalf("第一个同名用户创建失败：%v", err)
	}
	if _, err := st.CreateUserWithEmail(tid, "same_name", "", "同名", iam.RoleUser, 0, 0, "same2@example.com", time.Now().Format(time.RFC3339), 1); err != nil {
		t.Fatalf("同租户用户名必须允许重名：%v", err)
	}
}

func TestIAMRedoDisabledEmailCanBeReused(t *testing.T) {
	fx := f77Fixture(t)
	st := fx.srv.Store
	tid := int64(3)
	old, err := st.CreateUserWithEmail(tid, "old_owner", "", "旧账号", iam.RoleUser, 0, 0, "reuse@example.com", time.Now().Format(time.RFC3339), 1)
	if err != nil {
		t.Fatalf("预置旧账号失败：%v", err)
	}
	if err := st.DeactivateSelf(old.ID, tid); err != nil {
		t.Fatalf("发起注销失败：%v", err)
	}
	st.FinalizeDeactivation(old.ID, tid)
	if _, err := st.GetUserByEmail("reuse@example.com"); err == nil {
		t.Fatalf("停用账号不该继续占着邮箱")
	}
	nu, err := st.CreateUserWithEmail(tid, "new_owner", "", "新账号", iam.RoleUser, 0, 0, "REUSE@example.com", time.Now().Format(time.RFC3339), 1)
	if err != nil || nu == nil || nu.ID == old.ID {
		t.Fatalf("停用账号的邮箱必须可被新账号复用：err=%v new=%v", err, nu)
	}
}

func TestIAMRedoEmailAutoCreateSharesRegisterIPGuard(t *testing.T) {
	s, first := iamEmailFixture(t)
	putLoginCode(first, "555555")
	if code, resp := postEmailLogin(t, s, first, "555555", true); code != 200 {
		t.Fatalf("首笔自动建号应成功：code=%d body=%v", code, resp)
	}
	// 同一 IP 立刻换一个邮箱再建：IP 最小间隔那一档必须与注册同源地拦住。
	second := fmt.Sprintf("iam_otp_%d@example.com", atomic.AddInt64(&iamEmailSeq, 1))
	putLoginCode(second, "666666")
	code, resp := postEmailLogin(t, s, second, "666666", true)
	if code != 429 {
		t.Fatalf("同 IP 连续自动建号应被注册 IP 闸拦住：code=%d body=%v", code, resp)
	}
	if _, err := s.Store.GetUserByEmail(second); err == nil {
		t.Fatalf("被限流的那一单不许留下账号")
	}
}
