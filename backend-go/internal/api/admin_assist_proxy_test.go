// ============================================================================
// admin_assist_proxy_test.go — 主后台 → AI 助手管理面受管代理测试（★ #34，2026-09-21）
//
// 覆盖（每条都对应旧 iframe 方案的一个真实风险）：
//
//	A) 鉴权范围：未登录 / 非超管一律 403（代理不得成为超管专属能力的旁路）
//	B) fail-closed：管理 Token 未配置时**不发上游请求**，回业务可读提示
//	C) 正常转发：路径映射、query 透传、上游响应体原样回传、服务端注入 X-Assist-Admin
//	D) 凭据不入 URL：浏览器传来的 admin_token 查询参数必须被剥掉
//	E) 写操作留审计，且审计 detail 绝不含请求体（配置项可能是 llm_api_key 明文）
//	F) 上游不可达 → 回「不可达 + 处置指引」，不外泄 Go 错误串
//	G) 白名单外路径 404（防主后台退化成任意内网 HTTP 中继）
//	H) 基址优先级 env > 库 > 默认，且非法 scheme 回落默认
//
// 方言：本文件全程走 newAdminScopeTestServer 的内存 SQLite（该 helper 直接 sql.Open("sqlite")，
// 不经 config.Default()，因此不会把 PG 方言泄漏给同包其它用例）。
// ============================================================================
package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/store"
)

// assistUpstream 假 assist 管理面：记录最后一次收到的请求要素，便于逐条断言。
type assistUpstream struct {
	t         *testing.T
	hits      int
	path      string
	rawQuery  string
	adminTok  string
	ctHeader  string
	reqBody   string
	respCode  int
	respBody  string
	tokenSeen []string // 每次请求携带的 X-Assist-Admin（断言「服务端注入」而非浏览器透传）
}

func (a *assistUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.hits++
	a.path = r.URL.Path
	a.rawQuery = r.URL.RawQuery
	a.adminTok = r.Header.Get("X-Assist-Admin")
	a.ctHeader = r.Header.Get("Content-Type")
	a.tokenSeen = append(a.tokenSeen, a.adminTok)
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		a.reqBody = string(b)
	}
	if a.respCode == 0 {
		a.respCode = 200
	}
	if a.respBody == "" {
		a.respBody = `{"rows":[]}`
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(a.respCode)
	_, _ = w.Write([]byte(a.respBody))
}

