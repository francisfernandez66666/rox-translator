// ============================================================================
// main_hydrate_test.go — ★ R-1 修法 A/B 的启动水合断言（A1 / A2 / A3，2026-10-04）。
//
// 钉住的历史缺陷（《发布前E2E_UAT_20261003/修改文档》§一）：
//
//	「读后台库配置」那五句长在 `for _, r := range cfg.ModelRoutes` 的循环体内，
//	于是管理台把全局路由存成 0 行的那一刻起，库里明明带着可用 Key 也永远读不到 ⇒
//	cfg.OnlineAPIKey 停在 config.go 生成的随机占位符 ⇒ Pro 翻译／知识库／匿名试用
//	三条客户腿 100% 拿到上游 401，而 /api/health 一路回 status:ok、日志只有 INFO。
//	现网实证：2026-10-03 UAT 抓到连续 401，最后一次成功水合停在 09-28。
//
// 三条断言各自的反证（把修法改回旧形态必须红，逐条写在下面用例注释里）：
//
//	A1 TestStartupHydratesGlobalKeyWithZeroRoutes —— 反证：把 DB 水合搬回 range 内 ⇒ 红
//	A2 TestStartupPrefersDBKeyOverRouteKey        —— 反证：让路由腿先跑/直接 break ⇒ 红
//	A3 TestPlaceholderKeyRaisesErrorAndAlert      —— 反证：降回 INFO 或删告警 ⇒ 红
//
// 方言口径（AGENTS §一·4）：本测试族固定内存 SQLite，并在使用 config.Default() 后
// 立刻还原全局 config.C，避免把 sqlite 方言漏给同包后续用例。
// ============================================================================
package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/llmsource"
	"translator/internal/store"
)

// newHydrateStore 内存 SQLite 上跑一遍真实迁移，拿到可用的 system_config / alerts 表。
// 返回的 Store 会在用例结束时随 db 一起关闭。
func newHydrateStore(t *testing.T) *store.Store {
	t.Helper()
	// ★ 自钉方言（AGENTS §一·4）：config.Default() 读 DB_DRIVER 且副作用写全局 config.C，
	//   不钉的话 PG 模式跑批会让 store.New 走 PG 迁移路径、在内存 SQLite 上查
	//   information_schema 直接炸出假红。
	oldCfg := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = oldCfg })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("构建测试 Store 失败: %v", err)
	}
	return st
}

// placeholderCfg 构造一份「环境变量没配 ⇒ 随机占位 Key」的运行期配置。
// 这里刻意走 config.Default()（占位符就是它生成的），不自己拼字符串，
// 否则测的就不是同一条路径了。
func placeholderCfg(t *testing.T) *config.Config {
	t.Helper()
	// 两个可能供给 Key 的环境变量都要清掉：ONLINE_API_KEY 是 Embed 腿的回退名，
	// SILICONFLOW_API_KEY 是翻译腿的正主（config.go 里读的就是这个名字）。
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("ONLINE_API_KEY", "")
	cfg := config.Default()
	if !cfg.OnlineAPIKeyIsPlaceholder {
		t.Fatalf("前置不成立：清空环境变量后应拿到占位 Key（说明占位判据本身变了，本测试族需同步改）")
	}
	return cfg
}

// captureSlog 把 slog 默认输出重定向到 buffer（用例结束自动还原）。
// 返回 buffer；断言方按 JSON 行找 "level":"ERROR" 与 msg 字段。
func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

// countSlogLevel 数日志 buffer 里指定级别的行数（按 JSON 的 level 字段，不靠字符串碰运气）。
func countSlogLevel(t *testing.T, buf *bytes.Buffer, level, msgContains string) int {
	t.Helper()
	n := 0
	for _, line := range strings.Split(buf.String(), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var rec map[string]interface{}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec["level"] != level {
			continue
		}
		if msgContains != "" && !strings.Contains(line, msgContains) {
			continue
		}
		n++
	}
	return n
}

