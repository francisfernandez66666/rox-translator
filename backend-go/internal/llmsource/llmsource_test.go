// ============ 本文件职责中文说明 ============
// llmsource 包单测：验证「环境变量 ＞ 后台库配置 ＞ 主路由」三档解析、
// 热加载探测（TTL＋指纹）、库不可用时沿用上一份快照，以及 Live() 永不返回 nil 的兜底形态。
// 全部用内存 SQLite 建库，并把重探间隔钉成 0（每次调用都真探），
// 避免「看调度运气翻红」的形态；每条断言都直接问**内存里生效的那份值**，不问日志。
// ★ 本文件同时是 ㊻「每台热加载」的机械锁：把 Refresh 的调用摘掉，
//
//	TestRefreshAdoptsAdminSaveAfterStartup 立刻红灯（见文件末尾反证口径）。
//
// ========================================
package llmsource

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"translator/internal/config"
	"translator/internal/store"
)

// newStore 测试辅助：内存 SQLite ＋ 自钉方言（AGENTS §一·4：新增后端单测必须自钉，
// 否则 run_uat 的 PG 模式会把全局 config.C 的方言泄漏给同包后续用例）。
func newStore(t *testing.T) *store.Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		config.C = old
	})
	return st
}

// placeholderCfg 构造"启动期没拿到环境变量 Key"的配置（即随机占位符形态，R-1 的现场）。
func placeholderCfg() *config.Config {
	c := config.Default()
	c.OnlineAPIKey = "sk-random-placeholder"
	c.OnlineAPIKeyIsPlaceholder = true
	// 来源档位一起钉成"没来源"：config.Default() 在本机配了 SILICONFLOW_API_KEY 时会写 env，
	// 留着它这个夹具就名不副实（〇-AR 第 5 波补腿：env 短路只认档位，不认值像不像）。
	c.OnlineAPIKeyOrigin = config.OriginAPIKeyNone
	c.OnlineAPIBase = ""
	c.OnlineModel = ""
	c.ModelRoutes = nil
	return c
}

// envCfg 构造"环境变量给了可用 Key"的配置。
func envCfg(key string) *config.Config {
	c := config.Default()
	c.OnlineAPIKey = key
	c.OnlineAPIKeyIsPlaceholder = false
	c.OnlineAPIKeyOrigin = config.OriginAPIKeyEnv // ★ 只有这一档才让 Resolve 的 env 短路生效
	c.OnlineAPIBase = "https://env.example/v1"
	c.OnlineModel = "env-model"
	c.ModelRoutes = nil
	return c
}

// forceEveryProbe 把重探间隔钉成 0（每个用例都要真探一次库，否则 TTL 会让第二次调用直接返回 false）。
func forceEveryProbe(t *testing.T) {
	t.Helper()
	old := os.Getenv(envTTLKey)
	if err := os.Setenv(envTTLKey, "0"); err != nil {
		t.Fatalf("设置重探间隔失败: %v", err)
	}
	ResetForTest()
	t.Cleanup(func() {
		_ = os.Setenv(envTTLKey, old)
		ResetForTest()
	})
}

// TestResolveEnvLegBeatsStore 环境变量档必须压过库里的运营配置（AGENTS §一·3）。
func TestResolveEnvLegBeatsStore(t *testing.T) {
	forceEveryProbe(t)
	st := newStore(t)
	if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-from-db")); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	cfg := envCfg("sk-from-env")
	snap := Resolve(cfg, st)
	if snap.From != FromEnv {
		t.Fatalf("来源档位=%q，期望 env", snap.From)
	}
	if snap.OnlineAPIKey != "sk-from-env" {
		t.Fatalf("env 档被库值覆盖：key=%q", snap.OnlineAPIKey)
	}
	if snap.Placeholder {
		t.Fatal("env 可用 Key 却被标成占位符")
	}
}

