// ============ login_f47_ratelimit_test.go · 职责说明 ============
// 2026-09-26 发布前 UAT 修复批 I-7：登录锁定「状态码诚实」的端到端闸门。
//
// 缺陷 F-47（UAT 实跑撞出来的现象，非猜测）：登录连错 5 次后被锁，
// 服务器回的是 **HTTP 400**（与「请求参数格式错误」同一个状态码），响应体里也没有
// 「还要等多久」；而同仓注册／找回密码／邮箱验证码三条限流分支回的是 429＋Retry-After。
// 于是同一个产品里「你被限流了」有两套互相矛盾的对外表达，且旧注释写的是 429、
// 实际发的是 400——注释与代码分家，前端和 SDK 只能靠猜文案分支。
//
// 本文件锁的是**整链**（errors 包映射见 internal/errors/codes_f47_test.go）：
//
//	A. 前 loginFailThreshold 次失败仍是 404（凭证错就是凭证错，不许提前伪装成限流）；
//	B. 撞阈值后的请求＝429 + Retry-After 头 + 体 retry_after 字段 + code=RATE_LIMITED，
//	   三处时长必须来自同一个判据（不许头 300 / 字段 299）；
//	C. 锁定期内的请求**不再**走密码校验（不产生新 404，也就不会把冷却窗口一路往后推）；
//	D. 冷却届满即恢复 404 口径（闸门不是永久封禁）；
//	E. 状态码与「用户名不存在」的 404 **不可区分**，文案也不泄露账号存在性
//	   （防枚举口径是 F-47 的约束条件，不是它可以被绕过的理由）。
//	F. ★ P0-6（2026-10-21）：按**账号**并行的锁定跨 IP 成立、不连坐别的账号，且不存在的账号
//	   同样记账（否则 429/404 的差别本身就是账号存在性回执）。
//
// 方言：自钉 SQLite 内存库（AGENTS.md §一·4），不依赖 DB_DRIVER 环境变量。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run TestF47
// =============================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/store"
)

// f47ProbeIP 固定测试 IP：限流按 IP 计数，用例之间必须隔离（每个用例各用一个）。
const f47ProbeIP = "203.0.113.77"

// newF47LoginProbe 起一个「有库、有一个真用户、无租户表」的最小登录链路。
// 返回的 Server 里 loginLimit 走内存回退（Store 传 nil 给 newLoginLimiter），
// 目的：让冷却时长可控可伪造（D 段要把 lockedUntil 拨到过去验证届满恢复），
// 持久化路径的同一判据由 ratelimit.go 内部共用 retryAfterSec 保证，另有单测覆盖。
func newF47LoginProbe(t *testing.T) *Server {
	t.Helper()
	pinSqliteDialect(t)
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	st, err := store.New(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	// 建一个启用状态的真用户：密码哈希与登录时提交的口令**故意不同**，走失败计数这条路。
	if _, err := st.CreateUser(1, "f47_user", auth.PasswordHash("right-password-123"),
		"F47 限流测试账号", store.RoleUser, 0, 0); err != nil {
		t.Fatal(err)
	}
	// ★ P0-6 起「另一 IP 不受影响」这一腿需要**另一个账号**：同一账号如今有第二本账（按账号锁定），
	// 换 IP 也照样 429——那是新行为，不是 IP 隔离失效（见 TestF47_AccountLockIsCrossIP）。
	if _, err := st.CreateUser(1, "f47_other", auth.PasswordHash("right-password-123"),
		"F47 另一账号", store.RoleUser, 0, 0); err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st, loginLimit: newLoginLimiter(nil)}
}

// postLogin 以指定 IP 直调 handleLogin，返回 recorder。
func postLogin(t *testing.T, s *Server, ip, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"username": username, "password": password})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.RemoteAddr = ip + ":51234" // 只认 RemoteAddr：trustProxyXFF 默认关，XFF 不参与归属
	w := httptest.NewRecorder()
	s.handleLogin(w, r)
	return w
}

// f47Body 解码登录响应体。
func f47Body(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("响应体非合法 JSON: %v\n%s", err, w.Body.String())
	}
	return out
}

