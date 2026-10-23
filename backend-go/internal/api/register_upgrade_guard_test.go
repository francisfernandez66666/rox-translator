// ============ register_upgrade_guard_test.go · 职责说明 ============
// 2026-10-23 用户报障回归：「注册后引导完善信息改选企业」即报
// 「注册过于频繁，请 N 秒后再试」（429 RATE_LIMITED）。
//
// 事故链：主站邮箱验证码登录会为未注册邮箱自动建空白个人账号（email_login.go
// createPersonalByEmail，记一次 IP 账）→ 登录后 AI 补全面板改选「企业」时走
// authRegister（handleRegister 的升级通道：JWT 本人 + 同邮箱 + 空白账号）⇒ 而 IP 频率
// 护栏当时挂在 handleRegister 最前面（body 解码之前），对升级通道一视同仁地按下限：
// 最小间隔 60 秒内必撞（注册完就弹补全面板，怎么可能等 60 秒），日限 3 次当天试两轮就满。
//
// 本文件锁四件：
//
//	A) 升级通道在 IP 档**双满**（日限计数打满 + 刚记过间隔）时仍放行（200）；
//	B) 反证一：同一载荷**不带 JWT**（游客注册）照样 429，且不建租户、不落账号；
//	C) 反证二：带 JWT 但账号**不是空白账号**（有口令）照样 429——豁免只认
//	   「本人空白账号」三条件，不是「带了 token 就免检」；
//	D) 升级成功**不推进 IP 账**（guard_day 计数与 guard_int 时间戳都不动）——
//	   一次用户动作不扣两次 IP 账；设备档/平台档照记（占格在 reserveFreeAccount）。
//
// ==========================================================
package api

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"
)

// upgradeGuardFixture 在 f77Fixture 之上补一样东西：把测试 IP 的注册护栏打到**双满**——
// guard_day 计数=3（日限默认值）且 guard_int=now（最小间隔 60s 默认值）。
// 返回夹具、该 IP 与护栏读数函数（供 D 段比对）。
func upgradeGuardFixture(t *testing.T) (*renewFixture, string, func() (int64, int64)) {
	t.Helper()
	f := f77Fixture(t)
	// 测试请求默认 RemoteAddr=192.0.2.1:1234 ⇒ clientIP 恒为 192.0.2.1（与 f77Register 同口径）
	ip := "192.0.2.1"
	for i := 0; i < 3; i++ {
		f.srv.regGuard.record(ip) // 三次成功注册：日限打满 + 间隔戳刷新到 now
	}
	read := func() (int64, int64) {
		var dayCnt int64
		if st, err := f.srv.Store.RateLoad("guard_day", ip); err == nil {
			dayCnt = st.Count
		}
		var intStart int64
		if st, err := f.srv.Store.RateLoad("guard_int", ip); err == nil {
			intStart = st.WindowStart
		}
		return dayCnt, intStart
	}
	return f, ip, read
}

// upgradeBlankUser 造一个「主站自动建号」形态的空白账号：个人租户 + 无口令 +
// profile_setup_required=1（升级通道三条件的库侧形态，逐字对齐 createPersonalByEmail）。
func upgradeBlankUser(t *testing.T, f *renewFixture, email string) *store.User {
	t.Helper()
	ten, err := f.srv.Ten.Create("p_upg_"+email, "upg", "", "{}")
	if err != nil {
		t.Fatalf("建个人租户失败: %v", err)
	}
	u, err := f.srv.Store.CreateUserWithEmail(ten.ID, "upg_"+email, "", "upg", store.RoleTenantAdmin, 0, 0,
		email, time.Now().Format(time.RFC3339), 1)
	if err != nil {
		t.Fatalf("建空白账号失败: %v", err)
	}
	return u
}

// upgradeRegisterBody 企业注册载荷（升级通道与游客注册共用，只靠 Authorization 头区分）。
func upgradeRegisterBody(email string) map[string]any {
	return map[string]any{
		"username": "upger", "password": "Passw0rd!", "type": "enterprise",
		"email": email, "code": "upgco", "name": "升级企业", "agreed": true,
	}
}