// TestResolveAdoptsDBConfigAfterStartup 是 ㊻ 的核心断言：
// 一台实例**启动时**库里没配 Key（拿到随机占位符），运营**之后**在管理台保存了可用 Key，
// 这台实例必须在下一次取配置时换上真值——不需要重启。
func TestResolveAdoptsDBConfigAfterStartup(t *testing.T) {
	forceEveryProbe(t)
	st := newStore(t)
	cfg := placeholderCfg()

	first := Resolve(cfg, st)
	if first.From != FromNone {
		t.Fatalf("两腿皆空时档位=%q，期望 none", first.From)
	}
	if !first.Placeholder {
		t.Fatal("库里没配 Key 却把占位标记洗掉了")
	}
	Publish(first)
	if got := Live().OnlineAPIKey; got != cfg.OnlineAPIKey {
		t.Fatalf("Live() 未返回已发布的占位快照：got=%q", got)
	}

	// 运营在另一台实例上保存配置（本用例直接写共享库，模拟"别的进程写库"）
	if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-real-after-save")); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	if err := st.SetConfig(KeyOnlineBase, "https://gw.example/v1"); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	if err := st.SetConfig(KeyOnlineModel, "mt-pro"); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	if !Refresh(context.Background(), st, cfg) {
		t.Fatal("库内已保存新配置，Refresh 却判「没换上新快照」（热加载腿失效）")
	}
	got := Live()
	if got.From != FromDB {
		t.Fatalf("档位=%q，期望 db", got.From)
	}
	if got.OnlineAPIKey != "sk-real-after-save" || got.OnlineAPIBase != "https://gw.example/v1" || got.OnlineModel != "mt-pro" {
		t.Fatalf("热加载后的三件套不对：key=%q base=%q model=%q", got.OnlineAPIKey, got.OnlineAPIBase, got.OnlineModel)
	}
	if got.Placeholder {
		t.Fatal("已取到真 Key 却仍标占位符（健康面会继续报 placeholder）")
	}
}

// TestRefreshSkipsWhenUnchanged 值没变 ⇒ 不换指针、不复算，读侧拿到同一份对象。
// 这一条钉的是"热加载不等于每次重建"：扇出几十路并发调用时，指纹相同必须零分配返回。
func TestRefreshSkipsWhenUnchanged(t *testing.T) {
	forceEveryProbe(t)
	st := newStore(t)
	cfg := placeholderCfg()
	if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-keep")); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	Publish(Resolve(cfg, st))
	before := Live()
	if Refresh(context.Background(), st, cfg) {
		t.Fatal("库值未变却判「换上了新快照」（指纹判据没生效）")
	}
	if Live() != before {
		t.Fatal("未变化却换了快照指针（读侧会看到无意义的对象抖动）")
	}
}

// TestRefreshHonorsTTL 重探间隔内不许真的去碰库（TTL 是这条链的扇出保护腿）。
// 判据用"库值已变但仍在 TTL 内 ⇒ 不换上"来表达，比数查询次数更稳。
func TestRefreshHonorsTTL(t *testing.T) {
	old := os.Getenv(envTTLKey)
	if err := os.Setenv(envTTLKey, "60"); err != nil {
		t.Fatalf("设置重探间隔失败: %v", err)
	}
	ResetForTest()
	t.Cleanup(func() {
		_ = os.Setenv(envTTLKey, old)
		ResetForTest()
	})
	st := newStore(t)
	cfg := placeholderCfg()
	// 用 PublishFrom 而不是裸 Publish：启动水合就是"刚读完这座库就发布"，
	// 必须把库身份钉住，否则第一次 Refresh 会被当成从没探过而直冲 TTL。
	PublishFrom(st, Resolve(cfg, st))
	if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-too-soon")); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	if Refresh(context.Background(), st, cfg) {
		t.Fatal("TTL 窗口内就重探并换上了快照（并发扇出会把这个窗口打成 N 次库往返）")
	}
	if Live().OnlineAPIKey == "sk-too-soon" {
		t.Fatal("TTL 内却读到了新值")
	}
}

