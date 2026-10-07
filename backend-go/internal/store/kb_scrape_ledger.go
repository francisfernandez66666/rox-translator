// ============ kb_scrape_ledger.go · 职责说明 ============
// store 包「行业包采集进度账」的**回收腿**（★ 2026-10-07 〇-AR 第 7 波，缺陷入账（51））。
//
// 为什么必须有这一条腿：kbscrape.go 的断点/完成标记是**按日拼键**写进 system_config 的
//
//	kb_scrape_checkpoint_<YYYY-MM-DD>_<source_id>      断点游标（某源某日采到哪一块）
//	kb_scrape_daily_source_done_<YYYY-MM-DD>_<source_id> 某源某日是否已采完
//
// 而读侧（internal/crawler/crawler.go 的 RunSource）只用 `time.Now()` 拼**当天**那一把键 ⇒
// 昨天及更早的键**没有任何读方**：既不会被复用，也不会被覆盖。
// 现网实证（2026-10-07 只读复核）：`system_config` 共 13,844 行，其中本族 10,244＋3,549＝**13,793 行**，
// 最新键是当天日期——写侧每天为每个启用源各写一到两行，采集域却**零回收**（全仓两处
// `DELETE FROM system_config` 都是各自域的精确键：支付渠道密文配置、汇率表）。
// 与「13 个会话壳行」同族：写侧带日期、清侧无判据 ⇒ 账目只涨不落，
// 而 `system_config` 是配置表，读它的路径（ConfigsByKeys／管理台整表视图）都会被这份膨胀拖累。
//
// 三条硬口径（改这一腿前必读）：
//  1. **判据问日期，不问"键在不在"**：只有能解析出合法日期、且日期早于保留截止日的键才删；
//     形状不合（缺日期段、日期解析失败、id 段不是纯数字）与**未来日期**一律**跳过不删**——
//     宁留一批旧账，也不能把还在用的键猜掉（猜错的代价是断点丢失、当天那一轮从头再采）。
//  2. ** LIKE 只做粗筛，真判据在 Go 侧**：SQL 里 `LIKE 'kb_scrape_%'` 的下划线是单字符通配符，
//     会把 `kbXscrape…` 这类无关键一并捞进候选；精确前缀与日期解析都在 Go 侧完成，
//     不在名单里的键**永远进不了 DELETE 的参数列表**。
//  3. **删除分块、不许带尾随 LIMIT**（AGENTS §一·4）：可移植形态是 `key IN (?,?,…)`，
//     每块 200 个占位符——SQLite 与 PG 都吃这一种，参数上限也留足余量。
//
// =============================================
package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
)

// 采集进度账的两把键前缀。**必须与 kbscrape.go 的 ScrapeCheckpointKey／SourceDoneKey 逐字同源**，
// 漂移由 kb_scrape_ledger_test.go 的 TestScrapeLedgerPrefixesMatchKeyBuilders 机械锁住
// （改 builder 名／改前缀忘改这里 ⇒ 当场红，回收腿静默扫不到任何行是最难发现的一类失效）。
const (
	scrapeCheckpointPrefix = "kb_scrape_checkpoint_"
	scrapeSourceDonePrefix = "kb_scrape_daily_source_done_"
	// scrapeLedgerLikePattern 粗筛模式（仅用于缩小候选集，不承担判定，见上面口径 2）。
	scrapeLedgerLikePattern = "kb_scrape_%"
	// scrapeLedgerDeleteChunk 一次 DELETE 的键数上限（占位符个数，两方言都远未触顶）。
	scrapeLedgerDeleteChunk = 200
)

// ScrapeLedgerRetentionDays 采集进度账保留天数（默认 14；可由 system_config.scrape_ledger_retention_days 覆盖）。
// 天数怎么定的：
//   - **功能需要只有 1 天**——读侧只认当天那一把键，隔日的断点/完成标记本就无人再读；
//   - 留 14 天是给**排障**留余量（运营问"这个源上周三到底采到哪、有没有报错"时，
//     库里那几天的键是唯一一份按源按日的原始账），口径与工单留存默认 14 天对齐；
//   - 想拉长就配大天数，**0 或非法值一律回落默认**（与 audit_retention_days 同形：
//     这里不设"关掉回收"这一档，因为关掉等于回到只写不删的现网形态）。
func (s *Store) ScrapeLedgerRetentionDays() int {
	if v, err := s.GetConfig("scrape_ledger_retention_days"); err == nil {
		if n, e := strconv.Atoi(strings.TrimSpace(v)); e == nil && n > 0 {
			return n
		}
	}
	return 14
}

