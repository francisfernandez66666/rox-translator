// ============ llm/embed_test.go · 职责说明 ============
// Embed 嵌入调用单测（httptest 假 /embeddings 端点）：协议形态（OpenAI 兼容、
// Bearer 鉴权）、L2 归一化正确性、以及各失败路径必须显式报错（供 engine 侧降级），
// 绝不 panic。分级召回第 3 级的传输层依赖即此文件守护的行为。
// =============================================
package llm

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newEmbedClient 指向假嵌入端点的单 provider 客户端
func newEmbedClient(url string) *Client {
	return New([]Provider{{Name: "main", BaseURL: url, APIKey: "sk-t", Model: "chat-m"}}, 5)
}

func TestEmbedOK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Errorf("路径应为 OpenAI 兼容 /embeddings: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-t" {
			t.Errorf("鉴权头: %s", got)
		}
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Model != "bge-m3" || len(req.Input) != 2 {
			t.Errorf("请求体: %+v", req)
		}
		resp := map[string]any{"data": []any{
			map[string]any{"embedding": []float64{3, 4}}, // 模长 5 → 归一 (0.6,0.8)
			map[string]any{"embedding": []float64{0, 0}}, // 零向量：除零防护样本，须原样返回不 NaN
		}}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	vecs, err := newEmbedClient(srv.URL+"/v1").Embed(context.Background(), "bge-m3", []string{"甲", "乙"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 2 {
		t.Fatalf("向量形态: %v", vecs)
	}
	if math.Abs(float64(vecs[0][0])-0.6) > 1e-5 || math.Abs(float64(vecs[0][1])-0.8) > 1e-5 {
		t.Fatalf("L2 归一化错误: %v", vecs[0])
	}
	// 零向量不得 NaN：原样返回
	if vecs[1][0] != 0 || vecs[1][1] != 0 {
		t.Fatalf("零向量应原样返回: %v", vecs[1])
	}
}

func TestEmbedErrors(t *testing.T) {
	// 无 provider → 显式 error（engine 侧据此绕过第 3 级）
	if _, err := New(nil, 5).Embed(context.Background(), "m", []string{"x"}); err == nil {
		t.Fatal("no provider should error")
	}
	// 模型名缺失 → error
	if _, err := New([]Provider{{Name: "a", BaseURL: "http://x", APIKey: "k", Model: "m"}}, 5).
		Embed(context.Background(), "", []string{"x"}); err == nil {
		t.Fatal("empty model should error")
	}
	// HTTP 500 → error 不 panic
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := newEmbedClient(srv.URL).Embed(context.Background(), "bge-m3", []string{"x"}); err == nil {
		t.Fatal("http 500 should error")
	}
	// 空 data → error
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv2.Close()
	if _, err := newEmbedClient(srv2.URL).Embed(context.Background(), "bge-m3", []string{"x"}); err == nil {
		t.Fatal("empty data should error")
	}
}
