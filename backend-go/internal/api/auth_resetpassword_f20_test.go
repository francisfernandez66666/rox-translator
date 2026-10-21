// ============================================================================
// api/auth_resetpassword_f20_test.go — 观察2②（2026-09-25 UAT 修复批G）后端半断言
// handleResetPassword 的验证码失败族（验证码缺失或已过期 / 错次超限作废 / 一次性码失配）
// 已从「HTTP 200 + success:false」内联 writeJSON 收口为
// s.writeError(ErrValidation) → **HTTP 400 + 统一错误码 VALIDATION_ERROR**。
// 本测锁三层：
//
//	① 错码→400+code 双锁：每个失败族 HTTP 状态恰为 400，且响应体含
//	  success:false、code:"VALIDATION_ERROR"、message 逐字等于原文案；
//	② 邮箱不在库→404 + EMAIL_NOT_REGISTERED（2026-10-21 用户后令：发码/重置前先查库）；
//	③ 找回密码只认邮箱：只带 username 的报文必须 400，不得悄悄按用户名发码。
//
// 方言：自钉内存 SQLite（AGENTS.md §一·4，经 pinSqliteDialect 统一钉法）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run 'TestUATBatchG_'
// ============================================================================
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"database/sql"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/store"
)

// f20ResetServer 装配重置密码链路最小服务器（Store + 注册护栏；forgot/reset 只用到这两件）。
// 内存库用命名共享缓存（同 f17 的口径）：防并发嵌套查询打到独立空连接。
func f20ResetServer(t *testing.T) *Server {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", "file:f20reset?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	return &Server{Store: st, Cfg: config.C, regGuard: newRegisterGuard(st)}
}

// f20ClearCode 双端清干净某用户的重置码（本地 map + vcode 降级存储），
// 保证每个失败族用例从「无码」初态出发，不串别的用例留下的状态。
func f20ClearCode(uid int64) {
	resetCodes.Lock()
	delete(resetCodes.m, uid)
	resetCodes.Unlock()
	vcodeDel(vcodeResetKey(uid))
}

// f20SeedCode 以指定 resetCode 状态预置某用户的重置码（经 vcodeSet 写入降级存储，
// 与 handleResetPassword 的「Redis 优先读」路径同键，无需再动本地 map）。
func f20SeedCode(t *testing.T, uid int64, rc resetCode) {
	t.Helper()
	b, err := json.Marshal(rc)
	if err != nil {
		t.Fatalf("序列化预置验证码失败: %v", err)
	}
	vcodeSet(vcodeResetKey(uid), b, 10*time.Minute)
	t.Cleanup(func() { f20ClearCode(uid) })
}

// f20CallReset 直调 handleResetPassword，返回录得的响应（来源 IP 用 httptest 默认值）。
func f20CallReset(t *testing.T, s *Server, email, code, newPwd string) *httptest.ResponseRecorder {
	t.Helper()
	return f20CallResetFrom(t, s, "", email, code, newPwd)
}

// f20CallResetFrom 同 f20CallReset，但可指定来源 IP。
// ★ 为什么需要它（IAM P0-6「限流占格时机」改了这条链的口径，2026-10-21）：
// 重置接口的最小间隔是 10s／IP（`reserve("pwd-reset:"+ip, 20, 10)`），而**占格时机从
// "发码成功才记"前移到"放行即占格"**之后，第二条请求（哪怕上一条只是验证码填错回了 400）
// 落在 10s 内就会被 429。那是**接口既有的限流行为、不是本族要验的东西**——本族问的是
// "验证码失败族有没有回到 400 + 统一码 + 原文案"。所以每次换一个来源 IP，让判据只对准失败族；
// 同一 IP 的 10s 拦截另由 TestResetPasswordSameIPMinInterval 正向钉住，两边各管各的。
func f20CallResetFrom(t *testing.T, s *Server, ip, email, code, newPwd string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"email": email, "code": code, "new_password": newPwd})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if ip != "" {
		r.RemoteAddr = ip + ":40000"
	}
	w := httptest.NewRecorder()
	s.handleResetPassword(w, r)
	return w
}