// doAssistProxy 直接调用代理 handler（绕开 mux，路径由参数决定以便测白名单）。
// 返回：状态码、响应体字符串、解析后的 map（解析失败时为 nil）。
func doAssistProxy(s *Server, method, path, query, token, body string) (int, string, map[string]any) {
	r := httptest.NewRequest(method, path+"?"+query, strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handleAdminAssistProxy(w, r)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, w.Body.String(), out
}

// newAssistProxyFixture 起假上游 + 主后台测试实例，并把 ASSIST_BASE_URL 指向上游。
func newAssistProxyFixture(t *testing.T, adminTok string) (*Server, *assistUpstream, map[string]string) {
	t.Helper()
	up := &assistUpstream{}
	srv := httptest.NewServer(up)
	t.Cleanup(srv.Close)
	s, tokens := newAdminScopeTestServer(t)
	t.Setenv("ASSIST_ADMIN_TOKEN", adminTok)
	t.Setenv("ASSIST_BASE_URL", srv.URL)
	return s, up, tokens
}

// TestAssistProxyAuthScope A) 鉴权分流：未登录 401 / 已登录非超管 403，且两条都不产生上游调用。
// ★ F-64③（批 I-10）：这两支原先是内联 403（无错误码）→ 先补 code=FORBIDDEN；
// 深夜收尾又发现「未登录也回 403」是同族的第二类失真——前端只在 401 走重登录，
// 超管 token 过期后会在后台看着「权限不足」原地打转。现走 s.writeAuthzError 分流。
func TestAssistProxyAuthScope(t *testing.T) {
	s, up, tokens := newAssistProxyFixture(t, "proxy-token")
	code, _, resp := doAssistProxy(s, "GET", "/api/admin/assist/kb", "", "", "")
	if code != 401 {
		t.Fatalf("未登录应 401（不是 403：403 会让前端跳过重登录链路），实际 %d", code)
	}
	if resp["code"] != "UNAUTHORIZED" {
		t.Fatalf("未登录应带稳定错误码 UNAUTHORIZED，实际 %s", resp["code"])
	}
	if code, _, resp := doAssistProxy(s, "GET", "/api/admin/assist/kb", "", tokens["u_co_a"], ""); code != 403 || resp["code"] != "FORBIDDEN" {
		t.Fatalf("非超管应 403/FORBIDDEN（登录了但不该做这事），实际 %d/%s", code, resp["code"])
	}
	if up.hits != 0 {
		t.Fatalf("鉴权失败不得转发上游，实际 hits=%d", up.hits)
	}
	// status 接口同样分流（同一个出口函数，漏改一支就是两套口径）
	w := httptest.NewRecorder()
	s.handleAdminAssistStatus(w, httptest.NewRequest("GET", "/api/admin/assist/status", nil))
	if w.Code != 401 {
		t.Fatalf("status 未登录应 401，实际 %d", w.Code)
	}
}

// TestAssistProxyFailClosedWithoutToken B) 无管理 Token → 503 结构化错误 + 零上游调用（不回退假数据）。
// ★ F-64③（批 I-10 2026-09-26 深夜）改判据：这一支原先回「HTTP 200 + success:false」，
// 界面读 body 尚可，但监控/SDK 按状态码分支会把「assist 根本没配凭据」读成一次成功调用。
// 现在钉成 503（ErrServiceUnavailable＝依赖未就绪，保存 Token 后即恢复，不是客户端请求有误），
// 并保留 success:false + 原文案，让老前端与 bizResp 收敛层都不断。
func TestAssistProxyFailClosedWithoutToken(t *testing.T) {
	s, up, tokens := newAssistProxyFixture(t, "")
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret("")); err != nil {
		t.Fatalf("清空库内 Token 失败: %v", err)
	}
	code, body, resp := doAssistProxy(s, "GET", "/api/admin/assist/kb", "", tokens["admin"], "")
	if code != 503 {
		t.Fatalf("Token 未配置应回 503（诚实状态码，F-64③），实际 %d %s", code, body)
	}
	if resp["success"] != false {
		t.Fatalf("错误体应带 success:false（与前端 bizResp/老调用点兼容），实际 %s", body)
	}
	if resp["code"] != "SERVICE_UNAVAILABLE" {
		t.Fatalf("错误体应带稳定错误码 SERVICE_UNAVAILABLE，实际 %s", body)
	}
	if !strings.Contains(body, "管理 Token 未配置") {
		t.Fatalf("提示应写明 Token 未配置的处置路径，实际 %s", body)
	}
	if up.hits != 0 {
		t.Fatalf("无凭据时不得请求上游，实际 hits=%d", up.hits)
	}
}

// TestAssistProxyForwardAndHeaderStrip C)+D) 路径映射/query 透传/服务端注入凭据/浏览器 admin_token 剥离。
func TestAssistProxyForwardAndHeaderStrip(t *testing.T) {
	s, up, tokens := newAssistProxyFixture(t, "server-side-token")
	up.respBody = `{"rows":[{"id":7,"term":"合同"}]}`
	code, body, _ := doAssistProxy(s, "GET", "/api/admin/assist/kb", "id=7&admin_token=leak-from-browser", tokens["admin"], "")
	if code != 200 {
		t.Fatalf("应 200，实际 %d %s", code, body)
	}
	if !strings.Contains(body, "合同") {
		t.Fatalf("上游响应体应原样回传，实际 %s", body)
	}
	if up.path != "/api/assist/admin/kb" {
		t.Fatalf("路径映射错误，实际 %s", up.path)
	}
	if up.adminTok != "server-side-token" {
		t.Fatalf("应由服务端注入 X-Assist-Admin，实际 %q", up.adminTok)
	}
	if strings.Contains(up.rawQuery, "leak-from-browser") || strings.Contains(up.rawQuery, "admin_token") {
		t.Fatalf("浏览器传的 admin_token 必须剥掉（凭据不得进 URL/日志），实际 query=%q", up.rawQuery)
	}
	if !strings.Contains(up.rawQuery, "id=7") {
		t.Fatalf("业务 query 应透传，实际 %q", up.rawQuery)
	}
}

