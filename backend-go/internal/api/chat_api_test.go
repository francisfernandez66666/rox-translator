// Package api 聊天对话历史接口单元测试（★ 〇-AM：工作台 SSE 翻译通道的后端存储）。
// 走真实 handler + httptest，覆盖鉴权、列表、详情、边界参数。
//
// ★ 单测自钉 sqlite 方言（AGENTS.md §一·4），避免 run_uat 的 PG 模式泄漏给同包内存库用例。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/api/ -run Chat
// ==========================================
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
	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// chatAPIFixture 聊天接口测试环境（一家企业租户 + 租户管理员 + 超管）。
type chatAPIFixture struct {
	srv      *Server
	raw      *sql.DB
	token    string // 租户管理员 JWT
	superTok string // 超管 JWT
	tid      int64
	userID   int64 // 租户管理员的 user ID（handler 用 authUser().ID 查询）
}

// newChatAPIFixture 建栈：内存 SQLite → store/tenant → 租户 → 账号 → JWT。
func newChatAPIFixture(t *testing.T) *chatAPIFixture {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })

	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	ten, err := tenant.NewStore(raw)
	if err != nil {
		t.Fatalf("创建 tenant.Store 失败: %v", err)
	}

	co, err := ten.Create("t_chat", "聊天测试公司", "", "{}")
	if err != nil {
		t.Fatalf("创建租户失败: %v", err)
	}
	if _, err := raw.Exec(`UPDATE tenants SET created_at=? WHERE id=?`,
		time.Now().Add(-31*24*time.Hour).Format(time.RFC3339), co.ID); err != nil {
		t.Fatalf("回拨注册时间失败: %v", err)
	}

	tadmin, err := st.CreateUser(co.ID, "chat_tadmin", "hash", "聊天租管", store.RoleTenantAdmin, 1, 0)
	if err != nil {
		t.Fatalf("创建租户管理员失败: %v", err)
	}
	tok, err := auth.Sign(tadmin, time.Hour)
	if err != nil {
		t.Fatalf("签发租管 JWT 失败: %v", err)
	}

	super, err := st.CreateUser(0, "chat_super", "hash", "聊天超管", store.RoleSuperAdmin, 0, 0)
	if err != nil {
		t.Fatalf("创建超管失败: %v", err)
	}
	stok, err := auth.Sign(super, time.Hour)
	if err != nil {
		t.Fatalf("签发超管 JWT 失败: %v", err)
	}

	return &chatAPIFixture{srv: &Server{Store: st, Ten: ten}, raw: raw, token: tok, superTok: stok, tid: co.ID, userID: tadmin.ID}
}

// call 发一个带 Bearer 的 JSON 请求。
func (f *chatAPIFixture) call(t *testing.T, method, path string, body interface{}, tok ...string) *httptest.ResponseRecorder {
	t.Helper()
	bearer := f.token
	if len(tok) > 0 {
		bearer = tok[0]
	}
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		b, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
	}
	// ★ 空字符串 = 不传 Authorization（匿名请求）；非空则带 Bearer 认证头。
	if bearer != "" {
		r.Header.Set("Authorization", "Bearer "+bearer)
	}
	rec := httptest.NewRecorder()
	f.srv.chatMux().ServeHTTP(rec, r)
	return rec
}

// decodeJSON 解析响应为 map（用例里按需取字段）。
func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("解析响应失败: %v (%s)", err, rec.Body.String())
	}
	return m
}

// chatMux 最小路由（注册本文件用到的两个 handler）。
func (s *Server) chatMux() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/chat/list", s.handleChatList)
	m.HandleFunc("/api/chat/messages", s.handleChatMessages)
	return m
}

// ──① handleChatList 鉴权──

