// ============================================================================
// ★ H6 竞速路由测试：慢主路 → 次路对冲胜出；快主路 → 不打第二路（控成本）；
//
//	双败 → 返回错误交回降级链；流式 sink 存在 → 竞速不适用。
//
// ============================================================================
package engine

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/llm"
)

func h6Srv(delay time.Duration, tag string, hits *atomic.Int64) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		time.Sleep(delay)
		if tag == "" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":"%s"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, tag)
	}))
}

func h6Engine(t *testing.T) *Engine {
	t.Helper()
	old := config.C
	t.Cleanup(func() { config.C = old })
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite" // 防 UAT PG 矩阵 env 泄漏方言（本包测试不落 PG）
	cfg.HedgeEnabled = true
	cfg.HedgeDelayMs = 120
	config.C = cfg
	return &Engine{LLM: llm.NewClient(cfg), Cfg: cfg}
}

func h6Call(e *Engine, sec config.ProviderConfig, base string) (string, error) {
	msgs := []map[string]string{{"role": "user", "content": "hi"}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := e.hedgedChat(ctx, e.Cfg, base, "k", "m", msgs, 100, sec)
	return c, err
}

func TestH6HedgedSlowPrimaryLosesRace(t *testing.T) {
	e := h6Engine(t)
	var h1, h2 atomic.Int64
	slow := h6Srv(1500*time.Millisecond, "PRIMARY", &h1)
	defer slow.Close()
	fast := h6Srv(5*time.Millisecond, "SECONDARY", &h2)
	defer fast.Close()

	start := time.Now()
	got, err := h6Call(e, config.ProviderConfig{APIBase: fast.URL, APIKey: "k", Model: "m2"}, slow.URL)
	el := time.Since(start)
	if err != nil {
		t.Fatalf("hedge: %v", err)
	}
	if got != "SECONDARY" {
		t.Fatalf("应次路胜出: %s", got)
	}
	if el > 1200*time.Millisecond {
		t.Fatalf("未提前拿次路结果: %v", el)
	}
	if h1.Load() == 0 || h2.Load() == 0 {
		t.Fatalf("两路都应被发起: p=%d s=%d", h1.Load(), h2.Load())
	}
	if _, ok := e.routeStatsSnapshot()[fast.URL+"|m2"]; !ok {
		t.Fatal("次路统计未记录")
	}
}

func TestH6FastPrimarySkipsHedge(t *testing.T) {
	e := h6Engine(t)
	var h1, h2 atomic.Int64
	fast := h6Srv(5*time.Millisecond, "PRIMARY", &h1)
	defer fast.Close()
	slow := h6Srv(2000*time.Millisecond, "SECONDARY", &h2)
	defer slow.Close()

	got, err := h6Call(e, config.ProviderConfig{APIBase: slow.URL, APIKey: "k", Model: "m2"}, fast.URL)
	if err != nil || got != "PRIMARY" {
		t.Fatalf("快主路应直接胜出: %s %v", got, err)
	}
	if h2.Load() != 0 {
		t.Fatal("快主路场景不应发起对冲（控成本）")
	}
}

func TestH6BothFailReturnsError(t *testing.T) {
	e := h6Engine(t)
	var h1, h2 atomic.Int64
	// 主路「慢败」（>对冲延迟）触发次路；两路皆败 → 返回错误交回降级链
	bad := h6Srv(250*time.Millisecond, "", &h1)
	defer bad.Close()
	bad2 := h6Srv(5*time.Millisecond, "", &h2)
	defer bad2.Close()
	if _, err := h6Call(e, config.ProviderConfig{APIBase: bad2.URL, APIKey: "k", Model: "m2"}, bad.URL); err == nil {
		t.Fatal("双败应返回错误")
	}
	if h1.Load() == 0 || h2.Load() == 0 {
		t.Fatalf("两路都应尝试: %d %d", h1.Load(), h2.Load())
	}
}

func TestH6NotApplicableForStreaming(t *testing.T) {
	e := h6Engine(t)
	secs := []config.ProviderConfig{{APIBase: "http://x", APIKey: "k", Model: "m"}}
	if !e.hedgeApplicable(context.Background(), false, false, secs) {
		t.Fatal("非流式应适用竞速")
	}
	ctx := withStreamSinkInner(context.Background(), func(string) {})
	if e.hedgeApplicable(ctx, false, false, secs) {
		t.Fatal("流式禁用竞速（无法安全对冲已吐字节）")
	}
	if e.hedgeApplicable(context.Background(), true, false, secs) {
		t.Fatal("熔断开启禁用竞速")
	}
	old := config.C.HedgeEnabled
	config.C.HedgeEnabled = false
	if e.hedgeApplicable(context.Background(), false, false, secs) {
		t.Fatal("开关关闭禁用竞速")
	}
	config.C.HedgeEnabled = old
}
