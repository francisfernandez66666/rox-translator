// ============================================================================
// llm_hotreload_test.go — ★ 〇-AR 第 5 波「每台热加载」的引擎侧断言（2026-10-05）。
//
// 钉住的缺陷本体（㊻）：运营在管理台配好上游 Key／换模型之后，
// 只有**发起写入的那一个进程**的运行配置被刷新，另一台实例继续拿着启动期的随机占位 Key
// 打 401。单机部署看不见这一条；共库多实例／灰度／演示单元与主单元并跑时，
// 客户面表现是"配置明明改好了，翻译还是全红"，而健康检查与看板都报正常。
//
// 本用例把"另一台实例"这一角色交给**库**来演：
// 引擎每取一次配置都先按 TTL＋指纹惰性重探库（internal/llmsource.Current），
// 所以库里换了内容，这台不用重启就该立刻按新值出站。
// 三条判据方向：
//
//	① 库里配了 ⇒ 三件套换成库值（旧形态这条恒红：它读的是开机 cfg）；
//	② 库里加了路由 ⇒ 走路由那条（路由优先级高于三件套，与改造前一致）；
//	③ 库里清空 ⇒ 回落"没有可用 Key"，而不是停在曾经配过的那一把（配置收回必须真收回）。
//
// 反证（都在 /tmp 副本上做，别在跑测期间就地改源）：
//
//	· 把 Engine.liveLLM 换回 `return &llmsource.Snapshot{...e.Cfg}`（即读开机 cfg）⇒ ①②③ 全红；
//	· 把 llmsource.Refresh 的指纹比较短路成"永不换"⇒ ①② 红；
//	· 把 Resolve 的"库里有一行就以库为准，含空列表"改回"条数 >0 才覆盖"⇒ ③ 红。
//
// 方言口径（AGENTS §一·4）：newTestStore 自钉内存 SQLite。
// ============================================================================
package engine

import (
	"context"
	"encoding/json"
	"testing"

	"translator/internal/config"
	"translator/internal/llmsource"
)

// placeholderStartupCfg 这台实例"开机时库里什么都没有"的运行配置：随机占位 Key。
// 用字面量而不是 config.Default()，避免它写全局 config.C 的同包副作用。
func placeholderStartupCfg() *config.Config {
	return &config.Config{
		OnlineAPIBase:             "https://startup.example/v1",
		OnlineAPIKey:              "sk-startup-placeholder",
		OnlineAPIKeyIsPlaceholder: true,
		OnlineModel:               "startup-old-model",
	}
}

// TestEngineHotLoadsLLMConfigFromStore 引擎取配置这一腿必须跟着库内现值走（不重启）。
func TestEngineHotLoadsLLMConfigFromStore(t *testing.T) {
	t.Setenv("LLM_CONFIG_RELOAD_TTL_SEC", "0") // 单测要"这一步就看到库里现值"，把节流压到 0
	llmsource.ResetForTest()
	t.Cleanup(llmsource.ResetForTest)

	st := newTestStore(t)
	e := &Engine{St: st, Cfg: placeholderStartupCfg(), LLM: nil}
	ctx := context.Background()

	// 前置：这台开机时库里空的 ⇒ 拿的是占位那份（旧形态与修法在这里读数相同）
	base, key, model := e.resolveModel(ctx)
	if base != "https://startup.example/v1" || model != "startup-old-model" {
		t.Fatalf("前置未成立：开机形态应回落本进程配置，实得 %s/%s", base, model)
	}
	if key != "sk-startup-placeholder" {
		t.Fatalf("前置未成立：开机形态应拿着占位 Key，实得 %q", key)
	}

	// —— ① 运营在另一台实例上配好了 Key／端点／模型名（这里等价于库里多了这三行）——
	if err := st.SetConfig("online_api_key", "sk-another-instance-configured"); err != nil {
		t.Fatalf("写入在线 Key 失败: %v", err)
	}
	if err := st.SetConfig("online_api_base", "https://db.example/v1"); err != nil {
		t.Fatalf("写入在线端点失败: %v", err)
	}
	if err := st.SetConfig("online_model", "db-model-7b"); err != nil {
		t.Fatalf("写入在线模型名失败: %v", err)
	}
	base, key, model = e.resolveModel(ctx)
	if base != "https://db.example/v1" || model != "db-model-7b" || key != "sk-another-instance-configured" {
		t.Errorf("判据① 红：库里配好了，这台还在拿开机占位出站（%s/%s/%s）⇒ ㊻ 的现网表现", base, key, model)
	}
	// 运行配置**不该**被别的实例改写：这条是"读点走快照、cfg 保持自己那份"的口径锁
	if e.Cfg.OnlineAPIKey != "sk-startup-placeholder" || !e.Cfg.OnlineAPIKeyIsPlaceholder {
		t.Errorf("e.Cfg 被热加载改写（%q）⇒ 运行期改共享配置就是数据竞争，这条不许成立", e.Cfg.OnlineAPIKey)
	}

	// —— ② 库里再加一条主路由 ⇒ 路由档优先（与改造前的优先序一致）——
	routesJSON, _ := json.Marshal([]config.ProviderConfig{
		{Provider: "global", APIBase: "https://route.example/v1", APIKey: "sk-route-real", Model: "route/model", Weight: 100},
	})
	if err := st.SetConfig("model_routes", string(routesJSON)); err != nil {
		t.Fatalf("写入 model_routes 失败: %v", err)
	}
	base, key, model = e.resolveModel(ctx)
	if base != "https://route.example/v1" || key != "sk-route-real" || model != "route/model" {
		t.Errorf("判据② 红：库内主路由没生效，实得 %s/%s/%s", base, key, model)
	}
	// 端点侧的模型名读数也要跟着翻（用量留痕与看板按它分组，两个名字各算一份账就是错账）
	if p, m := e.UsageModel(ctx); m == "startup-old-model" || p == "" {
		t.Errorf("UsageModel 仍回落开机模型名（%s/%s）⇒ 换配置之后的用量挂在旧模型名下", p, m)
	}

	// —— ③ 运营把路由收回（库里写空列表）⇒ 立刻按空处理，不停在曾经配过的那一把 ——
	if err := st.SetConfig("model_routes", "[]"); err != nil {
		t.Fatalf("清空 model_routes 失败: %v", err)
	}
	base, _, model = e.resolveModel(ctx)
	if base != "https://db.example/v1" || model != "db-model-7b" {
		t.Errorf("判据③ 红：路由清空没生效（实得 %s/%s）⇒ 配置收回要重启才落地", base, model)
	}
	// 再把三件套也清掉：这台必须认"没有可用配置"，而不是继续用刚才那把
	for _, k := range []string{"online_api_key", "online_api_base", "online_model"} {
		if err := st.SetConfig(k, ""); err != nil {
			t.Fatalf("清空 %s 失败: %v", k, err)
		}
	}
	base, _, model = e.resolveModel(ctx)
	// 库里三行都置空 ⇒ 回落本进程现值（这是 llmsource 的"各自独立判断、不许把端点洗成空串"口径）
	if base != "https://startup.example/v1" || model != "startup-old-model" {
		t.Errorf("判据③ 红：库里全清后没回落到本进程现值，实得 %s/%s", base, model)
	}
}
