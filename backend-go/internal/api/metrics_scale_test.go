// ============ metrics_scale_test.go · 职责说明 ============
// api 包 /metrics 昂贵段 TTL 快照与聚合腿（★ 2026-10-11 慢 SQL 批 P2⑥）的单测。
// 四条射程：
//
//	① 档位判据：METRICS_SCALE_TTL_SEC 默认 10s／0 排障档／非法回落（与 probeTTL 同口径）；
//	② 昂贵段内容：堆两档＋租户/余额/队列三档聚合 SQL 直读等值（KB 档在 s.DB=nil 时必须**省略**，
//	   不许造假 0）；期望值一律按「基线＋本用例种的量」现算（baseGauges），不假设表本来是空的；
//	③ TTL 窗口内吃快照（加插租户后读数不变）＋TTL=0 现算（读数必须跟上）——两腿互为对照；
//	④ 静态负向锁：metrics.go 不得再出现逐租户 GetBalance 抓取写风暴腿与 Ten.List 全表遍历腿，
//	   正向对照＝同一份源码里必须找得到替换后的聚合 SQL 与快照入口（防「文件读空也恒绿」）。
//
// =============================================
package api

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "modernc.org/sqlite"
)

// pinScaleDialect 自钉 sqlite 方言（AGENTS §一·4 模板，同包 openapi_quota_gate_test.go 同款）。
func pinScaleDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// newScaleStore 命名共享缓存 DSN（跨连接的表可见性同 openapi 夹具口径；连接数不收口）。
func newScaleStore(t *testing.T) *store.Store {
	t.Helper()
	pinScaleDialect(t)
	dsn := fmt.Sprintf("file:mscale_%s?mode=memory&cache=shared&_pragma=busy_timeout(5000)&_txlock=immediate",
		strings.NewReplacer("/", "_", " ", "_").Replace(t.Name()))
	raw, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	raw.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 Store 失败: %v", err)
	}
	// tenants 表**不在 store.New 的迁移清单里**（它由 internal/tenant.NewStore 幂等建表，
	// 生产装配同款）——夹具走真构造函数而不是手抄 CREATE，schema 漂移时当场暴露而非静默错表。
	if _, err := tenant.NewStore(raw); err != nil {
		t.Fatalf("建租户表失败: %v", err)
	}
	return st
}

// baseGauges 现读三条判据的基线读数（store.New 会不会灌默认行不进本测试的假设——
// 期望值一律＝基线＋本用例自己种的量，等值锁不依赖环境前提）。
func baseGauges(t *testing.T, db *sql.DB) (active int64, bal int64, queued int64) {
	t.Helper()
	if err := db.QueryRow("SELECT COUNT(*) FROM tenants WHERE status='active'").Scan(&active); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COALESCE(SUM(balance),0) FROM balance_accounts").Scan(&bal); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM jobs WHERE status='queued'").Scan(&queued); err != nil {
		t.Fatal(err)
	}
	return
}

// TestMetricsScaleTTLKnob ①：三档判据各一腿。
func TestMetricsScaleTTLKnob(t *testing.T) {
	t.Setenv("METRICS_SCALE_TTL_SEC", "")
	if got := metricsScaleTTL(); got != 10*time.Second {
		t.Fatalf("默认档期望 10s 实际 %v", got)
	}
	t.Setenv("METRICS_SCALE_TTL_SEC", "0")
	if got := metricsScaleTTL(); got != 0 {
		t.Fatalf("0 档应现算现查，实际 %v", got)
	}
	for _, bad := range []string{"-2", "xyz"} {
		t.Setenv("METRICS_SCALE_TTL_SEC", bad)
		if got := metricsScaleTTL(); got != 10*time.Second {
			t.Fatalf("非法值 %q 应回落默认 10s，实际 %v", bad, got)
		}
	}
}

