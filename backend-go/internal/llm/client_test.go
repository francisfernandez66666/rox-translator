// ============ 本文件职责中文说明 ============
// LLM 客户端调用链单测（改造 3，2026-09-17）：补齐此前无测试覆盖的核心分支——
//   - CallChat 正常路径 / 429 / 401 / 非 200 / 坏 JSON / 空 choices / ctx 超时
//   - CallChatFallback 429→降级模型重试 / 非 429 不重试 / 等待期取消止损（D9）
//   - StreamChat SSE 首块解析与 usage 计费口径 / 缺 usage 判不可信（D20）/ 非流式端点拒绝
//   - UsageCollector / OnUsage 计费钩子
//   - ★ B1 观测面：prompt_tokens_details.cached_tokens 归集与流式 TTFT 样本（差值断言）
//
// 全部用 httptest 本地 mock 端点，不依赖外网。
// =============================================
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
)

// newTestClient 构建测试用 Client（独立 cfg，互不污染全局）。
func newTestClient(t *testing.T, mutate func(*config.Config)) *Client {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	return NewClient(cfg)
}

// chatOKBody 标准 200 响应体。
func chatOKBody(content, finish string) string {
	b, _ := json.Marshal(map[string]any{
		"choices": []map[string]any{
			{"message": map[string]any{"content": content}, "finish_reason": finish},
		},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
	return string(b)
}

// capturedReq 记录 mock 端点收到的请求体（解析 model 字段）。
func capturedModel(body []byte) string {
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	s, _ := m["model"].(string)
	return s
}

// TestCallChatOK 正常路径：返回 content 与 finish_reason，usage 进收集器与 OnUsage 钩子。
func TestCallChatOK(t *testing.T) {
	var hookedPrompt, hookedComp int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatOKBody("你好世界", "stop"))
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	c.OnUsage = func(ctx context.Context, model string, prompt, completion int64) error {
		hookedPrompt, hookedComp = prompt, completion
		return nil
	}
	uc := &UsageCollector{}
	ctx := WithUsageCollector(context.Background(), uc)

	content, finish, err := c.CallChat(ctx, srv.URL+"/v1", "sk-test", "m1",
		[]map[string]string{{"role": "user", "content": "hi"}}, 128, false, 0.3)
	if err != nil {
		t.Fatalf("正常调用不应报错: %v", err)
	}
	if content != "你好世界" || finish != "stop" {
		t.Fatalf("返回值不符: content=%q finish=%q", content, finish)
	}
	if p, comp := uc.Totals(); p != 11 || comp != 7 {
		t.Fatalf("收集器 usage 不符: prompt=%d completion=%d", p, comp)
	}
	if hookedPrompt != 11 || hookedComp != 7 {
		t.Fatalf("OnUsage 钩子未收到用量: %d/%d", hookedPrompt, hookedComp)
	}
}

// TestCallChat429StatusError 429 返回类型化 StatusError（D15）且 isRateLimit 判真。
func TestCallChat429StatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 8, false, 0.1)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 429 {
		t.Fatalf("期望 StatusError{429}，得到: %v", err)
	}
	if !isRateLimit(err) {
		t.Fatalf("429 应判定为限流")
	}
}

// TestCallChat401 401 返回密钥无效错误。
func TestCallChat401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "bad", "m", nil, 8, false, 0.1)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("期望 401 错误，得到: %v", err)
	}
}

// TestCallChatNon200 其他非 200 抛 StatusError 并携带响应体摘要。
func TestCallChatNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(502)
		_, _ = io.WriteString(w, "upstream gone")
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 8, false, 0.1)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 502 || !strings.Contains(se.Body, "upstream gone") {
		t.Fatalf("期望 StatusError{502, body 摘要}，得到: %v", err)
	}
	if isRateLimit(err) {
		t.Fatalf("502 不应判为限流")
	}
}

// TestCallChatBadJSON 200 但响应非法 JSON → 解析错误。
func TestCallChatBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "not-json{{{")
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 8, false, 0.1)
	if err == nil || !strings.Contains(err.Error(), "解析响应") {
		t.Fatalf("期望解析错误，得到: %v", err)
	}
}

// TestCallChatNoChoices 200 但 choices 为空 → 无 choices 错误。
func TestCallChatNoChoices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 8, false, 0.1)
	if err == nil || !strings.Contains(err.Error(), "无 choices") {
		t.Fatalf("期望无 choices 错误，得到: %v", err)
	}
}

