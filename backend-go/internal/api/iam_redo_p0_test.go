// ============================================================================
// iam_redo_p0_test.go — ★ 2026-10-21 IAM 重做 P0 的机械锁（用户令「注册/登录/重置密码体系重做」）
//
// 这一批锁钉的是"三条面各写一套口径"这一族缺陷，每条都对应现网一条真实报障：
//
//	① 注册成功后登不进去   ← 注册不发会话 + 登录只认用户名 + 注册 trim 口令而登录不 trim
//	                         + 失败原因写在另一屏的报错位上
//	② 重置密码后登不进去   ← 邮箱档不回填标识，而登录不认邮箱（现走邮箱重置+自动登录）
//	③ 重置不校验邮箱对不对 ← 2026-10-21 用户后令：找回/重置只按注册邮箱，发码前先查库，
//	                         邮箱不在库明确拦截；登录口仍保留统一文案。
//
// 方言：自钉内存 SQLite（AGENTS §一·4，经 f77Fixture / pinSqliteDialect 统一钉法）。
// 运行：go test -count=1 ./internal/api/ -run 'TestIAMRedoP0'
// ============================================================================
package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/config"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"
)

// P0-1：账号归一化与口令强度**只有一把尺子**（纯函数层，先钉形状再钉接线）。
// 反证口径：把 iam.NormalizeAccount 改回"什么都不做"，P0-3 那条带空格口令的用例必红；
// 把 iam.ValidatePassword 改成 `p != ""`，本条第 4 段必红。
func TestIAMRedoP0_01_AccountAndPasswordRulers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  demo_user ", "demo_user"},          // 用户名：只 trim，大小写原样（库里存什么就查什么）
		{"Foo@Example.COM", "foo@example.com"}, // 邮箱：trim + 小写（库里 GetUserByEmail 一直这个口径）
		{"张三 ", "张三"},
	}
	for _, c := range cases {
		if got := auth.NormalizeAccount(c.in); got != c.want {
			t.Fatalf("NormalizeAccount(%q)=%q 期望 %q", c.in, got, c.want)
		}
	}
	// 口令**永远不进归一化**：这一条是缺陷①的一半（注册削空格、登录不削 ⇒ 同一个人口令两个值）
	if auth.NormalizeAccount(" pass word ") != "pass word" {
		t.Fatalf("归一化越界改了非账号字段")
	}
	if iam.ValidatePassword(strings.Repeat("a", iam.PasswordMinLen-1)) {
		t.Fatalf("弱口令被判合格：强度尺失效")
	}
	if !iam.ValidatePassword(strings.Repeat("a", iam.PasswordMinLen)) {
		t.Fatalf("恰好达标的口令被判不合格")
	}
	if iam.ValidatePassword("") {
		t.Fatalf("空口令被判合格")
	}
	// ★ 2026-10-22 一致性核查补漏：方案 §三·7 要求「≥6 **且非纯空白**」。
	//   修前只判 `p == ""`，下面三种"长度够但全是空白"的口令全部被判合格
	//   （实测 6 空格/6 全角空格/6 制表符均 true）——反证：把 TrimSpace 那行删掉，本段必红。
	for _, blank := range []string{
		strings.Repeat(" ", iam.PasswordMinLen),      // 半角空格
		strings.Repeat("\u3000", iam.PasswordMinLen), // 全角空格（中文输入法常见）
		strings.Repeat("\t", iam.PasswordMinLen),     // 制表符
	} {
		if iam.ValidatePassword(blank) {
			t.Fatalf("纯空白口令被判合格：%q", blank)
		}
	}
	// 反向对照：trim 只用于**判定**，不许改写口令本身——带空格的合法口令必须照样通过。
	if !iam.ValidatePassword(" pass word ") {
		t.Fatalf("含空格的合法口令被误杀（trim 越界改写了内容）")
	}
	// 形状判据：只用来选查询腿，判错方向的代价是"查不到"，不是"放行坏账号"
	for bad, want := range map[string]bool{"a@b": false, "a@b.c": false, "a@b.co": true, "plain": false, "a b@c.de": false} {
		if got := iam.LooksLikeEmail(bad); got != want {
			t.Fatalf("LooksLikeEmail(%q)=%v 期望 %v", bad, got, want)
		}
	}
}

// p0Register 走真实 handleRegister 建一个个人用户，返回状态码、响应体与错误说明。
func p0Register(t *testing.T, s *Server, username, password, email string) (int, map[string]any) {
	t.Helper()
	// 注册限流两档放开：同一测试 IP 连打会先撞"最小间隔"，那会把判据换成限流闸（假红）
	if err := s.Store.SetConfig("register_ip_min_interval_sec", "0"); err != nil {
		t.Fatalf("放开注册间隔失败: %v", err)
	}
	if err := s.Store.SetConfig("register_ip_daily_limit", "200"); err != nil {
		t.Fatalf("放开注册日限失败: %v", err)
	}
	body, _ := json.Marshal(map[string]any{
		"username": username, "password": password, "email": email,
		"type": "personal", "agreed": true,
	})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/register", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleRegister(w, r)
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("注册响应非 JSON: %s", w.Body.String())
	}
	return w.Code, m
}

// p0Login 走真实 handleLogin；账号字段可以是用户名**或**邮箱（P0-4 双入口）。
func p0Login(t *testing.T, s *Server, account, password string) (int, map[string]any) {
	t.Helper()
	// 夹具只装了 Store/Ten/regGuard，登录口自己还要一枚失败计数器；nil 会在成功分支的
	// clearLoginFails 上直接 panic（首跑真踩）——这里补内存回退那一档，判据一字不改。
	if s.loginLimit == nil {
		s.loginLimit = newLoginLimiter(s.Store)
	}
	body, _ := json.Marshal(map[string]string{"username": account, "password": password})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	var m map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &m)
	return w.Code, m
}

