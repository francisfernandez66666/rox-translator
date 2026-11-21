// ============================================================================
// stagemodel_multi_test.go — 阶段内多模型候选解析测试（2026-11-21）。
// 钉住三条口径：
//
//	① 多候选按 Weight 降序选主，resolveStageModel 返回最高权重那个；
//	② stageFallbackCandidates 返回其余候选、按 Weight 降序、跳过主档、key 继承全局；
//	③ 单模型形态（无 Candidates）行为完全等同旧逻辑（返回单元素、无需用备用）。
//
// ============================================================================
package engine

import (
	"context"
	"encoding/json"
	"testing"

	"translator/internal/config"
)

// TestStageCandidatesPickPrimaryByWeight 多候选按 Weight 选主
func TestStageCandidatesPickPrimaryByWeight(t *testing.T) {
	st := newTestStore(t)
	cfg := config.Default()
	e := &Engine{St: st, Cfg: cfg}
	ctx := context.Background()

	stm := config.StageModels{
		config.StageAIInitial: {
			Provider: "primary", APIBase: "https://a.example", APIKey: "sk-a", Model: "model-a", Weight: 1,
			Candidates: []config.StageCandidate{
				{Provider: "best", APIBase: "https://b.example", APIKey: "sk-b", Model: "model-b", Weight: 10},
				{Provider: "mid", APIBase: "https://c.example", APIKey: "sk-c", Model: "model-c", Weight: 5},
			},
		},
	}
	raw, _ := json.Marshal(stm)
	_ = st.SetConfig("stage_models", string(raw))

	// 主档 weight=1，候选 b weight=10 ⇒ 应选 b 为主
	base, key, model, ok := e.resolveStageModel(ctx, config.StageAIInitial)
	if !ok || base != "https://b.example" || key != "sk-b" || model != "model-b" {
		t.Fatalf("期望按 weight 选 model-b 为主，实际 base=%s key=%s model=%s ok=%v", base, key, model, ok)
	}
}

// TestStageFallbackCandidatesOrder 备用链按 weight 降序、跳过主、key 继承
func TestStageFallbackCandidatesOrder(t *testing.T) {
	st := newTestStore(t)
	cfg := config.Default()
	cfg.OnlineAPIKey = "global-key"
	e := &Engine{St: st, Cfg: cfg}
	ctx := context.Background()

	stm := config.StageModels{
		config.StageReview: {
			Provider: "p", APIBase: "https://a.example", APIKey: "sk-a", Model: "model-a", Weight: 3,
			Candidates: []config.StageCandidate{
				{Provider: "b", APIBase: "https://b.example", APIKey: "sk-b", Model: "model-b", Weight: 8},
				{Provider: "c", APIBase: "https://c.example", APIKey: "", Model: "model-c", Weight: 5}, // key 空 → 继承全局
				{Provider: "d", APIBase: "https://d.example", APIKey: "sk-d", Model: "model-d", Weight: 1},
			},
		},
	}
	raw, _ := json.Marshal(stm)
	_ = st.SetConfig("stage_models", string(raw))

	// 主 = model-b (weight 8)。四者按 weight 排序为 b(8),c(5),a(3),d(1)，
	// 跳过主 b 后备用顺序应为 c → a → d。
	fb := e.stageFallbackCandidates(ctx, config.StageReview, "https://b.example", "model-b")
	if len(fb) != 3 {
		t.Fatalf("备用数应为 3，实际 %d", len(fb))
	}
	wantModels := []string{"model-c", "model-a", "model-d"}
	for i, want := range wantModels {
		if fb[i].Model != want {
			t.Fatalf("备用 %d 应为 %s，实际 %s", i, want, fb[i].Model)
		}
	}
	// model-c 的 key 为空 ⇒ 继承全局
	if fb[0].APIKey != "global-key" {
		t.Fatalf("model-c 应继承全局 key，实际 %q", fb[0].APIKey)
	}
}

// TestStageFallbackSingleModelCompat 单模型形态无阶段内备用（向后兼容）
func TestStageFallbackSingleModelCompat(t *testing.T) {
	st := newTestStore(t)
	e := &Engine{St: st, Cfg: config.Default()}
	ctx := context.Background()

	stm := config.StageModels{
		config.StageAIInitial: {Provider: "p", APIBase: "https://a.example", APIKey: "sk-a", Model: "model-a"},
	}
	raw, _ := json.Marshal(stm)
	_ = st.SetConfig("stage_models", string(raw))

	// 单模型：resolveStageModel 命中、无阶段内备用
	base, _, model, ok := e.resolveStageModel(ctx, config.StageAIInitial)
	if !ok || base != "https://a.example" || model != "model-a" {
		t.Fatalf("单模型命中失败: %s %s %v", base, model, ok)
	}
	if fb := e.stageFallbackCandidates(ctx, config.StageAIInitial, base, model); len(fb) != 0 {
		t.Fatalf("单模型形态不应有阶段内备用，实际 %d", len(fb))
	}
}
