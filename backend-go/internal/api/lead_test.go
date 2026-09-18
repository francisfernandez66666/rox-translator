// ============ lead_test.go · 职责说明 ============
// ★ P1-3 留资接口回归断言（修改方案 2026-09-18）：
// 覆盖必填校验/邮箱格式/蜜罐假成功/成功落 feedbacks 通道/IP 最小间隔限流/方法限制。
// （真实 Turnstile siteverify 不在单测打外网，captcha 未启用即放行，见 captcha_test.go。）
package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"

	_ "modernc.org/sqlite"
)

// newLeadTestServer 构造留资测试 Server：内存 SQLite + 持久化 registerGuard。
func newLeadTestServer(t *testing.T) *Server {
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
	return &Server{Store: st, regGuard: newRegisterGuard(st)}
}

// postLead 发一次留资请求并返回 (状态码, 响应 JSON)。
func postLead(t *testing.T, s *Server, body map[string]any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	r := httptest.NewRequest("POST", "/api/lead", bytes.NewReader(b))
	w := httptest.NewRecorder()
	s.handleLeadCreate(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

// TestLeadHappyPath 合法请求：200 且落 feedbacks（target_type='lead'，content 含公司/邮箱）。
func TestLeadHappyPath(t *testing.T) {
	s := newLeadTestServer(t)
	code, resp := postLead(t, s, map[string]any{"company": "测试贸易", "email": "Biz@Example.com ", "langs": "英,日", "source": "pricing"})
	if code != 200 || resp["success"] != true {
		t.Fatalf("合法留资应 200 success，got %d %v", code, resp)
	}
	list, err := s.Store.ListFeedbacks("open")
	if err != nil || len(list) != 1 {
		t.Fatalf("应落 1 条 lead 反馈，got %d err=%v", len(list), err)
	}
	f := list[0]
	if f.TargetType != "lead" || !strings.Contains(f.Content, "测试贸易") || !strings.Contains(f.Content, "biz@example.com") {
		t.Fatalf("lead 行内容不符: %+v", f)
	}
	if !strings.Contains(f.Content, "来源：pricing") {
		t.Fatalf("来源字段未落摘要: %s", f.Content)
	}
}

// TestLeadValidation 必填与邮箱格式：缺公司 400、坏邮箱 400。
func TestLeadValidation(t *testing.T) {
	s := newLeadTestServer(t)
	if code, _ := postLead(t, s, map[string]any{"email": "a@b.co"}); code != 400 {
		t.Fatalf("缺公司名应 400，got %d", code)
	}
	for _, bad := range []string{"no-at", "@x.co", "a@b", "a b@c.co", "a@b."} {
		if code, _ := postLead(t, s, map[string]any{"company": "c", "email": bad}); code != 400 {
			t.Fatalf("坏邮箱 %q 应 400，got %d", bad, code)
		}
	}
}

// TestLeadHoneypot 蜜罐命中：假成功但不落库（不给 bot 信号）。
func TestLeadHoneypot(t *testing.T) {
	s := newLeadTestServer(t)
	code, resp := postLead(t, s, map[string]any{"company": "botco", "email": "bot@spam.io", "site": "http://seo.example"})
	if code != 200 || resp["success"] != true {
		t.Fatalf("蜜罐应假成功 200，got %d %v", code, resp)
	}
	list, _ := s.Store.ListFeedbacks("")
	if len(list) != 0 {
		t.Fatalf("蜜罐命中不得落库，got %d 条", len(list))
	}
}

// TestLeadRateLimit 同 IP 连续两次：第二条被最小间隔挡住（429）。
func TestLeadRateLimit(t *testing.T) {
	s := newLeadTestServer(t)
	if code, _ := postLead(t, s, map[string]any{"company": "A", "email": "a@x.co"}); code != 200 {
		t.Fatalf("首次应放行，got %d", code)
	}
	if code, resp := postLead(t, s, map[string]any{"company": "B", "email": "b@x.co"}); code != 429 {
		t.Fatalf("间隔内第二条应 429，got %d %v", code, resp)
	}
}

// TestLeadMethodGuard 非 POST 拒绝。
func TestLeadMethodGuard(t *testing.T) {
	s := newLeadTestServer(t)
	w := httptest.NewRecorder()
	s.handleLeadCreate(w, httptest.NewRequest("GET", "/api/lead", nil))
	if w.Code != 405 {
		t.Fatalf("GET 应 405，got %d", w.Code)
	}
}
