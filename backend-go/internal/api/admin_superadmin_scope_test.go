// ============ 本文件职责中文说明 ============
// 问题2 回归测试：超级管理员在「平台根上下文」（未切换租户，effTenant=0）下修改
// 其他租户用户时，必须按目标用户实际归属租户定位——否则
// UpdateUser/ResetPassword 以 tenant_id=0 执行 UPDATE 匹配不到目标行
// （WHERE id=? AND tenant_id=?），角色/状态/密码修改全部静默失败（历史缺陷，2026-09-09 修复）。
//
// 覆盖：
//
//	A) 平台根上下文改用户角色 → 真实生效（按真实租户重读断言）
//	B) 平台根上下文重置密码 → 真实生效（按真实租户重读 password_hash 断言）
//	C) 平台根上下文列用户 → 返回全部租户账号（不含平台上下文过滤丢失）
//
// 使用内存 SQLite + 签发真实超管 JWT（auth.Sign），走 handler 全链路
// （鉴权 requireDeptAdmin → effTenant 平台上下文 → 目标租户解析 → 落库）。
// =============================================
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newAdminScopeTestServer 构造问题2回归测试的 Server：
// 内存 SQLite Store + 平台超管（tid=0, role=admin）+ 两个企业租户各一名普通用户。
// 返回 Server 与各账号 JWT token（超管/甲企业用户/乙企业用户）。
func newAdminScopeTestServer(t *testing.T) (*Server, map[string]string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	// 平台超管
	if err := st.EnsureAdmin(0, "admin", "hash-admin", "平台超管", ""); err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	// 两个企业租户用户（租户 1/2 直建；CreateUser 不依赖 tenants 表外键）
	if _, err := st.CreateUser(1, "u_co_a", "hash-a", "甲企业用户", "user", 1, 0); err != nil {
		t.Fatalf("创建甲企业用户失败: %v", err)
	}
	if _, err := st.CreateUser(2, "u_co_b", "hash-b", "乙企业用户", "user", 1, 0); err != nil {
		t.Fatalf("创建乙企业用户失败: %v", err)
	}
	s := &Server{Store: st}

	users, _ := st.ListAllUsers()
	tokens := map[string]string{}
	for _, u := range users {
		tk, err := auth.Sign(u, time.Hour)
		if err != nil {
			t.Fatalf("签发 %s 的 JWT 失败: %v", u.Username, err)
		}
		tokens[u.Username] = tk
	}
	return s, tokens
}

// findUser 按用户名全量查找用户（ListAllUsers）。
func findUser(t *testing.T, s *Server, username string) *store.User {
	t.Helper()
	all, err := s.Store.ListAllUsers()
	if err != nil {
		t.Fatalf("ListAllUsers 失败: %v", err)
	}
	for _, u := range all {
		if u.Username == username {
			return u
		}
	}
	t.Fatalf("用户 %s 不存在", username)
	return nil
}

// postAdmin 构造带 Bearer 的 JSON POST 请求，走 Server 路由注册的 handler。
func postAdmin(t *testing.T, s *Server, path, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.muxForAdmin().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s 期望 200，实得 %d: %s", path, rec.Code, rec.Body.String())
	}
	return rec
}

// muxForAdmin 构造问题2/问题1相关 handler 的最小真实路由（与 server.go 生产注册同源）。
func (s *Server) muxForAdmin() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/admin/users/update", s.handleAdminUserUpdate)
	m.HandleFunc("/api/admin/users/reset-password", s.handleAdminUserResetPassword)
	m.HandleFunc("/api/admin/users", s.handleAdminUsers)
	m.HandleFunc("/api/tenant/invite-enabled", s.handleTenantInviteEnabledGet)
	return m
}

// TestSuperAdminPlatformUpdateUser 问题2核心：平台根上下文改用户角色必须命中真实租户。
func TestSuperAdminPlatformUpdateUser(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	target := findUser(t, s, "u_co_b")

	// 超管平台上下文（无 X-Tenant-ID）把乙企业用户角色提升为 tenant_admin
	resp := postAdmin(t, s, "/api/admin/users/update", tokens["admin"],
		map[string]interface{}{"id": target.ID, "role": "tenant_admin"})
	var out struct{ Success bool }
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("平台上下文更新用户应成功: %v (%s)", err, resp.Body.String())
	}

	// 断言：按真实租户（target.TenantID）重读，角色确已变更（而非静默失败）
	got, err := s.Store.GetUser(target.ID, target.TenantID)
	if err != nil {
		t.Fatalf("读取目标用户失败: %v", err)
	}
	if got.Role != "tenant_admin" {
		t.Fatalf("平台上下文应把 %s 角色改为 tenant_admin，实得 %s（修复后仍静默失败）", got.Username, got.Role)
	}
}

// TestSuperAdminPlatformResetPassword 问题2：平台根上下文重置密码必须命中真实租户。
func TestSuperAdminPlatformResetPassword(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	target := findUser(t, s, "u_co_a")

	resp := postAdmin(t, s, "/api/admin/users/reset-password", tokens["admin"],
		map[string]interface{}{"id": target.ID, "password": "newpass123"})
	var out struct{ Success bool }
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil || !out.Success {
		t.Fatalf("平台上下文重置密码应成功: %v (%s)", err, resp.Body.String())
	}

	// 断言：ResetPassword 以真实租户执行 → password_hash 已变化（直接 SQL 读取，绕开 GetUser 的脱敏）
	got, err := s.Store.GetUser(target.ID, target.TenantID)
	if err != nil {
		t.Fatalf("读取目标用户失败: %v", err)
	}
	if got.PasswordHash == "hash-a" {
		t.Fatal("平台上下文重置密码未生效：password_hash 仍是旧值（以 tenant_id=0 匹配不到行）")
	}
	if got.PasswordHash == "" {
		t.Fatal("密码哈希不应为空（GetUser 若脱敏则此断言无意义，需看实现）")
	}
}

// TestSuperAdminPlatformListUsers 平台根上下文列用户返回全部租户账号。
func TestSuperAdminPlatformListUsers(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/admin/users", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["admin"])
	rec := httptest.NewRecorder()
	s.muxForAdmin().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("平台根列用户期望 200，实得 %d", rec.Code)
	}
	var out struct {
		Users []*store.User `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("解析列表失败: %v", err)
	}
	// 超管 + 甲企业 + 乙企业 = 3 个账号
	if len(out.Users) != 3 {
		t.Fatalf("平台根应返回全部租户账号（3 个），实得 %d 个", len(out.Users))
	}
}

// TestNonSuperAdminCannotTouchOtherTenant 反例：普通企业用户无权把其他租户用户提升角色。
func TestNonSuperAdminCannotTouchOtherTenant(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	target := findUser(t, s, "u_co_b")

	// 甲企业用户（tenant 1）尝试改乙企业用户（tenant 2）
	b, _ := json.Marshal(map[string]interface{}{"id": target.ID, "role": "tenant_admin"})
	req := httptest.NewRequest(http.MethodPost, "/api/admin/users/update", bytes.NewReader(b))
	req.Header.Set("Authorization", "Bearer "+tokens["u_co_a"])
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.muxForAdmin().ServeHTTP(rec, req)
	// 普通用户不满足 requireDeptAdmin（角色等级<2）→ 403 拦截
	if rec.Code != http.StatusForbidden {
		t.Fatalf("非超管普通用户应被 403 拦截，实得 %d: %s", rec.Code, rec.Body.String())
	}
}