// TestRefreshKeepsPreviousSnapshotWhenStoreIsDown 库读不到 ⇒ 沿用上一份，绝不回落占位 Key。
// 反证形态：把这段改成"读不到就 Publish 一份 none"，本用例当场红
// （那是把「运维抖一下」打成「全站翻译 401」的新事故）。
func TestRefreshKeepsPreviousSnapshotWhenStoreIsDown(t *testing.T) {
	forceEveryProbe(t)
	old := config.C
	cfgx := config.Default()
	cfgx.DatabaseDriver = "sqlite"
	config.C = cfgx
	t.Cleanup(func() { config.C = old })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	st, err := store.New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	cfg := placeholderCfg()
	if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-working")); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	Publish(Resolve(cfg, st))
	if Live().OnlineAPIKey != "sk-working" {
		t.Fatalf("前置不成立：key=%q", Live().OnlineAPIKey)
	}
	// 关掉底层连接，模拟库不可达
	_ = db.Close()
	if Refresh(context.Background(), st, cfg) {
		t.Fatal("库已不可达却判「换上了新快照」")
	}
	if got := Live().OnlineAPIKey; got != "sk-working" {
		t.Fatalf("库不可达时把生效配置洗成了 %q（必须沿用上一份）", got)
	}
}

// TestResolveRouteLegAndDeadRoutes 主路由兜腿＋解密失败路由停用。
func TestResolveRouteLegAndDeadRoutes(t *testing.T) {
	forceEveryProbe(t)
	st := newStore(t)
	cfg := placeholderCfg()
	routes := []config.ProviderConfig{
		{Provider: "p1", APIBase: "https://p1/v1", APIKey: store.EncryptSecret("sk-p1"), Model: "m1", Weight: 10},
		// 一条"看起来是密文但解不开"的路由（JWT_SECRET 轮换没同步的现场）：只停用它自己
		{Provider: "p2", APIBase: "https://p2/v1", APIKey: "enc:v1:@@坏密文@@", Model: "m2", Weight: 99},
	}
	b, err := json.Marshal(routes)
	if err != nil {
		t.Fatalf("序列化路由失败: %v", err)
	}
	if err := st.SetConfig(KeyModelRoutes, string(b)); err != nil {
		t.Fatalf("写库失败: %v", err)
	}
	snap := Resolve(cfg, st)
	if len(snap.Routes) != 1 {
		t.Fatalf("路由条数=%d，期望只留 1 条可用（坏密文那条必须停用而不是整批打死）", len(snap.Routes))
	}
	if snap.Routes[0].APIKey != "sk-p1" {
		t.Fatalf("路由 Key 未解密：%q", snap.Routes[0].APIKey)
	}
	if snap.From != FromRoute {
		t.Fatalf("档位=%q，期望 route（库里 online_* 全空 ⇒ 主路由兜腿）", snap.From)
	}
	if snap.OnlineAPIKey != "sk-p1" || snap.Placeholder {
		t.Fatalf("主路由兜腿没接上：key=%q placeholder=%v", snap.OnlineAPIKey, snap.Placeholder)
	}
}

// TestLiveNeverNilBeforePublish 从未发布（单测、旧读点）时 Live() 必须按运行期配置兜一份，
// 而不是把 nil 送给调用方——否则每个读点都要写 nil 判断，迟早漏一个变成 panic。
func TestLiveNeverNilBeforePublish(t *testing.T) {
	old := os.Getenv(envTTLKey)
	if err := os.Setenv(envTTLKey, "5"); err != nil {
		t.Fatalf("设置重探间隔失败: %v", err)
	}
	ResetForTest()
	t.Cleanup(func() {
		_ = os.Setenv(envTTLKey, old)
		ResetForTest()
	})
	c := envCfg("sk-env-only")
	oldC := config.C
	config.C = c
	t.Cleanup(func() { config.C = oldC })

	snap := Live()
	if snap == nil {
		t.Fatal("Live() 返回 nil")
	}
	if snap.OnlineAPIKey != "sk-env-only" || snap.From != FromEnv {
		t.Fatalf("兜底快照不对：key=%q from=%q", snap.OnlineAPIKey, snap.From)
	}
	if Live() != snap {
		t.Fatal("两次 Live() 拿到不同对象（兜底那份必须被发布成唯一事实）")
	}
}

