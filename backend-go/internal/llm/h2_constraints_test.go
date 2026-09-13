// ============================================================================
// H2 术语约束解码（双轨·事前轨）单测：客户端 SupportsConstraints + ctx 注入 →
// 请求体含 x_term_constraints；开关关闭/无约束时不含该字段（降级 H1）。
// ============================================================================
package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"translator/internal/config"
)

func h2TestClient(t *testing.T, got map[string]func(body map[string]interface{})) (*Client, *httptest.Server) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var m map[string]interface{}
		_ = json.Unmarshal(body, &m)
		for _, f := range got {
			f(m)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ROX unveils"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":4}}`))
	}))
	c := NewClient(config.Default())
	return c, srv
}

func TestH2TermConstraintsInjection(t *testing.T) {
	var captured map[string]interface{}
	c, srv := h2TestClient(t, map[string]func(map[string]interface{}){"cap": func(m map[string]interface{}) { captured = m }})
	defer srv.Close()
	ctx := WithTermConstraints(context.Background(), []TermConstraint{{Source: "极石", Target: "ROX"}})
	msgs := []map[string]string{{"role": "user", "content": "极石发布新车"}}

	// 开关关闭（默认）→ 不注入（H1 降级轨道）
	_, _, err := c.CallChat(ctx, srv.URL+"/v1", "k", "m", msgs, 100, false, 0.3)
	if err != nil {
		t.Fatalf("CallChat(关闭): %v", err)
	}
	if _, has := captured["x_term_constraints"]; has {
		t.Fatal("SupportsConstraints=false 不应注入 x_term_constraints")
	}

	// 开关开启 → 注入且内容正确
	c.SupportsConstraints = true
	_, _, err = c.CallChat(ctx, srv.URL+"/v1", "k", "m", msgs, 100, false, 0.3)
	if err != nil {
		t.Fatalf("CallChat(开启): %v", err)
	}
	raw, has := captured["x_term_constraints"]
	if !has {
		t.Fatal("SupportsConstraints=true 应注入 x_term_constraints")
	}
	arr, _ := raw.([]interface{})
	if len(arr) != 1 {
		t.Fatalf("约束条数错误: %v", raw)
	}
	item := arr[0].(map[string]interface{})
	if item["source"] != "极石" || item["target"] != "ROX" {
		t.Fatalf("约束内容错误: %v", item)
	}

	// 开启但 ctx 无约束 → 不注入
	_, _, err = c.CallChat(context.Background(), srv.URL+"/v1", "k", "m", msgs, 100, false, 0.3)
	if err != nil {
		t.Fatalf("CallChat(无约束): %v", err)
	}
	if _, has := captured["x_term_constraints"]; has {
		t.Fatal("ctx 无约束不应注入")
	}
}
