// ============================================================================
// admin_models_hotreload_test.go — ★ 〇-AR 第 5 波「每台热加载」的 api 侧断言（2026-10-05）。
//
// 钉住的缺陷本体（㊻）：管理台保存模型配置只刷新**发起写入的那一个进程**的运行配置，
// 另一台实例带着启动期生成的随机占位 Key 继续跑 ⇒ 运营明明配好了 Key，客户侧还是全 401，
// 而这一台的健康检查与模型页都"看着正常"。现网单实例所以没爆，
// 双实例 UAT（multi_instance_e2e.sh）当初是靠"两台都用 env 起 Key"绕开的——
// 那是把产品缺口挪进测试脚手架，不是修好。本文件把这些判据从脚手架里收回代码层。
//
// 四条判据（各配一条反证，方向都是"故意破坏就必须红"）：
//
//	① 模型页读的是"这台此刻生效的快照"，不是开机 cfg：
//	   库里已配好、本进程 cfg 仍是占位 ⇒ 页面必须回真值与 set:true；
//	   反证：把 handleModels 改回读 s.Cfg ⇒ set 恒 false、model 恒旧值 ⇒ 红。
//	② 保存成功后本进程立刻到位（Publish 那一腿）：
//	   反证：删掉 save 里的 PublishFrom ⇒ Live 仍是写库前那份 ⇒ 红。
//	③ 写库失败**不许**先动进程（顺序锁）：
//	   反证：把 `s.Cfg.ModelRoutes = merged` 挪回 SetConfig 之前 ⇒ 本用例红
//	   （旧形态正是这样留下"这台按新配置跑、库还是旧的"的机群分叉）。
//	④ 匿名健康面的 llm_global_key 状态词跟着库值翻（不许停在开机那一刻的 placeholder）。
//
// 出栈负向锁（配正向对照，避免空转）：本端点仅超管可达，但 Key 仍只许以掩码出栈——
// 用例先把真值写进库，再断言响应体里搜不到那串明文。
//
// 方言口径（AGENTS §一·4）：经 newModelsSaveServer／pinSqliteDialect 自钉内存 SQLite。
// ============================================================================
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"translator/internal/config"
	"translator/internal/llmsource"
	"translator/internal/store"
)

// forceProbeEveryRead 把热加载节流压到 0 秒并清空全局快照，使每个用例都真探一次库。
// 为什么用 env 而不是改代码默认值：TTL 的默认 5 秒是给现网的（一次请求扇出几十路调用，
// 每路读库就是往返风暴），单测要的是"这一步立刻看到库里现值"，压节流才是对的口子。
func forceProbeEveryRead(t *testing.T) {
	t.Helper()
	t.Setenv("LLM_CONFIG_RELOAD_TTL_SEC", "0")
	llmsource.ResetForTest()
	t.Cleanup(llmsource.ResetForTest)
}

// startupPlaceholderCfg 构造"这台开机时库里什么都没有"的形态：占位 Key＋旧模型名。
// 刻意用字面量而不是 config.Default()：后者有**副作用写全局 config.C**（AGENTS §一·4 那条），
// 在用例辅助函数里调它会把同包后续用例的方言/配置基线一起换掉。
func startupPlaceholderCfg() *config.Config {
	return &config.Config{
		OnlineAPIBase:             "https://startup.example/v1",
		OnlineAPIKey:              "sk-startup-placeholder",
		OnlineAPIKeyIsPlaceholder: true,
		OnlineModel:               "startup-old-model",
		ModelRoutes:               nil,
	}
}

// callModelsGet 打一次 GET /api/admin/models（超管 token）。
func callModelsGet(t *testing.T, s *Server, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/models", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleModels(w, req)
	return w.Code, w.Body.String()
}

// callModelsSave 打一次 POST /api/admin/models/save（超管 token）。
func callModelsSave(t *testing.T, s *Server, token string, body map[string]interface{}) (int, string) {
	t.Helper()
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/api/admin/models/save", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.handleModelsSave(w, req)
	return w.Code, w.Body.String()
}