// TestChatListAuthRequired 匿名请求必须返回 401（传空字符串 = 不发送 Authorization 头）。
func TestChatListAuthRequired(t *testing.T) {
	f := newChatAPIFixture(t)
	rec := f.call(t, http.MethodGet, "/api/chat/list", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("匿名访问应返回 401，实际 %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	if msg, _ := body["message"].(string); msg != "未登录" {
		t.Errorf("未登录提示应为「未登录」，实际 %q", msg)
	}
}

// ──② handleChatList 基本功能──

// TestChatListEmpty 无对话时返回列表为空数组。
func TestChatListEmpty(t *testing.T) {
	f := newChatAPIFixture(t)
	rec := f.call(t, http.MethodGet, "/api/chat/list", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("正常访问应返回 200，实际 %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	if body["success"] != true {
		t.Fatalf("success 应为 true，实际 %s", rec.Body.String())
	}
	data, ok := body["data"].([]any)
	if !ok || len(data) != 0 {
		t.Errorf("空用户列表应为 []，实际 %v", data)
	}
}

// TestChatListRoundTrip 写对话 → 读列表验证。
func TestChatListRoundTrip(t *testing.T) {
	f := newChatAPIFixture(t)
	st := f.srv.Store

	// 写入两条会话（使用登录用户的 ID，与 authUser().ID 一致）
	if _, err := st.CreateChatConversation("conv-list-001", f.userID, f.tid, "第一个会话"); err != nil {
		t.Fatalf("创建会话 001 失败: %v", err)
	}
	if _, err := st.CreateChatConversation("conv-list-002", f.userID, f.tid, "第二个会话"); err != nil {
		t.Fatalf("创建会话 002 失败: %v", err)
	}
	// 确保排序确定：把 conv-list-001 的 updated_at 显式回拨到一个远古字面量，造出「002 更新」的确定新旧差。
	//   ⚠️ 原写法用 `datetime(updated_at,'+1 second')`，但会话行的 updated_at 实际是 Go 侧写入的 RFC3339 文本
	//      （带 T／Z），SQLite 的 datetime() 只认 'YYYY-MM-DD HH:MM:SS'，吃不下这个格式 ⇒ 回拨退化成 NULL、
	//      DESC 下 NULL 沉底，把顺序整个反了。updated_at 按 TEXT 字典序比较，直接写一个更小的字面量最稳、且跨方言成立。
	if _, err := f.raw.Exec(`UPDATE chat_conversations SET updated_at=? WHERE id='conv-list-001'`, "2000-01-01T00:00:00Z"); err != nil {
		t.Fatalf("回拨 001 的 updated_at 失败: %v", err)
	}

	rec := f.call(t, http.MethodGet, "/api/chat/list?limit=10", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	data := body["data"].([]any)
	if len(data) != 2 {
		t.Fatalf("应有 2 条，实际 %d", len(data))
	}
	// 降序排列：后创建的在前
	first := data[0].(map[string]any)
	if first["id"] != "conv-list-002" || first["title"] != "第二个会话" {
		t.Errorf("第一条应为最新会话，实际 %+v", first)
	}
	last := data[1].(map[string]any)
	if last["id"] != "conv-list-001" {
		t.Errorf("第二条应为 conv-list-001，实际 %+v", last)
	}
}

// TestChatListLimitDefault 默认 limit=20，上限 100。
func TestChatListLimitDefault(t *testing.T) {
	f := newChatAPIFixture(t)
	st := f.srv.Store

	for i := 1; i <= 30; i++ {
		id := "conv-limit-" + string(rune(i+48))
		st.CreateChatConversation(id, f.userID, f.tid, "")
	}

	// 默认（无 limit 参数）→ 20
	rec := f.call(t, http.MethodGet, "/api/chat/list", nil)
	body := decodeJSON(t, rec)
	data := body["data"].([]any)
	if len(data) != 20 {
		t.Fatalf("默认 limit 应为 20，实际 %d", len(data))
	}

	// limit=5
	rec = f.call(t, http.MethodGet, "/api/chat/list?limit=5", nil)
	body = decodeJSON(t, rec)
	data = body["data"].([]any)
	if len(data) != 5 {
		t.Fatalf("limit=5 应得 5 条，实际 %d", len(data))
	}

	// limit=200 → 上限封顶 100（只有 30 条，返回 30）
	rec = f.call(t, http.MethodGet, "/api/chat/list?limit=200", nil)
	body = decodeJSON(t, rec)
	data = body["data"].([]any)
	if len(data) != 30 {
		t.Fatalf("limit=200 超过存量时应全返回，实际 %d", len(data))
	}
}

// ──③ handleChatMessages 鉴权──

// TestChatMessagesAuthRequired 匿名请求必须返回 401（传空字符串 = 不发送 Authorization 头）。
func TestChatMessagesAuthRequired(t *testing.T) {
	f := newChatAPIFixture(t)
	rec := f.call(t, http.MethodGet, "/api/chat/messages?id=xxx", nil, "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("匿名访问应返回 401，实际 %d", rec.Code)
	}
}

// ──④ handleChatMessages 基本功能──

// TestChatMessagesEmptyConv 不存在的 ID 应返回空消息。
func TestChatMessagesEmptyConv(t *testing.T) {
	f := newChatAPIFixture(t)
	rec := f.call(t, http.MethodGet, "/api/chat/messages?id=nonexistent", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("应返回 200，实际 %d", rec.Code)
	}
	body := decodeJSON(t, rec)
	data := body["data"].([]any)
	if len(data) != 0 {
		t.Fatalf("不存在的 ID 消息应为空，实际 %d", len(data))
	}
}

// TestChatMessagesMissingID 缺 id 参数应返回 400。
func TestChatMessagesMissingID(t *testing.T) {
	f := newChatAPIFixture(t)
	rec := f.call(t, http.MethodGet, "/api/chat/messages", nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("缺 id 应返回 400，实际 %d", rec.Code)
	}
}

// TestChatMessagesRoundTrip 写消息 → 读详情验证。
func TestChatMessagesRoundTrip(t *testing.T) {
	f := newChatAPIFixture(t)
	st := f.srv.Store

	conv, err := st.CreateChatConversation("conv-msg-001", f.tid+1, f.tid, "消息测试")
	if err != nil {
		t.Fatalf("创建会话失败: %v", err)
	}
	st.AppendChatMessage(conv.ID, "user", "你好吗？", "")
	st.AppendChatMessage(conv.ID, "assistant", "我很好，谢谢！", "")
	st.AppendChatMessage(conv.ID, "user", "翻译成法语", "")
	st.AppendChatMessage(conv.ID, "assistant", "Bonjour, comment ça va ?", "glm-4")

	// ── 默认 limit=100 全部取出 ──
	rec := f.call(t, http.MethodGet, "/api/chat/messages?id=conv-msg-001", nil)
	body := decodeJSON(t, rec)
	msgs := body["data"].([]any)
	if len(msgs) != 4 {
		t.Fatalf("应有 4 条消息，实际 %d", len(msgs))
	}

	// ── 顺序：正序 ├──
	first := msgs[0].(map[string]any)
	if first["role"] != "user" || first["content"] != "你好吗？" {
		t.Errorf("第一条应该是 user:你好吗？，实际 %+v", first)
	}
	last := msgs[3].(map[string]any)
	if last["role"] != "assistant" || last["content"] != "Bonjour, comment ça va ?" || last["model"] != "glm-4" {
		t.Errorf("最后一条不符，实际 %+v", last)
	}

	// ── limit 截断：只取最近 2 条 ──
	rec = f.call(t, http.MethodGet, "/api/chat/messages?id=conv-msg-001&limit=2", nil)
	body = decodeJSON(t, rec)
	msgs = body["data"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("limit=2 应得 2 条，实际 %d", len(msgs))
	}
	// 最近的还是正序（ID 大的后出现）
	if msgs[0].(map[string]any)["content"] != "翻译成法语" || msgs[1].(map[string]any)["content"] != "Bonjour, comment ça va ?" {
		t.Errorf("limit=2 正序不符: [%v, %v]", msgs[0], msgs[1])
	}

	// ── limit 上限 500 ──
	rec = f.call(t, http.MethodGet, "/api/chat/messages?id=conv-msg-001&limit=9999", nil)
	body = decodeJSON(t, rec)
	msgs = body["data"].([]any)
	if len(msgs) != 4 { // 上限是 500，但只有 4 条
		t.Fatalf("limit=9999 不应超过实际条数，实际 %d", len(msgs))
	}
}

// TestChatListAndMessagesDifferentUsers 不同用户的对话隔离。
func TestChatListAndMessagesDifferentUsers(t *testing.T) {
	f := newChatAPIFixture(t)
	st := f.srv.Store

	// 创建两个用户的会话
	st.CreateChatConversation("conv-userA", f.tid+1, f.tid, "A 的会话")
	st.AppendChatMessage("conv-userA", "user", "A 的消息", "")
	st.CreateChatConversation("conv-userB", f.tid+2, f.tid, "B 的会话")
	st.AppendChatMessage("conv-userB", "user", "B 的消息", "")

	// A 的列表只有自己的
	listA, _ := st.ListChatConversations(f.tid+1, f.tid, 10)
	listB, _ := st.ListChatConversations(f.tid+2, f.tid, 10)
	if len(listA) != 1 || listA[0].Title != "A 的会话" {
		t.Errorf("A 列表不符: %+v", listA)
	}
	if len(listB) != 1 || listB[0].Title != "B 的会话" {
		t.Errorf("B 列表不符: %+v", listB)
	}

	// 各取各自的消息
	msgsA, _ := st.GetChatMessages("conv-userA", 10)
	msgsB, _ := st.GetChatMessages("conv-userB", 10)
	if len(msgsA) != 1 && msgsA[0].Content != "A 的消息" {
		t.Fatalf("A 消息不符: %v", msgsA)
	}
	if len(msgsB) != 1 && msgsB[0].Content != "B 的消息" {
		t.Fatalf("B 消息不符: %v", msgsB)
	}
}