// parseScrapeLedgerKey 解析一把进度账键。
// 参数 key=system_config 里的键名；返回（日期串 YYYY-MM-DD，源 ID，是否合法）。
// 判据严格：前缀必须是那两把之一、去掉前缀后必须恰好是 `<日期>_<纯数字>`、日期必须按 ISO 解析成功。
// 任何一条不满足即返回 ok=false，调用方**跳过该键**（不删）。
func parseScrapeLedgerKey(key string) (date string, sourceID int64, ok bool) {
	var rest string
	switch {
	case strings.HasPrefix(key, scrapeSourceDonePrefix):
		rest = strings.TrimPrefix(key, scrapeSourceDonePrefix)
	case strings.HasPrefix(key, scrapeCheckpointPrefix):
		rest = strings.TrimPrefix(key, scrapeCheckpointPrefix)
	default:
		return "", 0, false
	}
	i := strings.LastIndex(rest, "_")
	if i <= 0 || i == len(rest)-1 {
		return "", 0, false // 没有下划线分隔／日期段或 id 段为空
	}
	date, idPart := rest[:i], rest[i+1:]
	if _, err := strconv.ParseInt(idPart, 10, 64); err != nil {
		return "", 0, false // id 段不是纯数字＝不是本模块写的形态
	}
	d, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", 0, false
	}
	// time.Parse 对 `2026-1-2` 这类非补零写法也会退错，但 `2026-10-07T00:00:00` 这种带尾串的
	// 需再核一次长度：Go 的 Parse 对多余字符报错，这里只需再确认往返一致（防止日历上的非法日子被接住）。
	if d.Format("2006-01-02") != date {
		return "", 0, false
	}
	return date, mustParseInt(idPart), true
}

// mustParseInt 把已通过 ParseInt 校验的串转成 int64（调用前一定成功，这里只兜 error 值）。
func mustParseInt(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// PruneScrapeLedger 回收过期的采集进度账（断点游标＋按源完成标记）。
// 参数 now=当前时刻（**由调用方传入**，测试据此造边界；生产传 time.Now()）；
// 返回删除的键数与错误（错误直接上抛，由调用方记 WARN，不在这里吞）。
// 语义：删除「日期 < (now - 保留天数)」的那批键；截止日**当天**及之后一律保留，未来日期一律保留（口径 1）。
func (s *Store) PruneScrapeLedger(now time.Time) (int, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	days := s.ScrapeLedgerRetentionDays()
	cutoff := now.AddDate(0, 0, -days).Format("2006-01-02")
	today := now.Format("2006-01-02")

	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT key FROM system_config WHERE key LIKE ?", scrapeLedgerLikePattern)
	if err != nil {
		return 0, err
	}
	var stale []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return 0, err
		}
		date, _, ok := parseScrapeLedgerKey(k)
		if !ok {
			continue // 形状不合＝不是本模块的账，或写坏了：跳过，绝不猜删
		}
		if date >= today {
			continue // 当天与未来：还在用（或压根不该存在），一律保留
		}
		if date >= cutoff {
			continue // 保留窗口内：留给排障
		}
		stale = append(stale, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	deleted := 0
	for start := 0; start < len(stale); start += scrapeLedgerDeleteChunk {
		end := start + scrapeLedgerDeleteChunk
		if end > len(stale) {
			end = len(stale)
		}
		chunk := stale[start:end]
		ph := make([]string, len(chunk))
		args := make([]any, len(chunk))
		for i, k := range chunk {
			ph[i] = "?"
			args[i] = k
		}
		res, err := db.Exec(s.db, db.CurrentDialect(),
			"DELETE FROM system_config WHERE key IN ("+strings.Join(ph, ",")+")", args...)
		if err != nil {
			return deleted, err
		}
		n, _ := res.RowsAffected()
		deleted += int(n)
	}
	if deleted > 0 {
		observability.Info(context.Background(), "采集进度账已回收",
			"deleted_keys", deleted, "retention_days", days, "cutoff_date", cutoff)
	}
	return deleted, nil
}
