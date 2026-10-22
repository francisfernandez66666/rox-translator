// ============ profile_setup_mcp_test.go · 职责说明 ============
// 锁死 2026-10-22 一致性核查第 3 条缺陷的修复（详见 IAM重做方案_20261021.md §9.5）。
//
// 旧实现：handleProfileSetup 在带合格密码完成资料补全时**不清** MustChangePwd，
// 于是用户改完密码、重签 token 仍带 mcp=1，下一个请求立刻被 428 强制改密闸拦，
// 观感是「我明明改了密码还让我改」。修复＝与 handleChangePassword 同一把尺子，
// 清了才算改完。此前该路径前后端零单测覆盖，本条补上机械锁＋跳过对照。
//
// 反证：把 profile_setup.go 里 `if u.MustChangePwd > 0 { SetMustChangePwd(u.ID, u.TenantID, 0) }`
// 整段删掉 ⇒ TestProfileSetup_ClearsMustChangePwd 必红（改完仍是 1）。
// 方言：自钉内存 SQLite（AGENTS §一·4），复用 auth_logout_test.go 的 newLogoutProbe。
// =============================================
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/auth"
)

// profileSetupPost 直调 handleProfileSetup（带 Bearer token + JSON body）。
func profileSetupPost(t *testing.T, s *Server, token string, payload map[string]interface{}) (*httptest.ResponseRecorder, map[string]interface{}) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/profile/setup", bytes.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleProfileSetup(w, r)
	var out map[string]interface{}
	if w.Code == 200 {
		_ = json.Unmarshal(w.Body.Bytes(), &out)
	}
	return w, out
}

// 主锁：被强制改密标记的账号，带合格密码完成资料补全后，MustChangePwd 必须归 0。
func TestProfileSetup_ClearsMustChangePwd(t *testing.T) {
	s, u := newLogoutProbe(t)

	// 前置：模拟被 428 闸标记的账号。
	if err := s.Store.SetMustChangePwd(u.ID, u.TenantID, 1); err != nil {
		t.Fatal(err)
	}
	before, err := s.Store.GetUser(u.ID, u.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if before.MustChangePwd != 1 {
		t.Fatalf("前置条件失败：MustChangePwd 应为 1，实际 %d", before.MustChangePwd)
	}

	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w, out := profileSetupPost(t, s, tok, map[string]interface{}{
		"username": "mcp_fixed_user",
		"password": "goodpw123",
		"skip":     false,
	})
	if w.Code != 200 {
		t.Fatalf("资料补全应回 200，实际 %d（body=%s）", w.Code, w.Body.String())
	}
	if ok, _ := out["success"].(bool); !ok {
		t.Fatalf("资料补全响应 success≠true: %s", w.Body.String())
	}

	after, err := s.Store.GetUser(u.ID, u.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if after.MustChangePwd != 0 {
		t.Fatalf("修复回退：带合格密码完成资料补全后 MustChangePwd 应为 0，实际 %d（观感=改完仍被 428 拦）", after.MustChangePwd)
	}
	if after.Username != "mcp_fixed_user" {
		t.Fatalf("资料补全应写入用户名，实际 %q", after.Username)
	}
}

// 对照：跳过资料补全（skip=true）只结束本轮弹窗，不许消灭 MustChangePwd 标记，
// 也不许偷偷改密码——证明「清零」是改密路径的正确副作用，不是无条件清空。
func TestProfileSetup_SkipKeepsMustChangePwd(t *testing.T) {
	s, u := newLogoutProbe(t)

	if err := s.Store.SetMustChangePwd(u.ID, u.TenantID, 1); err != nil {
		t.Fatal(err)
	}
	// 记录跳过前的口令哈希，跳过不应改写它。
	pre, err := s.Store.GetUser(u.ID, u.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	preHash := pre.PasswordHash

	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	w, _ := profileSetupPost(t, s, tok, map[string]interface{}{"skip": true})
	if w.Code != 200 {
		t.Fatalf("跳过资料补全应回 200，实际 %d（body=%s）", w.Code, w.Body.String())
	}

	after, err := s.Store.GetUser(u.ID, u.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	if after.MustChangePwd != 1 {
		t.Fatalf("跳过资料补全不应清 MustChangePwd，实际 %d（跳过了还把提醒消灭了）", after.MustChangePwd)
	}
	if after.PasswordHash != preHash {
		t.Fatalf("跳过路径不应改写口令哈希（实际发生了变化，等于静默改密）")
	}
}
