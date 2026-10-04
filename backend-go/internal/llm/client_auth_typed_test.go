// ============================================================================
// client_auth_typed_test.go — ★ 修法 E 的 llm 侧断言（A6 前半，2026-10-04 〇-AR 第 2 波）。
//
// 钉住的历史缺陷：client.go 对 401 抛的是**裸 fmt.Errorf("api key 无效 (401)")**，
// 而下游两个判定只认 errors.As 的 *StatusError：
//  1. engine.isServerError（P1-6 明确写着「5xx/401 一并纳入降级与熔断」）——
//     于是主键错时备用供应商**一次都没被拨**，"多供应商降级"在鉴权失败这一族上是死腿；
//  2. 重试环无法识别「这是认证类失败」⇒ 同一张废键被打满「语种 × 3 轮」。
//
// 本文件钉的是"类型化＋判定"这一半，engine 侧的"不再重试"那一半见
// internal/engine/auth_retry_test.go 的 TestAuthErrorIsTypedAndNotRetried。
//
// ⚠️ 另一条必须同时钉住的东西：**日志字面量**。A9 的发版验收脚本
// （deploy/check_upstream_401.sh）grep 的就是 `api key 无效 (401)` 这句——
// 类型化如果把文案一起改掉，门禁会永远绿灯地扫不到真 401（假绿），所以这里断言前缀等值。
//
// 反证：把 401 改回 `return "", "", fmt.Errorf("api key 无效 (401)")` ⇒ 本文件三条全红；
// 把 StatusError.Error() 的认证分支删掉 ⇒ 前缀等值锁红。
//
// 方言口径（AGENTS §一·4）：本包不建库，无需自钉 DB_DRIVER。
// ============================================================================
package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authStatusServer 起一个恒回指定状态码＋固定错误体的假上游。
// 参数 code: HTTP 状态码；body: 供应商那侧的原文（用于验证「摘要进 Body、但不顶掉文案前缀」）。
func authStatusServer(t *testing.T, code int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// auth401Literal ★ 修法 E 的字面量常量：与 deploy/check_upstream_401.sh 的默认 BAD_TEXT 同源。
// 这里写成常量而不是内联串，是为了让「两边改一边必红」的保鲜锁只有一处可读（见 engine 侧同步测试）。
const auth401Literal = "api key 无效 (401)"

// TestCallChat401ReturnsTypedAuthError A6①：同步腿的 401 必须是**类型化** StatusError，
// 且 Auth 标记为真、文案前缀保持历史字面量（门禁 grep 的锚点）。
func TestCallChat401ReturnsTypedAuthError(t *testing.T) {
	srv := authStatusServer(t, 401, `{"error":{"message":"invalid api key"}}`)
	c := newTestClient(t, nil)
	_, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "sk-bad", "m", nil, 8, false, 0.1)
	if err == nil {
		t.Fatal("401 必须报错，不能当成功返回")
	}
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("401 必须是 *StatusError（旧形态的裸 fmt.Errorf 让 errors.As 落空，"+
			"于是 P1-6 的备用供应商降级在鉴权失败上恒不触发）: %T %v", err, err)
	}
	if se.Code != 401 || !se.Auth {
		t.Fatalf("StatusError 形态不符: code=%d auth=%v", se.Code, se.Auth)
	}
	if !strings.HasPrefix(se.Error(), auth401Literal) {
		t.Fatalf("401 文案前缀必须逐字保留 %q（发版验收脚本按它 grep 日志窗口），实得 %q",
			auth401Literal, se.Error())
	}
	if !strings.Contains(se.Error(), "invalid api key") {
		t.Fatalf("供应商原文应跟在摘要里供排障，实得 %q", se.Error())
	}
	if !IsAuthError(err) {
		t.Fatal("IsAuthError 必须认 401")
	}
	// 反向：401 不是限流（限流该退避重试，鉴权失败不该）。
	if isRateLimit(err) {
		t.Fatal("401 不得被判成限流：那会让重试环继续打同一张废键")
	}
}

