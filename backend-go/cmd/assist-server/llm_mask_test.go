// ============ llm_mask_test.go · 职责说明 ============
// 独立 assist 的 env 主/备用 Key 掩码拒绝锁：掩码值不能生成真实 provider。
// ================================================================
package main

import (
	"testing"

	"translator/internal/assist/config"
)

func TestBuildLLMRejectsMaskedKeys(t *testing.T) {
	cfg := &config.Config{LLMBaseURL: "https://assist.example/v1", LLMAPIKey: "sk-****", LLMModel: "main"}
	if buildLLM(cfg).Enabled() {
		t.Fatal("掩码主 Key 不得启用 LLM")
	}
	cfg.LLMAPIKey = "real"
	cfg.LLMModelBackup = "backup"
	cfg.LLMAPIKey2 = "sk-****"
	got := buildLLM(cfg)
	if !got.Enabled() || len(got.ProviderBaseURLs()) != 1 {
		t.Fatalf("掩码备用 Key 应只保留主 provider：%v", got.ProviderBaseURLs())
	}
}
