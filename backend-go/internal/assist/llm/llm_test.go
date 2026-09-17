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
