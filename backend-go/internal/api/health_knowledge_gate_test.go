// ============ health_knowledge_gate_test.go · 职责说明 ============
// 四期 W3 KN9 反证：主服务 /api/health 必须带「knowledge」段，且该段必须反映知识服务真实状态，
// 绝不允许硬编码成 ok（否则「知识服务挂了主服务还全绿」那类事故就探不出来）。
//
// 反证由变异体 M0291 提供：把 knowledgeStatus 里的 contract.ClassifyHealth(...) 改成恒 HealthOK，
// 本测试的「版本不匹配⇒degraded」断言即判红。
// =============================================
package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"translator/internal/knowledge"
	"translator/internal/knowledge/contract"
)

// TestHealthKnowledgeSegmentPresent KN9：/api/health 必须含 knowledge 段（知识服务可见）。
func TestHealthKnowledgeSegmentPresent(t *testing.T) {
	s := &Server{KnowledgeClient: &knowledge.Client{
		BaseURL:     "http://127.0.0.1:1", // 不可达地址
		Token:       "x",
		HTTPClient:  &http.Client{Timeout: 150 * time.Millisecond},
	}}
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handleHealth 应 200，实际 %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("无法解析健康面 JSON: %v", err)
	}
	seg, ok := body["knowledge"].(map[string]interface{})
	if !ok {
		t.Fatalf("KN9 主服务 /api/health 缺少 knowledge 段（知识服务不可见）")
	}
	// 不可达 ⇒ 不能报 ok（探测被改坏的典型形态）。
	if st, _ := seg["status"].(string); st == contract.HealthOK {
		t.Fatalf("KN9 知识服务不可达却报 ok（探针被改坏）")
	}
}

// TestHealthKnowledgeReflectsReality KN9：可达但契约版本不匹配必须判 degraded，而非 ok。
func TestHealthKnowledgeReflectsReality(t *testing.T) {
	// 起一个「假知识服务」：返回 200 但契约版本对不上（模拟半通/版本漂移）。
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok","version":"knowledge-w3","contract_version":"WRONG-VERSION"}`))
	}))
	defer fake.Close()

	s := &Server{KnowledgeClient: &knowledge.Client{
		BaseURL:    fake.URL,
		Token:      "x",
		HTTPClient: &http.Client{Timeout: 150 * time.Millisecond},
	}}
	rec := httptest.NewRecorder()
	s.handleHealth(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("handleHealth 应 200，实际 %d", rec.Code)
	}
	var body map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("无法解析健康面 JSON: %v", err)
	}
	seg, ok := body["knowledge"].(map[string]interface{})
	if !ok {
		t.Fatalf("KN9 主服务 /api/health 缺少 knowledge 段")
	}
	st, _ := seg["status"].(string)
	if st != contract.HealthDegraded {
		t.Fatalf("KN9 契约版本不匹配应判 degraded，实际 %q（探针被改坏）", st)
	}
}
