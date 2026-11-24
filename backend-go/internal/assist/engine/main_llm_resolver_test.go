// ============ main_llm_resolver_test.go · 职责说明 ============
// 本地 assist 的主服务 LLM 快照热加载锁：指纹变化必须换完整 providers，显式 env 不被覆盖。
// ================================================================
package engine

import (
	"context"
	"reflect"
	"testing"

	"translator/internal/assist/llm"
)

func TestMainLLMResolverReloadsOnFingerprintChange(t *testing.T) {
	e := newTestEngine(t)
	fp := "fp-1"
	e.SetMainLLMResolver(func(context.Context) ([]llm.Provider, string, string) {
		return []llm.Provider{{Name: "main", BaseURL: "https://a.example/v1", APIKey: "a", Model: "model-a"}}, fp, "db"
	})
	first := e.ensureLLM(context.Background())
	if got := first.ProviderBaseURLs(); !reflect.DeepEqual(got, []string{"a.example"}) {
		t.Fatalf("首份主服务快照未生效：%v", got)
	}
	again := e.ensureLLM(context.Background())
	if again != first {
		t.Fatal("指纹未变时不应重建 client")
	}
	fp = "fp-2"
	e.SetMainLLMResolver(func(context.Context) ([]llm.Provider, string, string) {
		return []llm.Provider{{Name: "main", BaseURL: "https://b.example/v1", APIKey: "b", Model: "model-b"}}, fp, "route"
	})
	second := e.ensureLLM(context.Background())
	if second == first {
		t.Fatal("指纹变化后必须重建 client")
	}
	if got := second.ProviderBaseURLs(); !reflect.DeepEqual(got, []string{"b.example"}) {
		t.Fatalf("新快照未生效：%v", got)
	}
	if mode := e.LLMMode(context.Background()); mode != "route" {
		t.Fatalf("来源徽标应随快照报 route，实得 %q", mode)
	}
}

func TestInjectedInitialClientSurvivesEmptyConfig(t *testing.T) {
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: "https://injected.example/v1", APIKey: "real", Model: "injected-model"}}, 5)
	got := e.ensureLLM(context.Background())
	if hosts := got.ProviderBaseURLs(); !reflect.DeepEqual(hosts, []string{"injected.example"}) {
		t.Fatalf("New 后注入的初始 client 被空配置清掉：%v", hosts)
	}
	if mode := e.LLMMode(context.Background()); mode != "env" {
		t.Fatalf("初始注入来源应报 env，实得 %q", mode)
	}
}

func TestClearedAssistConfigDropsOldClient(t *testing.T) {
	e := newTestEngine(t)
	_ = e.db.SetConfig("llm_base_url", "https://old.example/v1")
	_ = e.db.SetConfig("llm_api_key", "old-key")
	_ = e.db.SetConfig("llm_model", "old-model")
	if got := e.ensureLLM(context.Background()); !got.Enabled() {
		t.Fatal("完整 assist 配置应构建 client")
	}
	_ = e.db.SetConfig("llm_base_url", "")
	_ = e.db.SetConfig("llm_api_key", "")
	_ = e.db.SetConfig("llm_model", "")
	if got := e.ensureLLM(context.Background()); got.Enabled() {
		t.Fatal("配置清空后必须清掉旧 client，不能继续用旧 Key")
	}
}

func TestMaskedAssistConfigFallsBackToMainResolver(t *testing.T) {
	e := newTestEngine(t)
	_ = e.db.SetConfig("llm_base_url", "https://masked.example/v1")
	_ = e.db.SetConfig("llm_api_key", "sk-****")
	_ = e.db.SetConfig("llm_model", "masked-model")
	e.SetMainLLMResolver(func(context.Context) ([]llm.Provider, string, string) {
		return []llm.Provider{{Name: "main", BaseURL: "https://main.example/v1", APIKey: "real", Model: "main-model"}}, "fp", "db"
	})
	got := e.ensureLLM(context.Background())
	if hosts := got.ProviderBaseURLs(); !reflect.DeepEqual(hosts, []string{"main.example"}) {
		t.Fatalf("掩码 assist DB Key 应回落主服务快照：%v", hosts)
	}
}

func TestExplicitAssistEnvWinsOverMainResolver(t *testing.T) {
	e := newTestEngine(t)
	e.llm = llm.New([]llm.Provider{{Name: "main", BaseURL: "https://env.example/v1", APIKey: "env", Model: "env-model"}}, 5)
	e.llmFromEnv = true
	e.SetMainLLMResolver(func(context.Context) ([]llm.Provider, string, string) {
		return []llm.Provider{{Name: "main", BaseURL: "https://main.example/v1", APIKey: "main", Model: "main-model"}}, "fp", "env"
	})
	got := e.ensureLLM(context.Background())
	if hosts := got.ProviderBaseURLs(); !reflect.DeepEqual(hosts, []string{"env.example"}) {
		t.Fatalf("主服务解析器覆盖了显式 assist env：%v", hosts)
	}
}