// TestModelsPanelReadsLiveSnapshotNotStartupCfg 判据 ①＋④：
// 库里已经配好可用 Key、本进程运行配置还停在开机占位那一份时，
// 模型页与健康面都必须报"现在真能用的那把"，而不是"我开机那一刻的那把"。
func TestModelsPanelReadsLiveSnapshotNotStartupCfg(t *testing.T) {
	forceProbeEveryRead(t)
	s, token := newModelsSaveServer(t)
	cfg := startupPlaceholderCfg()
	s.Cfg = cfg
	// 模拟这台实例的启动期水合：库里此刻还是空的 ⇒ 快照就是占位那份
	llmsource.PublishFrom(s.Store, llmsource.Resolve(cfg, s.Store))
	if got := s.llmGlobalKeyState(context.Background()); got != "placeholder" {
		t.Fatalf("前置未成立：库里没配 Key 时健康面应报 placeholder，实际 %q", got)
	}

	// 运营在**另一台**实例上保存成功（这里直接落库，等价于那台走完 save 链路后的库内状态）
	const realKey = "sk-DB-REAL-NOT-A-KEY-1a2b"
	if err := s.Store.SetConfig("online_api_key", store.EncryptSecret(realKey)); err != nil {
		t.Fatalf("写入在线 Key 失败: %v", err)
	}
	if err := s.Store.SetConfig("online_api_base", "https://db.example/v1"); err != nil {
		t.Fatalf("写入在线端点失败: %v", err)
	}
	if err := s.Store.SetConfig("online_model", "db-model-7b"); err != nil {
		t.Fatalf("写入在线模型名失败: %v", err)
	}
	// 本进程运行配置必须仍是开机那一份（否则这条用例证的是"cfg 被同步了"，不是热加载）
	if !cfg.OnlineAPIKeyIsPlaceholder || cfg.OnlineModel != "startup-old-model" {
		t.Fatalf("前置被破坏：本进程 cfg 不应被另一台的保存改掉: %+v", cfg.OnlineModel)
	}

	code, body := callModelsGet(t, s, token)
	if code != http.StatusOK {
		t.Fatalf("模型页应 200，实际 %d: %s", code, body)
	}
	// 正向：读到的是库内现值（不是开机 cfg 的 startup-old-model）
	if !strings.Contains(body, `"model":"db-model-7b"`) {
		t.Errorf("模型页没热加载库内模型名（还停在开机 cfg）⇒ 另一台实例永远显示旧值。body=%s", body)
	}
	if strings.Contains(body, `"model":"startup-old-model"`) {
		t.Errorf("模型页把开机占位 cfg 当成了现值（判据 ① 的旧形态）: %s", body)
	}
	// set:true 这条前端就是拿它判"已配置"的——旧形态恒 false，运营看到"没配"就去重启进程
	if !strings.Contains(body, `"set":true`) {
		t.Errorf("模型页 set 仍为 false ⇒ 占位标记没跟着快照翻，页面会骗运营去重启: %s", body)
	}
	if !strings.Contains(body, `"api_base":"https://db.example/v1"`) {
		t.Errorf("模型页没热加载库内端点: %s", body)
	}
	// 负向（配正向对照：上面已经断言真值在库里且被读到）：明文 Key 不许出栈
	if strings.Contains(body, realKey) {
		t.Errorf("模型页泄漏明文 Key")
	}
	if !strings.Contains(body, "****") {
		t.Errorf("模型页的 Key 未掩码回显: %s", body)
	}
	// 判据 ④：匿名健康面的状态词跟着库值翻成 ok
	if got := s.llmGlobalKeyState(context.Background()); got != "ok" {
		t.Errorf("库里已配可用 Key，健康面仍报 %q ⇒ 运维会看到\"库里明明配了、健康检查说没配\"", got)
	}
}