// TestStreamChat401ReturnsTypedAuthError A6①′：流式腿与同步腿同一口径。
// 对话面（/api/chat/stream）走的正是这条腿，只修同步腿等于「面板绿了、聊天气泡还在三试」。
func TestStreamChat401ReturnsTypedAuthError(t *testing.T) {
	srv := authStatusServer(t, 401, `{"error":{"message":"invalid api key"}}`)
	c := newTestClient(t, nil)
	_, _, err := c.StreamChat(context.Background(), srv.URL+"/v1", "sk-bad", "m",
		[]map[string]string{{"role": "user", "content": "hi"}}, 8, 0.1, nil)
	if err == nil {
		t.Fatal("流式 401 必须报错")
	}
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 401 || !se.Auth {
		t.Fatalf("流式 401 也必须是类型化 StatusError{401, Auth:true}，实得 %T %v", err, err)
	}
	if !strings.HasPrefix(se.Error(), auth401Literal) {
		t.Fatalf("流式腿文案前缀不符，实得 %q", se.Error())
	}
	if !IsAuthError(err) {
		t.Fatal("IsAuthError 必须认流式 401")
	}
}

// TestCallChat403IsAuthError 403（密钥有效但无权限）与 401 同族：重试同样没有意义。
func TestCallChat403IsAuthError(t *testing.T) {
	srv := authStatusServer(t, 403, `{"error":{"message":"model not authorized"}}`)
	c := newTestClient(t, nil)
	_, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "sk-ok", "m", nil, 8, false, 0.1)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 403 || !se.Auth {
		t.Fatalf("403 应判为认证类类型化错误，实得 %T %v", err, err)
	}
	if !strings.HasPrefix(se.Error(), "api key 无访问权限 (403)") {
		t.Fatalf("403 文案不符，实得 %q", se.Error())
	}
	if !IsAuthError(err) {
		t.Fatal("IsAuthError 必须认 403")
	}
}

// TestIsAuthErrorDoesNotClaimOtherFailures 判定窄档：429/5xx/普通错误**都不算**认证类。
// 这一条是"不许把止损改成止损过度"的锁——429 与超时的正确解法就是退避重试，
// 若它们被误判成认证类，重试环会当场短路，偶发限流就直接变成缺语种（老缺陷回归）。
func TestIsAuthErrorDoesNotClaimOtherFailures(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantAuth bool
	}{
		{"nil", nil, false},
		{"429 限流", &StatusError{Code: 429}, false},
		{"500 上游故障", &StatusError{Code: 500, Body: "boom"}, false},
		{"503 但误置 Auth", &StatusError{Code: 503}, false},
		{"解析错误", fmt.Errorf("解析响应: %w", errors.New("bad json")), false},
		{"包装过的 401", fmt.Errorf("翻译失败: %w", &StatusError{Code: 401, Auth: true}), true},
		{"裸码 401（Auth 未置也要认）", &StatusError{Code: 401}, true},
		{"裸码 403（Auth 未置也要认）", &StatusError{Code: 403}, true},
	}
	for _, tc := range cases {
		got := IsAuthError(tc.err)
		if got != tc.wantAuth {
			t.Errorf("%s: IsAuthError=%v，期望 %v（err=%v）", tc.name, got, tc.wantAuth, tc.err)
		}
	}
	// 429 的归一化文案不能被认证分支吃掉（限流识别按 rate_limited 前缀工作）。
	if s := (&StatusError{Code: 429}).Error(); s != "rate_limited: HTTP 429" {
		t.Errorf("429 文案被改动，实得 %q", s)
	}
	// 5xx 仍带响应体摘要（排障抓手，不能被认证分支改成"密钥无效"这种错归因）。
	if s := (&StatusError{Code: 500, Body: "upstream gone"}).Error(); !strings.Contains(s, "upstream gone") {
		t.Errorf("5xx 文案丢了响应体摘要，实得 %q", s)
	}
}

// TestAuthErrorEmptyBodyKeepsLiteralOnly 供应商回空体时（现网真有这种：网关直接掐断）
// 文案必须恰好是那句字面量，不带尾部空冒号——门禁按 -F 子串匹配，多一个字符不影响判定，
// 但「api key 无效 (401): 」这种残尾会出现在日志里让运维误判体被吞了。
func TestAuthErrorEmptyBodyKeepsLiteralOnly(t *testing.T) {
	if s := (&StatusError{Code: 401, Auth: true, Body: "   "}).Error(); s != auth401Literal {
		t.Fatalf("空体时文案应恰为字面量，实得 %q", s)
	}
}
