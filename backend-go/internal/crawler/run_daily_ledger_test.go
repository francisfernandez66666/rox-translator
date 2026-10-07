// ============ run_daily_ledger_test.go · 职责说明 ============
// 锁「采集进度账回收腿挂在 RunDaily 入口」这一条**接线**（★ 2026-10-07 〇-AR 第 7 波，入账（51））。
//
// 为什么单测 store 那边还不够：回收的**判据**（日期窗口、形状白名单、分块排空）由
// internal/store/kb_scrape_ledger_test.go 覆盖，但「这一腿到底有没有被每日采集跑到」是另一件事——
// 现网那 13,793 行只写不删的形态，正是因为写侧存在、清侧压根没被任何入口调用过。
// 判据正确却没接线＝账目继续涨，而且日志一行都不会报。
//
// ★ 反证：把 crawler.RunDaily 里那段 `PruneScrapeLedger` 调用摘掉（或把它挪进
//
//	`for _, src := range sources` 循环里、让"零启用源"这一天不跑），本测当场红。
//
// 本测**不触发任何真实采集**：`store.New()` 会 seed 一批启用源，构造 helper 里先把它们全部停用并断言
// "启用源=0"（首跑真踩：不停用就会打外网并挂死测试）。RunDaily 走到入口回收后因无源可采而空转返回。
// =============================================
package crawler

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/store"
)

// newLedgerCrawler 建一个内存 SQLite 的 store 与挂它的 Crawler（自钉方言，AGENTS §一·4）。
//
// ★ 必须先把数据源全部停用：`store.New()` 在空表上会 `SeedDefaultScrapeSources()` 灌一批启用源，
// 而 RunDaily 是**真打外网**的（首跑真踩：本测挂 90 s，panic dump 里全是 persistConn 读循环）。
// 单元测试不许拨真上游——停用后先断言"启用源=0"，这道守卫让"以后又有人往 seed 里加源"
// 变成当场红，而不是某天 CI 白打一遍外网。
func newLedgerCrawler(t *testing.T) (*Crawler, *store.Store) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })

	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	st, err := store.New(raw)
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	all, err := st.ListScrapeSources()
	if err != nil {
		t.Fatalf("列数据源失败: %v", err)
	}
	for _, src := range all {
		if err := st.SetScrapeSourceEnabled(src.ID, 0); err != nil {
			t.Fatalf("停用源 %d 失败: %v", src.ID, err)
		}
	}
	if enabled, err := st.ListEnabledScrapeSources(); err != nil || len(enabled) != 0 {
		t.Fatalf("前置守卫失败：仍有 %d 个启用源（err=%v）＝本测会真打外网，禁止", len(enabled), err)
	}
	c := New(st)
	c.Quiet = true // 安静档：万一将来有源被启用，也别让日志淹掉真正的失败信息
	return c, st
}

// TestRunDailyPrunesScrapeLedger 每日采集入口必须顺带回收过期进度账（接线锁）。
func TestRunDailyPrunesScrapeLedger(t *testing.T) {
	c, st := newLedgerCrawler(t)

	// 一把远早于保留窗口的断点键（2025-01-01，源 1）＋一把当天的键（必须原样留着）。
	stale := store.ScrapeCheckpointKey("2025-01-01", 1)
	fresh := store.ScrapeCheckpointKey(time.Now().Format("2006-01-02"), 1)
	for _, k := range []string{stale, fresh} {
		if err := st.SetConfig(k, "cursor"); err != nil {
			t.Fatalf("预置键 %s 失败: %v", k, err)
		}
	}

	if _, err := c.RunDaily(context.Background()); err != nil {
		t.Fatalf("RunDaily 不应报错: %v", err)
	}

	if v, _ := st.GetConfig(stale); v != "" {
		t.Fatal("过期进度账仍留在库里＝回收腿没挂在每日采集入口上（现网 13,793 行只写不删的形态会一直续着）")
	}
	if v, _ := st.GetConfig(fresh); v == "" {
		t.Fatal("当天的进度账被删了＝回收范围越界，当天那一轮断点会从头再采")
	}
}

// TestRunDailyKeepsUnreadableLedger 形状不合的账目一律**跳过不删**（口径 1 的接线侧证据）。
// 为什么这一条不能省：判据函数的白名单在 store 侧已有单测，但**采集入口真的把那份判据当唯一判据**吗——
// 若将来有人在 RunDaily 里加一条"顺手按 LIKE 批量清"的腿，脏键会在生产里被猜掉，
// 而 `kb_scrape_daily_marker`（运营视图在读的"最近完成日"）恰在同族前缀里，被清＝采集状态读数静默丢失。
func TestRunDailyKeepsUnreadableLedger(t *testing.T) {
	c, st := newLedgerCrawler(t)

	dirty := []string{
		"kb_scrape_checkpoint_不是日期_1",
		"kb_scrape_daily_source_done_2025-13-40_1",
		"kb_scrape_daily_marker",
	}
	for _, k := range dirty {
		if err := st.SetConfig(k, "1"); err != nil {
			t.Fatalf("预置脏键 %s 失败: %v", k, err)
		}
	}
	if _, err := c.RunDaily(context.Background()); err != nil {
		t.Fatalf("RunDaily 不应因回收而报错: %v", err)
	}
	for _, k := range dirty {
		if v, _ := st.GetConfig(k); v == "" {
			t.Fatalf("脏键 %s 被删＝回收腿把\"解析不了\"当成了\"该删\"（AGENTS：宁可漏删不可误删）", k)
		}
	}
}