// A＋B 段：阈值前 404、阈值后 429，且时长三处一致。
func TestF47_LoginLockReturns429WithRetryAfter(t *testing.T) {
	s := newF47LoginProbe(t)

	for i := 1; i <= loginFailThreshold; i++ {
		w := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
		if w.Code != http.StatusNotFound {
			t.Fatalf("第 %d 次失败应仍为 404（凭证错不得提前伪装成限流），实际 %d body=%s",
				i, w.Code, w.Body.String())
		}
		if h := w.Header().Get("Retry-After"); h != "" {
			t.Fatalf("第 %d 次失败尚未锁定却带 Retry-After: %s", i, h)
		}
	}

	// 阈值已达：下一次请求必须在进业务逻辑前被拦下。
	w := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	if w.Code == http.StatusBadRequest {
		t.Error("F-47 未修：登录锁定仍回 400，与「请求参数错误」共用状态码，客户端无法区分「稍后再试」与「重试无意义」")
	}
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("锁定期应回 429，实际 %d body=%s", w.Code, w.Body.String())
	}
	hdr := w.Header().Get("Retry-After")
	if hdr == "" {
		t.Fatal("429 缺 Retry-After 头：客户端只能瞎猜退避窗口，猜短了继续撞闸并把失败窗口一路往后推")
	}
	hv, err := strconv.Atoi(hdr)
	if err != nil || hv <= 0 || hv > loginCooldownSec {
		t.Fatalf("Retry-After=%q 不在 (0, %d] 合理区间", hdr, loginCooldownSec)
	}
	out := f47Body(t, w)
	if b, _ := out["success"].(bool); b {
		t.Error("锁定响应 success 必须为 false")
	}
	if c, _ := out["code"].(string); c != "RATE_LIMITED" {
		t.Errorf("锁定响应 code=%q want RATE_LIMITED（前端/SDK 按 code 分支，不许只靠文案猜）", out["code"])
	}
	fv, ok := out["retry_after"]
	if !ok {
		t.Fatal("锁定响应缺 retry_after 字段：浏览器端 fetch 拿不到自定义头时没有兜底")
	}
	fn, _ := fv.(float64)
	if int(fn) != hv {
		t.Errorf("Retry-After 头=%d 与体 retry_after=%v 不一致：两处必须同一判据（loginLimiter.retryAfterSec）", hv, fv)
	}
	// 文案仍不得泄露账号存在性（防枚举口径不变）。
	msg, _ := out["message"].(string)
	if msg != "登录尝试过于频繁，请稍后再试" {
		t.Errorf("锁定文案漂移：%q（F-47 只改状态码与时长，文案逐字不动，否则 i18n catalog 与前端提示一起翻红）", msg)
	}
}

// C 段：锁定期内的请求不再计一次失败（否则每次撞闸都把 5 分钟窗口往后推，越试越久）。
func TestF47_LockedAttemptsDoNotExtendWindow(t *testing.T) {
	s := newF47LoginProbe(t)
	for i := 0; i < loginFailThreshold; i++ {
		postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	}
	first := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	h1 := first.Header().Get("Retry-After")
	snooze := time.Now().Add(2 * time.Second)
	// ★ IAM P0-6（2026-10-21）改了内存账的**键形**：两档（IP／账号）共用一张 map，
	//   键一律是 `scope + "|" + key`，IP 档的 scope 是 loginIPScope。
	//   旧写法直接取 data[f47ProbeIP]（键形还是"裸 IP"那代），本批改成 scope 前缀后它恒为 nil
	//   ——那是**夹具前提过时**，不是"锁不再生效"：下一条 429 与 C 段语义一字未动，
	//   只是伪造截止时刻的把手要跟着账本换。所以这里按同一把尺子取键，不放松任何断言。
	ipKey := loginIPScope + "|" + f47ProbeIP
	s.loginLimit.mu.Lock()
	a := s.loginLimit.data[ipKey]
	if a == nil {
		s.loginLimit.mu.Unlock()
		t.Fatal("内存回退路径下冷却项应存在（判据来自 loginLimiter.data[loginIPScope|ip]）")
	}
	a.lockedUntil = snooze // 人为把截止时刻拨近，观察是否被「再失败一次」推回去
	s.loginLimit.mu.Unlock()

	again := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	if again.Code != http.StatusTooManyRequests {
		t.Fatalf("锁定期内应继续 429，实际 %d", again.Code)
	}
	s.loginLimit.mu.Lock()
	remained := 0
	if cur := s.loginLimit.data[ipKey]; cur != nil {
		remained = int(time.Until(cur.lockedUntil) / time.Second)
	}
	s.loginLimit.mu.Unlock()
	if remained > 2 {
		t.Errorf("撞闸后冷却被延长（剩余 %ds，应在 2s 量级）：锁定分支不得再记一次失败，"+
			"否则用户每次重试都把窗口推回 5 分钟，越试越久", remained)
	}
	if h2 := again.Header().Get("Retry-After"); h2 == "" {
		t.Error("第二次锁定仍须带 Retry-After")
	}
	_ = h1
}