// TestRefreshNilStore 无库启动形态（st=nil）必须安静返回 false，不 panic、不抹快照。
func TestRefreshNilStore(t *testing.T) {
	forceEveryProbe(t)
	cfg := envCfg("sk-env")
	Publish(Resolve(cfg, nil))
	if Refresh(context.Background(), nil, cfg) {
		t.Fatal("无库形态却判换上了快照")
	}
	if Live().OnlineAPIKey != "sk-env" {
		t.Fatal("无库形态把 env 快照洗掉了")
	}
}

// TestCurrentIgnoresGlobalSnapshotWithoutStore 是硬口径 ①（"无库不许读全局"）的机械锁：
// 同进程里已经有一座库发布过快照（别的用例、甚至别台实例的模拟），
// 此时以 st=nil 取现值必须**只看本进程配置**，既不许拿全局那份当自己的现值，
// 也不许反向把全局指针改掉。漏掉 Current 里那句 `if st == nil` 的两种后果各有一条断言守着。
func TestCurrentIgnoresGlobalSnapshotWithoutStore(t *testing.T) {
	forceEveryProbe(t)
	ctx := context.Background()

	// 前置：先用一座库把全局指针填成"另一家"的配置（= 同进程里别的用例发布过快照）
	other := newStore(t)
	if err := other.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-other-instance")); err != nil {
		t.Fatalf("写入他家用例 Key 失败: %v", err)
	}
	if err := other.SetConfig(KeyOnlineBase, "https://other.example/v1"); err != nil {
		t.Fatalf("写入他家用例端点失败: %v", err)
	}
	if err := other.SetConfig(KeyOnlineModel, "other-model"); err != nil {
		t.Fatalf("写入他家用例模型名失败: %v", err)
	}
	if !Refresh(ctx, other, placeholderCfg()) {
		t.Fatal("前置：首次探测应取到库内现值并发布")
	}
	if got := Live().OnlineModel; got != "other-model" {
		t.Fatalf("前置：全局快照应为 other-model，实得 %q", got)
	}

	// ① 无库取现值 ⇒ 只按本进程配置算，读不到他家那一份
	got := Current(ctx, nil, envCfg("sk-env"))
	if got.OnlineModel == "other-model" || got.OnlineAPIKey == "sk-other-instance" {
		t.Fatalf("无库形态读到了全局快照（把别家配置当成自己现值）: %+v", got)
	}
	if got.OnlineAPIKey != "sk-env" || got.OnlineModel != "env-model" || got.From != FromEnv {
		t.Fatalf("无库形态没按本进程配置现值出: key=%q model=%q from=%q", got.OnlineAPIKey, got.OnlineModel, got.From)
	}
	// ② 反向：这一次读取不许把全局指针改写（否则同进程里真正有库的读点会被抹成 env 档）
	if after := Live(); after.OnlineModel != "other-model" || after.OnlineAPIKey != "sk-other-instance" {
		t.Fatalf("Current(nil) 改写了全局快照: model=%q key=%q", after.OnlineModel, after.OnlineAPIKey)
	}
}

