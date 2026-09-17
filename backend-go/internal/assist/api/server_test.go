// server_test.go — HTTP 层端到端单测：模拟前端完整调用序列（C 端接待 + 管理端 CRUD/鉴权）
package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"translator/internal/assist/engine"
	"translator/internal/assist/llm"
	"translator/internal/assist/store"
)

// newTestServer 建临时库 + seed 夹具 + 无 LLM 的测试服务
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	db, err := store.Open(t.TempDir() + "/api.db")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	// 夹具：知识/入口/话术/流程/配置
	_, _ = db.Create("kb_entries", map[string]any{
		"key": "kb-pdf", "category": "usage", "title": "格式", "priority": 9, "enabled": 1,
		"content": "支持 pdf 与 epub", "keywords": "pdf,epub,格式", "link_keys": "tickets",
	})
	_, _ = db.Create("feature_links", map[string]any{
		"key": "tickets", "name": "文件翻译", "url": "/tickets", "ftype": "route", "sort": 10, "enabled": 1,
	})
	_, _ = db.Create("scripts", map[string]any{
		"key": "sc-price", "stype": "keyword", "title": "价格", "priority": 9, "enabled": 1,
		"content": "按积分预充值", "keywords": "多少钱,价格", "link_keys": "billing",
	})
	_, _ = db.Create("flows", map[string]any{
		"key": "fl-a", "name": "引导", "enabled": 1, "trigger_keywords": "新手",
		"steps_json": `[{"ask":"一","wait":true,"actions":["tickets"]},{"ask":"二","wait":true,"actions":[]}]`,
	})
	_ = db.SetConfig("welcome", "欢迎光临")
	_ = db.SetConfig("quick_chips", "问题A,问题B")

	eng := engine.New(db, llm.New(nil, 5))
	srv := httptest.NewServer(NewServer(db, eng, "test-token", "*").Handler())
	t.Cleanup(srv.Close)
	return srv
}

