// ============ systemconfig_cache_test.go · 职责说明 ============
// store 包 system_config 进程内 TTL 缓存（★ 2026-10-11 慢 SQL 批 P2⑤）的单测。
// 五条射程，全部按「等价锁＋正向对照」写：
//
//	① 写侧纪律：SetConfig 成功即翻缓存（读己之写不等 TTL）；落库失败 ⇒ 缓存一字不动；
//	② TTL 窗口内现读缓存：绕过本层直接摸库的写（raw SQL）在窗口内不可见（这是刻意接受的代价），
//	   而 TTL=0 档必须立刻现查——没有后半段，前半段就是「读点恒返旧值」的死代码；
//	③ 负缓存：缺键回 ("", nil) 也进缓存（同一空键被反复读不再往返），补写后可见性同 ② 的口径；
//	④ 读库失败 ⇒ 沿用上一份快照并回 nil error；**没有快照时错误照抛**（fail-soft 只软在有账可沿时）；
//	⑤ ConfigsByKeys 不得套第二层缓存（它是 llmsource 热加载探针自己的腿，§一·3 一把尺子），
//	   静态锁判据函数体，且拿 GetConfig 体做正向对照（否则提取函数体写错也恒绿）。
//
// ★ 反证（逐条实测，见本批记录）：
//
//	CE-1 摘掉「读库失败沿用快照」分支 ⇒ TestSysCfgKeepsSnapshotOnDBError 红；
//	CE-2 把 SetConfig 的写穿翻到落库**之前** ⇒ TestSysCfgSetFailureKeepsCache 红（保存失败被说成已生效）；
//	CE-3 把 GetConfig 的窗口命中判据改成恒假（每读必查） ⇒ TestSysCfgTTLWindowHidesRawWrites 红。
//
// =============================================
package store

import (
	"database/sql"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"translator/internal/config"
)

// newSysCfgStore 自建内存 SQLite Store 并**自钉方言**（AGENTS §一·4 模板：
// config.Default() 副作用写全局 config.C，PG 模式下会泄漏方言给同包后续测试）。
// 每个用例一个新 Store——缓存挂在实例上，换库即失效正是 P2⑤ 的口径（§一·3 第三根尺子），
// 测试靠换实例拿到干净的缓存态，不需要任何「清缓存」后门。
// ★ DSN 口径沿用 openapi_quota_gate_test.go 同款夹具的注记：裸 ":memory:" 在 database/sql 下是
// 「每条连接各自的私有库」，而 store.New 的迁移腿会在 PRAGMA 游标未关时开嵌套查询——
// 收口到单连接会死锁在 New，事后钉回 1 又把带表的连接当超额闲置关掉（首跑两连败真踩）。
// 唯一稳的形态＝命名共享缓存 DSN：所有连接看见同一块内存库，连接数不用收口。
func newSysCfgStore(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	dsn := "file:syscfg_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()) +
		"?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate"
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	raw.SetMaxOpenConns(4) // 并发腿需要多条真连接（共享缓存下它们看同一块库）
	t.Cleanup(func() { _ = raw.Close() })
	s, err := New(raw)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return s
}

// TestSysCfgReadYourWrite ①：SetConfig 成功即翻缓存——连续两次不同值的写都在**同一窗口**里立刻读到新值。
// 若写穿缺失，第二次 Get 会吃到第一次的缓存（v1），当场红。
func TestSysCfgReadYourWrite(t *testing.T) {
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "60") // 窗口拉长到 60s，排除「恰好过期」的运气成分
	s := newSysCfgStore(t)

	if err := s.SetConfig("rw_key", "v1"); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}
	if v, err := s.GetConfig("rw_key"); err != nil || v != "v1" {
		t.Fatalf("写后立读期望 v1 实际 %q err=%v", v, err)
	}
	if err := s.SetConfig("rw_key", "v2"); err != nil {
		t.Fatalf("二次写入失败: %v", err)
	}
	if v, err := s.GetConfig("rw_key"); err != nil || v != "v2" {
		t.Fatalf("改后立读期望 v2 实际 %q err=%v——写侧没翻缓存＝本实例对自己刚保存的值说没保存", v, err)
	}
}