// TestStartupHydratesGlobalKeyWithZeroRoutes = 断言 A1。
// 病灶本体：库里 online_api_key 有可用密文、model_routes 是空数组、环境变量未配。
// 期望：水合后 cfg.OnlineAPIKey == 解密后的库值，且占位标记被清掉。
// 反证：把 hydrateLLMKeys 里的 DB 腿搬回 `for _, r := range cfg.ModelRoutes` 循环体内 ⇒
//
//	本用例必红（空路由 ⇒ 循环体一次都不跑 ⇒ Key 停在占位符），这正是 R-1 的现网形态。
func TestStartupHydratesGlobalKeyWithZeroRoutes(t *testing.T) {
	st := newHydrateStore(t)
	cfg := placeholderCfg(t)

	// 库里放一条真实 Key（密文，与管理台保存链路同一口径）＋配套端点与模型名
	const realKey = "sk-db-leg-key-for-a1"
	if err := st.SetConfig("online_api_key", store.EncryptSecret(realKey)); err != nil {
		t.Fatalf("写入 online_api_key 失败: %v", err)
	}
	if err := st.SetConfig("online_api_base", "https://db.example/v1"); err != nil {
		t.Fatalf("写入 online_api_base 失败: %v", err)
	}
	if err := st.SetConfig("online_model", "db/Model-A"); err != nil {
		t.Fatalf("写入 online_model 失败: %v", err)
	}
	// ★ 这条是本断言的题眼：路由表是**空数组**而不是缺键（现网就是"保存过 0 条"）
	if err := st.SetConfig("model_routes", "[]"); err != nil {
		t.Fatalf("写入空 model_routes 失败: %v", err)
	}
	cfg.ModelRoutes = nil

	if got := hydrateLLMKeys(cfg, st); got != llmKeyFromDB {
		t.Fatalf("水合来源应为 db，实际 %q", got)
	}
	if cfg.OnlineAPIKey != realKey {
		t.Fatalf("DB 腿没把库里的真实 Key 水合上来：got=%q want=%q", cfg.OnlineAPIKey, realKey)
	}
	if cfg.OnlineAPIKeyIsPlaceholder {
		t.Fatalf("水合成功却仍带着占位标记 ⇒ /api/health 会误报 placeholder")
	}
	// base/model 也必须跟上：旧形态这两句同样卡在循环体里，一旦没跑，
	// 引擎会拿"库里已改的 Key + 代码默认的端点"去拨号，是另一种更难查的错配。
	if cfg.OnlineAPIBase != "https://db.example/v1" || cfg.OnlineModel != "db/Model-A" {
		t.Fatalf("端点/模型名未随库水合：base=%q model=%q", cfg.OnlineAPIBase, cfg.OnlineModel)
	}
	// 反证守卫的另一半：两条腿皆空的场景绝不该在这里被误判成成功
	if countSlogLevel(t, &bytes.Buffer{}, "ERROR", "") != 0 {
		t.Fatalf("buffer 判据本身坏了")
	}
}

// TestStartupHydratesEmbedKeyWithZeroRoutes 同族的 Embed 腿（A1 的另一半）。
// Embedding Key 与翻译 Key 一样卡在同一个循环里，R-1 期间知识库向量重建是静默失败的；
// 这一条把「routes=0 也要水合 embed_*」单独钉住，防止将来有人只修翻译腿。
func TestStartupHydratesEmbedKeyWithZeroRoutes(t *testing.T) {
	st := newHydrateStore(t)
	cfg := placeholderCfg(t)
	const realEmbed = "embed-db-leg-key"
	if err := st.SetConfig("embed_api_key", store.EncryptSecret(realEmbed)); err != nil {
		t.Fatalf("写入 embed_api_key 失败: %v", err)
	}
	if err := st.SetConfig("embed_api_base", "https://embed.example/v1"); err != nil {
		t.Fatalf("写入 embed_api_base 失败: %v", err)
	}
	if err := st.SetConfig("model_routes", "[]"); err != nil {
		t.Fatalf("写入空 model_routes 失败: %v", err)
	}
	// 翻译腿先给一条可用 Key，确保 embed 腿不是"跟着翻译腿顺带跑"的假绿
	cfg.OnlineAPIKey = "sk-trans-ok"
	cfg.OnlineAPIKeyIsPlaceholder = false
	// ★ 这一档必须连"来源"一起声明：模拟的是**环境变量真给了 Key** 的部署形态。
	//   〇-AR 第 5 波补腿之后，env 短路只认 OnlineAPIKeyOrigin，不认"值非空＋非占位"这两个派生状态。
	cfg.OnlineAPIKeyOrigin = config.OriginAPIKeyEnv

	hydrateLLMKeys(cfg, st)
	// 注：翻译腿非占位时 hydrateLLMKeys 直接走 env 分支返回，embed 不覆盖——
	// 这条是刻意保留的既有优先序（env 已给可用 Key ⇒ 一条都不动，AGENTS §一·3）。
	if cfg.EmbedAPIKey == realEmbed {
		t.Fatalf("env 可用时不该覆盖 embed（优先序被改）")
	}
}

