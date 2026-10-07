// ============ kb_scrape_ledger_test.go · 职责说明 ============
// store 包「采集进度账回收腿」（kb_scrape_ledger.go）的单测（★ 2026-10-07 〇-AR 第 7 波，入账（51））。
// 四条射程：
//
//	① 键形状判据与两个 builder **同源**（改名/换前缀必须当场红，否则回收腿扫不到任何行却一片绿）；
//	② 保留窗口内／截止日当天／当天／未来日期／同表其他键／形状不合的键**一个都不许删**；
//	③ 早于截止日的键全部删掉，且**跨块**（>200 把）也排得空（锁分块 DELETE 的循环口径）；
//	④ 保留天数可被 system_config 覆盖，非法值回落默认。
//
// ★ 反证（三条，逐条实测过，见本批记录）：
//
//	A. 把 `date >= cutoff` 那档 continue 摘掉 ⇒ TestPruneKeepsWindowAndBoundary 红（保留窗口形同虚设）；
//	B. 把 parseScrapeLedgerKey 的"形状不合返回 false"改成"直接按 LIKE 结果删" ⇒
//	   TestParseScrapeLedgerKeyRejectsMalformed／TestPruneKeepsForeignKeys 红（LIKE 的下划线通配会误伤）；
//	C. 把分块循环的步进改成"一次跳到底"（只删第一块就出循环）⇒ TestPruneSpansMultipleChunks 红
//	   （实测读数 200/450）。注意这一条锁的是**跨块排空**，不是驱动参数上限——
//	   本机 SQLite 驱动 450 个占位符一条 IN 也能跑通（实测同一用例仍绿），
//	   所以"每块 200"是按 PG/SQLite 参数量留余量的**设计取向**，别把它写成有反证的机械锁。
//
// =============================================
package store

import (
	"database/sql"
	"testing"
	"time"

	"translator/internal/config"
)

// newLedgerStore 自建一个内存 SQLite Store，并**自钉方言**（AGENTS §一·4：
// config.Default() 有写全局 config.C 的副作用，PG 模式下会泄漏方言给同包后续测试）。
func newLedgerStore(t *testing.T) *Store {
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
	s, err := New(raw)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	return s
}

// TestScrapeLedgerPrefixesMatchKeyBuilders 回收腿的前缀常量必须与写侧两个 builder 逐字同源。
// 这是"改 builder 忘改判据 ⇒ 回收腿恒扫到 0 行、账目继续涨却一片绿"的唯一机械防线。
func TestScrapeLedgerPrefixesMatchKeyBuilders(t *testing.T) {
	ck := ScrapeCheckpointKey("2026-10-07", 12)
	dn := SourceDoneKey("2026-10-07", 12)
	if got := ck; len(got) < len(scrapeCheckpointPrefix) || got[:len(scrapeCheckpointPrefix)] != scrapeCheckpointPrefix {
		t.Fatalf("断点键 %q 不以回收腿前缀 %q 开头 ⇒ 写侧与清侧已分叉", ck, scrapeCheckpointPrefix)
	}
	if dn[:len(scrapeSourceDonePrefix)] != scrapeSourceDonePrefix {
		t.Fatalf("完成标记键 %q 不以回收腿前缀 %q 开头 ⇒ 写侧与清侧已分叉", dn, scrapeSourceDonePrefix)
	}
	// 解析必须还原出同一对（日期, 源 ID）——builder 若把两段顺序调反，这里当场红。
	for _, tc := range []struct{ key, wantDate string }{{ck, "2026-10-07"}, {dn, "2026-10-07"}} {
		date, id, ok := parseScrapeLedgerKey(tc.key)
		if !ok {
			t.Fatalf("%q 应被判为合法进度账键（回收腿会漏删它）", tc.key)
		}
		if date != tc.wantDate || id != 12 {
			t.Fatalf("%q 解析成 (%q,%d)，期望 (%q,12)", tc.key, date, id, tc.wantDate)
		}
	}
}

// TestParseScrapeLedgerKeyRejectsMalformed 形状不合的键必须判 false（不删），逐条点名。
func TestParseScrapeLedgerKeyRejectsMalformed(t *testing.T) {
	bad := []string{
		"kb_scrape_daily_marker",                     // 同族但不是按日拼键的进度账（运营视图在读它）
		"kb_scrape_checkpoint_2026-10-07",            // 缺源 ID 段
		"kb_scrape_checkpoint_2026-10-07_",           // 源 ID 段为空
		"kb_scrape_checkpoint__12",                   // 日期段为空
		"kb_scrape_checkpoint_2026-10-07_ab",         // 源 ID 不是纯数字
		"kb_scrape_checkpoint_2026-13-40_1",          // 日历上不存在的日期
		"kb_scrape_checkpoint_2026-1-7_1",            // 未补零（不是写侧形态，往返一致校验拒掉）
		"kb_scrape_checkpoint_2026-10-07_1_extra",    // 多一段（不认识的后＝不是本模块写的）
		"kbXscrape_checkpoint_2025-01-01_1",          // LIKE 下划线通配捞进来的无关键（真判据在 Go 侧）
		"kb_scrape_daily_source_done_2025-01-01_1_x", // 同上，多一段
	}
	for _, k := range bad {
		if _, _, ok := parseScrapeLedgerKey(k); ok {
			t.Fatalf("%q 被判成合法进度账键＝越界：回收腿可能删掉不该删的行", k)
		}
	}
}

