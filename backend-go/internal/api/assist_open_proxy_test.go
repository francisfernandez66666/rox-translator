// ============================================================================
// assist_open_proxy_test.go — C 端挂件访客转发测试（★ 2026-09-22）
//
// 立锁背景：挂件写死同源前缀 /assist-api，而该前缀原先只在 vite dev proxy 与生产 Caddy 里存在。
// 「主服务直出 dist」的形态（单二进制本地跑、发布闸门 run_uat）下请求落进 SPA 兜底拿到
// index.html，AI 助手在闸门里从来没被真正跑通过（e2e W3 因此假红）。本文件锁住补上的这条路径：
//
//	A) 白名单四个访客端点转发成功：上游路径/query/POST body 原样送达，响应体与状态码原样回传
//	B) 不注入凭据：访客端点靠 sid+tok 自证，代理绝不带 X-Assist-Admin（否则等于把管理凭据
//	   放进匿名可达入口）
//	C) 白名单外一律 404，尤其 /assist-api/api/assist/admin/* 不得成为管理面旁路
//	D) 方法闸：非 GET/POST 回 405
//	D2) 上游返回 401（令牌失效）必须原样回 401——前端据此走「清本地会话 + 重新 greet」自愈，
//	    若被改成 200/502 就把自愈链路钝掉
//	E) 上游不可达 → 502 + 可读 JSON（不是 Go 错误串，也不是 index.html）
//	路由注册顺序（/assist-api/ 不能被 SPA 兜底抢走）不在本文件锁：本包 helper 直接构造 Server、
//	不跑 registerRoutes，走 Handler() 会 nil panic；该口径由 e2e W1–W3 在真服务器上打真实请求兜住
//	（拿回 index.html 即红）。
//
// ============================================================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doAssistOpen 直接调用访客转发 handler（路径由参数决定以便测白名单拒绝分支）。
func doAssistOpen(s *Server, method, path, query, body string) (int, string, map[string]any) {
	r := httptest.NewRequest(method, path+"?"+query, strings.NewReader(body))
	w := httptest.NewRecorder()
	s.handleAssistOpenProxy(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, w.Body.String(), out
}

// newAssistOpenFixture 起假 assist 上游 + 主服务实例，并把 ASSIST_BASE_URL 指向上游。
func newAssistOpenFixture(t *testing.T) (*Server, *assistUpstream) {
	t.Helper()
	up := &assistUpstream{}
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	s, _ := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_BASE_URL", srv.URL)
	return s, up
}

// TestAssistOpenProxyForwards A)+B)：四个访客端点逐条转发，且不携带管理凭据。
func TestAssistOpenProxyForwards(t *testing.T) {
	s, up := newAssistOpenFixture(t)
	up.respBody = `{"greeting":"你好"}`
	cases := []struct{ method, path, query, body string }{
		{http.MethodGet, "/assist-api/api/assist/greeting", "page=/", ""},
		{http.MethodGet, "/assist-api/api/assist/history", "session=s1&tok=t1&limit=30", ""},
		{http.MethodGet, "/assist-api/api/assist/features", "", ""},
		{http.MethodPost, "/assist-api/api/assist/chat", "", `{"session":"s1","tok":"t1","message":"多少钱"}`},
	}
	for _, c := range cases {
		before := up.hits
		code, bodyStr, _ := doAssistOpen(s, c.method, c.path, c.query, c.body)
		if code != 200 {
			t.Fatalf("%s %s 应 200，实际 %d：%s", c.method, c.path, code, bodyStr)
		}
		if up.hits != before+1 {
			t.Fatalf("%s %s 未打到上游（hits=%d）", c.method, c.path, up.hits)
		}
		want := strings.TrimPrefix(c.path, assistOpenPrefix)
		if up.path != want {
			t.Fatalf("上游路径 %q ≠ %q（前缀剥离口径）", up.path, want)
		}
		if c.query != "" && up.rawQuery != c.query {
			t.Fatalf("query 未透传：want %q got %q", c.query, up.rawQuery)
		}
		if c.body != "" && up.reqBody != c.body {
			t.Fatalf("POST body 未透传：got %q", up.reqBody)
		}
		if up.adminTok != "" {
			t.Fatalf("访客转发不得注入 X-Assist-Admin，实际 %q", up.adminTok)
		}
	}
}

// TestAssistOpenProxyRejectsNonWhitelist C)：管理面与未知路径一律 404 且不打上游。
func TestAssistOpenProxyRejectsNonWhitelist(t *testing.T) {
	s, up := newAssistOpenFixture(t)
	for _, p := range []string{
		"/assist-api/api/assist/admin/kb",
		"/assist-api/api/assist/admin/config",
		"/assist-api/assist/admin",
		"/assist-api/api/anything/else",
		"/assist-api/",
	} {
		code, bodyStr, _ := doAssistOpen(s, http.MethodGet, p, "", "")
		if code != http.StatusNotFound {
			t.Fatalf("%s 应 404，实际 %d：%s", p, code, bodyStr)
		}
	}
	if up.hits != 0 {
		t.Fatalf("白名单外请求不得打到上游，实际 hits=%d", up.hits)
	}
}

// TestAssistOpenProxyMethodGate D)：非 GET/POST 回 405。
func TestAssistOpenProxyMethodGate(t *testing.T) {
	s, up := newAssistOpenFixture(t)
	for _, m := range []string{http.MethodDelete, http.MethodPut, http.MethodPatch} {
		code, _, _ := doAssistOpen(s, m, "/assist-api/api/assist/chat", "", "{}")
		if code != http.StatusMethodNotAllowed {
			t.Fatalf("%s 应 405，实际 %d", m, code)
		}
	}
	if up.hits != 0 {
		t.Fatalf("被方法闸拦下的请求不该打到上游，hits=%d", up.hits)
	}
}

// TestAssistOpenProxyPassesThroughUpstreamStatus D2)：令牌失效的 401 原样回传。
func TestAssistOpenProxyPassesThroughUpstreamStatus(t *testing.T) {
	s, up := newAssistOpenFixture(t)
	up.respCode, up.respBody = http.StatusUnauthorized, `{"error":"invalid token"}`
	code, bodyStr, _ := doAssistOpen(s, http.MethodGet, "/assist-api/api/assist/history", "session=s1&tok=dead", "")
	if code != http.StatusUnauthorized {
		t.Fatalf("上游 401 必须原样回 401（前端靠它做会话级自愈），实际 %d", code)
	}
	if !strings.Contains(bodyStr, "invalid token") {
		t.Fatalf("上游响应体应原样回传，got %s", bodyStr)
	}
}

// TestAssistOpenProxyUpstreamDown E)：上游不可达回 502 可读 JSON。
func TestAssistOpenProxyUpstreamDown(t *testing.T) {
	s, _ := newAdminScopeTestServer(t)
	// 指向一个已关闭的端口：httptest 关掉后端口即不可达
	down := httptest.NewServer(&assistUpstream{})
	base := down.URL
	down.Close()
	t.Setenv("ASSIST_BASE_URL", base)
	code, bodyStr, out := doAssistOpen(s, http.MethodGet, "/assist-api/api/assist/features", "", "")
	if code != http.StatusBadGateway {
		t.Fatalf("上游不可达应 502，实际 %d：%s", code, bodyStr)
	}
	if out == nil || out["success"] != false {
		t.Fatalf("502 必须回 JSON 业务失败体（不能吐 HTML），got %s", bodyStr)
	}
	if !strings.Contains(bodyStr, "不可达") {
		t.Fatalf("报错应给出可自助口径，got %s", bodyStr)
	}
}