// TestStartupPrefersDBKeyOverRouteKey = 断言 A2。
// 两条腿都有值时取 DB 那条：库里的 online_api_key 是运营在管理台显式保存的意图，
// model_routes[0] 只是同一次保存顺带写出的副本；两者可以漂移（历史上就漂过）。
// 反证：把路由腿提到 DB 腿之前、或恢复旧写法"先命中路由就 break 掉整段"⇒ 本用例红。
func TestStartupPrefersDBKeyOverRouteKey(t *testing.T) {
	st := newHydrateStore(t)
	cfg := placeholderCfg(t)
	if err := st.SetConfig("online_api_key", store.EncryptSecret("sk-from-db")); err != nil {
		t.Fatalf("写入 online_api_key 失败: %v", err)
	}
	// 路由腿也照生产口径从库里给（〇-AR 第 5 波：库内 model_routes 那一行是路由的事实源），
	// 这样"两腿皆有值 ⇒ 取库那一条"的对照是真的，而不是靠 cfg 上一个没人读的字段凑场景。
	routesRaw, err := json.Marshal([]config.ProviderConfig{
		{Provider: "global", APIBase: "https://route.example/v1", APIKey: "sk-from-route", Model: "route/Model", Weight: 100},
	})
	if err != nil {
		t.Fatalf("序列化路由失败: %v", err)
	}
	if err := st.SetConfig("model_routes", string(routesRaw)); err != nil {
		t.Fatalf("写入 model_routes 失败: %v", err)
	}

	if got := hydrateLLMKeys(cfg, st); got != llmKeyFromDB {
		t.Fatalf("两腿皆有值时来源应为 db，实际 %q", got)
	}
	if cfg.OnlineAPIKey != "sk-from-db" {
		t.Fatalf("没按「DB ＞ 路由」取值：got=%q", cfg.OnlineAPIKey)
	}
}

// TestStartupFallsBackToRouteOnlyWhenDBEmpty 路由腿仍然要能用（正向对照，防"只修一条腿"）。
// 库里没配 online_api_key、但主路由带真实 Key ⇒ 取路由那条。
// 反证：把路由腿整个删掉 ⇒ 本用例红（那样 R-1 的修法就只剩一半，
// 老库里"只在多供应商路由里配过 Key"的形态会重新变成占位起跑）。
func TestStartupFallsBackToRouteOnlyWhenDBEmpty(t *testing.T) {
	st := newHydrateStore(t)
	cfg := placeholderCfg(t)
	// ★ 〇-AR 第 5 波改的前置：路由现在**从库里给**（生产形态就是 system_config.model_routes）。
	//   旧写法把路由塞在 cfg 上、库里却写 "[]"——那正是"清空了却还带着旧列表"的旧形态；
	//   现在库里那一行是路由的唯一事实源，前置必须照生产口径摆（判据本身一字未动）。
	routesRaw, err := json.Marshal([]config.ProviderConfig{
		{Provider: "global", APIBase: "https://route.example/v1", APIKey: "sk-from-route", Model: "route/Model", Weight: 100},
	})
	if err != nil {
		t.Fatalf("序列化路由失败: %v", err)
	}
	if err := st.SetConfig("model_routes", string(routesRaw)); err != nil {
		t.Fatalf("写入 model_routes 失败: %v", err)
	}
	if got := hydrateLLMKeys(cfg, st); got != llmKeyFromRoute {
		t.Fatalf("库空时应回落到主路由水合，实际来源 %q", got)
	}
	if cfg.OnlineAPIKey != "sk-from-route" {
		t.Fatalf("路由腿取值错误：got=%q", cfg.OnlineAPIKey)
	}
}