// TestCallChatCtxTimeout ctx 超时 → 调用失败而非挂死。
func TestCallChatCtxTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = io.WriteString(w, chatOKBody("late", "stop"))
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, _, err := c.CallChat(ctx, srv.URL+"/v1", "k", "m", nil, 8, false, 0.1)
	if err == nil {
		t.Fatalf("期望超时错误")
	}
}

// TestCallChatFallback429Retry 首调 429 → 触发回调 → 等待后以 HunyuanFallbackModel 重试成功。
func TestCallChatFallback429Retry(t *testing.T) {
	var calls atomic.Int32
	var secondModel atomic.Value // string：第二次请求的 model 字段
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			w.WriteHeader(429)
			return
		}
		secondModel.Store(capturedModel(body))
		_, _ = io.WriteString(w, chatOKBody("fallback ok", "stop"))
	}))
	defer srv.Close()

	var cbCount atomic.Int32
	c := newTestClient(t, func(cfg *config.Config) { cfg.HunyuanFallbackModel = "fallback-model-x" })

	content, _, err := c.CallChatFallback(context.Background(), srv.URL+"/v1", "k", "primary-m",
		[]map[string]string{{"role": "user", "content": "hi"}}, 8, 0.1,
		func() { cbCount.Add(1) })
	if err != nil {
		t.Fatalf("降级重试后应成功: %v", err)
	}
	if content != "fallback ok" {
		t.Fatalf("降级结果不符: %q", content)
	}
	if cbCount.Load() != 1 {
		t.Fatalf("onRateLimited 应恰好触发 1 次，实际 %d", cbCount.Load())
	}
	if m, _ := secondModel.Load().(string); m != "fallback-model-x" {
		t.Fatalf("重试应用降级模型，实际: %q", m)
	}
}

// TestCallChatFallbackNonRateLimitNoRetry 非 429 失败不触发降级重试（只调一次）。
func TestCallChatFallbackNonRateLimitNoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.CallChatFallback(context.Background(), srv.URL+"/v1", "k", "m", nil, 8, 0.1,
		func() { t.Fatalf("非 429 不应触发 onRateLimited") })
	if err == nil {
		t.Fatalf("期望错误返回")
	}
	if calls.Load() != 1 {
		t.Fatalf("非 429 不应重试，实际调用 %d 次", calls.Load())
	}
}

// TestCallChatFallbackCancelDuringWait 429 后等待期 ctx 取消 → 立即返回原始错误（D9 止损）。
func TestCallChatFallbackCancelDuringWait(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := newTestClient(t, nil)
	_, _, err := c.CallChatFallback(ctx, srv.URL+"/v1", "k", "m", nil, 8, 0.1,
		func() { cancel() }) // 回调里取消 → sleepCtx 提前返回 false，不再重试
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 429 {
		t.Fatalf("等待期取消应保留原始 429 错误，得到: %v", err)
	}
}

// sseResponse 构造标准 SSE 流（两块增量 + 结束块带 usage + [DONE]）。
func sseResponse(w http.ResponseWriter, withUsage bool) {
	w.Header().Set("Content-Type", "text/event-stream")
	f := w.(http.Flusher)
	chunk := func(data string) {
		_, _ = io.WriteString(w, "data: "+data+"\n\n")
		f.Flush()
	}
	chunk(`{"choices":[{"delta":{"content":"你好"}}]}`)
	chunk(`{"choices":[{"delta":{"content":"，世界"},"finish_reason":null}]}`)
	last := `{"choices":[{"delta":{},"finish_reason":"stop"}]`
	if withUsage {
		last += `,"usage":{"prompt_tokens":5,"completion_tokens":3}`
	}
	chunk(last + "}")
	_, _ = io.WriteString(w, "data: [DONE]\n\n")
	f.Flush()
}

// TestStreamChatOK SSE 正常路径：增量拼接完整、finish_reason、usage 计费。
func TestStreamChatOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, true)
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	uc := &UsageCollector{}
	ctx := WithUsageCollector(context.Background(), uc)
	var deltas []string

	full, finish, err := c.StreamChat(ctx, srv.URL+"/v1", "k", "m", nil, 64, 0.3, func(s string) { deltas = append(deltas, s) })
	if err != nil {
		t.Fatalf("流式调用不应报错: %v", err)
	}
	if full != "你好，世界" || finish != "stop" {
		t.Fatalf("流式结果不符: full=%q finish=%q", full, finish)
	}
	if len(deltas) != 2 {
		t.Fatalf("onDelta 应收到 2 个增量，实际 %d", len(deltas))
	}
	if p, comp := uc.Totals(); p != 5 || comp != 3 {
		t.Fatalf("流式 usage 收集不符: %d/%d", p, comp)
	}
}