// P0-4＋P0-2：注册必须**直接下发会话**，且那一个会话要能被消费方读出值来
// （AGENTS §三「判『生效』必须由那一个消费方自己读出值」——只看响应里有 token 字样不算）。
func TestIAMRedoP0_04_RegisterIssuesUsableSession(t *testing.T) {
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	code, m := p0Register(t, s, "p0_reg_a", "p0pass123", "p0_a@example.com")
	if code != 200 || m["success"] != true {
		t.Fatalf("注册应 200+success，实得 code=%d body=%v", code, m)
	}
	tok, _ := m["token"].(string)
	if tok == "" {
		t.Fatalf("注册响应没带会话（旧形态只回 success，前端再拿明文口令回登一次 ⇒ 报障①）：%v", m)
	}
	// 消费方读数：拿这个 token 打 /api/auth/me，必须回同一个用户名
	r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.handleMe(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "p0_reg_a") {
		t.Fatalf("注册下发的会话不可用：code=%d body=%s", w.Code, w.Body.String())
	}
}

// P0-1＋P0-4：三条"注册进得去、登录登不进"的路径必须全部走通。
// 这三段各自对应一个历史缺陷形态，摘掉任意一处修法就有一段红。
func TestIAMRedoP0_03_LoginAcceptsEmailCaseAndSpacedPassword(t *testing.T) {
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	if _, m := p0Register(t, s, "p0_reg_b", "p0pass456", "P0_B@example.com"); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	// ① 邮箱登录（旧形态只按用户名查 ⇒ 注册填邮箱的人补登必失败）
	if code, m := p0Login(t, s, "p0_b@example.com", "p0pass456"); code != 200 {
		t.Fatalf("邮箱登录失败：code=%d body=%v（P0-4 双入口失效）", code, m)
	}
	// ② 用户名大小写不敏感（唯一命中那一档）
	if code, m := p0Login(t, s, "P0_REG_B", "p0pass456"); code != 200 {
		t.Fatalf("大小写不敏感登录失败：code=%d body=%v", code, m)
	}
	// ③ 带首尾空格的口令：注册与登录必须认**同一个值**
	//    （旧形态注册 TrimSpace 入库、登录原样比对 ⇒ 这一腿结构性必 401）
	if _, m := p0Register(t, s, "p0_reg_c", "  spaced007  ", "p0_c@example.com"); m["success"] != true {
		t.Fatalf("带空格口令的注册应被接受（空格是口令的一部分）: %v", m)
	}
	if code, m := p0Login(t, s, "p0_reg_c", "  spaced007  "); code != 200 {
		t.Fatalf("带空格口令注册后登不进去：code=%d body=%v（归一化又分叉了）", code, m)
	}
	// 反向对照：不带空格的**另一个**口令不许被当成同一个（否则说明登录侧在偷偷 trim）
	if code, _ := p0Login(t, s, "p0_reg_c", "spaced007"); code == 200 {
		t.Fatalf("登录侧在做 trim：去掉首尾空格的口令被判成了同一个账号的同一个口令")
	}
}

// P0-6（2026-10-21 IAM 重做后）：公开找回/重置端点必须整体下线，不能退成"可路由但无 UI"。
func TestIAMRedoP0_06_PublicPasswordRecoveryIsGone(t *testing.T) {
	fx := f77Fixture(t)
	s := fx.srv
	for _, path := range []string{"/api/auth/forgot-password", "/api/auth/reset-password"} {
		r := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()
		s.handlePasswordRecoveryGone(w, r)
		if w.Code != http.StatusGone {
			t.Fatalf("%s 必须返回 410，实得 code=%d body=%s", path, w.Code, w.Body.String())
		}
	}
}

// P0-3：改密链的新密码强度必须在**服务端**判；验证码可替代原密码，让仅持 OTP 的账号也能自助设置。
func TestIAMRedoP0_05_ChangePasswordUsesVerifiedEmailCode(t *testing.T) {
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	const email = "p0_change_password@example.com"
	if _, m := p0Register(t, s, "p0_change_pwd", "oldpass123", email); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	u, err := s.Store.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("读账号失败: %v", err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发改密会话失败: %v", err)
	}

	// 反证：未登录不得借 email-code 接口向任意邮箱发"改密码"。
	anonBody, _ := json.Marshal(map[string]string{"email": "victim@example.com", "purpose": emailPurposePassword})
	anonReq := httptest.NewRequest(http.MethodPost, "/api/auth/email-code", bytes.NewReader(anonBody))
	anonReq.RemoteAddr = "203.0.113.31:54321"
	anonW := httptest.NewRecorder()
	s.handleEmailCode(anonW, anonReq)
	if anonW.Code != http.StatusUnauthorized {
		t.Fatalf("匿名发改密码必须 401，实得 code=%d body=%s", anonW.Code, anonW.Body.String())
	}

	// 已登录：请求体里的邮箱不是收件地址，服务端必须改发当前账号绑定邮箱。
	sendBody, _ := json.Marshal(map[string]string{"email": "attacker@example.com", "purpose": emailPurposePassword})
	sendReq := httptest.NewRequest(http.MethodPost, "/api/auth/email-code", bytes.NewReader(sendBody))
	sendReq.Header.Set("Authorization", "Bearer "+tok)
	sendReq.RemoteAddr = "203.0.113.32:54321"
	sendW := httptest.NewRecorder()
	s.handleEmailCode(sendW, sendReq)
	if sendW.Code != http.StatusOK {
		t.Fatalf("已登录发改密码应成功，实得 code=%d body=%s", sendW.Code, sendW.Body.String())
	}
	emailCodes.mu.Lock()
	sent := emailCodes.codes[emailCodeKey(email, emailPurposePassword)]
	var sentCode string
	if sent != nil {
		sentCode = sent.Code
	}
	emailCodes.mu.Unlock()
	if sentCode == "" {
		t.Fatalf("改密码未写入当前账号邮箱（请求体邮箱不得决定收件地址）")
	}

	callChangePassword := func(body map[string]string) (int, string) {
		t.Helper()
		payload, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleChangePassword(w, r)
		return w.Code, w.Body.String()
	}
	if code, body := callChangePassword(map[string]string{"new_password": "123", "email_code": sentCode}); code != http.StatusBadRequest || !strings.Contains(body, auth.PasswordTooShortMsg) {
		t.Fatalf("弱口令必须被服务端拒掉，实得 code=%d body=%s", code, body)
	}
	// 原密码为空时，刚验过的邮箱码必须可替代原密码；同时旧密码仍不得继续有效。
	if code, body := callChangePassword(map[string]string{"new_password": "newpass123", "email_code": sentCode}); code != http.StatusOK || !strings.Contains(body, `"success":true`) {
		t.Fatalf("有效邮箱码改密应成功，实得 code=%d body=%s", code, body)
	}
	if code, m := p0Login(t, s, email, "oldpass123"); code == http.StatusOK {
		t.Fatalf("旧密码仍有效: code=%d body=%v", code, m)
	}
	if code, m := p0Login(t, s, email, "newpass123"); code != http.StatusOK {
		t.Fatalf("新密码应可登录: code=%d body=%v", code, m)
	}

	// 反向对照：没有验证码且原密码错误时，不能把"已登录"当成绕过原口令的许可。
	reloaded, err := s.Store.GetUserByEmail(email)
	if err != nil || reloaded == nil {
		t.Fatalf("读改密后账号失败: %v", err)
	}
	freshTok, _ := auth.Sign(reloaded, time.Hour)
	payload, _ := json.Marshal(map[string]string{"old_password": "wrongpass123", "new_password": "malicious123"})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(payload))
	r.Header.Set("Authorization", "Bearer "+freshTok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChangePassword(w, r)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "原密码错误") {
		t.Fatalf("无验证码时原密码错误必须拒绝，实得 code=%d body=%s", w.Code, w.Body.String())
	}
}