// TestStartupIgnoresRouteWithEmptyOrMaskedKey 路由腿的两条负向：
// 空 Key 与掩码 Key（sk-****）都**不算**可用来源。
// 为什么这条必须钉：resolveStageModel 有「阶段未填密钥 ⇒ 继承全局密钥」的语义，
// 拿一条空 Key 的路由去水合＝把所有阶段腿一起打成空 Key，比不修更糟。
func TestStartupIgnoresRouteWithEmptyOrMaskedKey(t *testing.T) {
	for _, tc := range []struct{ name, key string }{
		{"空密钥路由", ""},
		{"掩码密钥路由", "sk-****"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newHydrateStore(t)
			cfg := placeholderCfg(t)
			// 路由同样从库里给（事实源口径与上面两条一致），负向判据本身一字未动
			routesRaw, err := json.Marshal([]config.ProviderConfig{
				{Provider: "global", APIKey: tc.key, Model: "m", APIBase: "https://x.example/v1"},
			})
			if err != nil {
				t.Fatalf("序列化路由失败: %v", err)
			}
			if err := st.SetConfig("model_routes", string(routesRaw)); err != nil {
				t.Fatalf("写入 model_routes 失败: %v", err)
			}
			buf := captureSlog(t)
			if got := hydrateLLMKeys(cfg, st); got != llmKeyFromNone {
				t.Fatalf("%q 不该被当成可用来源，实际来源 %q", tc.key, got)
			}
			if !cfg.OnlineAPIKeyIsPlaceholder {
				t.Fatalf("%q 被写进了全局 Key 并清掉了占位标记 ⇒ 健康面会假绿", tc.key)
			}
			if n := countSlogLevel(t, buf, "ERROR", "llm_global_key_placeholder"); n != 1 {
				t.Fatalf("期望恰好 1 条 ERROR 级占位告警日志，实际 %d 条：\n%s", n, buf.String())
			}
		})
	}
}

