// ============================================================================
// admin_assist_test.go — AI 助手管理台 Token 下发/轮换测试（★ 改造 1A，2026-09-17）
//
// 覆盖：
//
//	A) 未登录 / 非超管 → 403（凭据不外泄给租户管理员与普通用户）
//	B) 超管 GET → 返回明文 Token 与来源；库内密文（enc:v1:）可正确解密
//	C) 优先级契约：env ASSIST_ADMIN_TOKEN 压过库内配置
//	D) POST 轮换 → 写库为 enc:v1: 密文（明文绝不落库），随后下发新值
//	E) POST 空串 → 清除库内配置并回落 env
//	F) 解密失败（JWT_SECRET 轮换未同步重存）→ 报 none 而非静默返回空串
//
// 使用内存 SQLite + 签发真实超管 JWT，走 handler 全链路。
// ============================================================================
package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"
)

// doAssistToken 以指定 JWT 调用 /api/admin/assist/token。
// 参数：s=服务，method=HTTP 方法，token=登录 JWT（空=未登录），body=请求体（可空）。
// 返回：状态码与解析后的响应体。
func doAssistToken(t *testing.T, s *Server, method, token, body string) (int, map[string]any) {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, "/api/admin/assist/token", rdr)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	s.handleAdminAssistToken(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestAssistTokenAuthScope 鉴权范围：未登录与非超管一律 403（Token 不外泄）。
func TestAssistTokenAuthScope(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	// 未登录
	if code, _ := doAssistToken(t, s, "GET", "", ""); code != 403 {
		t.Fatalf("未登录应 403，实际 %d", code)
	}
	// 普通用户（非超管）
	if code, _ := doAssistToken(t, s, "GET", tokens["u_co_a"], ""); code != 403 {
		t.Fatalf("普通用户应 403，实际 %d", code)
	}
	// 超管 → 200（此时库内无配置且 env 未设 → source=none、has_token=false）
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	code, resp := doAssistToken(t, s, "GET", tokens["admin"], "")
	if code != 200 {
		t.Fatalf("超管应 200，实际 %d %v", code, resp)
	}
	if resp["has_token"] != false || resp["source"] != "none" {
		t.Fatalf("未配置时应 none/false，实际 %v", resp)
	}
}

// TestAssistTokenDBDecryptAndEnvPriority 库内密文解密 + env 优先级高于库。
func TestAssistTokenDBDecryptAndEnvPriority(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	// 模拟主后台写入：密文落库（enc:v1:）
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret("db-secret-token")); err != nil {
		t.Fatalf("写入密文失败: %v", err)
	}
	// 库内明文绝不出现：原始值必须是密文前缀
	raw, _ := s.Store.GetConfig(assistAdminTokenKey)
	if !strings.HasPrefix(raw, "enc:v1:") {
		t.Fatalf("库内应为 enc:v1: 密文，实际 %q", raw)
	}

	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	code, resp := doAssistToken(t, s, "GET", tokens["admin"], "")
	if code != 200 || resp["token"] != "db-secret-token" || resp["source"] != "db" {
		t.Fatalf("应从库内解密下发，实际 %d %v", code, resp)
	}

	// env 优先级更高（部署侧保底配置压过库内值）
	t.Setenv("ASSIST_ADMIN_TOKEN", "env-secret-token")
	code, resp = doAssistToken(t, s, "GET", tokens["admin"], "")
	if code != 200 || resp["token"] != "env-secret-token" || resp["source"] != "env" {
		t.Fatalf("env 应压过库内配置，实际 %d %v", code, resp)
	}
}

// TestAssistTokenRotateAndClear 轮换写库为密文；空串清除并回落 env。
func TestAssistTokenRotateAndClear(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")

	// 轮换：写入新 Token
	code, resp := doAssistToken(t, s, "POST", tokens["admin"], `{"token":"rotated-token-1"}`)
	if code != 200 || resp["success"] != true {
		t.Fatalf("轮换应成功，实际 %d %v", code, resp)
	}
	raw, _ := s.Store.GetConfig(assistAdminTokenKey)
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "rotated-token-1") {
		t.Fatalf("轮换后库内应为密文且不含明文，实际 %q", raw)
	}
	// 下发新值
	_, resp = doAssistToken(t, s, "GET", tokens["admin"], "")
	if resp["token"] != "rotated-token-1" {
		t.Fatalf("轮换后应下发新 Token，实际 %v", resp)
	}

	// 清除：空串 → 库内配置清空，回落 env
	t.Setenv("ASSIST_ADMIN_TOKEN", "env-after-clear")
	code, resp = doAssistToken(t, s, "POST", tokens["admin"], `{"token":""}`)
	if code != 200 || resp["source"] != "env" {
		t.Fatalf("清除后应回落 env，实际 %d %v", code, resp)
	}
	_, resp = doAssistToken(t, s, "GET", tokens["admin"], "")
	if resp["token"] != "env-after-clear" || resp["source"] != "env" {
		t.Fatalf("清除后应下发 env 值，实际 %v", resp)
	}
}

// TestAssistTokenUndecryptable F 场景：库里是解不开的密文（JWT_SECRET 轮换）→ 明确报 none。
// 反例保护：旧实现若直接返回密文原串，前端会把它注入 iframe 造成「Token 不匹配」的迷惑现象。
func TestAssistTokenUndecryptable(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	// 构造不可解密文：合法前缀 + 非法 base64 体
	if err := s.Store.SetConfig(assistAdminTokenKey, "enc:v1:!!!not-base64!!!"); err != nil {
		t.Fatalf("写入损坏密文失败: %v", err)
	}
	code, resp := doAssistToken(t, s, "GET", tokens["admin"], "")
	if code != 200 {
		t.Fatalf("应 200（不报错，只是无可用 Token），实际 %d", code)
	}
	if resp["has_token"] != false || resp["source"] != "none" || resp["token"] != "" {
		t.Fatalf("不可解密文应报 none/空，实际 %v", resp)
	}
}

// TestAssistTokenMethodNotAllowed DELETE 等未支持方法应 405。
func TestAssistTokenMethodNotAllowed(t *testing.T) {
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	if code, _ := doAssistToken(t, s, "DELETE", tokens["admin"], ""); code != 405 {
		t.Fatalf("DELETE 应 405，实际 %d", code)
	}
}