// P0-11：租户停用／到期必须在**登录口**就拦住，且判定顺序是「先口令、后租户」。
//
// 两件事各有各的反证：
//   - 若把租户判定整段摘掉：第 ②④ 段从 403 变成 200（企业被停用后成员照样进后台，
//     进去之后每一跳都失败——旧形态就长这样）；
//   - 若把租户判定挪到口令校验**之前**：第 ③ 段会从"401 用户名或密码错误"变成
//     "403 该企业账号已停用"——等于给任何知道用户名的人一条免费的租户存在性回执，
//     与 P0-6 刚收敛的防枚举口径正面矛盾（这一条正是顺序锁的射程，不是多余的一腿）。
func TestIAMRedoP0_11_TenantStatusBlocksLogin(t *testing.T) {
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	if _, m := p0Register(t, s, "p0_tenant_a", "p0pass111", "p0_ta@example.com"); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	// 注册建出来的个人租户：从用户行反查，别猜 ID（f77Fixture 已经先占了宿主租户）
	us, err := s.Store.GetUserByUsernameGlobal("p0_tenant_a")
	if err != nil || len(us) != 1 {
		t.Fatalf("反查注册账号失败: %v len=%d", err, len(us))
	}
	tid := us[0].TenantID
	if tid <= 0 {
		t.Fatalf("注册账号没有落到租户里（tid=%d）", tid)
	}
	// ① 基线：租户正常时登得进去（否则后面三条红只是"这个人本来就登不进去"的假红）
	if code, m := p0Login(t, s, "p0_tenant_a", "p0pass111"); code != 200 {
		t.Fatalf("基线登录失败：code=%d body=%v", code, m)
	}
	// ② 手动停用 → 403 且理由点到「企业已停用」
	if err := s.Ten.SetStatus(tid, tenant.StatusDisabled); err != nil {
		t.Fatalf("停用租户失败: %v", err)
	}
	if code, m := p0Login(t, s, "p0_tenant_a", "p0pass111"); code != http.StatusForbidden || !strings.Contains(mustJSON(m), "该企业账号已停用") {
		t.Fatalf("停用租户的成员必须 403「该企业账号已停用」，实得 code=%d body=%v", code, m)
	}
	// ③ 顺序锁：口令都不对时只许回通用凭证错，不许泄露租户状态
	if code, m := p0Login(t, s, "p0_tenant_a", "wrongpass"); code != http.StatusNotFound || strings.Contains(mustJSON(m), "该企业") {
		t.Fatalf("租户判定跑到了口令校验之前（错误口令也应只回 401 通用文案）：code=%d body=%v", code, m)
	}
	// ④ 到期 → 403 且理由点到「服务已到期」（把 expires_at 改成过去，状态由 effectiveStatus 现算）
	if err := s.Ten.SetStatus(tid, tenant.StatusActive); err != nil {
		t.Fatalf("恢复租户状态失败: %v", err)
	}
	if err := s.Ten.Update(tid, "到期租户", time.Now().Add(-24*time.Hour).Format(time.RFC3339), "{}"); err != nil {
		t.Fatalf("写入过期时间失败: %v", err)
	}
	if code, m := p0Login(t, s, "p0_tenant_a", "p0pass111"); code != http.StatusForbidden || !strings.Contains(mustJSON(m), "该企业服务已到期") {
		t.Fatalf("到期租户的成员必须 403「该企业服务已到期」，实得 code=%d body=%v", code, m)
	}
	// ⑤ 反向对照：续期后同一个账号必须立刻登得进去（证明上面两档是租户状态造成的，不是账号被写坏了）
	if err := s.Ten.Update(tid, "续期租户", time.Now().Add(24*time.Hour).Format(time.RFC3339), "{}"); err != nil {
		t.Fatalf("续期失败: %v", err)
	}
	if code, m := p0Login(t, s, "p0_tenant_a", "p0pass111"); code != 200 {
		t.Fatalf("续期后应恢复登录：code=%d body=%v", code, m)
	}
}

// mustJSON 把响应体 map 折成字符串，只给判据里的 Contains 用（失败信息原样带上整段 body）。
func mustJSON(m map[string]any) string {
	b, _ := json.Marshal(m)
	return string(b)
}