// TestPlaceholderKeyRaisesErrorAndAlert = 断言 A3（日志腿＋告警腿）。
// 三档来源全空 ⇒ ① 恰好一条 ERROR 级结构化日志（不是 INFO）；② alerts 表落一条
// 平台级 critical 行（kind=llm_key_placeholder，去重由 store 保证不刷屏）。
// 反证：把 observability.Error 换回 log.Printf/log.Println ⇒ ①红（标准库日志不进 slog JSON 面，
//
//	且级别不再是 ERROR）；删掉 CreateAlert ⇒ ②红。
func TestPlaceholderKeyRaisesErrorAndAlert(t *testing.T) {
	st := newHydrateStore(t)
	cfg := placeholderCfg(t)
	cfg.ModelRoutes = nil
	buf := captureSlog(t)

	if got := hydrateLLMKeys(cfg, st); got != llmKeyFromNone {
		t.Fatalf("三档皆空时来源应为 none，实际 %q", got)
	}
	if n := countSlogLevel(t, buf, "ERROR", "llm_global_key_placeholder"); n != 1 {
		t.Fatalf("期望 1 条 ERROR 级占位日志，实际 %d 条：\n%s", n, buf.String())
	}
	// 标准库 log.Printf 的 INFO 形态不算数（修法 B 要的就是级别抬上来）
	if n := countSlogLevel(t, buf, "INFO", "llm_global_key_placeholder"); n != 0 {
		t.Fatalf("占位暴露不该停在 INFO 级：%d 条", n)
	}

	alerts, err := st.ListAlerts(0, "open", 100)
	if err != nil {
		t.Fatalf("读 alerts 失败: %v", err)
	}
	var hit *store.Alert
	for _, a := range alerts {
		if a.Kind == llmKeyAlertKind {
			hit = a
		}
	}
	if hit == nil {
		t.Fatalf("没有落平台级占位告警（open 列表 %d 条）", len(alerts))
	}
	if hit.Level != "critical" || hit.TenantID != 0 {
		t.Fatalf("告警档位不对：level=%q tenant_id=%d（应为 critical / 0）", hit.Level, hit.TenantID)
	}
	// 红线：告警正文里不许出现 Key 片段（这条会被管理台列表原样渲染给客户侧运营看）
	for _, forbidden := range []string{"sk-", "siliconflow", "api_key"} {
		if strings.Contains(strings.ToLower(hit.Message), forbidden) {
			t.Fatalf("告警正文泄露坐标 %q: %s", forbidden, hit.Message)
		}
	}

	// 去重正向对照：再水合一次不得追加第二条同 kind 的 open 告警（重启风暴不刷屏）
	if got := hydrateLLMKeys(cfg, st); got != llmKeyFromNone {
		t.Fatalf("第二次仍应为 none，实际 %q", got)
	}
	again, _ := st.ListAlerts(0, "open", 100)
	n := 0
	for _, a := range again {
		if a.Kind == llmKeyAlertKind {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("同 kind open 告警应去重，实际 %d 条", n)
	}
}

// TestEnvProvidedKeyIsNeverOverridden 优先序负向锁（AGENTS §一·3：环境变量 > 数据库配置）。
// env 已给可用 Key 时，即使库里有一条"不一样的"Key 也不许覆盖——
// 否则运维在 secrets.env 里显式配的凭据会被面板旧值悄悄换掉。
// 反证：删掉 hydrateLLMKeys 开头那条 env 短路 ⇒ 红。
func TestEnvProvidedKeyIsNeverOverridden(t *testing.T) {
	st := newHydrateStore(t)
	cfg := placeholderCfg(t)
	cfg.OnlineAPIKey = "sk-from-env"
	cfg.OnlineAPIKeyIsPlaceholder = false
	// 这一条测的是"环境变量真给了 Key"，档位必须照说（第 5 波补腿：短路判据改认来源锚点）
	cfg.OnlineAPIKeyOrigin = config.OriginAPIKeyEnv
	if err := st.SetConfig("online_api_key", store.EncryptSecret("sk-from-db")); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	if got := hydrateLLMKeys(cfg, st); got != llmKeyFromEnv {
		t.Fatalf("来源应为 env，实际 %q", got)
	}
	if cfg.OnlineAPIKey != "sk-from-env" {
		t.Fatalf("env 凭据被库值覆盖：got=%q", cfg.OnlineAPIKey)
	}
}

// TestHydratedKeyIsNotRecordedAsEnv ★ 第 5 波补腿（现网演示单元抓到的一条，见《修改文档》§十二）。
//
// 现象（都有日志实证）：一台**没配** SILICONFLOW_API_KEY 的实例，启动时从库里的模型路由水合到可用 Key，
// 随后的热加载日志却出 `from":"env"`。根因是水合把 cfg 的**派生状态**（值非空＋占位标记翻假）
// 留在了那儿，下一轮解析按"env 已给可用 Key ⇒ 库值一条都不覆盖"的短路把这台钉死在水合那一刻的旧值上——
// 运营之后在管理台改 Key，这台永远不跟，而日志的档位还会把排查方向整个带偏。
//
// 三条判据按因果链排：
//
//	① 水合之后 cfg 的来源档位必须写清楚是 route（不许是 env，也不许留空）；
//	② 库里随后出现新的 online_api_key ⇒ 同一台**不重启**必须跟上（这才是"每台热加载"）；
//	③ 反向对照：env 真给了 Key 的部署上，库值照旧不许覆盖（AGENTS §一·3 的优先序不能被这条改动削掉）。
//
// 反证（两条各摘一条分别实跑，别只看一条就以为两条都锁住了）：
//
//	删掉 Snapshot.ApplyTo 里回写 OnlineAPIKeyOrigin 那一行 ⇒ **①**红（档位停在 none，与快照不同源），
//	    ②照旧绿——"值写对了但来源没写"不会让这台停摆，只会让读档的人被骗，所以这一句只能由①管；
//	把 Resolve 的档位判据摘掉（回到只看派生状态 `!placeholder && key != ""`）⇒ **②**红（这台停在旧路由 Key 上），
//	    ①照旧绿。两条合起来才盖住「现网演示单元」那一台的完整因果链。
func TestHydratedKeyIsNotRecordedAsEnv(t *testing.T) {
	t.Setenv("SILICONFLOW_API_KEY", "")
	t.Setenv("ONLINE_API_KEY", "")
	t.Setenv("LLM_CONFIG_RELOAD_TTL_SEC", "0") // 每个读点都真探一次库，TTL 节流不许替这条判据打掩护
	llmsource.ResetForTest()
	t.Cleanup(llmsource.ResetForTest)

	st := newHydrateStore(t)
	cfg := config.Default()
	routesRaw, err := json.Marshal([]config.ProviderConfig{
		{Provider: "global", APIBase: "https://route.example/v1", APIKey: store.EncryptSecret("sk-from-route"), Model: "route/Model", Weight: 100},
	})
	if err != nil {
		t.Fatalf("序列化路由失败: %v", err)
	}
	if err := st.SetConfig("model_routes", string(routesRaw)); err != nil {
		t.Fatalf("写入 model_routes 失败: %v", err)
	}

	if got := hydrateLLMKeys(cfg, st); got != llmKeyFromRoute {
		t.Fatalf("前置不成立：库里只有路由带 Key，水合来源应为 route，实际 %q", got)
	}
	if cfg.OnlineAPIKeyOrigin != llmsource.FromRoute {
		t.Fatalf("①水合后 cfg 来源档位=%q，期望 route（值与来源必须同时回写，否则 env 短路会被派生状态骗住）", cfg.OnlineAPIKeyOrigin)
	}

	// ② 运营在管理台保存了一把新的在线 Key（库里 online_api_key 现值 ＝ 运营显式意图，优先级高于路由）
	if err := st.SetConfig("online_api_key", store.EncryptSecret("sk-ops-new")); err != nil {
		t.Fatalf("写入 online_api_key 失败: %v", err)
	}
	if ok := llmsource.Refresh(context.Background(), st, cfg); !ok {
		t.Fatalf("②库里换了 Key 而这台没换快照（Refresh 判没变）⇒ 档位短路仍在生效")
	}
	if live := llmsource.Live(); live.OnlineAPIKey != "sk-ops-new" {
		t.Fatalf("②热加载后仍用水合那一刻的旧值：got=%q from=%q", live.OnlineAPIKey, live.From)
	}
	if live := llmsource.Live(); live.From != llmsource.FromDB {
		t.Fatalf("②来源档位应翻成 db，实得 %q（档位是排障抓手，不是装饰）", live.From)
	}

	// ③ 反向对照：env 那档真给了 Key ⇒ 库里的新值一条都不覆盖
	t.Setenv("SILICONFLOW_API_KEY", "sk-real-env-key")
	envCfg := config.Default()
	if envCfg.OnlineAPIKeyOrigin != llmsource.FromEnv {
		t.Fatalf("③前置不成立：env 给了 Key 却没钉上 env 档位，origin=%q", envCfg.OnlineAPIKeyOrigin)
	}
	snap := llmsource.Resolve(envCfg, st)
	if snap.OnlineAPIKey != "sk-real-env-key" || snap.From != llmsource.FromEnv {
		t.Fatalf("③env 优先序被这条补腿改坏：key=%q from=%q", snap.OnlineAPIKey, snap.From)
	}
}
