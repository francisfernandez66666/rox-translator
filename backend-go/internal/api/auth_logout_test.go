// ============ auth_logout_test.go · 职责说明 ============
// IAM P0-10（2026-10-21）「登出真撤销」的机械锁。
//
// 旧形态只有前端清 localStorage：一条被复制出去的 token 在 24h 有效期内照样通行。
// 现形态＝登出把这条 token 的 jti 写进撤销名单（Redis 优先、进程内存兜底），
// authUser 每请求查一次。本文件锁四件事：
//
//	A. 登出后**这一条** token 立刻失效（authUser 回 nil）；
//	B. 同一账号在**另一台设备**上的 token 不受影响（单条撤销，不是全端踢下线）；
//	C. 降级诚实：没有 Redis 时响应体 revoked=false，文案必须点明「其他实例可能仍接受」，
//	   不许把"只撤了本进程"说成"已经安全了"；
//	D. 空 jti 的老 token 不炸：revokeJWT 回 false、jwtRevoked 回 false（仅受 token_version 约束）。
//
// 反证：把 authUserUncached 里的 jwtRevoked 判断摘掉 ⇒ A 红而 B/C/D 仍绿。
// 方言：自钉内存 SQLite（AGENTS §一·4）。
// =============================================
package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/auth"
	"translator/internal/store"
)

// newLogoutProbe 起一个最小服务器（真库 + 一个启用用户）。
func newLogoutProbe(t *testing.T) (*Server, *store.User) {
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
	u, err := st.CreateUser(1, "logout_user", auth.PasswordHash("pw123456"), "登出测试账号", store.RoleUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st}, u
}

// logoutPost 直调 handleLogout（带 Bearer token）。
func logoutPost(t *testing.T, s *Server, token string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleLogout(w, r)
	return w
}

// A＋B 段：登出只作废当前这一条；同账号另一条照常可用。
func TestLogout_RevokesOnlyCurrentToken(t *testing.T) {
	s, u := newLogoutProbe(t)
	tok1, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	tok2, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c1, err := auth.Verify(tok1)
	if err != nil {
		t.Fatal(err)
	}
	if c1.JTI == "" {
		t.Fatal("签发的 token 必须带 jti（否则单条撤销无从谈起）")
	}

	w := logoutPost(t, s, tok1)
	if w.Code != 200 {
		t.Fatalf("登出应回 200，实际 %d（body=%s）", w.Code, w.Body.String())
	}

	// A：这一条立刻失效
	r1 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r1.Header.Set("Authorization", "Bearer "+tok1)
	if got := s.authUser(r1); got != nil {
		t.Fatalf("登出后同一 token 仍能通过鉴权（uid=%d）⇒ 服务端撤销没生效", got.ID)
	}

	// B：另一条不受牵连（单设备登出，不是全端踢）
	r2 := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	r2.Header.Set("Authorization", "Bearer "+tok2)
	if got := s.authUser(r2); got == nil {
		t.Fatal("登出一条 token 把同账号另一台设备也踢了（只能由改密/重置的 token_version 负责全端撤销）")
	}
}

// C 段：降级诚实——没有 Redis 时 revoked=false，且文案必须把影响说清。
func TestLogout_DegradeReadoutIsHonest(t *testing.T) {
	s, u := newLogoutProbe(t)
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w := logoutPost(t, s, tok)
	if w.Code != 200 {
		t.Fatalf("登出应回 200，实际 %d", w.Code)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("登出响应非 JSON: %s", w.Body.String())
	}
	msg, _ := out["message"].(string)
	revoked, ok := out["revoked"].(bool)
	if !ok {
		t.Fatalf("响应必须带布尔 revoked 读数（前端/运维据此判断跨实例是否生效），实际 %v", out["revoked"])
	}
	if !revoked && !strings.Contains(msg, "其他实例") {
		t.Errorf("revoked=false 时文案必须点明「其他实例可能仍接受此凭证」，实际 %q", msg)
	}
	if revoked && msg != "已登出" {
		t.Errorf("revoked=true 时文案应是干净的「已登出」，实际 %q（读数与文案分家）", msg)
	}
}

// D 段：空 jti 的老 token 不炸，也不假装撤销成功。
func TestLogout_EmptyJTIDegrades(t *testing.T) {
	if revokeJWT("", time.Hour) {
		t.Error("空 jti 不可能写进名单，revoke 应回 false（不许假装成功）")
	}
	if jwtRevoked("") {
		t.Error("空 jti 一律视为不可撤销，jwtRevoked 应回 false")
	}
	if revokeJWT("already-expired", -time.Minute) != true {
		t.Error("已过期 token 无需写名单，按「已失效」回 true（调用方语义是「这条不再可用」）")
	}
}