// doUpgradeRegister 发一次注册请求；tok 空=游客形态（不带 Authorization 头）。
func doUpgradeRegister(t *testing.T, f *renewFixture, tok, email string) (int, map[string]any) {
	t.Helper()
	b, err := json.Marshal(upgradeRegisterBody(email))
	if err != nil {
		t.Fatalf("编码请求体失败: %v", err)
	}
	r := httptest.NewRequest("POST", "/api/auth/register", bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	f77Mux(f.srv).ServeHTTP(rec, r)
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	return rec.Code, m
}

// TestRegisterUpgradeBypassesIPGuard A)＋D)：IP 档双满时升级通道仍放行，且不推进 IP 账。
func TestRegisterUpgradeBypassesIPGuard(t *testing.T) {
	f, _, read := upgradeGuardFixture(t)
	const email = "upg_a@example.test"
	blank := upgradeBlankUser(t, f, email)
	tok, err := auth.Sign(blank, time.Hour)
	if err != nil {
		t.Fatalf("签 token 失败: %v", err)
	}
	dayBefore, intBefore := read()

	code, m := doUpgradeRegister(t, f, tok, email)
	if code != 200 || m["success"] != true {
		t.Fatalf("IP 档双满时升级通道应放行：code=%d body=%v", code, m)
	}

	// D) IP 账一动不动：日限计数与间隔戳都不推进
	dayAfter, intAfter := read()
	if dayAfter != dayBefore {
		t.Errorf("升级成功不该推进 IP 日账：%d → %d", dayBefore, dayAfter)
	}
	if intAfter != intBefore {
		t.Errorf("升级成功不该刷新 IP 间隔戳：%d → %d", intBefore, intAfter)
	}
	// 空白账号已停用、邮箱由新账号接管（升级通道的既定动作，顺带锁住）
	if u, _ := f.srv.Store.GetUserByEmail(email); u == nil || u.ID == blank.ID {
		t.Errorf("升级后邮箱应接管到新账号（旧空白号 %d 已停用），实得 %+v", blank.ID, u)
	}
}

// TestRegisterUpgradeGuardControls B)＋C)：豁免只认「本人空白账号」，游客与非空白账号照旧 429。
func TestRegisterUpgradeGuardControls(t *testing.T) {
	f, _, _ := upgradeGuardFixture(t)

	// B) 游客形态（不带 JWT）：429 且不建租户、不落账号
	baseTenants := f77TenantCount(t, f)
	code, m := doUpgradeRegister(t, f, "", "upg_b@example.test")
	if code != 429 {
		t.Fatalf("游客注册在 IP 档触顶时应 429，实得 %d body=%v", code, m)
	}
	if msg, _ := m["message"].(string); msg == "" || !bytes.Contains([]byte(msg), []byte("注册过于频繁")) {
		t.Errorf("429 文案应点明注册频率，实得 %q", msg)
	}
	if got := f77TenantCount(t, f); got != baseTenants {
		t.Errorf("被拒的那轮不该建出租户：%d → %d", baseTenants, got)
	}

	// C) 带 JWT 但账号有口令（不是空白账号）：照旧 429
	ten, err := f.srv.Ten.Create("p_upg_c", "upgc", "", "{}")
	if err != nil {
		t.Fatalf("建租户失败: %v", err)
	}
	hash := auth.PasswordHash("Passw0rd!")
	real, err := f.srv.Store.CreateUserWithEmail(ten.ID, "upgc", hash, "upgc", store.RoleTenantAdmin, 0, 0,
		"upg_c@example.test", time.Now().Format(time.RFC3339), 0)
	if err != nil {
		t.Fatalf("建有口令账号失败: %v", err)
	}
	tok, _ := auth.Sign(real, time.Hour)
	code, m = doUpgradeRegister(t, f, tok, "upg_c@example.test")
	if code != 429 {
		t.Fatalf("非空白账号不该吃升级豁免，应 429，实得 %d body=%v", code, m)
	}
}

// TestRegisterIPGuardStillAppliesToFreshRegister 反向自锁：IP 档没被改坏——
// 未触顶时新注册照放行，触顶时照拦（证明 A 段的放行不是"护栏整个失效"）。
func TestRegisterIPGuardStillAppliesToFreshRegister(t *testing.T) {
	f := f77Fixture(t)
	// 默认档：日限 3 / 间隔 60s。不预置计数 ⇒ 第一笔放行
	code, m := doUpgradeRegister(t, f, "", "fresh_1@example.test")
	if code != 200 || m["success"] != true {
		t.Fatalf("未触顶时新注册应放行：code=%d body=%v", code, m)
	}
	// 同一 IP 紧接着第二笔：最小间隔 60s 内 ⇒ 429
	code, m = doUpgradeRegister(t, f, "", "fresh_2@example.test")
	if code != 429 {
		t.Fatalf("间隔内的第二笔新注册应 429，实得 %d body=%v", code, m)
	}
	if msg, _ := m["message"].(string); !bytes.Contains([]byte(msg), []byte("注册过于频繁")) {
		t.Errorf("429 文案应点明注册频率，实得 %q", msg)
	}
}
