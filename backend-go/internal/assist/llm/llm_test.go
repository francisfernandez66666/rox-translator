// llm_test.go — LLM 降级链单测：主模型失败降级备用、冷却熔断（httptest 假 OpenAI 端点）
package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// fakeOpenAI 假 /chat/completions 端点：failMain=true 时主模型永远 500
func fakeOpenAI(t *testing.T, failMain bool, mainCalls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model == "main-model" {
			if mainCalls != nil {
				mainCalls.Add(1)
			}
			if failMain {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"boom"}`))
				return
			}
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"fake-reply"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestChatFallbackChain 主模型 500 → 备用模型成功
func TestChatFallbackChain(t *testing.T) {
	var mains atomic.Int32
	srv := fakeOpenAI(t, true, &mains)
	c := New([]Provider{
		{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "main-model"},
		{Name: "backup", BaseURL: srv.URL, APIKey: "k", Model: "backup-model"},
	}, 5)
	text, model, usage, err := c.Chat(context.Background(), 0.5, 100, []Message{{Role: "user", Content: "hi"}})
	if err != nil || text != "fake-reply" {
		t.Fatalf("chat: %v %s", err, text)
	}
	if model != "backup-model" {
		t.Fatalf("should fall to backup, got %s", model)
	}
	if usage.TotalTokens != 15 {
		t.Fatalf("usage: %+v", usage)
	}
	if mains.Load() != 1 {
		t.Fatalf("main should be tried once, got %d", mains.Load())
	}
}

// TestChatPrimarySuccess 主模型正常时不走备用
func TestChatPrimarySuccess(t *testing.T) {
	srv := fakeOpenAI(t, false, nil)
	c := New([]Provider{
		{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "main-model"},
		{Name: "backup", BaseURL: srv.URL, APIKey: "k", Model: "backup-model"},
	}, 5)
	text, model, _, err := c.Chat(context.Background(), 0.5, 100, []Message{{Role: "user", Content: "hi"}})
	if err != nil || text != "fake-reply" || model != "main-model" {
		t.Fatalf("chat: %v %s %s", err, text, model)
	}
}

// TestCooldown 连续失败 3 次进冷却，冷却期内直接跳过（不发请求）
func TestCooldown(t *testing.T) {
	var mains atomic.Int32
	srv := fakeOpenAI(t, true, &mains)
	c := New([]Provider{{Name: "main", BaseURL: srv.URL, APIKey: "k", Model: "main-model"}}, 5)
	for i := 0; i < 3; i++ {
		_, _, _, err := c.Chat(context.Background(), 0.5, 100, []Message{{Role: "user", Content: "hi"}})
		if err == nil {
			t.Fatal("should fail")
		}
	}
	c.mu.RLock()
	cooling := c.coolUntil[0].After(time.Now())
	c.mu.RUnlock()
	if !cooling {
		t.Fatal("should be cooling after 3 fails")
	}
	before := mains.Load()
	_, _, _, _ = c.Chat(context.Background(), 0.5, 100, []Message{{Role: "user", Content: "hi"}})
	if mains.Load() != before {
		t.Fatal("cooling provider must not be called")
	}
}

// TestDisabled 无 provider 时报错
func TestDisabled(t *testing.T) {
	c := New(nil, 5)
	if c.Enabled() {
		t.Fatal("should be disabled")
	}
	_, _, _, err := c.Chat(context.Background(), 0.5, 100, nil)
	if err == nil {
		t.Fatal("expect error")
	}
}

// TestEndpointURLNormalizesPastedFullEndpoint 钉住 2026-09-29 生产「测试连通 http 404: Not Found」的修法。
// 现场：管理台把**整条接口地址**填进了 llm_base_url，客户端又在其后拼 /chat/completions，
// 于是打到 …/chat/completions/chat/completions ⇒ 上游网关回 404 纯文本 "Not Found"。
// 判据分两腿：① 纯函数归一表（含重复粘层、斜杠、embed 端互换）；
// ② 真发一次请求，钉「服务端收到的路径」——只测拼接会把「实际请求走别的路径」漏掉。
func TestEndpointURLNormalizesPastedFullEndpoint(t *testing.T) {
	cases := []struct{ in, path, want string }{
		{"https://api.siliconflow.cn/v1", "/chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		{"https://api.siliconflow.cn/v1/", "/chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		{"https://api.siliconflow.cn/v1/chat/completions", "/chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		{"https://api.siliconflow.cn/v1/chat/completions/", "/chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		// 重复粘两层也要收敛到唯一端点，而不是留下一条永远 404 的路径
		{"https://api.siliconflow.cn/v1/chat/completions/chat/completions", "/chat/completions", "https://api.siliconflow.cn/v1/chat/completions"},
		// 填了 chat 尾巴、本次调用是 embeddings：尾巴照样得剥掉
		{"https://api.siliconflow.cn/v1/chat/completions", "/embeddings", "https://api.siliconflow.cn/v1/embeddings"},
		{"https://api.siliconflow.cn/v1/embeddings", "/embeddings", "https://api.siliconflow.cn/v1/embeddings"},
	}
	for _, c := range cases {
		if got := endpointURL(c.in, c.path); got != c.want {
			t.Errorf("endpointURL(%q, %q) = %q, want %q", c.in, c.path, got, c.want)
		}
	}
	// 反证：归一函数不许把「路径里本来就含 chat/completions 字样」的正常前缀吃掉
	if got := endpointURL("https://gw.example.com/api/v1chat", "/chat/completions"); got != "https://gw.example.com/api/v1chat/chat/completions" {
		t.Errorf("正常前缀被误剥: %q", got)
	}

	// ② 真发一次请求，钉服务端收到的路径
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.URL.Path != "/v1/chat/completions" {
			// 复刻真实上游网关的形态：错路径回 404 + 纯文本 Not Found
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":1}}`))
	}))
	defer srv.Close()
	// 故意用「整条接口地址」这种填法（srv.URL + /v1/chat/completions）
	c := New([]Provider{{Name: "main", BaseURL: srv.URL + "/v1/chat/completions", APIKey: "k", Model: "m"}}, 5)
	text, _, _, err := c.Chat(context.Background(), 0, 8, []Message{{Role: "user", Content: "hi"}})
	if err != nil || text != "ok" {
		t.Fatalf("归一后仍打不通：path=%q err=%v text=%q", gotPath, err, text)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("服务端收到的路径不对：/v1/chat/completions != %q", gotPath)
	}
}
