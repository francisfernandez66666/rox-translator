// ============ 本文件职责中文说明 ============
// 人机验证接缝单元测试：未启用恒通过、启用后缺 token/缺密钥拒绝。
// （真实 siteverify 网络调用不在此覆盖，避免单测依赖外网。）
package api

import (
	"database/sql"
	"net/http/httptest"
	"testing"

	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newCaptchaTestServer 构造最小可用 Server（内存 SQLite 的 Store，供配置读写）。
func newCaptchaTestServer(t *testing.T) *Server {
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
	return &Server{Store: st}
}

// TestCaptchaDisabledAlwaysPass 未配置 captcha_provider 时任何请求都放行。
func TestCaptchaDisabledAlwaysPass(t *testing.T) {
	s := newCaptchaTestServer(t)
	r := httptest.NewRequest("POST", "/api/auth/register", nil)
	if err := s.verifyCaptcha(r, ""); err != nil {
		t.Fatalf("未启用人机验证应放行: %v", err)
	}
}

// TestCaptchaTurnstileRequiresToken 启用 turnstile 后空 token 拒绝。
func TestCaptchaTurnstileRequiresToken(t *testing.T) {
	s := newCaptchaTestServer(t)
	_ = s.Store.SetConfig("captcha_provider", "turnstile")
	r := httptest.NewRequest("POST", "/api/auth/register", nil)
	if err := s.verifyCaptcha(r, "   "); err == nil {
		t.Fatal("启用 turnstile 且无 token 应拒绝")
	}
}

// TestCaptchaTurnstileMissingSecret 启用 turnstile、有 token 但缺密钥：可读错误而非崩溃。
func TestCaptchaTurnstileMissingSecret(t *testing.T) {
	s := newCaptchaTestServer(t)
	_ = s.Store.SetConfig("captcha_provider", "turnstile")
	r := httptest.NewRequest("POST", "/api/auth/register", nil)
	if err := s.verifyCaptcha(r, "tok"); err == nil {
		t.Fatal("缺 secret_key 应返回错误")
	}
}