// TestPruneKeepsWindowAndBoundary 保留窗口／边界／外键五档都必须留着。
// 钉住"现在"=2026-10-07，默认保留 14 天 ⇒ 截止日=2026-09-23：
// 早于 09-23 的删，09-23 当天留、09-24 留、今天留、明天留、marker 留。
func TestPruneKeepsWindowAndBoundary(t *testing.T) {
	s := newLedgerStore(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)

	seed := []string{
		ScrapeCheckpointKey("2026-09-22", 1), // 早于截止日 ⇒ 唯一该删的
		ScrapeCheckpointKey("2026-09-23", 2), // 截止日当天 ⇒ 留
		SourceDoneKey("2026-09-23", 2),       // 截止日当天 ⇒ 留
		ScrapeCheckpointKey("2026-10-07", 3), // 当天 ⇒ 留（正在被读）
		SourceDoneKey("2026-10-07", 3),       // 当天 ⇒ 留
		ScrapeCheckpointKey("2026-12-01", 4), // 未来日期 ⇒ 留（宁可不删）
		"kb_scrape_daily_marker",             // 同前缀族但非日期键 ⇒ 留（运营视图在读）
	}
	for _, k := range seed {
		if err := s.SetConfig(k, "1"); err != nil {
			t.Fatalf("预置键 %s 失败: %v", k, err)
		}
	}

	n, err := s.PruneScrapeLedger(now)
	if err != nil {
		t.Fatalf("回收不应报错: %v", err)
	}
	if n != 1 {
		t.Fatalf("应只删 1 把过期键，实际删 %d 把（窗口判据或形状判据漂了）", n)
	}
	for _, k := range seed {
		v, err := s.GetConfig(k)
		if err != nil {
			t.Fatalf("复查键 %s 失败: %v", k, err)
		}
		if k == ScrapeCheckpointKey("2026-09-22", 1) {
			if v != "" {
				t.Fatalf("%s 应被回收掉（早于截止日 2026-09-23）", k)
			}
			continue
		}
		if v == "" {
			t.Fatalf("%s 被误删＝越界：保留窗口/当天/未来/外键都不许动", k)
		}
	}
}

// TestPruneKeepsForeignKeys 外键独立成一条锁（文件头反证 B 点名的就是这条）。
// 为什么要单独一条：SQL 里粗筛用的是 `LIKE 'kb_scrape_%'`，而 LIKE 的 `_` 是**单字符通配符**，
// 于是 `kbZscrape_checkpoint_2025-01-01_1` 这种"看着像、其实不是本模块写的"键也会被捞进候选集；
// 真判据只在 Go 侧的 parseScrapeLedgerKey。这条用例同时压一把**该删**的旧键做正锁——
// 只有负向没有正向的锁，判据被整个摘掉时照样绿灯（AGENTS 那条「负向锁必须配正向对照」）。
// 反证：把 `if !ok { continue }` 那档摘掉（＝按 LIKE 结果直接删）⇒ 本用例当场红。
func TestPruneKeepsForeignKeys(t *testing.T) {
	s := newLedgerStore(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)

	// 五把我键：三把会被 LIKE 捞进候选（下划线通配／同族 marker／id 段非数字），
	// 两把压根不匹配粗筛（回收旋钮本身与另一域的保留天数键）。
	foreign := []string{
		"kbZscrape_checkpoint_2025-01-01_1", // LIKE 的 `_` 把它捞进来，但它没有那两把前缀
		"kb_scrape_daily_marker",            // 同族非日期键（运营视图在读）
		"kb_scrape_checkpoint_2025-01-01_x", // 前缀对、id 段不是纯数字＝不是本模块写的形态
		"scrape_ledger_retention_days",      // 回收旋钮自己：删了它下一次就静默回落默认天数
		"ticket_retention_days",             // 另一域的保留天数键
	}
	for _, k := range foreign {
		if err := s.SetConfig(k, "keepme"); err != nil {
			t.Fatalf("预置外键 %s 失败: %v", k, err)
		}
	}
	// 同一时刻压一把真该删的旧账，证明"外键留着"不是"整条回收腿没干活"。
	stale := ScrapeCheckpointKey("2026-09-01", 5)
	if err := s.SetConfig(stale, "cursor"); err != nil {
		t.Fatalf("预置过期键失败: %v", err)
	}

	n, err := s.PruneScrapeLedger(now)
	if err != nil {
		t.Fatalf("回收不应报错: %v", err)
	}
	if n != 1 {
		t.Fatalf("只该回收那把过期进度账，实际删了 %d 把＝外键被当成进度账删了（LIKE 粗筛越界）", n)
	}
	if v, _ := s.GetConfig(stale); v != "" {
		t.Fatal("过期进度账没被回收＝正锁落空，本条绿灯属于假绿")
	}
	for _, k := range foreign {
		v, err := s.GetConfig(k)
		if err != nil {
			t.Fatalf("复查外键 %s 失败: %v", k, err)
		}
		if v != "keepme" {
			t.Fatalf("外键 %s 被删或被改（读到 %q）＝回收范围越界", k, v)
		}
	}
}

