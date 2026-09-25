// ============ stream_f29_test.go 职责中文说明 ============
// F-29 后端半（2026-09-25 批 D）chat 通道护栏的回归断言：
//   - 超长文本在 SSE 头写出**前**收 400 JSON（code=chat_text_too_long），
//     响应体不得出现 SSE 帧（data:）——修复文档纪律「拒绝必须在头前」；
//   - 上限键 chat_max_chars（system_config，默认 5,000）可调 + 非法回退；
//   - 判据边界等值锁（恰好等于上限放行 / 超 1 字符拒 / 尾部空白不计体积）；
//   - 错误码 HTTP 映射 = 400。
//
// ========================================
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
	"translator/internal/config"
	apierrors "translator/internal/errors"
	"translator/internal/iam"
	"translator/internal/store"
)

// f29Server 最小装配：内存 SQLite + 一个租户成员 JWT（Bill/Engine 不在本测试射程，
// 超长请求必须在触达它们之前就被拒绝）。
func f29Server(t *testing.T) (*Server, string) {
	t.Helper()
	pinSqliteDialect(t)
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(1, "uat_f29_user", auth.PasswordHash("pw123456"), "护栏探针", iam.RoleUser, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Sign(u, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{Store: st, Cfg: config.C}, tok
}

// f29PostChat 直调 handleChatStream，返回响应记录。
func f29PostChat(t *testing.T, s *Server, tok, message string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"message": message})
	r := httptest.NewRequest(http.MethodPost, "/api/chat/stream", strings.NewReader(string(body)))
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.handleChatStream(w, r)
	return w
}

// TestUATBatchD_ChatTextTooLongRejectedBeforeSSE 5,001 字符（默认上限 5,000）：
// 必须在 SSE 头之前收 400 结构化 JSON。改坏了会怎样：拒绝发生在头后 ⇒ 状态码恒 200、
// 前端把 JSON 错误当 SSE 流解析失败，退回 524/HTML 塞气泡的原缺陷形态。
func TestUATBatchD_ChatTextTooLongRejectedBeforeSSE(t *testing.T) {
	s, tok := f29Server(t)
	w := f29PostChat(t, s, tok, strings.Repeat("测", 5001))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("超长文本应 400（且在 SSE 头前），got %d body=%s", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if strings.Contains(ct, "text/event-stream") {
		t.Fatalf("拒绝发生在 SSE 头写出之后（ct=%s），客户端将按流解析错误体", ct)
	}
	bodyStr := w.Body.String()
	if strings.Contains(bodyStr, "data:") {
		t.Fatalf("400 响应体混入 SSE 帧：%q", bodyStr)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(bodyStr), &out); err != nil {
		t.Fatalf("400 非 JSON: %v body=%s", err, bodyStr)
	}
	if out["code"] != "chat_text_too_long" {
		t.Fatalf("错误码应为 chat_text_too_long（前端批 G 据此引导建工单），got %v", out["code"])
	}
	if out["success"] != false {
		t.Fatalf("success 必须为 false，got %v", out["success"])
	}
}

// TestUATBatchD_ChatMaxCharsConfigurableAndFallback 上限键 chat_max_chars 可配 + 非法回退 5,000。
func TestUATBatchD_ChatMaxCharsConfigurableAndFallback(t *testing.T) {
	s := f41StoreServer(t) // 同包复用：内存 SQLite Server 装配
	if got := s.chatMaxChars(); got != 5000 {
		t.Fatalf("缺省上限应为 5,000, got %d", got)
	}
	if err := s.Store.SetConfig("chat_max_chars", "200"); err != nil {
		t.Fatal(err)
	}
	if got := s.chatMaxChars(); got != 200 {
		t.Fatalf("上限未跟随配置, got %d", got)
	}
	for _, bad := range []string{"", "abc", "0", "-3"} {
		if err := s.Store.SetConfig("chat_max_chars", bad); err != nil {
			t.Fatal(err)
		}
		if got := s.chatMaxChars(); got != 5000 {
			t.Fatalf("非法配置 %q 应回退默认 5,000, got %d", bad, got)
		}
	}
	// 配置收紧后同一请求应被拒（证明上限真的接在判据上，而不是摆设）
	srv, tok := f29Server(t)
	if err := srv.Store.SetConfig("chat_max_chars", "10"); err != nil {
		t.Fatal(err)
	}
	if w := f29PostChat(t, srv, tok, strings.Repeat("字", 11)); w.Code != http.StatusBadRequest {
		t.Fatalf("上限=10 时 11 字请求应 400, got %d body=%s", w.Code, w.Body.String())
	}
	// 正向控制走纯判据（handler 内 10 字请求会越过护栏直达引擎，非本用例射程）：
	if chatTextOverLimit(strings.Repeat("字", 10), 10) {
		t.Fatal("恰好等于上限必须放行（不得 off-by-one 误拦）")
	}
}

// TestUATBatchD_ChatTextLimitBoundary 判据边界等值锁：等于上限放行、超 1 拒、trim 后计数。
func TestUATBatchD_ChatTextLimitBoundary(t *testing.T) {
	if chatTextOverLimit(strings.Repeat("a", 5000), 5000) {
		t.Fatal("恰好等于上限必须放行（F-29 决策项：上限 5,000 含边界）")
	}
	if !chatTextOverLimit(strings.Repeat("a", 5001), 5000) {
		t.Fatal("超 1 字符必须拒")
	}
	if chatTextOverLimit("  "+strings.Repeat("a", 5000)+"\n\t", 5000) {
		t.Fatal("首尾空白不计体积（TrimSpace 后判定）")
	}
	// 中文一字一符（rune 口径，不是字节口径——UTF-8 中文 3 字节，按字节会放大 3 倍误拦）
	if chatTextOverLimit(strings.Repeat("中", 5000), 5000) {
		t.Fatal("5,000 个汉字应放行（判据必须是 rune 而非 byte）")
	}
	if !chatTextOverLimit(strings.Repeat("中", 5001), 5000) {
		t.Fatal("5,001 个汉字必须拒")
	}
}

// TestUATBatchD_ChatTooLongCodeMapsTo400 错误码 HTTP 映射锁：400（非 500 兜底）。
func TestUATBatchD_ChatTooLongCodeMapsTo400(t *testing.T) {
	e := apierrors.New(apierrors.ErrChatTextTooLong, "探针")
	if e.HTTPStatus() != http.StatusBadRequest {
		t.Fatalf("chat_text_too_long 应映射 400, got %d", e.HTTPStatus())
	}
	if string(e.Code) != "chat_text_too_long" {
		t.Fatalf("码值漂移（前端/SDK 按该 code 分支）: %q", e.Code)
	}
}