// TestAssistProxyAuditNoRequestBody E) 写操作留审计，但 detail 不含请求体（防 api_key 明文入审计）。
func TestAssistProxyAuditNoRequestBody(t *testing.T) {
	s, up, tokens := newAssistProxyFixture(t, "audit-token")
	up.respBody = `{"ok":true}`
	body := `{"key":"llm_api_key","value":"sk-SUPER-SECRET-123"}`
	code, _, _ := doAssistProxy(s, "PUT", "/api/admin/assist/config", "", tokens["admin"], body)
	if code != 200 {
		t.Fatalf("PUT 应 200，实际 %d", code)
	}
	if up.reqBody != body {
		t.Fatalf("请求体应透传给上游，实际 %q", up.reqBody)
	}
	var detail string
	if err := s.Store.DB().QueryRow("SELECT detail FROM audit_logs WHERE action='assist_admin_write' ORDER BY id DESC LIMIT 1").Scan(&detail); err != nil {
		t.Fatalf("未查到 assist_admin_write 审计: %v", err)
	}
	if !strings.Contains(detail, "PUT") || !strings.Contains(detail, "config") {
		t.Fatalf("审计应记录方法与区域，实际 %q", detail)
	}
	if strings.Contains(detail, "sk-SUPER-SECRET") {
		t.Fatalf("审计 detail 不得含请求体明文，实际 %q", detail)
	}
	// 读操作不留写审计（只有一条）
	var n int
	_ = s.Store.DB().QueryRow("SELECT COUNT(*) FROM audit_logs WHERE action='assist_admin_write'").Scan(&n)
	if n != 1 {
		t.Fatalf("仅写操作应记审计，实际条数 %d", n)
	}
}

// TestAssistProxyUpstreamUnreachable F) 上游不可达时回可自助的处置提示，不外泄 Go 错误串。
// ★ F-64③（批 I-10）加两条硬锁：状态码必须是 502（原 200 壳会让「assist 全挂」在监控里判成成功），
// 且错误码必须是 UPSTREAM_UNAVAILABLE——文案对得上不代表契约对得上，SDK 按 code 分支。
func TestAssistProxyUpstreamUnreachable(t *testing.T) {
	s, up, tokens := newAssistProxyFixture(t, "tok")
	t.Setenv("ASSIST_BASE_URL", "http://127.0.0.1:1") // 保留端口，必然连接失败
	code, body, resp := doAssistProxy(s, "GET", "/api/admin/assist/sessions", "", tokens["admin"], "")
	if code != 502 {
		t.Fatalf("上游不可达应回 502（F-64③ 诚实状态码），实际 %d %s", code, body)
	}
	if resp["code"] != "UPSTREAM_UNAVAILABLE" {
		t.Fatalf("错误码应为 UPSTREAM_UNAVAILABLE，实际 %s", body)
	}
	if resp["success"] != false {
		t.Fatalf("应回 success:false，实际 %s", body)
	}
	if !strings.Contains(body, "不可达") || !strings.Contains(body, "ASSIST_BASE_URL") {
		t.Fatalf("提示应含「不可达」与处置项，实际 %s", body)
	}
	if strings.Contains(body, "connect: connection refused") || strings.Contains(body, "dial ") {
		t.Fatalf("不得外泄底层错误串，实际 %s", body)
	}
	if up.hits != 0 {
		t.Fatalf("上游不可达时不应有命中，实际 %d", up.hits)
	}
}

// TestAssistProxyUpstreamStatusPassthrough F-64③ 的**反向锁**（防过度修复）：
// 上游（assist 服务）自己回的 4xx/5xx 必须**原样透传**状态码与响应体，
// 本层不许把它改写成 502/500，也不许重造错误体——AGENTS §一·8 的白名单豁免就是这一支。
// 没有这条，「把代理里所有非 2xx 都换成 writeError」这种改法也能让上面几条全绿。
func TestAssistProxyUpstreamStatusPassthrough(t *testing.T) {
	s, up, tokens := newAssistProxyFixture(t, "tok")
	up.respCode = 400
	up.respBody = `{"error":"key not allowed"}`
	code, body, resp := doAssistProxy(s, "PUT", "/api/admin/assist/config", "", tokens["admin"], `{"key":"nope","value":"x"}`)
	if code != 400 {
		t.Fatalf("上游 400 必须原样透传，实际 %d %s", code, body)
	}
	if body != `{"error":"key not allowed"}` {
		t.Fatalf("上游响应体必须原样回传（不重造错误体），实际 %s", body)
	}
	// 透传支不带本层的 code 字段：出现即说明被改写成了统一错误体
	if _, ok := resp["code"]; ok {
		t.Fatalf("上游透传不得被本层重造成统一错误体，实际 %s", body)
	}
	if up.hits != 1 {
		t.Fatalf("应恰好转发一次，实际 hits=%d", up.hits)
	}
}