// TestSysCfgTTLWindowHidesRawWrites ②＋③：窗口内绕过缓存腿的直接摸库不可见（含负缓存同判据），
// TTL=0 档必须现查（正向对照——没有这一段，② 的前半段就是「读点恒返旧值」的死代码也测不出来）。
func TestSysCfgTTLWindowHidesRawWrites(t *testing.T) {
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "60")
	s := newSysCfgStore(t)

	if err := s.SetConfig("win_key", "v1"); err != nil {
		t.Fatal(err)
	}
	// 绕过 SetConfig 直接改库（对应台账里 billing 启动默认项/points_rate_rebase 那批 raw 写点）
	if _, err := s.db.Exec("UPDATE system_config SET value='v2' WHERE key='win_key'"); err != nil {
		t.Fatalf("raw 改库失败: %v", err)
	}
	if v, err := s.GetConfig("win_key"); err != nil || v != "v1" {
		t.Fatalf("窗口内期望读到缓存 v1 实际 %q err=%v（缓存没生效＝P2⑤ 白做）", v, err)
	}
	// 正向对照：TTL=0 显式现查档必须立刻现读现见
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "0")
	if v, err := s.GetConfig("win_key"); err != nil || v != "v2" {
		t.Fatalf("TTL=0 档期望现查 v2 实际 %q err=%v（现查腿是死的 ⇒ 上面那条窗口锁恒真是空转）", v, err)
	}

	// ③ 负缓存：缺键先读一次（进缓存 ""），再 raw 补行，窗口内仍不可见；TTL=0 现查可见
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "60")
	if v, err := s.GetConfig("ghost_key"); err != nil || v != "" {
		t.Fatalf("缺键契约期望 (\"\", nil) 实际 (%q, %v)", v, err)
	}
	if _, err := s.db.Exec("INSERT INTO system_config (key, value, updated_at) VALUES ('ghost_key','g1','now')"); err != nil {
		t.Fatalf("raw 补行失败: %v", err)
	}
	if v, err := s.GetConfig("ghost_key"); err != nil || v != "" {
		t.Fatalf("负缓存窗口内期望仍为 \"\" 实际 %q err=%v", v, err)
	}
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "0")
	if v, err := s.GetConfig("ghost_key"); err != nil || v != "g1" {
		t.Fatalf("TTL=0 档期望现查到 g1 实际 %q err=%v（负缓存没被现查档绕开＝补键永远看不见）", v, err)
	}
}

// TestSysCfgKeepsSnapshotOnDBError ④：读库失败沿用上一份快照（回缓存值、错误吞掉），
// 但**没有快照时错误照抛**——两腿都在，缺一腿就是「把抖动写成配置没了」或「把坏库伪装成空配置」。
// ★ 快照腿必须把 **TTL 窗口放过期**再打坏库：窗口内命中走的是「零往返」那条支，
// 根本不会走到「查询失败⇒沿用快照」——首跑反证 CE-1（整条摘掉快照支）照样全绿，
// 就是被这 60 秒窗口掩护的（同 AGENTS §三「单句靶子被 fail-soft 掩护」一族）。
// 同用例另收 ① 的失败支（CE-2 靶）：落库失败 ⇒ 缓存一字不动。
func TestSysCfgKeepsSnapshotOnDBError(t *testing.T) {
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "1")
	s := newSysCfgStore(t)

	if err := s.SetConfig("warm_key", "v1"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // 窗口过期：条目仍在 map，但读腿必须去查库
	// 毁掉读腿：Drop 表后任何 SELECT/INSERT 都失败，模拟「库坏了/连接断了」
	if _, err := s.db.Exec("DROP TABLE system_config"); err != nil {
		t.Fatalf("DROP 失败: %v", err)
	}
	// 有快照 ⇒ 沿用上份、错误不外抛（口径：读不到 ≠ 没有）——查询此刻必失败，只有快照支能给出 v1
	if v, err := s.GetConfig("warm_key"); err != nil || v != "v1" {
		t.Fatalf("库故障时有快照应沿用 v1 实际 %q err=%v", v, err)
	}
	// 无快照 ⇒ 错误照抛，不许伪装成「这个键是空的」
	if v, err := s.GetConfig("cold_key"); err == nil {
		t.Fatalf("库故障且无快照却回了 (%q, nil)——把坏库读成空配置是静默失败的形态", v)
	}
	// ① 失败支：落库失败（表已没了）⇒ 返回错误且缓存不翻；随后沿用快照读到的仍是旧值
	if err := s.SetConfig("warm_key", "v2"); err == nil {
		t.Fatal("表已删除而 SetConfig 却报成功——写侧根本没落库")
	}
	time.Sleep(1100 * time.Millisecond) // 再让窗口过一次，确保读一定踩到坏库、只能靠快照
	if v, err := s.GetConfig("warm_key"); err != nil || v != "v1" {
		t.Fatalf("保存失败后期望缓存仍为 v1 实际 %q err=%v（失败也把新值翻进缓存＝把「保存失败」说成「这台已生效」）", v, err)
	}
}

