// ============ assist_local_wiring_test.go · 职责说明 ============
// 本地接待引擎的 LLM 装配锁：assist env 优先；主服务快照必须保留路由完整三件套，
// 占位 Key/mock 不得生成假 provider，无路由时才回落 online 档。
// ================================================================
package main

import (
	"testing"

	assistconfig "translator/internal/assist/config"
	"translator/internal/config"
	"translator/internal/llmsource"
)

func TestAssistLLMProvidersPreferExplicitConfig(t *testing.T) {
	cfg := &assistconfig.Config{LLMBaseURL: "https://assist.example/v1", LLMAPIKey: "assist-key", LLMModel: "assist-model"}
	got := assistLLMProviders(cfg)
	if len(got) != 1 || got[0].Model != "assist-model" || got[0].APIKey != "assist-key" {
		t.Fatalf("assist 显式配置未优先：%+v", got)
	}
	if !buildAssistLLM(cfg).Enabled() {
		t.Fatal("assist env 完整时 client 必须可用")
	}
	cfg.LLMAPIKey = "sk-****"
	if got := assistLLMProviders(cfg); len(got) != 0 {
		t.Fatalf("掩码 assist env Key 不得生成 provider：%+v", got)
	}
	cfg.LLMAPIKey = "real"
	cfg.LLMModelBackup = "backup-model"
	cfg.LLMAPIKey2 = "sk-****"
	if got := assistLLMProviders(cfg); len(got) != 1 || got[0].Model != "assist-model" {
		t.Fatalf("掩码备用 Key 不得生成 backup provider：%+v", got)
	}
}

func TestAssistProvidersFromSnapshotKeepRouteTriplets(t *testing.T) {
	snap := &llmsource.Snapshot{From: llmsource.FromRoute, OnlineAPIKey: "key-a", Routes: []config.ProviderConfig{
		{APIBase: "https://route-a.example/v1", APIKey: "key-a", Model: "model-a", Weight: 1},
		{APIBase: "https://route-b.example/v1", APIKey: "key-b", Model: "model-b", Weight: 9},
	}}
	got := assistProvidersFromSnapshot(snap)
	if len(got) != 2 || got[0].Model != "model-b" || got[0].BaseURL != "https://route-b.example/v1" || got[0].APIKey != "key-b" {
		t.Fatalf("主路由完整三件套或权重顺序失真：%+v", got)
	}
	if got[1].Model != "model-a" || got[1].APIKey != "key-a" {
		t.Fatalf("fallback 路由三件套失真：%+v", got)
	}
}

func TestAssistProvidersFromSnapshotFallbackAndGuards(t *testing.T) {
	online := &llmsource.Snapshot{From: llmsource.FromDB, OnlineAPIBase: "https://online.example/v1", OnlineAPIKey: "online-key", OnlineModel: "online-model"}
	got := assistProvidersFromSnapshot(online)
	if len(got) != 1 || got[0].Model != "online-model" {
		t.Fatalf("无路由时未回落 online 档：%+v", got)
	}
	online.Routes = []config.ProviderConfig{{APIBase: "https://route.example/v1", APIKey: "route-key", Model: "route-model", Weight: 99}}
	if got := assistProvidersFromSnapshot(online); len(got) != 1 || got[0].Model != "online-model" {
		t.Fatalf("数据库全局档被低优先级路由覆盖：%+v", got)
	}
	online.OnlineAPIKey = "key****mask"
	if got := assistProvidersFromSnapshot(online); len(got) != 0 {
		t.Fatalf("掩码全局 Key 不得生成 provider：%+v", got)
	}
	online.Placeholder = true
	if got := assistProvidersFromSnapshot(online); len(got) != 0 {
		t.Fatalf("占位 Key 不得生成 provider：%+v", got)
	}
	masked := &llmsource.Snapshot{From: llmsource.FromRoute, OnlineAPIKey: "sk-****", Routes: []config.ProviderConfig{
		{APIBase: "https://route.example/v1", APIKey: "sk-****", Model: "route-model"},
	}}
	if got := assistProvidersFromSnapshot(masked); len(got) != 0 {
		t.Fatalf("掩码路由 Key 不得生成 provider：%+v", got)
	}
	if got := assistLLMProviders(&assistconfig.Config{MockMode: true, LLMBaseURL: "x", LLMAPIKey: "k", LLMModel: "m"}); len(got) != 0 {
		t.Fatalf("mock 档不得生成真实 provider：%+v", got)
	}
}