func doJSON(t *testing.T, srv *httptest.Server, method, path, token string, body any) (int, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = strings.NewReader(string(b))
	} else {
		rd = strings.NewReader("")
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-Assist-Admin", token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestGuestJourney C 端完整旅程：引导→知识兜底→话术→流程推进→走完→历史恢复
func TestGuestJourney(t *testing.T) {
	srv := newTestServer(t)

	// ① 开场引导（含 chips 与欢迎词，落一条 assistant 消息）
	code, g := doJSON(t, srv, "GET", "/api/assist/greeting?page=/pricing", "", nil)
	if code != 200 {
		t.Fatalf("greeting code %d", code)
	}
	sid, _ := g["session"].(string)
	if sid == "" || g["greeting"] != "欢迎光临" {
		t.Fatalf("greeting: %+v", g)
	}
	chips, _ := g["chips"].([]any)
	if len(chips) != 2 {
		t.Fatalf("chips: %+v", g)
	}

	// ② 知识兜底（无 LLM）：命中 kb-pdf，带动作按钮
	code, r := doJSON(t, srv, "POST", "/api/assist/chat", "", map[string]any{"session": sid, "message": "支持 pdf 吗"})
	if code != 200 || r["source"] != "fallback" {
		t.Fatalf("chat kb: %d %+v", code, r)
	}
	if acts := r["actions"].([]any); len(acts) != 1 || acts[0].(map[string]any)["name"] != "文件翻译" {
		t.Fatalf("actions: %+v", r["actions"])
	}

	// ③ 话术直配
	_, r = doJSON(t, srv, "POST", "/api/assist/chat", "", map[string]any{"session": sid, "message": "多少钱"})
	if r["source"] != "rule" || !strings.Contains(r["reply"].(string), "预充值") {
		t.Fatalf("rule: %+v", r)
	}

	// ④ 流程触发 + 推进 + 走完
	_, r = doJSON(t, srv, "POST", "/api/assist/chat", "", map[string]any{"session": sid, "message": "我是新手"})
	if r["source"] != "flow" || r["reply"] != "一" {
		t.Fatalf("flow start: %+v", r)
	}
	_, r = doJSON(t, srv, "POST", "/api/assist/chat", "", map[string]any{"session": sid, "message": "ok"})
	if r["source"] != "flow" || r["reply"] != "二" {
		t.Fatalf("flow step: %+v", r)
	}
	_, r = doJSON(t, srv, "POST", "/api/assist/chat", "", map[string]any{"session": sid, "message": "ok"})
	if r["source"] != "flow" || !strings.Contains(r["reply"].(string), "介绍完") {
		t.Fatalf("flow done: %+v", r)
	}

	// ⑤ 历史恢复：欢迎词 1 + 三轮对话×2（知识/话术/流程走完）+ 流程开始与推进…
	// 精确条数：greeting 1 + (u+a)×5 = 11，首条为 assistant 欢迎词
	_, h := doJSON(t, srv, "GET", "/api/assist/history?session="+sid, "", nil)
	msgs := h["messages"].([]any)
	if len(msgs) != 11 {
		t.Fatalf("history len: %d", len(msgs))
	}
	if msgs[0].(map[string]any)["role"] != "assistant" {
		t.Fatal("first msg should be greeting")
	}

	// ⑥ 缺参会话被拒
	code, _ = doJSON(t, srv, "POST", "/api/assist/chat", "", map[string]any{"message": "hi"})
	if code != 400 {
		t.Fatalf("missing session should 400, got %d", code)
	}
}

// TestAdminGuard 管理端鉴权：无 token 401，错 token 401，对 token 放行
func TestAdminGuard(t *testing.T) {
	srv := newTestServer(t)
	code, _ := doJSON(t, srv, "GET", "/api/assist/admin/kb", "", nil)
	if code != 401 {
		t.Fatalf("no token: %d", code)
	}
	code, _ = doJSON(t, srv, "GET", "/api/assist/admin/kb", "wrong", nil)
	if code != 401 {
		t.Fatalf("bad token: %d", code)
	}
	code, _ = doJSON(t, srv, "GET", "/api/assist/admin/kb", "test-token", nil)
	if code != 200 {
		t.Fatalf("good token: %d", code)
	}
}

// TestAdminCRUD 管理端四表 CRUD + 配置读写 + 会话统计
func TestAdminCRUD(t *testing.T) {
	srv := newTestServer(t)

	// 知识库：建→改→查→删
	code, r := doJSON(t, srv, "POST", "/api/assist/admin/kb", "test-token",
		map[string]any{"key": "kb-x", "title": "X", "content": "C", "keywords": "x", "priority": 5, "enabled": 1})
	if code != 200 {
		t.Fatalf("kb create: %d %v", code, r)
	}
	id := int(r["id"].(float64))
	_, _ = doJSON(t, srv, "PUT", "/api/assist/admin/kb?id="+itoa(id), "test-token", map[string]any{"enabled": 0})
	_, r = doJSON(t, srv, "GET", "/api/assist/admin/kb", "test-token", nil)
	rows := r["rows"].([]any)
	found := false
	for _, row := range rows {
		m := row.(map[string]any)
		if m["id"].(float64) == float64(id) {
			found = m["enabled"].(float64) == 0
		}
	}
	if !found {
		t.Fatal("kb update not applied")
	}
	code, _ = doJSON(t, srv, "DELETE", "/api/assist/admin/kb?id="+itoa(id), "test-token", nil)
	if code != 200 {
		t.Fatal("kb delete")
	}

	// 配置：写→读回
	_, _ = doJSON(t, srv, "PUT", "/api/assist/admin/config", "test-token", map[string]any{"key": "temperature", "value": "0.3"})
	_, r = doJSON(t, srv, "GET", "/api/assist/admin/config", "test-token", nil)
	cfgs := r["configs"].([]any)
	val := ""
	for _, c := range cfgs {
		if c.(map[string]any)["key"] == "temperature" {
			val = c.(map[string]any)["value"].(string)
		}
	}
	if val != "0.3" {
		t.Fatalf("config roundtrip: %s", val)
	}

	// 会话统计（guest journey 未跑，但 greeting 等也未调用——此处仅验证结构）
	code, r = doJSON(t, srv, "GET", "/api/assist/admin/sessions", "test-token", nil)
	if code != 200 || r["total"] == nil {
		t.Fatalf("sessions: %d %+v", code, r)
	}

	// 非法方法
	code, _ = doJSON(t, srv, "DELETE", "/api/assist/admin/config", "test-token", nil)
	if code != 405 {
		t.Fatalf("config DELETE should 405, got %d", code)
	}
}

// TestHealthAndAdminPage 健康检查与管理台页面
// ★ 改造 1A（2026-09-17）：管理台改为随二进制内嵌（web.AdminHTML），
// 测试环境无 web 目录也应稳定 200 且吐真实页面——原「缺文件必 404」断言已过时。
func TestHealthAndAdminPage(t *testing.T) {
	srv := newTestServer(t)
	code, r := doJSON(t, srv, "GET", "/health", "", nil)
	if code != 200 || r["ok"] != true {
		t.Fatalf("health: %d %+v", code, r)
	}
	// 管理页：内嵌资源兜底，无外置 web 目录也必须可用（部署零外部文件依赖）
	t.Setenv("ASSIST_WEB", "")
	resp, err := http.Get(srv.URL + "/assist/admin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("内嵌管理页应 200，实际 %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte("AI 助手管理台")) {
		t.Fatalf("管理页内容异常（应含标题『AI 助手管理台』），长度 %d", len(body))
	}
}

// TestCORSPreflight 跨域预检
func TestCORSPreflight(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest(http.MethodOptions, srv.URL+"/api/assist/chat", nil)
	req.Header.Set("Origin", "https://langcross.lexicorn.cn")
	req.Header.Set("Access-Control-Request-Method", "POST")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("preflight: %d", resp.StatusCode)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "https://langcross.lexicorn.cn" {
		t.Fatalf("cors: %s", resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// ============================================================================
// ★ R0 批次回归（2026-09-16）：config 白名单 / api_key 掩码 / llm 测试连通
// ============================================================================

// TestConfigWhitelist 未登记 key 一律 400，白名单 key 可写
func TestConfigWhitelist(t *testing.T) {
	srv := newTestServer(t)
	code, _ := doJSON(t, srv, "PUT", "/api/assist/admin/config", "test-token",
		map[string]any{"key": "arbitrary_key", "value": "x"})
	if code != 400 {
		t.Fatalf("non-whitelisted key should 400: %d", code)
	}
	code, _ = doJSON(t, srv, "PUT", "/api/assist/admin/config", "test-token",
		map[string]any{"key": "llm_base_url", "value": "https://api.example.com/v1"})
	if code != 200 {
		t.Fatalf("llm_base_url should write: %d", code)
	}
}

// TestAPIKeyMasked api_key 读回掩码；掩码值回写被跳过不覆盖明文
func TestAPIKeyMasked(t *testing.T) {
	srv := newTestServer(t)
	_, _ = doJSON(t, srv, "PUT", "/api/assist/admin/config", "test-token",
		map[string]any{"key": "llm_api_key", "value": "sk-abcdef123456"})
	_, r := doJSON(t, srv, "GET", "/api/assist/admin/config", "test-token", nil)
	var masked string
	for _, c := range r["configs"].([]any) {
		m := c.(map[string]any)
		if m["key"] == "llm_api_key" {
			masked = m["value"].(string)
		}
	}
	if masked != "sk-***56" {
		t.Fatalf("mask: %q", masked)
	}
	// 管理台把掩码原样回传 → 后端 skipped，不覆盖明文
	_, r2 := doJSON(t, srv, "PUT", "/api/assist/admin/config", "test-token",
		map[string]any{"key": "llm_api_key", "value": "sk-***56"})
	if r2["skipped"] != true {
		t.Fatalf("masked write should skip: %v", r2)
	}
}

// TestLLMTestEndpoint 测试连通端点：未接入时返回可读错误
func TestLLMTestEndpoint(t *testing.T) {
	srv := newTestServer(t)
	code, r := doJSON(t, srv, "POST", "/api/assist/admin/llm/test", "test-token", nil)
	if code != 200 || r["ok"] != false {
		t.Fatalf("llm test: %d %v", code, r)
	}
	if s, _ := r["error"].(string); s == "" {
		t.Fatal("error message empty")
	}
}

// TestSessionsPayload 会话统计含未答清单与 LLM 模式徽标（R0.2/R0.3）
func TestSessionsPayload(t *testing.T) {
	srv := newTestServer(t)
	code, r := doJSON(t, srv, "GET", "/api/assist/admin/sessions", "test-token", nil)
	if code != 200 {
		t.Fatalf("sessions: %d", code)
	}
	if _, ok := r["unanswered"].([]any); !ok {
		t.Fatalf("unanswered missing: %v", r)
	}
	if m, _ := r["llm_mode"].(string); m != "rule" {
		t.Fatalf("llm_mode: %v", r["llm_mode"])
	}
}