// P0-12：管理台改账号状态的入口必须有白名单。
//
// 缺陷形态不是"某个值写错"，而是**接受任意字符串**：写进 `desabled` 这类拼错值不会报错，
// 只会造出一个「能拿到 token、下一个请求就 401」的账号（登录口只拦 disabled／中间件拦
// 非 active 且非 deactivating／EffectiveUserStatus 再算一次，三把尺子都不认识那个值）。
// 反证：把白名单那一段摘掉，第 ① 段（非法值）从 400 变成 200，其余三段仍绿——
// 这正是"只测合法值"的锁会漏掉的那一半。
func TestIAMRedoP0_12_AdminStatusWhitelist(t *testing.T) {
	s, tid, tok, _ := newF31Probe(t)
	target, err := s.Store.CreateUser(tid, "p0_status_target", auth.PasswordHash("pw123456"), "状态靶子", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatalf("预置目标账号失败: %v", err)
	}
	call := func(status string) *httptest.ResponseRecorder {
		body := map[string]interface{}{"id": target.ID}
		if status != "" {
			body["status"] = status
		}
		return callUserUpdate(t, s, tok, body)
	}
	readStatus := func() string {
		u, e := s.Store.GetUser(target.ID, tid)
		if e != nil {
			t.Fatalf("回读账号失败: %v", e)
		}
		return u.Status
	}
	// ① 非法值一律 400 结构化错误，且**库里一个字节都不许动**
	for _, bad := range []string{"desabled", "paused", "ACTIVE"} {
		rec := call(bad)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "VALIDATION_ERROR") {
			t.Fatalf("非法状态 %q 必须 400+VALIDATION_ERROR，实得 %d: %s", bad, rec.Code, rec.Body.String())
		}
		if got := readStatus(); got != store.UserActive {
			t.Fatalf("被拒的状态写入落库了：status=%q（旧形态会把任意字符串直写进去）", got)
		}
	}
	// ② 三个合法值必须照常写得动（白名单不是把入口关死）
	for _, good := range []string{store.UserDisabled, store.UserDeactivating, store.UserActive} {
		rec := call(good)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"success":true`) {
			t.Fatalf("合法状态 %q 应成功，实得 %d: %s", good, rec.Code, rec.Body.String())
		}
		if got := readStatus(); got != good {
			t.Fatalf("合法状态 %q 没落库，实得 %q", good, got)
		}
	}
	// ③ 缺席＝不动（F-35 合并语义仍然有效，别让白名单把"只改别的字段"的请求打回）
	rec := call("")
	if rec.Code != http.StatusOK {
		t.Fatalf("不带 status 的更新应成功，实得 %d: %s", rec.Code, rec.Body.String())
	}
	if got := readStatus(); got != store.UserActive {
		t.Fatalf("缺席 status 时不应改动现值，实得 %q", got)
	}
}

// ============================================================================
// P0-3（方案 §六 ⑤）：「后端强度闸四处同源（一处放宽即红）」——补的是那条**当时缺位**的锁。
//
// 为什么要有这一条：2026-10-22 一致性核查发现 `handleAdminUserCreate` 只判
// `req.Password == ""`，5 位密码能直接建号，而**当时没有任何用例会因此变红**——
// P0-01 只锁了 `iam.ValidatePassword` 这个纯函数本身的行为，锁不住"哪个写口忘了调它"。
// 于是"四处同源"在文档里是承诺、在代码里是巧合。
//
// 做法取与 errorstyle_gate_test.go 同一手法（AST 静态扫描）：不建库、不起服务，
// 只问一句"这五个 handler 的函数体里到底有没有 ValidatePassword"。
// 行为面的正确性由 P0-01（纯函数）＋各写口自己的 400 用例承担，两者互补：
//
//	· 本锁防「接线漏了」——删任一处判断即红；
//	· P0-01 防「尺子本身被改宽」——把 TrimSpace 那行删掉即红。
//
// 反证口径：删掉任一 handler 里的 `auth.ValidatePassword` 判断，本用例点名报红。
// ============================================================================
func TestIAMRedoP0_03b_PasswordStrengthGateIsWiredIntoEveryWritePath(t *testing.T) {
	// handler → 所在文件。新增口令写口必须同时登记到这里，否则"同源"又变成口头承诺。
	writes := map[string]string{
		"handleRegister":               "register.go",      // 注册
		"handleChangePassword":         "auth.go",          // 改密
		"handleAdminUserResetPassword": "auth.go",          // 管理台代客重置
		"handleAdminUserCreate":        "auth.go",          // 管理台建号（2026-10-22 补齐的那一处）
		"handleProfileSetup":           "profile_setup.go", // 资料补全里设密码
	}
	fset := token.NewFileSet()
	parsed := map[string]*ast.File{}
	for handler, file := range writes {
		f, ok := parsed[file]
		if !ok {
			var err error
			f, err = parser.ParseFile(fset, file, nil, 0)
			if err != nil {
				t.Fatalf("解析 %s 失败: %v", file, err)
			}
			parsed[file] = f
		}
		if !funcCallsValidatePassword(f, handler) {
			t.Errorf("★ %s（%s）没有调用 auth.ValidatePassword ⇒ 口令强度闸「五处同源」被破坏。\n"+
				"  修法：在设置口令之前加 `if !auth.ValidatePassword(pwd) { 400 + auth.PasswordTooShortMsg }`。\n"+
				"  这正是 2026-10-22 核查抓到的缺陷形态：管理台建号只判了非空，5 位密码能直接建号。",
				handler, file)
		}
	}
}

// funcCallsValidatePassword 判某个具名函数体内是否出现 `<x>.ValidatePassword(...)` 调用。
func funcCallsValidatePassword(f *ast.File, name string) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Name == nil || fd.Name.Name != name || fd.Body == nil {
			return true
		}
		ast.Inspect(fd.Body, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel != nil && sel.Sel.Name == "ValidatePassword" {
				found = true
				return false
			}
			return true
		})
		return false
	})
	return found
}

// ============================================================================
// 以下是 2026-10-22 一致性核查后的补锁（核查文档 §九 标「待补」的三条）。
// 共同点：缺陷都已修掉，但**修的时候没有任何用例会因此变红**——
// 也就是说下次谁再改回去，测试是绿的。这三条就是把那个洞补上。
// ============================================================================

// funcBodyOf 取某个具名顶层函数的函数体；找不到返回 nil。
func funcBodyOf(f *ast.File, name string) *ast.BlockStmt {
	var body *ast.BlockStmt
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if ok && fd.Name != nil && fd.Name.Name == name && fd.Body != nil {
			body = fd.Body
			return false
		}
		return true
	})
	return body
}

// calleeName 取调用表达式的被调名：`f()` → "f"，`x.y.f()` → "f"。
func calleeName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		if fn.Sel != nil {
			return fn.Sel.Name
		}
	}
	return ""
}

// bodyCalls 判函数体内是否出现名为 callee 的调用（普通调用与 x.callee(...) 都算）。
func bodyCalls(body *ast.BlockStmt, callee string) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if calleeName(call) == callee {
			found = true
			return false
		}
		return true
	})
	return found
}

// firstCallPos 返回函数体内首次出现某调用的位置（没出现返回 token.NoPos）。
// 顺序类判据（「先写库、后作废码」）只能靠位置比大小，不能靠"两个都调了"。
func firstCallPos(body *ast.BlockStmt, callee string) token.Pos {
	var pos token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if calleeName(call) == callee {
			pos = call.Pos()
			return false
		}
		return true
	})
	return pos
}

// ============================================================================
// P0-14（方案 §三·14）：改密链上「作废验证码」必须晚于「口令写库成功」。
//
// 为什么要有这一条：方案明令「写库失败不许烧用户的码」，但 2026-10-22 核查发现
// handleChangePassword 用的是 verifyEmailCodeFor（验证通过即删码）——
// ResetPassword 一旦 err / 0 行，用户密码没改成、码却被烧掉，只能重新发一次。
// 修好之后**没有任何用例会因此变红**，等于下次改回去也没人拦。
//
// 两腿互补（单靠任一腿都不够）：
//   - 静态腿：钉住「用哪把尺子」+「两句的先后」。行为腿钉不住顺序——
//     本夹具注入不了写库失败，而失败路径恰恰是 P0-14 的射程；
//   - 行为腿：钉住「成功之后码真的没了」。静态腿钉不住"调了但没生效"
//     （比如 consumeEmailCode 传错 purpose，源码里照样有这行）。
//
// 反证口径：
//   - 把 verifyEmailCodeKeep 改回 verifyEmailCodeFor → 静态腿第一条红；
//   - 把 consumeEmailCode 挪到 ResetPassword 之前 → 静态腿顺序判据红；
//   - 把 consumeEmailCode 的 purpose 写成 login → 行为腿红（码还在）。
//
// ============================================================================
func TestIAMRedoP0_14_EmailCodeIsNotBurnedBeforePasswordWrite(t *testing.T) {
	// ── 静态腿 ──────────────────────────────────────────────────────────
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "auth.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 auth.go 失败: %v", err)
	}
	body := funcBodyOf(f, "handleChangePassword")
	if body == nil {
		t.Fatalf("auth.go 里找不到 handleChangePassword")
	}
	posKeep := firstCallPos(body, "verifyEmailCodeKeep")
	posFor := firstCallPos(body, "verifyEmailCodeFor")
	posReset := firstCallPos(body, "ResetPassword")
	posConsume := firstCallPos(body, "consumeEmailCode")

	if posKeep == token.NoPos {
		t.Errorf("★ handleChangePassword 没有用 verifyEmailCodeKeep ⇒ 又变回「验证即删码」（P0-14 旧形态）。\n" +
			"  ResetPassword 一旦 err / 0 行，用户的码被烧掉而密码没改，只能重新发一次。")
	}
	if posFor != token.NoPos {
		t.Errorf("★ handleChangePassword 仍在用 verifyEmailCodeFor（验证通过即作废）⇒ P0-14 退化。\n" +
			"  改密链必须用 keep 版，成功后再自行 consumeEmailCode。")
	}
	if posConsume == token.NoPos {
		t.Errorf("★ handleChangePassword 没有调 consumeEmailCode ⇒ 码永不作废，TTL 内可被重复使用（比旧形态更危险）。")
	}
	if posReset != token.NoPos && posConsume != token.NoPos && posConsume < posReset {
		t.Errorf("★ consumeEmailCode 出现在 ResetPassword 之前 ⇒ 又回到「先烧码、后写库」，P0-14 的顺序判据被破坏。")
	}

	// ── 行为腿：改密成功后，同一个码必须已经用不了 ──────────────────────
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	const email = "p0_code_burn@example.com"
	if _, m := p0Register(t, s, "p0_code_burn", "oldpass123", email); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	u, err := s.Store.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("读账号失败: %v", err)
	}
	// 直接注入一枚改密用途的码：本锁测的是「作废时机」，发码链路由 P0-05 覆盖。
	const pwdCode = "246810"
	emailCodes.mu.Lock()
	emailCodes.codes[emailCodeKey(email, emailPurposePassword)] = &emailCode{
		Code: pwdCode, Purpose: emailPurposePassword,
		ExpiresAt: time.Now().Add(emailCodeTTL), SentAt: time.Now(),
	}
	emailCodes.mu.Unlock()

	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发会话失败: %v", err)
	}
	changeWith := func(code string) (int, string) {
		t.Helper()
		payload, _ := json.Marshal(map[string]string{"new_password": "brandnew123", "email_code": code})
		r := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(payload))
		r.Header.Set("Authorization", "Bearer "+tok)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handleChangePassword(w, r)
		return w.Code, w.Body.String()
	}
	if code, b := changeWith(pwdCode); code != http.StatusOK {
		t.Fatalf("有效邮箱码改密应成功，实得 code=%d body=%s", code, b)
	}
	emailCodes.mu.Lock()
	_, still := emailCodes.codes[emailCodeKey(email, emailPurposePassword)]
	emailCodes.mu.Unlock()
	if still {
		t.Errorf("★ 改密成功后该码仍在内存里 ⇒ consumeEmailCode 没生效（查 purpose 是否与发码时一致）。")
	}
	// 消费方读数：同一个码再用一次必须被拒（成功路径的另一面）。
	reloaded, err := s.Store.GetUserByEmail(email)
	if err != nil || reloaded == nil {
		t.Fatalf("读改密后账号失败: %v", err)
	}
	tok2, _ := auth.Sign(reloaded, time.Hour)
	payload, _ := json.Marshal(map[string]string{"new_password": "another123", "email_code": pwdCode})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/change-password", bytes.NewReader(payload))
	r.Header.Set("Authorization", "Bearer "+tok2)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleChangePassword(w, r)
	if w.Code == http.StatusOK {
		t.Errorf("★ 同一个改密码被用了两次 ⇒ 一次性语义失效（body=%s）", w.Body.String())
	}
}

// ============================================================================
// P0-9（强制改密真拦）：两条「设口令」的路径对 must_change_pwd 必须是**同一把尺子**。
//
// 为什么要有这一条：2026-10-22 核查发现 handleChangePassword 改密后清标记
// （auth.go），而 handleProfileSetup 设完密码**不清**——用户在首登资料屏设好合格密码、
// 拿到的重签 token 仍带 mcp=1，下一个请求立刻被 428 拦，观感是「我明明改了密码还让我改」。
// 两条路都是"改密"，标记语义必须一致；不一致就是又一处"各写一套口径"。
//
// 反证口径：删掉 handleProfileSetup 里的 SetMustChangePwd → 静态腿与行为腿同时红。
// ============================================================================
func TestIAMRedoP0_09_MustChangePwdClearedOnEveryPasswordWritePath(t *testing.T) {
	// ── 静态腿：两条路都必须清标记 ──────────────────────────────────────
	for _, tc := range []struct{ file, handler string }{
		{"auth.go", "handleChangePassword"},
		{"profile_setup.go", "handleProfileSetup"},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, tc.file, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", tc.file, err)
		}
		if body := funcBodyOf(f, tc.handler); !bodyCalls(body, "SetMustChangePwd") {
			t.Errorf("★ %s（%s）设完口令没有清 must_change_pwd ⇒ 与另一条改密路径口径不一致，\n"+
				"  用户会撞上「我明明改了密码还让我改」（428 强制闸见 auth_password_gate.go）。", tc.handler, tc.file)
		}
	}

	// ── 行为腿：资料补全里设密码 = 改密，库里的标记必须真清掉 ────────────
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	const email = "p0_mcp@example.com"
	if _, m := p0Register(t, s, "p0_mcp", "initpass123", email); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	u, err := s.Store.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("读账号失败: %v", err)
	}
	if err := s.Store.SetMustChangePwd(u.ID, u.TenantID, 1); err != nil {
		t.Fatalf("置 must_change_pwd=1 失败: %v", err)
	}
	u.MustChangePwd = 1
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatalf("签发会话失败: %v", err)
	}
	payload, _ := json.Marshal(map[string]any{"username": "p0_mcp_done", "password": "brandnew123"})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/profile-setup", bytes.NewReader(payload))
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleProfileSetup(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("资料补全设密码应成功，实得 code=%d body=%s", w.Code, w.Body.String())
	}
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应非 JSON: %s", w.Body.String())
	}
	if um, ok := m["user"].(map[string]any); ok {
		if v, _ := um["must_change_pwd"].(float64); v != 0 {
			t.Errorf("★ 响应里 must_change_pwd=%v ⇒ 前端会立刻再弹一次强制改密窗。", um["must_change_pwd"])
		}
	}
	// 库读数（不只看响应：判「生效」必须由消费方自己读出值）
	after, err := s.Store.GetUserByEmail(email)
	if err != nil || after == nil {
		t.Fatalf("读资料补全后账号失败: %v", err)
	}
	if after.MustChangePwd != 0 {
		t.Errorf("★ 资料补全设好密码后库里 must_change_pwd 仍为 %d ⇒ 下一个请求立刻被 428 拦（「我明明改了密码还让我改」）。",
			after.MustChangePwd)
	}
}

// ============================================================================
// B-5（2026-10-22 核查）：登录响应只有**一个**出口，两条登录腿字段不许分家。
//
// 缺陷形态：finishLogin（邮箱验证码腿）自己拼了一份 user map，且**漏掉 brand_host/sso_code**，
// 于是专属品牌域里用验证码登录的用户拿不到跳转目标、停在主站——而密码登录同一账号是好的。
// 修法是抽 writeLoginResponse/loginUserPayload 共用。本条把「共用」钉死。
//
// 三腿：
//   - 静态①：handleLogin 与 finishLogin 都必须走 writeLoginResponse；
//   - 静态②：两个函数体内不许再手工拼 "user" 键（只能是 loginUserPayload(...) 的调用）；
//   - 行为：同一账号两条腿的响应**字段集合必须逐字相同**，且品牌域下都带 brand_host。
//
// 反证口径：把 finishLogin 改回自己拼 map（不带 brand_host）→ 静态②与行为腿同时红。
// ============================================================================
func TestIAMRedoP0_03c_LoginResponseIsSingleSourced(t *testing.T) {
	// ── 静态腿 ──────────────────────────────────────────────────────────
	for _, tc := range []struct{ file, handler string }{
		{"auth.go", "handleLogin"},
		{"email_login.go", "finishLogin"},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, tc.file, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", tc.file, err)
		}
		body := funcBodyOf(f, tc.handler)
		if body == nil {
			t.Fatalf("%s 里找不到 %s", tc.file, tc.handler)
		}
		if !bodyCalls(body, "writeLoginResponse") {
			t.Errorf("★ %s（%s）没有走 writeLoginResponse ⇒ 登录响应又分家了，字段必然随改动漂移。", tc.handler, tc.file)
		}
		if bodyBuildsUserMap(body) {
			t.Errorf("★ %s（%s）手工拼了 \"user\" 字段 ⇒ 应改用 loginUserPayload(u)，否则两腿字段会不一致。", tc.handler, tc.file)
		}
	}

	// ── 行为腿：同一账号，两条腿的字段集合必须逐字相同 ──────────────────
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	const email = "p0_login_shape@example.com"
	if _, m := p0Register(t, s, "p0_login_shape", "shapepass123", email); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	u, err := s.Store.GetUserByEmail(email)
	if err != nil || u == nil {
		t.Fatalf("读账号失败: %v", err)
	}
	// 给该租户配一个专属域：品牌跳转分支才会真的被走到（否则两条腿都是空 brand_host，缺陷测不出来）
	if err := s.Ten.SetBranding(u.TenantID, "核查品牌", "", "p0brand", ""); err != nil {
		t.Fatalf("设置租户专属域失败: %v", err)
	}

	_, byPwd := p0Login(t, s, email, "shapepass123")
	if byPwd["success"] != true {
		t.Fatalf("密码登录失败: %v", byPwd)
	}

	// 验证码腿：注入一枚 login 用途的码，走同一个 handleLogin（isEmailLogin 分派）
	const loginCode = "135790"
	emailCodes.mu.Lock()
	emailCodes.codes[emailCodeKey(email, emailPurposeLogin)] = &emailCode{
		Code: loginCode, Purpose: emailPurposeLogin,
		ExpiresAt: time.Now().Add(emailCodeTTL), SentAt: time.Now(),
	}
	emailCodes.mu.Unlock()
	if s.loginLimit == nil {
		s.loginLimit = newLoginLimiter(s.Store)
	}
	body, _ := json.Marshal(map[string]string{"email": email, "code": loginCode})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("验证码登录应成功，实得 code=%d body=%s", w.Code, w.Body.String())
	}
	var byCode map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &byCode); err != nil {
		t.Fatalf("验证码腿响应非 JSON: %s", w.Body.String())
	}

	if diff, same := sameKeySet(byPwd, byCode); !same {
		t.Errorf("★ 两条登录腿的响应字段集合不一致（%s）。\n"+
			"  这正是 B-5 的缺陷形态：验证码腿漏了 brand_host/sso_code，\n"+
			"  专属品牌域里用验证码登录的用户拿不到跳转目标、停在主站。\n"+
			"  密码腿=%v\n  验证码腿=%v", diff, byPwd, byCode)
	}
	// 品牌域下两条腿都必须给出跳转目标（空串也算给了字段，这里要求非空才算真的走到分支）
	for name, m := range map[string]map[string]any{"密码腿": byPwd, "验证码腿": byCode} {
		if bh, _ := m["brand_host"].(string); strings.TrimSpace(bh) == "" {
			t.Errorf("★ %s 没有 brand_host ⇒ 专属域租户的成员登录后停在主站（本用例已为该租户配了专属域 p0brand）。", name)
		}
	}
}

// bodyBuildsUserMap 判函数体内是否手工拼了 "user" 键（且值不是 loginUserPayload(...) 的调用）。
func bodyBuildsUserMap(body *ast.BlockStmt) bool {
	bad := false
	ast.Inspect(body, func(n ast.Node) bool {
		cl, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range cl.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.BasicLit)
			if !ok || key.Kind != token.STRING || key.Value != `"user"` {
				continue
			}
			call, ok := kv.Value.(*ast.CallExpr)
			if !ok || calleeName(call) != "loginUserPayload" {
				bad = true
				return false
			}
		}
		return true
	})
	return bad
}

// sameKeySet 比较两个 JSON 对象的顶层键集合，返回差异说明与是否一致。
func sameKeySet(a, b map[string]any) (string, bool) {
	var onlyA, onlyB []string
	for k := range a {
		if _, ok := b[k]; !ok {
			onlyA = append(onlyA, k)
		}
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			onlyB = append(onlyB, k)
		}
	}
	if len(onlyA) == 0 && len(onlyB) == 0 {
		return "", true
	}
	return "只在密码腿有: [" + strings.Join(onlyA, ",") + "]；只在验证码腿有: [" + strings.Join(onlyB, ",") + "]", false
}

// ============================================================================
// P0-6 补腿（2026-10-22）：邮箱验证码腿必须有**账号级**失败锁定，与密码腿同一把尺子。
//
// 缺陷形态：密码腿有 IP 档 + 账号档两本账（auth.go 的 loginAccountKey），
// 而 OTP 腿只有"单码 5 次错误作废"这一道，IP 档也只读不记（handleLogin 顶部查了冷却，
// 但这条腿从不 recordLoginFail）——等于验证码腿一本账都没有。
// 后果不是"能爆破 6 位码"（5 次/码 + 1 码/分 算下来爆不动），而是**口径不一致**：
// 方案 §三·6 承诺"按账号锁定与按 IP 并行"，在验证码这条主路径上根本没落地。
//
// 两腿：
//   - 静态腿：handleEmailCodeLogin 必须调 loginAcctCooldownSec（查）与 recordLoginAcctFail（记），
//     以及成功路径的 clearLoginAcctFails（清）——少任何一个，锁定都会变成"只查不记"或"只记不清"；
//   - 行为腿：**每次换一个 IP** 打同一邮箱，把 IP 档排除在外；第 6 次必须 429。
//     再补一条反向对照：换个没失败过的邮箱用同一个 IP，不得 429 ——
//     否则证明拦的是 IP 档，本用例根本没测到账号档。
//
// 反证口径：把 recordLoginAcctFail 那行删掉，行为腿第 6 次会变回 401（本用例点名报红）。
// ============================================================================
func TestIAMRedoP0_06b_EmailCodeLoginHasAccountLevelLockout(t *testing.T) {
	// ── 静态腿 ──────────────────────────────────────────────────────────
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "email_login.go", nil, 0)
	if err != nil {
		t.Fatalf("解析 email_login.go 失败: %v", err)
	}
	body := funcBodyOf(f, "handleEmailCodeLogin")
	if body == nil {
		t.Fatalf("email_login.go 里找不到 handleEmailCodeLogin")
	}
	for _, need := range []struct{ callee, why string }{
		{"loginAcctCooldownSec", "查账号冷却"},
		{"recordLoginAcctFail", "记账号失败"},
		{"clearLoginAcctFails", "成功清零"},
	} {
		if !bodyCalls(body, need.callee) {
			t.Errorf("★ handleEmailCodeLogin 没有调 %s（%s）⇒ 验证码腿的账号级锁定缺一环。\n"+
				"  密码腿有两本账、验证码腿一本都没有，与方案 §三·6「按账号锁定与按 IP 并行」不一致。",
				need.callee, need.why)
		}
	}

	// ── 行为腿 ──────────────────────────────────────────────────────────
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	if s.loginLimit == nil {
		s.loginLimit = newLoginLimiter(s.Store)
	}
	const email = "p0_otp_lock@example.com"
	if _, m := p0Register(t, s, "p0_otp_lock", "lockpass123", email); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	post := func(ip, mail, code string) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"email": mail, "code": code})
		r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = ip + ":54321"
		w := httptest.NewRecorder()
		s.handleLogin(w, r)
		return w
	}
	// 每次换一个 IP：IP 档必然是干净的，累计只能来自账号档
	for i := 0; i < loginFailThreshold; i++ {
		ip := fmt.Sprintf("203.0.113.%d", 100+i)
		if w := post(ip, email, "000000"); w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错码应 401，实得 %d body=%s", i+1, w.Code, w.Body.String())
		}
	}
	// 第 6 次：全新 IP ⇒ 必须由**账号档**拦成 429
	if w := post("198.51.100.77", email, "000000"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("★ 同一邮箱连续 %d 次失败后，换新 IP 仍应被账号档拦成 429，实得 %d body=%s\n"+
			"  这就是 OTP 腿缺账号级锁定的缺陷形态：只有 IP 档时，换 IP 即可无限试码。",
			loginFailThreshold, w.Code, w.Body.String())
	}
	// 反向对照：另一个没失败过的邮箱，用同一个"刚用过"的 IP ⇒ 不得 429（拦的是账号，不是 IP）
	if w := post("198.51.100.77", "p0_otp_other@example.com", "000000"); w.Code == http.StatusTooManyRequests {
		t.Fatalf("反向对照失败：换了个邮箱仍被 429 ⇒ 拦的是 IP 档而不是账号档，本用例没测到要测的东西（body=%s）",
			w.Body.String())
	}
}

// ============================================================================
// P0-2 / 方案 §六 ⑥（2026-10-22 补锁）：写路径「0 行影响」必须判失败。
//
// 为什么要有这一条：§六 列了 10 条「重做后必须新增的锁」，逐条核对后**只有第 ⑥ 条缺**。
// 缺的后果很具体：`ResetPassword` 的 0 行语义（旧缺陷本体——`err==nil` 就当成功，
// 用户看到"已修改"而库里一字未动）没有任何用例守着，谁把 `n == 0` 分支删掉都不会红。
//
// 两腿：
//   - store 腿：喂一个不存在的 id，`ResetPassword` 必须回 (0, nil) 而**不是** error。
//     这条钉的是"0 行能被读出来"这个前提——如果 store 把 0 行包装成 error，
//     上层就退回"只看 err"的旧形态，判据名存实亡；
//   - 静态腿：两条写路径（改密 / 管理台重置）的函数体里都必须有 `n == 0` 且该分支写出错误响应。
//     行为腿造不出 0 行（u 来自 token / target 来自前置查询），只能靠静态腿守接线。
//
// 反证口径：删掉任一 handler 的 `if n == 0 { ... }`，静态腿点名报红；
// 把 store 的 0 行改成返回 error，store 腿报红。
// ============================================================================
func TestIAMRedoP0_02_ZeroRowWriteIsJudgedFailure(t *testing.T) {
	// ── store 腿 ────────────────────────────────────────────────────────
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	n, err := s.Store.ResetPassword(987654321, 1, auth.PasswordHash("whatever123"))
	if err != nil {
		t.Fatalf("不存在的 id 不应报错（否则上层退回「只看 err」的旧形态）：%v", err)
	}
	if n != 0 {
		t.Fatalf("不存在的 id 必须回 0 行影响，实得 %d —— 这正是旧缺陷「0 行当成功」的地基", n)
	}

	// ── 静态腿 ──────────────────────────────────────────────────────────
	for _, tc := range []struct{ file, handler string }{
		{"auth.go", "handleChangePassword"},
		{"auth.go", "handleAdminUserResetPassword"},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, tc.file, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", tc.file, err)
		}
		body := funcBodyOf(f, tc.handler)
		if body == nil {
			t.Fatalf("%s 里找不到 %s", tc.file, tc.handler)
		}
		if !bodyRejectsZeroRows(body) {
			t.Errorf("★ %s（%s）没有「n == 0 ⇒ 写出错误响应」的分支 ⇒ 0 行影响可能又变回 success。\n"+
				"  旧缺陷形态：err==nil 就当改密成功，用户看到「已修改」而库里一字未动。", tc.handler, tc.file)
		}
	}
}

// bodyRejectsZeroRows 判函数体内是否存在 `if <x> == 0 { ...writeError/writeJSON... }`。
// 只看「与 0 比较 + 该分支写出错误响应」这个骨架，不绑定变量名（n / rows / affected 都认）。
func bodyRejectsZeroRows(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Cond == nil || ifs.Body == nil {
			return true
		}
		be, ok := ifs.Cond.(*ast.BinaryExpr)
		if !ok || be.Op != token.EQL {
			return true
		}
		lit, ok := be.Y.(*ast.BasicLit)
		if !ok || lit.Value != "0" {
			return true
		}
		if bodyCalls(ifs.Body, "writeError") || bodyCalls(ifs.Body, "writeJSON") {
			found = true
			return false
		}
		return true
	})
	return found
}
