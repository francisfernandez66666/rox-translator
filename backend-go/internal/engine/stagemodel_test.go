// ============ 本文件职责中文说明 ============
// 阶段模型解析单元测试：验证 stage_models 命中/回退/密钥继承逻辑。
// 使用内存 SQLite 构建 Store，不依赖业务数据。
// ========================================
package engine

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	s, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

func TestResolveStageModel(t *testing.T) {
	st := newTestStore(t)
	e := &Engine{St: st, Cfg: config.Default()}
	ctx := context.Background()

	// 未配置 stage_models → 回退（ok=false）
	if _, _, _, ok := e.resolveStageModel(ctx, config.StageAIInitial); ok {
		t.Fatalf("期望未配置时 ok=false")
	}

	// 配置 ai_initial 独立模型
	stm := config.StageModels{
		config.StageAIInitial: {Provider: "p1", APIBase: "https://a.example", APIKey: "sk-abc", Model: "m1"},
	}
	raw, _ := json.Marshal(stm)
	if err := st.SetConfig("stage_models", string(raw)); err != nil {
		t.Fatalf("SetConfig 失败: %v", err)
	}

	// 命中 ai_initial
	base, key, model, ok := e.resolveStageModel(ctx, config.StageAIInitial)
	if !ok || base != "https://a.example" || key != "sk-abc" || model != "m1" {
		t.Fatalf("命中 ai_initial 结果不符: %s %s %s %v", base, key, model, ok)
	}

	// 未配置的阶段 → 回退
	if _, _, _, ok := e.resolveStageModel(ctx, config.StageReview); ok {
		t.Fatalf("期望 review 未配置时 ok=false")
	}

	// 缺 model 的阶段视为未配置
	stm[config.StageReview] = config.StageModel{Provider: "p2", APIBase: "https://r.example", Model: ""}
	raw2, _ := json.Marshal(stm)
	_ = st.SetConfig("stage_models", string(raw2))
	if _, _, _, ok := e.resolveStageModel(ctx, config.StageReview); ok {
		t.Fatalf("期望缺 model 的 review ok=false")
	}
}

func TestResolveStageModelKeyInherit(t *testing.T) {
	st := newTestStore(t)
	cfg := config.Default()
	cfg.OnlineAPIKey = "global-key"
	e := &Engine{St: st, Cfg: cfg}
	ctx := context.Background()

	stm := config.StageModels{
		config.StageEvals: {Provider: "p", APIBase: "https://e.example", APIKey: "", Model: "judge"},
	}
	raw, _ := json.Marshal(stm)
	_ = st.SetConfig("stage_models", string(raw))

	// 阶段模型未填 APIKey → 继承全局默认密钥
	_, key, model, ok := e.resolveStageModel(ctx, config.StageEvals)
	if !ok || key != "global-key" || model != "judge" {
		t.Fatalf("密钥继承结果不符: key=%s model=%s ok=%v", key, model, ok)
	}
}
