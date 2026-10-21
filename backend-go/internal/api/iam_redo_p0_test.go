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

// P0-6（2026-10-21 用户后令覆盖当日早先的防枚举取舍）：
// 找回密码只按注册邮箱定位，发码前先查库；邮箱不在库必须明确拦截。
// 登录口仍保留统一文案，不在本锁射程内。
func TestIAMRedoP0_06_ForgotRequiresRegisteredEmail(t *testing.T) {
	fx := f77Fixture(t)
	s := fx.srv
	s.Cfg = config.C
	if _, m := p0Register(t, s, "p0_reg_d", "p0pass789", "p0_d@example.com"); m["success"] != true {
		t.Fatalf("预置账号失败: %v", m)
	}
	exists := f20CallForgot(t, s, "203.0.113.11", map[string]string{"email": "p0_d@example.com"})
	if exists.Code != 200 || !strings.Contains(exists.Body.String(), forgotSentMsg) {
		t.Fatalf("已注册邮箱应 200+发码文案，实得 code=%d body=%s", exists.Code, exists.Body.String())
	}
	ghost := f20CallForgot(t, s, "203.0.113.12", map[string]string{"email": "p0_ghost_never_registered@example.com"})
	if ghost.Code != http.StatusNotFound || !strings.Contains(ghost.Body.String(), "EMAIL_NOT_REGISTERED") {
		t.Fatalf("未注册邮箱应 404+EMAIL_NOT_REGISTERED，实得 code=%d body=%s", ghost.Code, ghost.Body.String())
	}
	// 用户名入口必须关闭：只带 username 的报文不得发码。
	byName := f20CallForgot(t, s, "203.0.113.13", map[string]string{"username": "p0_reg_d"})
	if byName.Code != http.StatusBadRequest {
		t.Fatalf("只带用户名应 400（邮箱为唯一入口），实得 code=%d body=%s", byName.Code, byName.Body.String())
	}
}

// P0-3：重置链的新密码强度必须在**服务端**判（旧形态只判非空，6 位那条只存在于前端硬编码）。
func TestIAMRedoP0_05_ResetRejectsWeakPasswordServerSide(t *testing.T) {
	f20ClearCode(0) // 冷启动：本文件与 f20 那批共用同一个包级 map
	s := f20ResetServer(t)
	u, err := s.Store.CreateUser(7, "p0_reset_weak", auth.PasswordHash("origpass1"), "弱口令靶子", "user", 0, 0)
	if err != nil {
		t.Fatalf("预置账号失败: %v", err)
	}
	const weakMail = "p0_reset_weak@example.com"
	if err := s.Store.SetUserEmail(u.ID, u.TenantID, weakMail); err != nil {
		t.Fatalf("绑定邮箱失败: %v", err)
	}
	f20SeedCode(t, u.ID, resetCode{Code: "123456", ExpiresAt: time.Now().Add(10 * time.Minute)})
	body, _ := json.Marshal(map[string]string{"email": weakMail, "code": "123456", "new_password": "123"})
	r := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// ★ 两次调用各占一个来源 IP：reset 口是「20 次/日 + 10 秒间隔」的放行即占格，
	//   同一测试 IP 连打两枪时第二枪必 429，会把「弱口令被拒」这条判据换成限流闸的判据（假红）。
	r.RemoteAddr = "203.0.113.201:54321"
	w := httptest.NewRecorder()
	s.handleResetPassword(w, r)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "VALIDATION_ERROR") {
		t.Fatalf("弱口令必须被服务端拒掉 400+VALIDATION_ERROR，实得 code=%d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), auth.PasswordTooShortMsg) {
		t.Fatalf("拒绝理由必须是那把唯一尺子的文案，body=%s", w.Body.String())
	}
	// 反向对照：同一张码喂合格口令必须改得动（否则上面那条红只是"码本身无效"的假红）
	good, _ := json.Marshal(map[string]string{"email": weakMail, "code": "123456", "new_password": "goodpass1"})
	r2 := httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", bytes.NewReader(good))
	r2.Header.Set("Content-Type", "application/json")
	r2.RemoteAddr = "203.0.113.202:54321" // 同上：与第一枪换一个来源 IP，否则撞 10 秒最小间隔
	w2 := httptest.NewRecorder()
	s.handleResetPassword(w2, r2)
	if w2.Code != 200 || !strings.Contains(w2.Body.String(), `"success":true`) {
		t.Fatalf("合格口令的重置应成功，实得 code=%d body=%s", w2.Code, w2.Body.String())
	}
	// P0-5：出参要回"可用来登录的那个标识"（邮箱档找回的人靠它回填，报障②的前端半边）
	if !strings.Contains(w2.Body.String(), "login_account") {
		t.Fatalf("reset 出参缺 login_account（前端无从回填登录标识）：%s", w2.Body.String())
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
	if code, m := p0Login(t, s, "p0_tenant_a", "wrongpass"); code != http.StatusUnauthorized || strings.Contains(mustJSON(m), "该企业") {
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
