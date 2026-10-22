// ============ register_upgrade_test.go · 职责说明 ============
// 2026-10-22 统一登录批：「完善信息」升级注册通道的回归锁（register.go 的 upgradeBlankID）。
//
// 背景：主站邮箱验证码登录会为未注册邮箱自动创建空白个人账号（email_login.go
// createPersonalByEmail：无口令 + profile_setup_required=1）。登录后 AI 补全面板里
// 改选「企业」（新建组织 / 凭邀请码加入）再提交时，邮箱唯一腿原本会 409 拒死——
// 挡住注册的"其他账号"正是调用者本人几分钟前刚自动建的那个空白号。本锁钉五件事：
//
//	A) 放行：带本人空白账号 JWT + 同邮箱 + 企业字段 → 200；新企业账号落库、
//	   旧空白账号被停用（邮箱释放、数据行保留）、审计留痕；
//	B) 游客同邮箱注册（无 JWT）→ 409，与通道上线前逐字节一致；
//	C) 他人 JWT + 该邮箱 → 409，且原账号**不被**停用（不能借道停用别人的号）；
//	D) 非空白账号本人（已设口令）→ 409（升级通道只属于"还没设口令的空白号"）；
//	E) email_verify_enabled=1 时：升级通道不索要 register-purpose 验证码
//	   （归属已由本次登录的 login-purpose OTP 证明）→ 200；同一请求去掉 JWT
//	   仍按老规矩 400 要验证码——证明豁免只长在升级通道上。
//
// ★ 方言由 f77Fixture 自钉 sqlite（AGENTS §一·4）；IP 档每轮放开（同 f77Register）。
// ==========================================================
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// upgBlankAccount 造一个与 createPersonalByEmail 同形的空白个人账号：
// 个人租户 + tenant_admin + 无口令 + profile_setup_required=1 + 已绑邮箱。
func upgBlankAccount(t *testing.T, f *renewFixture, email string) *store.User {
	t.Helper()
	uname := localPartOfEmail(email)
	ten, err := f.srv.Ten.Create("p_upg_"+uname, uname, "", "{}")
	if err != nil {
		t.Fatalf("建个人租户失败: %v", err)
	}
	u, err := f.srv.Store.CreateUserWithEmail(ten.ID, uname, "", uname, store.RoleTenantAdmin, 0, 0,
		email, time.Now().Format(time.RFC3339), 1)
	if err != nil {
		t.Fatalf("建空白账号失败: %v", err)
	}
	if err := f.srv.Ten.SetPersonal(ten.ID, true); err != nil {
		t.Fatalf("个人租户标记失败: %v", err)
	}
	return u
}

// upgToken 给账号签一枚一小时 JWT（模拟补全面板提交时前端带上的登录态）。
func upgToken(t *testing.T, u *store.User) string {
	t.Helper()
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发 JWT 失败: %v", err)
	}
	return tok
}

// upgRegister 以主站 Host 发起一次注册；token 非空时带 Bearer 头。
// 返回状态码与响应体（IP 档每轮放开，避免第二腿起被限流闸换成判据）。
func upgRegister(t *testing.T, f *renewFixture, token, email string, body map[string]any) (int, map[string]any) {
	t.Helper()
	body["email"] = email
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("编码注册请求体失败: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	r.Host = "langcross.lexicorn.cn"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if err := f.srv.Store.SetConfig("register_ip_min_interval_sec", "0"); err != nil {
		t.Fatalf("放开注册间隔失败: %v", err)
	}
	if err := f.srv.Store.SetConfig("register_ip_daily_limit", "200"); err != nil {
		t.Fatalf("放开注册日限失败: %v", err)
	}
	rec := httptest.NewRecorder()
	f77Mux(f.srv).ServeHTTP(rec, r)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析注册响应失败: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, m
}

// upgStatusOf 读某账号当前状态（行不存在返回空串）。
func upgStatusOf(t *testing.T, f *renewFixture, id int64) string {
	t.Helper()
	var st string
	if err := f.srv.Store.DB().QueryRow("SELECT status FROM users WHERE id=?", id).Scan(&st); err != nil {
		return ""
	}
	return st
}

// upgEnterpriseBody 一次「新建企业」注册载荷（企业管理员分支：无邀请码）。
func upgEnterpriseBody(user string) map[string]any {
	return map[string]any{
		"username": user, "password": "Passw0rd!", "type": "enterprise",
		"code": "upg_" + user, "name": user + " 公司", "agreed": true,
	}
}

