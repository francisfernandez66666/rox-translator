// ============================================================================
// stagemodel_crypto_test.go — 阶段模型解析补充用例（改造 3，2026-09-17）：
//   - stage_models 密文密钥（enc:v1:）解密命中
//   - stage_models 非法 JSON → 安全回退 ok=false
//
// ============================================================================
package engine

import (
	"context"
	"encoding/json"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// TestResolveStageModelEncryptedKey stage_models 携带 enc:v1: 密文密钥 → 解密后返回明文。
func TestResolveStageModelEncryptedKey(t *testing.T) {
	t.Setenv("JWT_SECRET", "uat-stage-model-crypto")
	st := newTestStore(t)
	cfg := config.Default()
	e := &Engine{St: st, Cfg: cfg}
	ctx := context.Background()

	stm := config.StageModels{
		config.StageReview: {Provider: "p", APIBase: "https://r.example", APIKey: store.EncryptSecret("sk-plain-123"), Model: "mr"},
	}
	raw, _ := json.Marshal(stm)
	if err := st.SetConfig("stage_models", string(raw)); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}

	base, key, model, ok := e.resolveStageModel(ctx, config.StageReview)
	if !ok || base != "https://r.example" || key != "sk-plain-123" || model != "mr" {
		t.Fatalf("密文密钥解密结果不符: base=%s key=%s model=%s ok=%v", base, key, model, ok)
	}
}

// TestResolveStageModelBadJSON stage_models 配置非法 JSON → ok=false 回退（不 panic）。
func TestResolveStageModelBadJSON(t *testing.T) {
	st := newTestStore(t)
	e := &Engine{St: st, Cfg: config.Default()}
	if err := st.SetConfig("stage_models", "{not-json"); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}
	if _, _, _, ok := e.resolveStageModel(context.Background(), config.StageAIInitial); ok {
		t.Fatalf("非法 JSON 应安全回退 ok=false")
	}
}