// f20AssertValidation400 等值锁：状态码恰 400 + success:false + code:VALIDATION_ERROR + 文案逐字相等。
func f20AssertValidation400(t *testing.T, w *httptest.ResponseRecorder, wantMsg, scene string) {
	t.Helper()
	if w.Code != 400 {
		t.Fatalf("%s：应回 HTTP 400，实际 %d（body=%s）", scene, w.Code, w.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("%s：错误响应非 JSON: %s", scene, w.Body.String())
	}
	if out["success"] != false {
		t.Fatalf("%s：success 应为 false，实际 %v", scene, out["success"])
	}
	if out["code"] != "VALIDATION_ERROR" {
		t.Fatalf("%s：应带统一错误码 VALIDATION_ERROR，实际 %v", scene, out["code"])
	}
	if out["message"] != wantMsg {
		t.Fatalf("%s：文案必须逐字保留（i18n catalog 锚点），期望 %q 实际 %q", scene, wantMsg, out["message"])
	}
}

// TestUATBatchG_ResetPasswordFailFamily400 四类失败族的「400 + code + 原文案」三锁。
func TestUATBatchG_ResetPasswordFailFamily400(t *testing.T) {
	s := f20ResetServer(t)
	u, err := s.Store.CreateUser(1, "f20_reset_user", auth.PasswordHash("pw123456"), "批G重置账号", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("预置用户失败: %v", err)
	}
	const mail = "f20-reset-family@example.com"
	if err := s.Store.SetUserEmail(u.ID, u.TenantID, mail); err != nil {
		t.Fatalf("绑定邮箱失败: %v", err)
	}

	// 失败族①：邮箱不在库 → 明确拦截（2026-10-21 用户后令，不再回恒真的验证码错误）
	f20ClearCode(u.ID)
	ghostW := f20CallResetFrom(t, s, "198.51.100.11", "f20_ghost_not_exists@example.com", "123456", "newpass123")
	if ghostW.Code != http.StatusNotFound || !strings.Contains(ghostW.Body.String(), "EMAIL_NOT_REGISTERED") {
		t.Fatalf("失败族①邮箱不存在：应回 404+EMAIL_NOT_REGISTERED，实际 code=%d body=%s", ghostW.Code, ghostW.Body.String())
	}

	// 失败族①补锁：真实用户但从未发码（同样落 u!=nil→无码路径的「验证码缺失」形态）
	f20AssertValidation400(t, f20CallResetFrom(t, s, "198.51.100.12", mail, "123456", "newpass123"),
		"验证码错误或已过期", "失败族①b无码用户")

	// 失败族②：验证码已过期（ExpiresAt 在过去）
	f20SeedCode(t, u.ID, resetCode{Code: "654321", ExpiresAt: time.Now().Add(-time.Minute)})
	f20AssertValidation400(t, f20CallResetFrom(t, s, "198.51.100.13", mail, "654321", "newpass123"),
		"验证码错误或已过期", "失败族②已过期")

	// 失败族③：错误尝试超限作废（Attempts ≥ resetCodeMaxTries，即使答对也不放行）
	f20SeedCode(t, u.ID, resetCode{Code: "111222", ExpiresAt: time.Now().Add(10 * time.Minute), Attempts: resetCodeMaxTries})
	f20AssertValidation400(t, f20CallResetFrom(t, s, "198.51.100.14", mail, "111222", "newpass123"),
		"验证码错误次数过多，请重新获取", "失败族③超限作废")
	// 超限分支应已销毁该码：双端（降级存储 + 本地 map）都不应再残留
	if _, ok := vcodeGet(vcodeResetKey(u.ID)); ok {
		t.Fatalf("失败族③：超限作废后重置码应被删除，降级存储仍可读回")
	}
	resetCodes.Lock()
	_, mapLeak := resetCodes.m[u.ID]
	resetCodes.Unlock()
	if mapLeak {
		t.Fatalf("失败族③：超限作废后本地 map 仍残留该用户条目")
	}

	// 失败族④：一次性码失配（正确码在场、提交错码）→ 400 + 错计 +1 的旧语义不回退
	f20SeedCode(t, u.ID, resetCode{Code: "334455", ExpiresAt: time.Now().Add(10 * time.Minute)})
	f20AssertValidation400(t, f20CallResetFrom(t, s, "198.51.100.15", mail, "000000", "newpass123"),
		"验证码错误或已过期", "失败族④码失配")
	if b, ok := vcodeGet(vcodeResetKey(u.ID)); ok {
		var rc resetCode
		if json.Unmarshal(b, &rc) == nil && rc.Attempts != 1 {
			t.Fatalf("失败族④：失配后应回写 Attempts=1，实际 %d", rc.Attempts)
		}
	} else {
		t.Fatalf("失败族④：失配错计后重置码应仍在场（未超限不作废）")
	}
}

// f20CallResetEmail 按邮箱唯一入口发重置报文（2026-10-21 起不再接受 username）。
func f20CallResetEmail(t *testing.T, s *Server, email, code, newPwd string) *httptest.ResponseRecorder {
	t.Helper()
	return f20CallResetEmailFrom(t, s, "", email, code, newPwd)
}

// f20CallResetEmailFrom 同 f20CallResetEmail，可指定来源 IP（理由见 f20CallResetFrom）。
func f20CallResetEmailFrom(t *testing.T, s *Server, ip, email, code, newPwd string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(map[string]string{"email": email, "code": code, "new_password": newPwd})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if ip != "" {
		r.RemoteAddr = ip + ":40000"
	}
	w := httptest.NewRecorder()
	s.handleResetPassword(w, r)
	return w
}

// TestResetPasswordByEmailLocatesAccount 只用绑定邮箱找回的用户必须能重置。
// 复现的缺陷：handleResetPassword 旧形态只认 username，邮箱档报文 username 为空
// ⇒ 直接落「请求格式错误」分支，用户刚收到验证码却重置不了（现网报障原始报文）。
// 本测同时反向钉住「email 为空必须判格式错误」，防止把必填校验一起放宽。
func TestResetPasswordByEmailLocatesAccount(t *testing.T) {
	s := f20ResetServer(t)
	u, err := s.Store.CreateUser(1, "f20_email_user", auth.PasswordHash("pw123456"), "邮箱找回账号", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("预置用户失败: %v", err)
	}
	const mail = "f20-reset@example.com"
	if err := s.Store.SetUserEmail(u.ID, u.TenantID, mail); err != nil {
		t.Fatalf("绑定邮箱失败: %v", err)
	}

	// 反向对照：email 为空 ⇒ 仍必须是格式错误
	raw, _ := json.Marshal(map[string]string{"username": "", "email": "", "code": "123456", "new_password": "newpass123"})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = "198.51.100.21:40000" // 与下一条换 IP：10s 最小间隔不参与本族判据（见 f20CallResetFrom 注释）
	w := httptest.NewRecorder()
	s.handleResetPassword(w, r)
	if w.Code != 400 {
		t.Fatalf("空定位腿应回 400 格式错误，实际 %d（body=%s）", w.Code, w.Body.String())
	}

	// 正向：只用邮箱定位 + 正确码 ⇒ 200 + success:true，且码被消费
	f20SeedCode(t, u.ID, resetCode{Code: "778899", ExpiresAt: time.Now().Add(10 * time.Minute)})
	w2 := f20CallResetEmailFrom(t, s, "198.51.100.22", mail, "778899", "newpass456")
	if w2.Code != 200 {
		t.Fatalf("邮箱定位重置应回 200，实际 %d（body=%s）", w2.Code, w2.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w2.Body.Bytes(), &out); err != nil || out["success"] != true {
		t.Fatalf("邮箱定位重置应 success:true，body=%s err=%v", w2.Body.String(), err)
	}
	if _, ok := vcodeGet(vcodeResetKey(u.ID)); ok {
		t.Fatalf("重置成功后验证码应被消费，实际仍在场")
	}
}

// TestUATBatchG_ForgotRequiresRegisteredEmail ★ 2026-10-21 用户后令：
// 找回密码只按注册邮箱定位，发码前必须先查库；邮箱不在库必须明确拦截，不能回假成功。
// 反证：把 handleForgotPassword 里的 GetUserByEmail 判空摘掉，本锁第一段立刻红。
func TestUATBatchG_ForgotRequiresRegisteredEmail(t *testing.T) {
	s := f20ResetServer(t)
	u, err := s.Store.CreateUser(1, "f20_forgot_user", auth.PasswordHash("pw123456"), "找回邮箱账号", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("预置用户失败: %v", err)
	}
	const mail = "f20-forgot@example.com"
	if err := s.Store.SetUserEmail(u.ID, u.TenantID, mail); err != nil {
		t.Fatalf("绑定邮箱失败: %v", err)
	}

	ghost := f20CallForgot(t, s, "203.0.113.111", map[string]string{"email": "f20-ghost@example.com"})
	if ghost.Code != http.StatusNotFound {
		t.Fatalf("未注册邮箱应回 404，实际 %d（body=%s）", ghost.Code, ghost.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(ghost.Body.Bytes(), &out); err != nil {
		t.Fatalf("forgot 响应非 JSON: %s", ghost.Body.String())
	}
	if out["code"] != "EMAIL_NOT_REGISTERED" {
		t.Fatalf("未注册邮箱应带 EMAIL_NOT_REGISTERED，实际 %v", out["code"])
	}

	ok := f20CallForgot(t, s, "203.0.113.112", map[string]string{"email": mail})
	if ok.Code != 200 {
		t.Fatalf("已注册邮箱应回 200，实际 %d（body=%s）", ok.Code, ok.Body.String())
	}
	if err := json.Unmarshal(ok.Body.Bytes(), &out); err != nil || out["message"] != forgotSentMsg {
		t.Fatalf("已注册邮箱文案必须逐字为 forgotSentMsg，body=%s err=%v", ok.Body.String(), err)
	}

	// 用户名入口已关闭：只带 username 的报文必须被判格式错误，而不是悄悄查用户名发码。
	byName := f20CallForgot(t, s, "203.0.113.113", map[string]string{"username": "f20_forgot_user"})
	if byName.Code != 400 {
		t.Fatalf("只带用户名应回 400（邮箱为唯一入口），实际 %d（body=%s）", byName.Code, byName.Body.String())
	}
}

// f20CallForgot 直调 handleForgotPassword（可指定来源 IP；两条报文走不同 IP 是**刻意的**：
// forgot 的最小间隔是 30s／IP，同一 IP 连打第二枪会被 429 挡掉，那与"账号是否存在"无关，
// 见 reserve 注释与 TestResetPasswordSameIPMinInterval）。
func f20CallForgot(t *testing.T, s *Server, ip string, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(http.MethodPost, "/api/auth/forgot-password", bytes.NewReader(raw))
	r.Header.Set("Content-Type", "application/json")
	if ip != "" {
		r.RemoteAddr = ip + ":40000"
	}
	w := httptest.NewRecorder()
	s.handleForgotPassword(w, r)
	return w
}

// TestResetPasswordSameIPMinInterval ★ IAM P0-6「限流占格时机」的正向锁（2026-10-21）：
// reserve 把「占格」前移到**放行那一刻**，于是即便上一条请求因验证码错误回了 400，
// 它也已经占掉当日一格、并推进了最小间隔。本锁钉住三件事：
//
//	① 同一 IP 10s 内的第二枪 = 429 + retry_after ∈ (0,10]（最小间隔仍在，且时长诚实）；
//	② 换一个 IP 仍放行（限流是**按 IP** 的，不是全站一刀切）；
//	③ 打**不存在的邮箱**同样占格（额度只表达请求次数，判据直接读库里那一格，不看响应体）。
//
// 反证：把 handleResetPassword 的 reserve 换回 allow+record（只在成功腿计数），③ 立刻红；
// 摘掉最小间隔判断，① 立刻红。
func TestResetPasswordSameIPMinInterval(t *testing.T) {
	s := f20ResetServer(t)
	const ip = "203.0.113.61"

	first := f20CallResetFrom(t, s, ip, "f20_ghost_quota@example.com", "000000", "newpass123")
	if first.Code != http.StatusNotFound {
		t.Fatalf("第一枪（不存在的邮箱 + 错码）应回 404，实际 %d（body=%s）", first.Code, first.Body.String())
	}

	second := f20CallResetFrom(t, s, ip, "f20_ghost_quota@example.com", "000000", "newpass123")
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("同 IP 10s 内第二枪应被 429 拦下（最小间隔 10s），实际 %d（body=%s）", second.Code, second.Body.String())
	}
	var out map[string]interface{}
	if err := json.Unmarshal(second.Body.Bytes(), &out); err != nil {
		t.Fatalf("429 响应非 JSON: %s", second.Body.String())
	}
	if out["code"] != "RATE_LIMITED" {
		t.Fatalf("429 应带 code=RATE_LIMITED，实际 %v", out["code"])
	}
	wait, _ := out["retry_after"].(float64)
	if wait <= 0 || wait > 10 {
		t.Fatalf("retry_after 应落在 (0,10]，实际 %v（限流时长必须诚实，客户端据此退避）", out["retry_after"])
	}

	// ② 换 IP 放行：证明上面那 429 是"这个 IP 太密"，不是"接口坏了"。
	third := f20CallResetFrom(t, s, "203.0.113.62", "f20_ghost_quota@example.com", "000000", "newpass123")
	if third.Code != http.StatusNotFound {
		t.Fatalf("换一个 IP 应回到业务分支（404 邮箱不存在），实际 %d（body=%s）", third.Code, third.Body.String())
	}

	// ③ 占格与邮箱是否存在无关：第一枪打的邮箱根本不存在，但那一格必须已经记在库里。
	// ⚠️ 库里的键是**调用方传进来的整串**（`"pwd-reset:"+ip`，见 handleResetPassword），
	//    不是裸 IP——拿裸 IP 读会得到 count=0，那读起来像"没占格"，实际是查错了那一行
	//    （同 AGENTS §一·3「探针写的键必须先问产品读哪一把」）。
	stDay, err := s.Store.RateLoad("guard_day", "pwd-reset:"+ip)
	if err != nil {
		t.Fatalf("读取日额度计数失败: %v", err)
	}
	if stDay.Count < 1 {
		t.Fatalf("不存在的账号没有占格（count=%d）⇒ 攻击者可据「额度是否被消耗」枚举账号存在性", stDay.Count)
	}
}