// TestCurrentReProbesWhenStoreIdentityChanges 把 TTL 故意放到一小时，测的却是"换了一座库"：
// 节流判据必须带 Store 身份，否则第二个 Store 会被判成"TTL 内不用探"、
// 拿到上一座库发布的那份快照（多实例 UAT 与同进程多库单测都会命中，本批真踩）。
// 反证：去掉 Refresh 里 `probe.st == any(st)` 这一半判据 ⇒ 第二段断言当场红。
func TestCurrentReProbesWhenStoreIdentityChanges(t *testing.T) {
	old := os.Getenv(envTTLKey)
	defer func() { _ = os.Setenv(envTTLKey, old) }()
	_ = os.Setenv(envTTLKey, "3600")
	ResetForTest()
	ctx := context.Background()

	writeTriple := func(t *testing.T, st *store.Store, key, model string) {
		t.Helper()
		if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret(key)); err != nil {
			t.Fatalf("写入 %s 失败: %v", key, err)
		}
		if err := st.SetConfig(KeyOnlineBase, "https://db.example/v1"); err != nil {
			t.Fatalf("写入端点失败: %v", err)
		}
		if err := st.SetConfig(KeyOnlineModel, model); err != nil {
			t.Fatalf("写入 %s 失败: %v", model, err)
		}
	}

	stA := newStore(t)
	writeTriple(t, stA, "sk-instance-a", "model-a")
	if got := Current(ctx, stA, placeholderCfg()); got.OnlineModel != "model-a" || got.From != FromDB {
		t.Fatalf("第一座库取现值不对: model=%q from=%q", got.OnlineModel, got.From)
	}

	stB := newStore(t)
	writeTriple(t, stB, "sk-instance-b", "model-b")
	got := Current(ctx, stB, placeholderCfg())
	if got.OnlineModel != "model-b" || got.OnlineAPIKey != "sk-instance-b" {
		t.Fatalf("换库后仍读到上一座库的快照（TTL 判据没带 Store 身份）: model=%q key=%q", got.OnlineModel, got.OnlineAPIKey)
	}
	// 回到第一座库：同样必须重新探，不许停在 B 那一份上
	if back := Current(ctx, stA, placeholderCfg()); back.OnlineModel != "model-a" {
		t.Fatalf("换回第一座库没重新探测: model=%q", back.OnlineModel)
	}
}

// TestReloadTTLParsesEnv TTL 解析：合法值照用、非法值回落默认（配错档位不许改变行为）。
func TestReloadTTLParsesEnv(t *testing.T) {
	old := os.Getenv(envTTLKey)
	defer func() { _ = os.Setenv(envTTLKey, old) }()
	_ = os.Setenv(envTTLKey, "17")
	if got := reloadTTL(); got != 17*time.Second {
		t.Fatalf("env=17 时 TTL=%v，期望 17s", got)
	}
	_ = os.Setenv(envTTLKey, "abc")
	if got := reloadTTL(); got != 5*time.Second {
		t.Fatalf("env 非法时 TTL=%v，期望回落 5s", got)
	}
	_ = os.Setenv(envTTLKey, "")
	if got := reloadTTL(); got != 5*time.Second {
		t.Fatalf("env 未配时 TTL=%v，期望 5s", got)
	}
}

