// ============================================================================
// health_llmkey_test.go — ★ R-1 修法 B 的健康面断言（A3 的第三腿，2026-10-04）。
//
// 钉住的历史缺陷：全局 LLM Key 是随机占位符时，三件事同时成立——
// 启动日志只有一条 INFO、/api/health 恒回 status:"ok"、每一次真调用都在打 401。
// 于是「配错了」在现网的表征是「服务健康、客户面坏了」，排障方向从第一天就是错的。
// 修法 B 把这一状态词挂到 /api/health 上（`llm_global_key`: ok / placeholder），
// 让监控与发版验收能直接对字段告警，不必再翻启动日志。
//
// 两条判据方向必须同时锁住（AGENTS §一·5 的「负向锁配正向对照」口径）：
//
//	① 正向：字段恒存在，且**随构造期来源标志翻转**（placeholder ⇔ ok）——
//	   否则一个写死的字符串也能骗过"字段在"这种弱判据；
//	② 负向：本端点匿名可达，所以响应体里**不许出现**密钥片段、供应商域名、
//	   API 基地址这些可定位坐标。为了让这条负向不是空转，用例把真值先放进 Cfg，
//	   再断言它没漏出去（"库里根本没这个值"式的负向锁＝白锁）。
//
// 反证：llmGlobalKeyState 恒回 "ok" ⇒ ①红；把 Cfg.OnlineAPIKey 原样写进响应 ⇒ ②红。
//
// 方言口径（AGENTS §一·4）：经 pinSqliteDialect 自钉内存 SQLite。
// ============================================================================
package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/config"
)

// llmKeySentinelKey 一条"看着像真 Key"的哨兵值：只用于验证它不会出现在匿名响应里。
// 刻意不是任何真实凭据（本机/CI 都可跑，且永远不会进日志或 git）。
const (
	llmKeySentinelKey  = "sk-SENTINEL-NOT-A-REAL-KEY-8f2c61"
	llmKeySentinelBase = "https://api.siliconflow-sentinel.example/v1"
	llmKeySentinelHost = "siliconflow-sentinel.example"
)

// callHealth 匿名打一次 /api/health 并返回原始响应体。
func callHealth(t *testing.T, s *Server) (int, string) {
	t.Helper()
	// Engine/Kb/Redis 都不装配：本用例只关心全局 Key 状态词，坏境越差越能验证不 panic
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	s.handleHealth(rec, req)
	return rec.Code, rec.Body.String()
}

// TestHealthLLMKeyStateWordFlipsWithSourceFlag 状态词随构造期来源标志翻转，且字段恒存在。
func TestHealthLLMKeyStateWordFlipsWithSourceFlag(t *testing.T) {
	cases := []struct {
		name string
		cfg  *config.Config
		want string
	}{
		{"占位 Key（R-1 现网形态）", &config.Config{OnlineAPIKey: "sk-placeholder-random", OnlineAPIKeyIsPlaceholder: true}, "placeholder"},
		{"真 Key", &config.Config{OnlineAPIKey: llmKeySentinelKey, OnlineAPIBase: llmKeySentinelBase}, "ok"},
		{"空 Key（未配置）", &config.Config{}, "placeholder"},
		{"Cfg 未装配（启动早期）", nil, "placeholder"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		pinSqliteDialect(t)
		s := &Server{Cfg: c.cfg}
		code, body := callHealth(t, s)
		if code != http.StatusOK {
			t.Fatalf("%s: 匿名健康面应 200，实际 %d", c.name, code)
		}
		state, ok := jsonFieldString(body, "llm_global_key")
		if !ok {
			t.Fatalf("%s: /api/health 出参里没有 llm_global_key 字段 ⇒ 修法 B 的状态词没挂上（body=%s）", c.name, body)
		}
		if state != c.want {
			t.Fatalf("%s: llm_global_key 应为 %s，实际 %s", c.name, c.want, state)
		}
		seen[state] = true
	}
	// 决定性正向：两种状态词都得出现过（若实现写死一个常量，这一条会红）
	if !seen["ok"] || !seen["placeholder"] {
		t.Fatalf("llm_global_key 只出现了 %v ⇒ 状态词没跟着来源标志翻转", seen)
	}
}

// TestHealthLLMKeyLeaksNothing 匿名面上不许出现密钥/基址/供应商域名（带真值哨兵的负向锁）。
func TestHealthLLMKeyLeaksNothing(t *testing.T) {
	pinSqliteDialect(t)
	s := &Server{Cfg: &config.Config{
		OnlineAPIKey:  llmKeySentinelKey,
		OnlineAPIBase: llmKeySentinelBase,
		OnlineModel:   "some/vendor-model",
		EmbedAPIKey:   llmKeySentinelKey,
		EmbedAPIBase:  llmKeySentinelBase,
		ModelRoutes:   []config.ProviderConfig{{Provider: "vendor", APIBase: llmKeySentinelBase, APIKey: llmKeySentinelKey, Model: "m"}},
	}}
	code, body := callHealth(t, s)
	if code != http.StatusOK {
		t.Fatalf("应 200，实际 %d", code)
	}
	// 前置：状态词确实是 "ok"，证明这条请求真走到了"有真 Key"那一支（否则"没泄漏"是空转）
	if state, ok := jsonFieldString(body, "llm_global_key"); !ok || state != "ok" {
		t.Fatalf("本用例必须先证明服务端持有真 Key（llm_global_key=ok），实际 state=%q ok=%v", state, ok)
	}
	for _, banned := range []string{"sk-", llmKeySentinelKey, llmKeySentinelBase, llmKeySentinelHost, "api_key", "Bearer"} {
		if strings.Contains(strings.ToLower(body), strings.ToLower(banned)) {
			t.Fatalf("匿名 /api/health 泄漏了 %q ⇒ 密钥面/供应商坐标不许出现在无鉴权出参里（body=%s）", banned, body)
		}
	}
	// 反向对照：这个面仍然得是"有用的"，不能被裁成一个空对象
	for _, must := range []string{"status", "llm_global_key", "store_ready", "distributed", "dispatch"} {
		if !strings.Contains(body, must) {
			t.Fatalf("健康面缺字段 %s ⇒ 收敛泄漏不该顺手删掉既有状态词", must)
		}
	}
}

// jsonFieldString 从 JSON 响应体里取一个字符串字段（不引额外解码结构，便于"字段缺失"与
// "字段为空串"分开判定——后者会被 map 解码当成合法值）。
func jsonFieldString(body, key string) (string, bool) {
	frag := `"` + key + `":"`
	i := strings.Index(body, frag)
	if i < 0 {
		return "", false
	}
	rest := body[i+len(frag):]
	j := strings.Index(rest, `"`)
	if j < 0 {
		return "", false
	}
	return rest[:j], true
}
