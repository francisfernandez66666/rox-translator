// ============ route_balance_test.go · 职责说明（2026-11-22）============
// 多账户 API Key 均衡：同一 (api_base, model) 配多把 Key 时按调用轮次轮转，
// 不同端点/模型不混池；RPM/TPM 配额字段可解析、默认零值不改老行为。
// ================================================
package engine

import (
	"encoding/json"
	"testing"

	"translator/internal/config"
)

func TestPickPrimaryRotatesKeysInSamePool(t *testing.T) {
	restore := pinDynamicRouting(t, false)
	defer restore()
	routes := []config.ProviderConfig{
		{Provider: "global", APIBase: "https://a/v1", APIKey: "k1", Model: "m", Weight: 100, RPM: 1000, TPM: 80000},
		{Provider: "global", APIBase: "https://a/v1", APIKey: "k2", Model: "m", Weight: 100, RPM: 1000, TPM: 80000},
		{Provider: "global", APIBase: "https://a/v1", APIKey: "k3", Model: "m", Weight: 100, RPM: 1000, TPM: 80000},
		{Provider: "global", APIBase: "https://b/v1", APIKey: "other", Model: "m2", Weight: 1},
	}
	e := &Engine{Cfg: config.Default()}
	e.Cfg.ModelRoutes = routes
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		seen[e.pickPrimaryRoute().APIKey]++
	}
	for _, k := range []string{"k1", "k2", "k3"} {
		if seen[k] != 2 {
			t.Fatalf("同池三把 Key 应各轮到 2 次，实得 %+v", seen)
		}
	}
}

func TestPickPrimaryDoesNotMixDifferentPools(t *testing.T) {
	restore := pinDynamicRouting(t, false)
	defer restore()
	e := &Engine{Cfg: config.Default()}
	e.Cfg.ModelRoutes = []config.ProviderConfig{
		{Provider: "global", APIBase: "https://a/v1", APIKey: "a1", Model: "m", Weight: 5},
		{Provider: "global", APIBase: "https://a/v1", APIKey: "a2", Model: "m", Weight: 5},
		{Provider: "global", APIBase: "https://b/v1", APIKey: "b1", Model: "m", Weight: 5},
	}
	seen := map[string]int{}
	for i := 0; i < 6; i++ {
		seen[e.pickPrimaryRoute().APIKey]++
	}
	if seen["b1"] != 0 {
		t.Fatalf("不同端点的 Key 不得混进同一轮转池：%+v", seen)
	}
	if seen["a1"] != 3 || seen["a2"] != 3 {
		t.Fatalf("同端点池应均分 3/3：%+v", seen)
	}
}

func TestRouteQuotaFieldsOptional(t *testing.T) {
	var r config.ProviderConfig
	if err := json.Unmarshal([]byte(`{"api_base":"https://a/v1","api_key":"k","model":"m","rpm":1000,"tpm":80000}`), &r); err != nil {
		t.Fatal(err)
	}
	if r.RPM != 1000 || r.TPM != 80000 {
		t.Fatalf("配额字段没解析出来：%+v", r)
	}
	var old config.ProviderConfig
	if err := json.Unmarshal([]byte(`{"api_base":"https://a/v1","api_key":"k","model":"m"}`), &old); err != nil {
		t.Fatal(err)
	}
	if old.RPM != 0 || old.TPM != 0 {
		t.Fatalf("老配置零值不该变：%+v", old)
	}
}