// TestHydratedValueNeverMasqueradesAsEnv 来源锚点锁（★ 2026-10-05 〇-AR 第 5 波现网补腿）。
// 形态是现网抓到的那一条：一台**没配** SILICONFLOW_API_KEY 的实例启动 ⇒ 水合从库里的
// 模型路由取到一把可用 Key 并 ApplyTo 回 cfg。此后 cfg 里"值非空＋占位标记为假"，
// 与 env 配的 Key 完全同形。如果 env 档的短路只看这两条（派生状态），这台就把水合那一刻
// 取到的值当成 env 的既有配置，**库里以后再改它永远不跟**，而热加载日志还写着 from=env。
// 反证（已逐条实跑，两条各摘一条，红的位置不同）：
//
//	把 Resolve 里 envUsable 的档位判据去掉（回到 `!placeholder && key != ""`）⇒ 本条红在 ②
//	（库里换了 Key 而这台仍用水合那一刻的旧值，档位读出 env）；
//	把 ApplyTo 里回写 OnlineAPIKeyOrigin 那一行删掉 ⇒ 本条红在 ①
//	（档位停在 none，与快照不同源；①用的是 t.Fatalf，所以 ②根本不再执行，别说成"②照旧绿"）。
//	★ 实测：这条反证下跨进程闸门**全绿**（multi_instance_e2e 装回后跑 PASS=30 FAIL=0，
//	  因为档位没人读、库里腿照旧跑）⇒ "值写对了、来源没写"只有本条 ① 管，
//	  拿"D 段还绿"当作这一行可以删就是读错了归属。
func TestHydratedValueNeverMasqueradesAsEnv(t *testing.T) {
	forceEveryProbe(t)
	st := newStore(t)
	// 库里只有一条带 Key 的主路由（演示单元的现网形态：env 没配 Key，Key 来自路由）
	routes, err := json.Marshal([]config.ProviderConfig{{Provider: "ops", APIBase: "https://route.example/v1",
		Model: "route-model", APIKey: store.EncryptSecret("sk-from-route")}})
	if err != nil {
		t.Fatalf("序列化路由失败: %v", err)
	}
	if err := st.SetConfig(KeyModelRoutes, string(routes)); err != nil {
		t.Fatalf("写路由失败: %v", err)
	}
	cfg := placeholderCfg() // env 档没给可用 Key ⇒ 档位是 none

	// —— ① 启动水合：应取到路由那把，且来源档位不许翻成 env ——
	first := Resolve(cfg, st)
	if first.From != FromRoute {
		t.Fatalf("水合来源应为 route，实得 %q", first.From)
	}
	first.ApplyTo(cfg)
	if cfg.OnlineAPIKey != "sk-from-route" {
		t.Fatalf("水合没把路由 Key 装进 cfg: %q", cfg.OnlineAPIKey)
	}
	if cfg.OnlineAPIKeyOrigin != first.From {
		t.Fatalf("①水合后 cfg 来源档位=%q，期望与快照同源 %q（值与来源必须同时回写；"+
			"只比不等 env 是弱判据——字段停在 none／空串同样说明回写没跑到）", cfg.OnlineAPIKeyOrigin, first.From)
	}

	// —— ② 运营随后在管理台保存了一把新的在线 Key：这台必须跟得上 ——
	if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-ops-new")); err != nil {
		t.Fatalf("写库内 Key 失败: %v", err)
	}
	second := Resolve(cfg, st)
	if second.OnlineAPIKey != "sk-ops-new" {
		t.Fatalf("②库里改了 Key 而这台仍用水合那一刻的旧值（＝派生状态冒充 env 的那条短路）：got=%q from=%q",
			second.OnlineAPIKey, second.From)
	}
	if second.From != FromDB {
		t.Fatalf("②来源档位应翻成 db，实得 %q", second.From)
	}

	// —— 反向对照：env 真给了 Key 时，库值**照旧不许**覆盖（AGENTS §一·3 的优先序不能被这条改动削掉）——
	envC := envCfg("sk-from-env")
	if got := Resolve(envC, st); got.OnlineAPIKey != "sk-from-env" || got.From != FromEnv {
		t.Fatalf("env 档优先序被改坏：key=%q from=%q", got.OnlineAPIKey, got.From)
	}
}