// TestModelsSavePublishesSnapshotForThisInstance 判据 ②：保存成功后本进程立刻到位。
// 热加载腿是惰性的（引擎取配置时才按 TTL 探库），save 里不 Publish 的话，
// 改完配置的这一台要等下一次翻译请求才换上新配置，而管理台紧接着的 GET 已在读快照。
func TestModelsSavePublishesSnapshotForThisInstance(t *testing.T) {
	forceProbeEveryRead(t)
	s, token := newModelsSaveServer(t)
	cfg := startupPlaceholderCfg()
	s.Cfg = cfg
	llmsource.PublishFrom(s.Store, llmsource.Resolve(cfg, s.Store))

	code, body := callModelsSave(t, s, token, map[string]interface{}{
		"api_base": "https://saved.example/v1",
		"api_key":  "sk-saved-not-a-key-9c8d",
		"model":    "saved-model-9b",
	})
	if code != http.StatusOK {
		t.Fatalf("保存应 200，实际 %d: %s", code, body)
	}
	// 直接读全局快照（不再走任何探测），必须已经是刚保存的那一份
	live := llmsource.Live()
	if live.OnlineModel != "saved-model-9b" || live.OnlineAPIBase != "https://saved.example/v1" {
		t.Errorf("保存后 Live 仍是旧快照（%s/%s）⇒ save 那一腿没 Publish", live.OnlineAPIBase, live.OnlineModel)
	}
	if live.Placeholder || live.OnlineAPIKey == "" {
		t.Errorf("保存后 Live 仍是占位 ⇒ 本进程要等下一次翻译请求才用上新 Key")
	}
	// 既有口径不许退化：路由与三件套仍要回写 cfg（启动期判定与 B5 断言都读那里）
	if len(cfg.ModelRoutes) == 0 || cfg.ModelRoutes[0].Model != "saved-model-9b" {
		t.Errorf("保存后 cfg.ModelRoutes 没热同步（ApplyTo 那一腿被删）: %+v", cfg.ModelRoutes)
	}
	if cfg.OnlineAPIKeyIsPlaceholder {
		t.Errorf("保存后 cfg 仍标占位 ⇒ 保存链路回退到旧形态")
	}
}

// TestModelsSaveDBFailureLeavesProcessUntouched 判据 ③（顺序锁）：
// 写库失败时，进程侧一点都不许动——旧写法先 `s.Cfg.ModelRoutes = merged` 再 SetConfig，
// 500 返回后这台已经按客户没提交成功的那份配置跑，而其他实例按库里的旧配置跑。
func TestModelsSaveDBFailureLeavesProcessUntouched(t *testing.T) {
	forceProbeEveryRead(t)
	s, token := newModelsSaveServer(t)
	cfg := startupPlaceholderCfg()
	s.Cfg = cfg

	// 先存一条正常路由，作为"进程侧基线"
	baseCode, baseBody := callModelsSave(t, s, token, map[string]interface{}{
		"routes": []map[string]interface{}{
			{"provider": "global", "api_base": "https://base.example/v1", "api_key": "sk-base-not-a-key", "model": "base-model", "weight": 100},
		},
	})
	if baseCode != http.StatusOK {
		t.Fatalf("基线保存应 200，实际 %d: %s", baseCode, baseBody)
	}
	beforeRoutes := len(cfg.ModelRoutes)
	beforeLiveModel := llmsource.Live().OnlineModel

	// 把 system_config 表打掉：鉴权仍走 users 表，因此这次请求会一路走到 SetConfig 才失败
	if _, err := s.Store.DB().Exec("DROP TABLE system_config"); err != nil {
		t.Fatalf("打掉 system_config 失败（本用例前置）: %v", err)
	}
	code, _ := callModelsSave(t, s, token, map[string]interface{}{
		"routes": []map[string]interface{}{
			{"provider": "global", "api_base": "https://broken.example/v1", "api_key": "sk-broken-not-a-key", "model": "broken-model-should-not-apply", "weight": 100},
		},
	})
	if code != http.StatusInternalServerError {
		t.Fatalf("写库失败必须 500（诚实化口径），实际 %d", code)
	}
	// 进程侧一点没动：路由条数与模型名都还是基线那一份
	if len(cfg.ModelRoutes) != beforeRoutes {
		t.Errorf("写库失败后 cfg.ModelRoutes 却变了（%d→%d）⇒ 顺序锁破了，这台会按没提交成功的配置跑",
			beforeRoutes, len(cfg.ModelRoutes))
	}
	for _, rt := range cfg.ModelRoutes {
		if rt.Model == "broken-model-should-not-apply" {
			t.Errorf("失败的保存把新路由装进了本进程（机群分叉形态）: %+v", cfg.ModelRoutes)
		}
	}
	if got := llmsource.Live(); got != nil && len(got.Routes) > 0 {
		for _, rt := range got.Routes {
			if rt.Model == "broken-model-should-not-apply" {
				t.Errorf("失败的保存还发布了新快照: %+v", got.Routes)
			}
		}
	}
	if m := llmsource.Live().OnlineModel; m == "broken-model-should-not-apply" {
		t.Errorf("失败的保存改了本进程生效的模型名（基线 %q）", beforeLiveModel)
	}
}