// TestAssistProxyWhitelist G) 未登记路径 404，代理不放行任意上游地址。
// ★ F-64③（批 I-10）补 code 与文案锁：这一支原先是内联 404（有状态码、没有错误码），
// 现在进统一出口，前端/SDK 才能按 code 认出「这条路径主后台没接」而不是去猜文案。
func TestAssistProxyWhitelist(t *testing.T) {
	s, up, tokens := newAssistProxyFixture(t, "tok")
	code, body, resp := doAssistProxy(s, "GET", "/api/admin/assist/evil", "", tokens["admin"], "")
	if code != 404 {
		t.Fatalf("白名单外应 404，实际 %d", code)
	}
	if resp["code"] != "NOT_FOUND" || !strings.Contains(body, "接口不存在") {
		t.Fatalf("白名单外应回 NOT_FOUND＋「接口不存在」，实际 %s", body)
	}
	if up.hits != 0 {
		t.Fatalf("白名单外不得触达上游，实际 %d", up.hits)
	}
}

// TestAssistBaseURLPrecedence H) env > 库 > 默认；非法 scheme 回落默认（防 file:// 等怪值注入）。
func TestAssistBaseURLPrecedence(t *testing.T) {
	s, _, _ := newAssistProxyFixture(t, "tok")
	if got := s.assistBaseURL(); !strings.HasPrefix(got, "http://127.0.0.1:") {
		t.Fatalf("env 应生效且尾斜杠去掉，实际 %s", got)
	}
	t.Setenv("ASSIST_BASE_URL", "")
	if err := s.Store.SetConfig(assistBaseURLKey, "https://assist.internal:8790/"); err != nil {
		t.Fatalf("写库基址失败: %v", err)
	}
	if got := s.assistBaseURL(); got != "https://assist.internal:8790" {
		t.Fatalf("应取库内配置并去尾斜杠，实际 %s", got)
	}
	if err := s.Store.SetConfig(assistBaseURLKey, "file:///etc/passwd"); err != nil {
		t.Fatalf("写非法基址失败: %v", err)
	}
	if got := s.assistBaseURL(); got != assistDefaultBaseURL {
		t.Fatalf("非法 scheme 应回落默认，实际 %s", got)
	}
}

// TestAssistStatusEndpoint 状态接口：可达 + Token 来源；不可达时给原因（面板状态条数据源）。
func TestAssistStatusEndpoint(t *testing.T) {
	s, _, tokens := newAssistProxyFixture(t, "status-token")
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/admin/assist/status", nil)
	r.Header.Set("Authorization", "Bearer "+tokens["admin"])
	s.handleAdminAssistStatus(w, r)
	var ok map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &ok)
	if w.Code != 200 || ok["success"] != true || ok["reachable"] != true || ok["token_src"] != "env" {
		t.Fatalf("上游在线且 env 有 Token 时应 reachable=true/env，实际 %d %s", w.Code, w.Body.String())
	}
	// 状态接口只回「来源」，绝不回传 Token 明文
	if strings.Contains(w.Body.String(), "server-side-token") || strings.Contains(w.Body.String(), "\"tok\"") {
		t.Fatalf("状态接口不得回传 Token 明文，实际 %s", w.Body.String())
	}

	// 未配置 Token → token_src=none（面板据此提示处置路径，而非静默空列表）
	t.Setenv("ASSIST_ADMIN_TOKEN", "")
	if err := s.Store.SetConfig(assistAdminTokenKey, store.EncryptSecret("")); err != nil {
		t.Fatalf("清空 Token 失败: %v", err)
	}
	w = httptest.NewRecorder()
	r = httptest.NewRequest("GET", "/api/admin/assist/status", nil)
	r.Header.Set("Authorization", "Bearer "+tokens["admin"])
	s.handleAdminAssistStatus(w, r)
	ok = nil
	_ = json.Unmarshal(w.Body.Bytes(), &ok)
	if ok == nil || ok["token_src"] != "none" {
		t.Fatalf("未配置时应 token_src=none，实际 %s", w.Body.String())
	}
}

// 编译期保证假上游实现了 http.Handler（改签名时立即暴露）
var _ http.Handler = (*assistUpstream)(nil)