// TestRouteHydratedValueNeverMasqueradesAsDB 是上一条的**另一半**（★ 2026-10-05 第 6 波，现网演示单元抓到）。
// 上一波把"水合值冒充 env"修掉了，同一族还剩一条：第 ② 档的收尾判据
// `!snap.Placeholder && snap.OnlineAPIKey != ""` 问的是**值像不像可用**，而那个值就是上一次
// ApplyTo 写回 cfg 的产物（档位 route）——库里**根本没有 online_api_key 那一行**时它照样成立。
//
// 现网实证（只读实测，两条读数同刻取）：演示单元的热加载日志出 `"from":"db"、routes:1`，
// 而 langcross_demo 的 system_config 里**只有 model_routes 一行**（len=268），没有任何 online_api_* 行
// ⇒ 那个 "db" 是编的（主库 langcross 才有 online_api_key|len=115|enc 那一行）。
//
// 后果不止于日志撒谎：运营把那条路由（或后台那份 Key）删掉之后，这台**永远洗不掉**——
// 继续拿着库里已经不存在的凭据打上游，而健康面与日志都说"配置来自库"。
// 这正是「每台热加载」要消灭的那类形态：配置在库里改，读的人不在库里读。
func TestRouteHydratedValueNeverMasqueradesAsDB(t *testing.T) {
	forceEveryProbe(t)
	st := newStore(t)
	// 现网演示单元的库里形态：只有一条带 Key 的主路由，online_api_* 一行都没有
	routes, err := json.Marshal([]config.ProviderConfig{{Provider: "ops", APIBase: "https://route.example/v1",
		Model: "route-model", APIKey: store.EncryptSecret("sk-route-only")}})
	if err != nil {
		t.Fatalf("序列化路由失败: %v", err)
	}
	if err := st.SetConfig(KeyModelRoutes, string(routes)); err != nil {
		t.Fatalf("写路由失败: %v", err)
	}
	cfg := placeholderCfg() // env 档没给可用 Key

	// —— ① 启动水合（route），并把结果写回 cfg（ApplyTo 就是"这台此刻生效"的形态）——
	first := Resolve(cfg, st)
	if first.From != FromRoute {
		t.Fatalf("①水合档位=%q，期望 route（库里没有 online_api_key 那一行）", first.From)
	}
	first.ApplyTo(cfg)

	// —— ② 同一份库、同一台，再解析一次：档位必须**仍是 route**，不许翻成 db ——
	// 这一条就是现网那行谎言的复现点：库里始终没被写过，值也没变，档位却换了来源。
	second := Resolve(cfg, st)
	if second.From != FromRoute {
		t.Fatalf("②库里根本没有 online_api_key 那一行，档位却报 %q（期望 route）"+
			"⇒ 上一次 ApplyTo 写回的产物被当成了一种独立来源", second.From)
	}
	if second.OnlineAPIKey != "sk-route-only" {
		t.Fatalf("②档位修对了但值被洗掉了：key=%q", second.OnlineAPIKey)
	}

	// —— ③ 运营把那条路由清空（库里写 "[]"）：这台必须跟着洗掉，不许继续打已删除的凭据 ——
	if err := st.SetConfig(KeyModelRoutes, "[]"); err != nil {
		t.Fatalf("清空路由失败: %v", err)
	}
	third := Resolve(cfg, st)
	if third.OnlineAPIKey != "" {
		t.Fatalf("③库里已经把路由删了，这台还拿着已不存在的凭据：key=%q from=%q", third.OnlineAPIKey, third.From)
	}
	if third.From != FromNone {
		t.Fatalf("③三档皆空时期望档位 none，实得 %q", third.From)
	}

	// —— ④ 反向对照：库里**真**保存了 online_api_key 时，档位照旧是 db、值照旧取自库
	//（这条改动不许把 ② 那一档打掉——它清的是"本机上一次写回的产物"，不是库里的现值）——
	if err := st.SetConfig(KeyOnlineKey, store.EncryptSecret("sk-db-real")); err != nil {
		t.Fatalf("写库内 Key 失败: %v", err)
	}
	fourth := Resolve(cfg, st)
	if fourth.From != FromDB || fourth.OnlineAPIKey != "sk-db-real" {
		t.Fatalf("④库里给了 Key 却判成 key=%q from=%q（期望 sk-db-real／db）", fourth.OnlineAPIKey, fourth.From)
	}
	if fourth.Placeholder {
		t.Fatal("④已取到库里真 Key 却仍标占位符（健康面会继续报 placeholder）")
	}
}