// TestStreamChatNoUsage 判不可信端点：SSE 缺 usage → errNoStreamUsage（调用方回退非流式）。
func TestStreamChatNoUsage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sseResponse(w, false)
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.StreamChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 64, 0.3, nil)
	if !errors.Is(err, errNoStreamUsage) {
		t.Fatalf("期望 errNoStreamUsage，得到: %v", err)
	}
}

// TestStreamChatNonSSE JSON 端点（不支持流式）→ 明确报错供调用方回退 CallChat。
func TestStreamChatNonSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, chatOKBody("x", "stop"))
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.StreamChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 64, 0.3, nil)
	if err == nil || !strings.Contains(err.Error(), "不支持流式") {
		t.Fatalf("期望不支持流式错误，得到: %v", err)
	}
}

// TestCallChat429SSE 流式端点 429 同样抛类型化限流错误。
func TestCallChat429SSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer srv.Close()

	c := newTestClient(t, nil)
	_, _, err := c.StreamChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 64, 0.3, nil)
	var se *StatusError
	if !errors.As(err, &se) || se.Code != 429 {
		t.Fatalf("流式 429 应为 StatusError，得到: %v", err)
	}
}

// ============ ★ B1 观测面（prompt 缓存命中 + 流式 TTFT，2026-09-19） ============
// 计数是进程级原子量（同包其他用例会并发累加），断言一律用「调用前后差值」。

// TestObservabilityCachedTokensNonStream 非流式：usage.prompt_tokens_details.cached_tokens
// 应进观测计数，且不影响 OnUsage 计费口径（仍按 prompt/completion 原值回调）。
func TestObservabilityCachedTokensNonStream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],
		  "usage":{"prompt_tokens":1200,"completion_tokens":30,"prompt_tokens_details":{"cached_tokens":1024}}}`)
	}))
	defer srv.Close()

	before := Observability()
	c := newTestClient(t, nil)
	var hookedPrompt, hookedComp int64
	c.OnUsage = func(ctx context.Context, model string, p, comp int64) error {
		hookedPrompt, hookedComp = p, comp
		return nil
	}
	if _, _, err := c.CallChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 8, false, 0.1); err != nil {
		t.Fatalf("调用不应报错: %v", err)
	}
	after := Observability()
	if dp, dc := after.PromptTokens-before.PromptTokens, after.CachedTokens-before.CachedTokens; dp != 1200 || dc != 1024 {
		t.Fatalf("观测计数差值不符: prompt+%d cached+%d（期望 1200/1024）", dp, dc)
	}
	if hookedPrompt != 1200 || hookedComp != 30 {
		t.Fatalf("计费钩子口径不应被观测改动: %d/%d", hookedPrompt, hookedComp)
	}
}

// TestObservabilityStreamTTFT 流式：完成调用计数 +1、TTFT 样本 +1、cached 归集。
func TestObservabilityStreamTTFT(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"He\"}}]}\n\n")
		f.Flush()
		time.Sleep(20 * time.Millisecond) // 拉开首 token 与后续块的间隔，TTFT 样本可测
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"llo\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"usage\":{\"prompt_tokens\":110,\"completion_tokens\":2,\"prompt_tokens_details\":{\"cached_tokens\":96}}}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
		f.Flush()
	}))
	defer srv.Close()

	before := Observability()
	c := newTestClient(t, nil)
	if _, _, err := c.StreamChat(context.Background(), srv.URL+"/v1", "k", "m", nil, 8, 0.1, nil); err != nil {
		t.Fatalf("流式调用不应报错: %v", err)
	}
	after := Observability()
	if after.StreamCalls-before.StreamCalls != 1 {
		t.Fatalf("流式完成计数应 +1，实际 +%d", after.StreamCalls-before.StreamCalls)
	}
	if dp, dc := after.PromptTokens-before.PromptTokens, after.CachedTokens-before.CachedTokens; dp != 110 || dc != 96 {
		t.Fatalf("流式观测归集不符: prompt+%d cached+%d（期望 110/96）", dp, dc)
	}
	// TTFT 每调用恰记 1 个样本（首个非空 delta 触发）；均值为全局量不做精确断言
	if after.TtftSamples-before.TtftSamples != 1 {
		t.Fatalf("TTFT 样本应 +1，实际 +%d", after.TtftSamples-before.TtftSamples)
	}
}
