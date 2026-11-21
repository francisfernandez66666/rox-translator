// ============================================================================
// admin_models_stage_multi_test.go — 阶段内多模型（Candidates）保存链测试（2026-11-21）。
//
// 钉住四条口径：
//
//	① 多候选落库后每个 api_key（主档 + 候选）都必须是 enc:v1: 密文；
//	② 读面返回的主档 + 候选 key 都是掩码，不含明文片段；
//	③ 掩码原样回传再保存 ⇒ 所有 key 按位置回填旧真值，不被掩码串覆盖；
//	④ 候选缺 api_base/model 的半成品 400 拒收，不污染库。
//
// 向后兼容：单模型形态（无 candidates）仍按旧链路工作，由既有 stage 测试覆盖。
// ============================================================================
package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/store"
)

// TestStageMultiCandidatesRoundTrip 多候选：落库加密 → 读回掩码 → 掩码回填不丢 key
func TestStageMultiCandidatesRoundTrip(t *testing.T) {
	s, token := newModelsSaveServer(t)

	// 第一次保存：主档 + 2 个候选，明文 key
	stages := map[string]interface{}{
		config.StageAIInitial: map[string]interface{}{
			"provider": "primary", "api_base": "https://a.example/v1", "api_key": "sk-primary-REAL", "model": "model-a", "weight": 10,
			"candidates": []map[string]interface{}{
				{"provider": "backup1", "api_base": "https://b.example/v1", "api_key": "sk-backup1-REAL", "model": "model-b", "weight": 5},
				{"provider": "backup2", "api_base": "https://c.example/v1", "api_key": "sk-backup2-REAL", "model": "model-c", "weight": 1},
			},
		},
	}
	if code, out := postStages(t, s, token, stages); code != 200 {
		t.Fatalf("首次保存应 200，实际 %d：%v", code, out)
	}

	// ① 落库形态：主档 + 每个候选 key 都是密文
	snap := stageSnapshot(t, s.Store)
	sm := snap[config.StageAIInitial]
	if !strings.HasPrefix(sm.APIKey, "enc:v1:") {
		t.Fatalf("主档 key 未加密: %q", sm.APIKey)
	}
	if len(sm.Candidates) != 2 {
		t.Fatalf("候选数应为 2，实际 %d", len(sm.Candidates))
	}
	for i, c := range sm.Candidates {
		if !strings.HasPrefix(c.APIKey, "enc:v1:") {
			t.Fatalf("候选 %d key 未加密: %q", i, c.APIKey)
		}
		if c.Weight == 0 {
			t.Fatalf("候选 %d weight 丢失", i)
		}
	}

	// ② 读面：主档 + 候选都掩码，无明文
	req := httptest.NewRequest("GET", "/api/admin/models/stage", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	s.handleStageModels(rec, req)
	var readBack struct {
		Stages map[string]config.StageModel `json:"stages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &readBack); err != nil {
		t.Fatal(err)
	}
	got := readBack.Stages[config.StageAIInitial]
	if !strings.Contains(got.APIKey, "****") || got.APIKey == "sk-primary-REAL" {
		t.Fatalf("主档读回应掩码: %q", got.APIKey)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("读回候选数应为 2，实际 %d", len(got.Candidates))
	}
	for i, c := range got.Candidates {
		if !strings.Contains(c.APIKey, "****") {
			t.Fatalf("候选 %d 读回应掩码: %q", i, c.APIKey)
		}
	}

	// ③ 把读到的掩码原样回传再保存 ⇒ 真值按位置回填
	reStages := map[string]interface{}{
		config.StageAIInitial: got, // got 里的 key 都是掩码
	}
	if code, out := postStages(t, s, token, reStages); code != 200 {
		t.Fatalf("掩码回传保存应 200，实际 %d：%v", code, out)
	}
	snap2 := stageSnapshot(t, s.Store)
	sm2 := snap2[config.StageAIInitial]
	if dec := store.DecryptSecret(sm2.APIKey); dec != "sk-primary-REAL" {
		t.Fatalf("主档 key 掩码回填错误: %q", dec)
	}
	if len(sm2.Candidates) != 2 {
		t.Fatalf("回填后候选数应为 2，实际 %d", len(sm2.Candidates))
	}
	wantKeys := []string{"sk-backup1-REAL", "sk-backup2-REAL"}
	for i, c := range sm2.Candidates {
		if dec := store.DecryptSecret(c.APIKey); dec != wantKeys[i] {
			t.Fatalf("候选 %d key 掩码回填错误: got %q want %q", i, dec, wantKeys[i])
		}
	}
}

// TestStageCandidateHalfBakedRejected 候选缺 api_base/model 的半成品 400 拒收，库不变
func TestStageCandidateHalfBakedRejected(t *testing.T) {
	s, token := newModelsSaveServer(t)

	stages := map[string]interface{}{
		config.StageAIInitial: map[string]interface{}{
			"provider": "primary", "api_base": "https://a.example/v1", "api_key": "sk-a", "model": "model-a",
			"candidates": []map[string]interface{}{
				{"provider": "bad", "api_base": "", "api_key": "sk-bad", "model": "model-b"}, // 缺 api_base
			},
		},
	}
	code, out := postStages(t, s, token, stages)
	if code != 400 {
		t.Fatalf("半成品候选应 400 拒收，实际 %d：%v", code, out)
	}
	snap := stageSnapshot(t, s.Store)
	if len(snap) != 0 {
		t.Fatalf("拒收后库不应有该阶段配置，实际 %+v", snap)
	}
}