// —— 反证口径（改本文件判据后必须逐条实跑，"绿"说明锁没射程）——
//
//	① 摘掉 Refresh 的换指针那一步（把 Publish(next) 注释掉）：
//	   TestResolveAdoptsDBConfigAfterStartup 必须红（热加载根本没生效）。
//	② 把"库读不到 ⇒ 沿用上一份"改成"读不到就按 cfg 重发一份"：
//	   TestRefreshKeepsPreviousSnapshotWhenStoreIsDown 必须红。
//	③ 把 env 档短路（envUsable 恒 false）：
//	   TestResolveEnvLegBeatsStore 必须红（库值会盖掉环境变量，违反 AGENTS §一·3）。
//	④ 去掉指纹判据（每次都 Publish）：TestRefreshSkipsWhenUnchanged 必须红。
//	⑤ 把 Current 里 `if st == nil` 那一支删掉（无库也去读全局指针）：
//	   TestCurrentIgnoresGlobalSnapshotWithoutStore 必须红（断言①命中别家配置）。
//	⑥ 去掉 Refresh 节流里的 Store 身份判据（只比 TTL）：
//	   TestCurrentReProbesWhenStoreIdentityChanges 必须红（第二座库读到第一座的快照）。
//	⑦ ★ 本批补腿的两条锁，各摘一条分别实跑：
//	   ⑦-a 把 envUsable 的**档位判据**摘掉（回到 `!placeholder && key != ""`）⇒
//	       本文件 TestHydratedValueNeverMasqueradesAsEnv 的 ② 红（库里换了 Key 而这台不跟）。
//	       ⚠️ 同包的 TestResolveAdoptsDBConfigAfterStartup 在这条反证下**照旧绿**：
//	       那个用例从不 ApplyTo，cfg 的占位标记一直是真 ⇒ 够不着这条短路。
//	       这就是为什么必须专门有一条"先水合、后改库"的锁（本函数＋cmd/server 的
//	       TestHydratedKeyIsNotRecordedAsEnv＋UAT 的 D 实例）：只测"启动时没 Key"那一台，抓不到这一族。
//	   ⑦-b 删掉 ApplyTo 里回写 OnlineAPIKeyOrigin 那一行 ⇒ 本文件 ① 红（档位停在 none，
//	       与快照不同源），cmd/server 的 TestHydratedKeyIsNotRecordedAsEnv ① 同因红。
//	       ⚠️ 跨进程那段（multi_instance_e2e 的 D 实例）**测不到 ⑦-b**：已实跑坐实——
//	       装回这一行后整脚本仍 PASS=30 FAIL=0（档位没人读，库里腿照旧跑），
//	       所以"回写这一句"只能由单测管——别拿 UAT 绿当这一句可以删。
//	   （现网演示单元的真实形态：env 没配 Key、Key 从路由水合，档位被派生状态冒充成 env ⇒ 以后库里再改不跟）。
//	⑧ 跑法：env DB_DRIVER=sqlite go test -count=1 ./internal/llmsource/
//	   （整包跑，不带 -run；单跑一条会漏掉同包方言泄漏）。
//	⑨ ★ 第 6 波那条锁 TestRouteHydratedValueNeverMasqueradesAsDB 的两条反证，**均已实跑**：
//	   ⑨-a 把 Resolve 里"非 env 来源的 base Key 按本轮无 Key 处理"那三行删掉 ⇒
//	       只有本函数红，读数正是现网那句谎言：
//	         「②库里根本没有 online_api_key 那一行，档位却报 "db"（期望 route）」
//	       同批实跑的 TestResolveEnvLegBeatsStore／TestResolveAdoptsDBConfigAfterStartup／
//	       TestHydratedValueNeverMasqueradesAsEnv **三条照旧绿**——
//	       前两条要么不 ApplyTo、要么在 ② 之前就把库里的 online_api_key 写上了，
//	       够不着"库里没这一行"这一形态。⇒ 又是一条"只测库里换了 Key 的那台抓不到这一族"，
//	       与 ⑦-a 的教训同形：**归属要按"库里有没有那一行"分派**，别拿老锁的绿当这条可以删。
//	   ⑨-b 把那三行**挪到库里 Key 赋值之后**（＝把库里真给的那份也一起清掉）⇒ 两条一起红：
//	       本函数「④库里给了 Key 却判成 key="" from="none"（期望 sk-db-real／db）」＋
//	       TestHydratedValueNeverMasqueradesAsEnv「②库里改了 Key 而这台仍用水合那一刻的旧值：
//	         got="sk-from-route" from="route"」。
//	       ⇒ 清除块的**位置**（必须在第 ② 档之前、env 短路之后）由断言钉住，不是靠注释。
//	⑩ 跑法同上；改这一族判据时 ⑨-a／⑨-b 各跑一次，两条都要看到点名到行号的读数才算数。