// D 段：冷却届满自动恢复（闸门不是永久封禁），恢复后回到 404 口径。
// ★ P0-6 起要**两本账一起**拨到过去：IP 档闸门先于账号档拦人，只到期一本仍会 429，
// 那读起来像"冷却不恢复"，实际是另一本账还在（判据要跟着行为一起更新，不能只改期望值）。
func TestF47_CooldownExpiryRestores404(t *testing.T) {
	s := newF47LoginProbe(t)
	for i := 0; i < loginFailThreshold; i++ {
		postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	}
	if w := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("前置条件：应已锁定，实际 %d", w.Code)
	}
	s.loginLimit.mu.Lock()
	// ★ 键形同 C 段：IP 档也带 scope 前缀（IAM P0-6 起两档共用一张账）。
	if a := s.loginLimit.data[loginIPScope+"|"+f47ProbeIP]; a != nil {
		a.lockedUntil = time.Now().Add(-time.Second) // 视为已届满
	}
	if a := s.loginLimit.data[loginAcctScope+"|f47_user"]; a != nil {
		a.lockedUntil = time.Now().Add(-time.Second)
	}
	s.loginLimit.mu.Unlock()

	w := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	if w.Code != http.StatusNotFound {
		t.Errorf("冷却届满后应回到 404 凭证错误口径，实际 %d body=%s", w.Code, w.Body.String())
	}
	if h := w.Header().Get("Retry-After"); h != "" {
		t.Errorf("已解除锁定仍带 Retry-After: %s", h)
	}
	if stringsContains(f47Body(t, w)["message"], "过于频繁") {
		t.Error("已解除锁定，文案仍是「过于频繁」＝判据与文案分家")
	}
}

// E 段：IP 档仍然按 IP 归属，别把一刀切成全局。
// ★ P0-6 起同一条判据必须**换一个账号**来测：拿 f47_user 换 IP 仍会 429（账号档在拦），
// 那是设计；这里问的是"另一个 IP 上的**另一个人**会不会被连坐"。
func TestF47_LockIsPerIP(t *testing.T) {
	s := newF47LoginProbe(t)
	const otherIP = "203.0.113.78"
	for i := 0; i < loginFailThreshold; i++ {
		postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	}
	if w := postLogin(t, s, otherIP, "f47_other", "wrong-password"); w.Code != http.StatusNotFound {
		t.Errorf("另一 IP 上另一账号应为 404，实际 %d：限流键归属错会全站误伤（同 F-30 XFF 口径）", w.Code)
	}
}

// F 段（★ P0-6 新增）：按账号的锁定与按 IP 的锁定**并行**，且账号档跨 IP 成立。
// 判据形态：每次失败换一个来源 IP（不碰 IP 档），第 loginFailThreshold 次之后，
// 从**又一个全新 IP** 用同一个账号再打，必须是 429 而不是 404——否则"分布式换 IP 打同一个账号"
// 这条直路还开着（那正是本档存在的理由）。
func TestF47_AccountLockIsCrossIP(t *testing.T) {
	s := newF47LoginProbe(t)
	for i := 0; i < loginFailThreshold; i++ {
		ip := "198.51.100." + strconv.Itoa(10+i) + ":50000"
		if w := postLogin(t, s, ip, "f47_user", "wrong-password"); w.Code != http.StatusNotFound {
			t.Fatalf("第 %d 次（来源 %s）应是 404 凭证错，实际 %d body=%s", i+1, ip, w.Code, w.Body.String())
		}
	}
	w := postLogin(t, s, "198.51.100.200:50000", "f47_user", "right-password-123")
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("账号档跨 IP 失效：换 IP 后拿**正确口令**应仍被 429 挡在业务之前，实际 %d body=%s", w.Code, w.Body.String())
	}
	if h := w.Header().Get("Retry-After"); h == "" {
		t.Fatal("账号档 429 缺 Retry-After：客户端无从退避")
	}
	// 反向对照：同一次分布式打击不连坐别的人——另一个账号从最新 IP 打，必须回到 404。
	if w := postLogin(t, s, "198.51.100.200:50000", "f47_other", "wrong-password"); w.Code != http.StatusNotFound {
		t.Errorf("账号档连坐了别的账号：f47_other 应 404，实际 %d", w.Code)
	}
	// 反向对照②：**不存在的账号**同样要被记账，否则"打 5 次存在账号被锁、打 5 次不存在不锁"
	// 本身就是一条账号存在性回执（防枚举在限流面漏回来）。用全新 IP 隔离 IP 档。
	for i := 0; i < loginFailThreshold; i++ {
		ip := "198.51.100." + strconv.Itoa(50+i) + ":50000"
		if w := postLogin(t, s, ip, "f47_ghost", "whatever"); w.Code != http.StatusNotFound {
			t.Fatalf("第 %d 次不存在的账号应仍是 404（不许提前伪装成限流），实际 %d", i+1, w.Code)
		}
	}
	if w := postLogin(t, s, "198.51.100.250:50000", "f47_ghost", "whatever"); w.Code != http.StatusTooManyRequests {
		t.Errorf("不存在的账号未被记账：第 6 次仍 %d ⇒ 攻方可据 429/404 的差别枚举账号是否存在", w.Code)
	}
}

// stringsContains 小工具：interface{} 值的安全包含判断（避免为一条断言引入 fmt）。
func stringsContains(v interface{}, sub string) bool {
	sv, ok := v.(string)
	if !ok {
		return false
	}
	return bytes.Contains([]byte(sv), []byte(sub))
}