// TestMetricsScaleSegmentLegs ②：聚合腿直读等值＋缺 KB 库时省略该档。
func TestMetricsScaleSegmentLegs(t *testing.T) {
	st := newScaleStore(t)
	db := st.DB()
	baseActive, baseBal, baseQueued := baseGauges(t, db)
	if _, err := db.Exec("INSERT INTO tenants (code, name, status) VALUES ('ms_a1','A1','active'),('ms_a2','A2','active'),('ms_off','OFF','disabled')"); err != nil {
		t.Fatalf("种租户失败: %v", err)
	}
	if _, err := db.Exec("INSERT INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (1,1000,'tokens','now'),(2,500,'tokens','now')"); err != nil {
		t.Fatalf("种余额失败: %v", err)
	}
	if _, err := db.Exec("INSERT INTO jobs (type, payload, status) VALUES ('ticket_run','{}','queued')"); err != nil {
		t.Fatalf("种任务失败: %v", err)
	}
	s := &Server{Store: st, metrics: newMetrics()}
	seg := s.buildScaleSegment()
	for _, want := range []string{
		fmt.Sprintf("translator_tenants_active %d\n", baseActive+2),       // 只数启用（disabled 那条不入账）
		fmt.Sprintf("translator_balance_tokens_total %d\n", baseBal+1500), // 全表 SUM
		fmt.Sprintf("translator_jobs_queued %d\n", baseQueued+1),
		"# TYPE go_heap_bytes gauge",
		"# TYPE go_alloc_bytes counter",
	} {
		if !strings.Contains(seg, want) {
			t.Fatalf("昂贵段缺 %q，实际:\n%s", want, seg)
		}
	}
	// s.DB=nil（无 KB 库）⇒ KB 档必须**不出现**：读不到就不许造一档 0（旧实现同口径）
	if strings.Contains(seg, "translator_kb_entries_total") {
		t.Fatalf("无 KB 库却吐了 kb 档（造假读数的形态）:\n%s", seg)
	}
}

// TestMetricsScaleSegmentCached ③：窗口内吃快照，TTL=0 现算跟上——两腿互为对照。
func TestMetricsScaleSegmentCached(t *testing.T) {
	t.Setenv("METRICS_SCALE_TTL_SEC", "60")
	st := newScaleStore(t)
	baseActive, _, _ := baseGauges(t, st.DB())
	s := &Server{Store: st, metrics: newMetrics()}

	seg1 := s.expensiveScaleSegment()
	if _, err := st.DB().Exec("INSERT INTO tenants (code, name, status) VALUES ('ms_live','LIVE','active')"); err != nil {
		t.Fatal(err)
	}
	seg2 := s.expensiveScaleSegment()
	if seg2 != seg1 {
		t.Fatal("窗口内昂贵段现算了（TTL 快照没生效＝P2⑥ 白做）")
	}
	// 正向对照：TTL=0 排障档必须现查并带上新种的启用租户
	t.Setenv("METRICS_SCALE_TTL_SEC", "0")
	seg3 := s.expensiveScaleSegment()
	if want := fmt.Sprintf("translator_tenants_active %d\n", baseActive+1); !strings.Contains(seg3, want) {
		t.Fatalf("TTL=0 档没现查到新租户（应含 %q）——现算腿是死的 ⇒ 上面那条窗口不变属恒真:\n%s", want, seg3)
	}

	// metrics 采集器缺失的裸 Server：不许 panic，直接现算（测试/局部装配的常见形态）
	bare := &Server{Store: st}
	if seg := bare.expensiveScaleSegment(); !strings.Contains(seg, "go_heap_bytes") {
		t.Fatalf("无采集器时昂贵段应仍可现算堆档，实际:\n%s", seg)
	}
}

// TestMetricsNoNPlusOneOnScrapePath ④：静态负向锁——抓取路径不再逐租户 GetBalance（每次带 INSERT）
// 也不再 Ten.List 全表遍历；正向对照＝同一份源码里必须找得到替换后的聚合 SQL 与快照入口。
func TestMetricsNoNPlusOneOnScrapePath(t *testing.T) {
	src, err := os.ReadFile("metrics.go")
	if err != nil {
		t.Fatalf("读源文件失败: %v", err)
	}
	body := string(src)
	for _, forbidden := range []string{"s.Store.GetBalance(", "s.Ten.List("} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metrics.go 里复活了抓取写风暴腿 %q（P2⑥ 的取证本体）", forbidden)
		}
	}
	for _, want := range []string{"COALESCE(SUM(balance),0)", "expensiveScaleSegment()", "FROM tenants WHERE status='active'"} {
		if !strings.Contains(body, want) {
			t.Fatalf("metrics.go 缺 %q——负向锁的对照腿（否则文件读空也恒绿）", want)
		}
	}
}