// TestSysCfgTTLKnob 档位判据：默认 5s、0 关档、非法回落默认（与 probeTTL 同口径，三档各有一腿）。
func TestSysCfgTTLKnob(t *testing.T) {
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "")
	if got := sysConfigCacheTTL(); got != 5*time.Second {
		t.Fatalf("默认档期望 5s 实际 %v", got)
	}
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "0")
	if got := sysConfigCacheTTL(); got != 0 {
		t.Fatalf("0 档应显式关缓存，实际 %v", got)
	}
	for _, bad := range []string{"-3", "abc", "1.5"} {
		t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", bad)
		if got := sysConfigCacheTTL(); got != 5*time.Second {
			t.Fatalf("非法值 %q 应回落默认 5s，实际 %v", bad, got)
		}
	}
}

// TestConfigsByKeysNotDoubleCached ⑤：静态锁——ConfigsByKeys 的**函数体**不得碰 sysCfgCache/sysCfgMu
// （它是 llmsource 热加载探针自己的腿，那把尺子已带 TTL＋指纹节流，套第二层＝同一把尺子下面垫第二层延迟）。
// 正向对照：同一段提取逻辑下 GetConfig 体必须**含** sysCfgCache——否则「提取失败得到空串」也能让负向恒真。
func TestConfigsByKeysNotDoubleCached(t *testing.T) {
	src, err := os.ReadFile("systemconfig.go")
	if err != nil {
		t.Fatalf("读源文件失败: %v", err)
	}
	body := funcBody(string(src), "func (s *Store) ConfigsByKeys(")
	if body == "" {
		t.Fatal("函数体提取失败（ConfigsByKeys 签名变了？提取器要跟着改，别让负向锁变空转）")
	}
	if strings.Contains(body, "sysCfgCache") || strings.Contains(body, "sysCfgMu") {
		t.Fatal("ConfigsByKeys 碰了第二层缓存——llmsource 那把尺子之下不许再垫一层")
	}
	// 正向对照：GetConfig 体必须真带缓存腿
	cb := funcBody(string(src), "func (s *Store) GetConfig(")
	if !strings.Contains(cb, "sysCfgCache") {
		t.Fatalf("GetConfig 体里找不到 sysCfgCache——提取逻辑或实现已偏离，⑤ 的负向锁失去对照: %q", cb)
	}
}

// funcBody 从 header 签名起截取到下一个顶层 func 之前的体（简单行扫描，够用且不引 AST 依赖）。
func funcBody(src, header string) string {
	i := strings.Index(src, header)
	if i < 0 {
		return ""
	}
	rest := src[i:]
	nl := strings.Index(rest, "\n")
	if nl < 0 {
		return rest
	}
	if j := strings.Index(rest[nl+1:], "\nfunc "); j >= 0 {
		return rest[:nl+1+j] // 截到下一个顶层 func 的换行之前
	}
	return rest
}

// TestSysCfgCacheConcurrent -race 腿：并发读写同一 Store 的缓存不得触发 map 竞争。
// 8 个 goroutine 交替 Set/Get 同一个键（TTL=5s——**必须开着缓存**才有 sysCfgMu 保护的读写可压；
// TTL=0 档两条缓存腿都不走，拿它跑并发等于没测）。
func TestSysCfgCacheConcurrent(t *testing.T) {
	t.Setenv("SYSTEM_CONFIG_CACHE_TTL_SEC", "5")
	s := newSysCfgStore(t) // 共享缓存 DSN：8 条真并发连接看见同一块库（夹具注释里有两连败的坑账）
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if err := s.SetConfig("conc_key", "v"); err != nil {
					t.Errorf("并发写失败: %v", err)
					return
				}
				if v, err := s.GetConfig("conc_key"); err != nil || v != "v" {
					t.Errorf("并发读异常: %q %v", v, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
}