// TestPruneSpansMultipleChunks 过期键超过一块（>200）时必须全部排空。
// 现网实测本族 13,793 行，一次回收就是几百上千把键——分块循环的"最后一块"最容易漏。
func TestPruneSpansMultipleChunks(t *testing.T) {
	s := newLedgerStore(t)
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)

	const total = 450 // 200＋200＋50：三个块，最后一块不足整块
	stale := make([]string, 0, total)
	for i := 0; i < total; i++ {
		// 日期都落在 2025 年（远早于截止日），每个索引配一个源 ID，键名唯一。
		k := ScrapeCheckpointKey("2025-03-04", int64(i+1))
		if err := s.SetConfig(k, "cursor"); err != nil {
			t.Fatalf("预置第 %d 把键失败: %v", i, err)
		}
		stale = append(stale, k)
	}
	// 同时压一把"该留"的：今天的完成标记，防止分块删除把范围写宽。
	keep := SourceDoneKey("2026-10-07", 999)
	if err := s.SetConfig(keep, "1"); err != nil {
		t.Fatalf("预置保留键失败: %v", err)
	}

	n, err := s.PruneScrapeLedger(now)
	if err != nil {
		t.Fatalf("回收不应报错: %v", err)
	}
	if n != total {
		t.Fatalf("应回收 %d 把键，实际 %d 把＝分块循环只删了第一块或提前退出", total, n)
	}
	for _, k := range stale {
		if v, _ := s.GetConfig(k); v != "" {
			t.Fatalf("%s 残留在库＝跨块未排空", k)
		}
	}
	if v, _ := s.GetConfig(keep); v == "" {
		t.Fatal("当天的键被删＝回收范围写宽了")
	}
}

// TestPruneEmptyLedgerIsNoop 库内一把进度账都没有时，必须**正常返回 (0,nil)**。
// 与 〇-AP 那条同族教训：清侧一旦"无事可做"就报错或空转，会把调用方（每日采集入口）拖红。
func TestPruneEmptyLedgerIsNoop(t *testing.T) {
	s := newLedgerStore(t)
	n, err := s.PruneScrapeLedger(time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local))
	if err != nil {
		t.Fatalf("空账回收不应报错: %v", err)
	}
	if n != 0 {
		t.Fatalf("空账应删 0 把，实际 %d", n)
	}
}

// TestScrapeLedgerRetentionDaysOverrideAndFallback 保留天数三级口径：库内合法值 > 默认 14；
// 非法值（空/非数字/0/负数）一律回落默认——不许出现"配 0 就把历史账全删光"这种读法。
func TestScrapeLedgerRetentionDaysOverrideAndFallback(t *testing.T) {
	s := newLedgerStore(t)
	if got := s.ScrapeLedgerRetentionDays(); got != 14 {
		t.Fatalf("键不存在时应回落默认 14，实际 %d", got)
	}
	for _, tc := range []struct {
		v    string
		want int
	}{
		{"3", 3}, {"365", 365}, {" 5 ", 5},
		{"", 14}, {"abc", 14}, {"0", 14}, {"-1", 14},
	} {
		if err := s.SetConfig("scrape_ledger_retention_days", tc.v); err != nil {
			t.Fatalf("预置保留天数 %q 失败: %v", tc.v, err)
		}
		if got := s.ScrapeLedgerRetentionDays(); got != tc.want {
			t.Fatalf("保留天数 %q 读成 %d，期望 %d", tc.v, got, tc.want)
		}
	}
}

// TestPruneRespectsConfiguredRetention 配置生效要落到**删除范围**上，不只是读数：
// 同样一份账，保留 3 天时删、保留 30 天时留。
func TestPruneRespectsConfiguredRetention(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.Local)
	k := ScrapeCheckpointKey("2026-10-02", 7) // 距 now 5 天

	for _, tc := range []struct {
		days string
		want int
	}{{"3", 1}, {"30", 0}} {
		s := newLedgerStore(t)
		if err := s.SetConfig("scrape_ledger_retention_days", tc.days); err != nil {
			t.Fatalf("预置保留天数失败: %v", err)
		}
		if err := s.SetConfig(k, "cursor"); err != nil {
			t.Fatalf("预置进度账失败: %v", err)
		}
		n, err := s.PruneScrapeLedger(now)
		if err != nil {
			t.Fatalf("回收不应报错: %v", err)
		}
		if n != tc.want {
			t.Fatalf("保留 %s 天时应删 %d 把，实际 %d 把（配置没接到删除判据上）", tc.days, tc.want, n)
		}
		v, _ := s.GetConfig(k)
		if (tc.want == 1) == (v != "") {
			t.Fatalf("保留 %s 天：键归宿与删除读数不一致（deleted=%d value=%q）", tc.days, n, v)
		}
	}
}
