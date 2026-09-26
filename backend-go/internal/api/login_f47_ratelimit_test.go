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
//	A. 前 loginFailThreshold 次失败仍是 401（凭证错就是凭证错，不许提前伪装成限流）；
//	B. 撞阈值后的请求＝429 + Retry-After 头 + 体 retry_after 字段 + code=RATE_LIMITED，
//	   三处时长必须来自同一个判据（不许头 300 / 字段 299）；
//	C. 锁定期内的请求**不再**走密码校验（不产生新 401，也就不会把冷却窗口一路往后推）；
//	D. 冷却届满即恢复 401 口径（闸门不是永久封禁）；
//	E. 状态码与「用户名不存在」的 401 可区分，但**文案仍不泄露账号存在性**
//	   （防枚举口径是 F-47 的约束条件，不是它可以被绕过的理由）。
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

// A＋B 段：阈值前 401、阈值后 429，且时长三处一致。
func TestF47_LoginLockReturns429WithRetryAfter(t *testing.T) {
	s := newF47LoginProbe(t)

	for i := 1; i <= loginFailThreshold; i++ {
		w := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败应仍为 401（凭证错不得提前伪装成限流），实际 %d body=%s",
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
	s.loginLimit.mu.Lock()
	a := s.loginLimit.data[f47ProbeIP]
	if a == nil {
		s.loginLimit.mu.Unlock()
		t.Fatal("内存回退路径下冷却项应存在（判据来自 loginLimiter.data）")
	}
	a.lockedUntil = snooze // 人为把截止时刻拨近，观察是否被「再失败一次」推回去
	s.loginLimit.mu.Unlock()

	again := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	if again.Code != http.StatusTooManyRequests {
		t.Fatalf("锁定期内应继续 429，实际 %d", again.Code)
	}
	s.loginLimit.mu.Lock()
	remained := 0
	if cur := s.loginLimit.data[f47ProbeIP]; cur != nil {
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

// D 段：冷却届满自动恢复（闸门不是永久封禁），恢复后回到 401 口径。
func TestF47_CooldownExpiryRestores401(t *testing.T) {
	s := newF47LoginProbe(t)
	for i := 0; i < loginFailThreshold; i++ {
		postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	}
	if w := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("前置条件：应已锁定，实际 %d", w.Code)
	}
	s.loginLimit.mu.Lock()
	if a := s.loginLimit.data[f47ProbeIP]; a != nil {
		a.lockedUntil = time.Now().Add(-time.Second) // 视为已届满
	}
	s.loginLimit.mu.Unlock()

	w := postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	if w.Code != http.StatusUnauthorized {
		t.Errorf("冷却届满后应回到 401 凭证错误口径，实际 %d body=%s", w.Code, w.Body.String())
	}
	if h := w.Header().Get("Retry-After"); h != "" {
		t.Errorf("已解除锁定仍带 Retry-After: %s", h)
	}
	if stringsContains(f47Body(t, w)["message"], "过于频繁") {
		t.Error("已解除锁定，文案仍是「过于频繁」＝判据与文案分家")
	}
}

// E 段：不同 IP 不共享冷却（限流按 IP 归属，别把一刀切成全局把真人挡在门外）。
func TestF47_LockIsPerIP(t *testing.T) {
	s := newF47LoginProbe(t)
	const otherIP = "203.0.113.78"
	for i := 0; i < loginFailThreshold; i++ {
		postLogin(t, s, f47ProbeIP, "f47_user", "wrong-password")
	}
	if w := postLogin(t, s, otherIP, "f47_user", "wrong-password"); w.Code != http.StatusUnauthorized {
		t.Errorf("另一 IP 应为 401，实际 %d：限流键归属错会全站误伤（同 F-30 XFF 口径）", w.Code)
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