// TestRegisterUpgradeFromOwnBlankAccount A/E 两段：本人空白账号升级放行 + 验码豁免。
func TestRegisterUpgradeFromOwnBlankAccount(t *testing.T) {
	f := f77Fixture(t)
	blank := upgBlankAccount(t, f, "upg-a@ex.com")
	tok := upgToken(t, blank)

	code, m := upgRegister(t, f, tok, "upg-a@ex.com", upgEnterpriseBody("acme_admin"))
	if code != 200 || m["success"] != true {
		t.Fatalf("升级注册应放行，实得 code=%d body=%v", code, m)
	}
	// 新账号落库且带同一邮箱
	u, _ := m["user"].(map[string]any)
	if u == nil || u["email"] != "upg-a@ex.com" {
		t.Fatalf("响应应带回新账号与同一邮箱，实得 %v", m["user"])
	}
	// 旧空白账号被停用（邮箱释放），行仍保留（数据不删）
	if got := upgStatusOf(t, f, blank.ID); got != store.UserDisabled {
		t.Fatalf("旧空白账号应被停用，实得 status=%q", got)
	}
	// 审计留痕
	var audits int64
	if err := f.srv.Store.DB().QueryRow("SELECT COUNT(*) FROM audit_logs WHERE action='account_upgrade'").Scan(&audits); err != nil {
		t.Fatalf("查审计失败: %v", err)
	}
	if audits == 0 {
		t.Fatal("升级应留 account_upgrade 审计")
	}

	// E) email_verify_enabled=1：升级通道不索要 register 验证码（归属已由登录 OTP 证明）
	if err := f.srv.Store.SetConfig("email_verify_enabled", "1"); err != nil {
		t.Fatalf("开邮箱验证失败: %v", err)
	}
	blank2 := upgBlankAccount(t, f, "upg-e@ex.com")
	code2, m2 := upgRegister(t, f, upgToken(t, blank2), "upg-e@ex.com", upgEnterpriseBody("acme_e"))
	if code2 != 200 || m2["success"] != true {
		t.Fatalf("开验部署下升级通道应豁免验码放行，实得 code=%d body=%v", code2, m2)
	}
	// 反向对照：同一请求去掉 JWT（游客）→ 仍按老规矩 400 要验证码
	blank3 := upgBlankAccount(t, f, "upg-e2@ex.com")
	code3, m3 := upgRegister(t, f, "", "upg-e2@ex.com", upgEnterpriseBody("acme_e2"))
	if code3 != 400 {
		t.Fatalf("游客在开验部署下应被要验证码（400），实得 code=%d body=%v", code3, m3)
	}
	if msg, _ := m3["message"].(string); msg != "请填写邮箱并输入验证码" {
		t.Fatalf("游客拒因应是验码要求，实得 %q", msg)
	}
	// 对照账号未被误伤：仍是启用态
	if got := upgStatusOf(t, f, blank3.ID); got != store.UserActive {
		t.Fatalf("被拒的游客请求不应动账号状态，实得 %q", got)
	}
}

// TestRegisterUpgradeRejectsNonOwners B/C/D 三段：游客、他人、非空白本人一律 409。
func TestRegisterUpgradeRejectsNonOwners(t *testing.T) {
	f := f77Fixture(t)
	blank := upgBlankAccount(t, f, "upg-b@ex.com")

	// B) 游客同邮箱 → 409（与通道上线前一致）
	code, m := upgRegister(t, f, "", "upg-b@ex.com", upgEnterpriseBody("guest_acme"))
	if code != http.StatusConflict {
		t.Fatalf("游客同邮箱应 409，实得 code=%d body=%v", code, m)
	}
	if msg, _ := m["message"].(string); msg != "该邮箱已被其他账号绑定" {
		t.Fatalf("拒因应是邮箱被占用，实得 %q", msg)
	}
	if got := upgStatusOf(t, f, blank.ID); got != store.UserActive {
		t.Fatalf("游客被拒不应停用任何账号，实得 %q", got)
	}

	// C) 他人 JWT + 该邮箱 → 409，且原账号不被停用
	other := upgBlankAccount(t, f, "upg-c@ex.com")
	code2, _ := upgRegister(t, f, upgToken(t, other), "upg-b@ex.com", upgEnterpriseBody("evil_acme"))
	if code2 != http.StatusConflict {
		t.Fatalf("他人 JWT 应 409，实得 code=%d", code2)
	}
	if got := upgStatusOf(t, f, blank.ID); got != store.UserActive {
		t.Fatalf("他人请求不得停用目标账号，实得 %q", got)
	}

	// D) 非空白账号本人（已设口令 + 无待补全标记）→ 409
	ten, err := f.srv.Ten.Create("p_upg_d", "d", "", "{}")
	if err != nil {
		t.Fatalf("建租户失败: %v", err)
	}
	full, err := f.srv.Store.CreateUserWithEmail(ten.ID, "full_user", "hash-x", "full_user", store.RoleTenantAdmin, 0, 0,
		"upg-d@ex.com", time.Now().Format(time.RFC3339), 0)
	if err != nil {
		t.Fatalf("建成员账号失败: %v", err)
	}
	code3, _ := upgRegister(t, f, upgToken(t, full), "upg-d@ex.com", upgEnterpriseBody("full_acme"))
	if code3 != http.StatusConflict {
		t.Fatalf("非空白本人应 409，实得 code=%d", code3)
	}
}
