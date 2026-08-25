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

// TestResolveModelGlobalRoutes 全局多供应商路由（平台统一网关）按权重选主模型。
// ★ 2026-08-26 BYOK 移除改造：原「租户路由优先」用例随租户模型配置能力一并退役，
//
//	现验证超管全局 ModelRoutes 的主路由选择与回退链语义。
func TestResolveModelGlobalRoutes(t *testing.T) {
	// 全局默认（不配 ModelRoutes）→ 回退全局默认模型
	cfg := config.Default()
	e := &Engine{Cfg: cfg}
	ctx := context.Background()

	base, key, model := e.resolveModel(ctx)
	if model != cfg.OnlineModel || base != cfg.OnlineAPIBase || key != cfg.OnlineAPIKey {
		t.Fatalf("未配置路由时未回退全局默认: base=%s model=%s", base, model)
	}

	// 配置两条全局路由 → 取权重最高者为主路由
	cfg.ModelRoutes = []config.ProviderConfig{
		{Provider: "openai", APIBase: "https://api.openai.com/v1", APIKey: "sk-openai", Model: "gpt-4o-mini", Weight: 2},
		{Provider: "gemini", APIBase: "https://generativelanguage.googleapis.com/v1beta/openai", APIKey: "gem-key", Model: "gemini-1.5-flash", Weight: 1},
	}
	e.Cfg = cfg
	base, key, model = e.resolveModel(ctx)
	if base != "https://api.openai.com/v1" || key != "sk-openai" || model != "gpt-4o-mini" {
		t.Fatalf("全局主路由选择错误: base=%s key=%s model=%s", base, key, model)
	}

	// 降级链应排除主路由、按权重降序仅剩备用路由
	fb := e.resolveRouteFallbacks(cfg.ModelRoutes[0])
	if len(fb) != 1 || fb[0].Model != "gemini-1.5-flash" {
		t.Fatalf("全局降级链异常: %+v", fb)
	}
}
