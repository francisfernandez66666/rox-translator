// ============ crawler.go · 职责说明 ============
// crawler 包「行业包/语言文化包自动采集」主入口与调度状态机（2026-09-01 新功能）。
// 设计要点：
//   - 纯低占用驱动：空闲才采集，负载上升即暂停，靠 checkpoint 断点续传
//   - 每日一轮：kb_scrape_daily_marker 记录完成日，次日自然开始新一轮
//   - 三层数据源（tier）：1官方API / 2受限网页抓取 / 3LLM生成，按 tier 标注可信度
//   - 产出统一为 NormalizedEntry（行业/语言文化条目）与 NormalizedPhrase（语言文化安全句），
//     经 store.StageEntriesBatch / StagePhrasesBatch 写入待审池（hash 去重、跨日幂等）
// =============================================
package crawler

import (
	"context"
	"fmt"
	"log"
	"sort"
	"time"

	"translator/internal/store"
)

// LoadProbe 负载探针：由 api 层注入（基于进行中翻译任务 / LLM 错误率 / 内存水位判定）。
// 返回 true=低占用（可采集）；false=高占用（应暂停）。
type LoadProbe func() bool

// LLMCaller tier3 生成器所需的 LLM 调用能力（最小接口，便于测试替身）。
type LLMCaller interface {
	// CallChat 单轮对话；返回 (内容, 实际模型, 错误)。
	CallChat(ctx context.Context, baseURL, apiKey, model string, messages []map[string]string,
		maxTokens int, hunyuan bool, fallbackTemp float64) (string, string, error)
}

// Crawler 采集器：驱动每日采集循环。
type Crawler struct {
	St     *store.Store // 平台存储（数据源/待审池/断点）
	LLM    LLMCaller    // LLM 客户端（tier3 用；可为 nil 则跳过 llm_gen 源）
	Probe  LoadProbe    // 低占用探针（可为 nil=始终视为低占用）
	Chunk  int          // 每块最大条目数（默认 100）
	Quiet  bool         // 安静模式（不打印每源日志）
}

// New 创建采集器。参数 st=平台存储；返回采集器实例。
func New(st *store.Store) *Crawler {
	return &Crawler{St: st, Chunk: 100}
}

// SourceDeps 单次采集的运行时依赖（数据源 + 目标包解析）。
type SourceDeps struct {
	TargetPackID int64 // 审批后落入的正式包 ID
}

// RunDaily 执行每日一轮采集（幂等：同日已完成的源跳过）。
// 对每个启用源：若当日已完成（SourceDone）或处于高频冷却（freq_hours 未到）则跳过；
// 否则 RunSource 采集（低占用驱动，可中断续传）。
// 参数 ctx=上下文（可取消）；返回完成源数。
func (c *Crawler) RunDaily(ctx context.Context) (int, error) {
	if c.St == nil {
		return 0, fmt.Errorf("store 未初始化")
	}
	date := time.Now().Format("2006-01-02")
	sources, err := c.St.ListEnabledScrapeSources()
	if err != nil {
		return 0, err
	}
	done := 0
	for _, src := range sources {
		if ctx.Err() != nil {
			return done, ctx.Err()
		}
		if c.St.SourceDone(date, src.ID) {
			continue // 当日已完成
		}
		// 频次冷却：距 last_run_at 未满 freq_hours 则跳过（首日铺底：last_run_at 为空不跳过）
		if src.LastRunAt != "" {
			last, perr := time.Parse(time.RFC3339, src.LastRunAt)
			if perr == nil && time.Since(last) < time.Duration(src.FreqHours)*time.Hour {
				continue
			}
		}
		if !c.Idle() {
			log.Printf("[crawler] 高占用，暂停采集（源 %s 待续传）", src.Name)
			return done, nil
		}
		n, serr := c.RunSource(ctx, src)
		if serr != nil {
			_ = c.St.SetScrapeSourceStatus(src.ID, "error:"+serr.Error())
			log.Printf("[crawler] 源 %s 采集失败: %v", src.Name, serr)
			continue
		}
		_ = c.St.MarkSourceDone(date, src.ID)
		_ = c.St.SetScrapeSourceStatus(src.ID, "ok")
		done++
		if !c.Quiet {
			log.Printf("[crawler] 源 %s 采集完成（新增 %d 条待审）", src.Name, n)
		}
	}
	// 当日全部启用源完成 → 标记 daily marker（供运营视图展示最近完成日）
	if done > 0 {
		_ = c.St.SetDailyMarker(date)
	}
	return done, nil
}

// Idle 判断当前是否低占用（探针未注入视为空闲）。
func (c *Crawler) Idle() bool {
	if c.Probe == nil {
		return true
	}
	return c.Probe()
}

// RunSource 采集单个数据源（可中断续传）。
// 逻辑：
//   1. 解析目标包（locale→租户1语言文化包；industry→按行业 code 匹配租户1行业包）
//   2. 读取当日断点游标（checkpoint），从游标处继续
//   3. 按源类型分发到 tier1/tier2/tier3 抓取函数，逐块产出并写待审池
//   4. 块间检测负载：高占用则暂停并返回（游标已保存，下次续传）
// 参数 ctx=上下文；src=数据源；返回新增待审条数。
func (c *Crawler) RunSource(ctx context.Context, src *store.KBScrapeSource) (int, error) {
	deps, err := c.resolvePack(src)
	if err != nil {
		return 0, err
	}
	date := time.Now().Format("2006-01-02")
	cursor := c.St.GetScrapeCheckpoint(date, src.ID)
	producer, err := c.producerFor(src)
	if err != nil {
		return 0, err
	}
	added := 0
	batch := make([]*store.KBStagedEntry, 0, c.ChunkSize())
	phrases := make([]*store.KBStagedPhrase, 0, c.ChunkSize())
	offset := 0
	for {
		if ctx.Err() != nil {
			return added, ctx.Err()
		}
		// 低占用驱动：每个条目批次前后探测负载
		if !c.Idle() {
			_ = c.St.SetScrapeCheckpoint(date, src.ID, cursor)
			log.Printf("[crawler] 负载上升，暂停源 %s 采集（游标 %s）", src.Name, cursor)
			return added, nil
		}
		entries, phr, nextCursor, done, perr := producer.Next(ctx, deps, cursor, offset)
		if perr != nil {
			return added, perr
		}
		for _, e := range entries {
			if e == nil {
				continue
			}
			e.SourceID = src.ID
			e.PackType = src.PackType
			if e.Tier <= 0 {
				e.Tier = src.Tier
			}
			batch = append(batch, e)
		}
		for _, p := range phr {
			if p == nil {
				continue
			}
			if p.Tier <= 0 {
				p.Tier = src.Tier
			}
			phrases = append(phrases, p)
		}
		if len(batch) >= c.ChunkSize() {
			n, aerr := c.St.StageEntriesBatch(batch)
			batch = batch[:0]
			if aerr != nil {
				return added, aerr
			}
			added += n
		}
		if len(phrases) >= c.ChunkSize() {
			n, aerr := c.St.StagePhrasesBatch(phrases)
			phrases = phrases[:0]
			if aerr != nil {
				return added, aerr
			}
			added += n
		}
		if done {
			break
		}
		offset++
		cursor = nextCursor
	}
	// 收尾剩余批次
	if len(batch) > 0 {
		n, aerr := c.St.StageEntriesBatch(batch)
		if aerr != nil {
			return added, aerr
		}
		added += n
	}
	if len(phrases) > 0 {
		n, aerr := c.St.StagePhrasesBatch(phrases)
		if aerr != nil {
			return added, aerr
		}
		added += n
	}
	// 保存最终游标（断点续传落点）
	if cursor != "" {
		_ = c.St.SetScrapeCheckpoint(date, src.ID, cursor)
	}
	return added, nil
}

// resolvePack 解析数据源目标包。
// locale → 租户1 语言文化包（code='locale'）；industry → 租户1 按 code 匹配的行业包。
// 找不到时返回错误（源停用提示用户先建对应包）。
func (c *Crawler) resolvePack(src *store.KBScrapeSource) (*SourceDeps, error) {
	// 宿主租户1 全包列表，按 code 匹配
	pkgs, err := c.St.ListKBPackages(1)
	if err != nil {
		return nil, err
	}
	for _, p := range pkgs {
		if p.PackType == src.PackType && p.Code == src.Industry {
			return &SourceDeps{TargetPackID: p.ID}, nil
		}
	}
	if src.PackType == "locale" {
		for _, p := range pkgs {
			if p.PackType == "locale" && p.Code == "locale" {
				return &SourceDeps{TargetPackID: p.ID}, nil
			}
		}
	}
	return nil, fmt.Errorf("目标包不存在：%s/%s（请先在知识库管理创建对应包）", src.PackType, src.Industry)
}

// ChunkSize 返回采集块大小（默认 100，可经 system_config.scrape_chunk_size 覆盖）。
func (c *Crawler) ChunkSize() int {
	if c.Chunk > 0 {
		return c.Chunk
	}
	if c.St != nil {
		if v := c.St.ConfigInt("scrape_chunk_size", 100); v > 0 {
			return v
		}
	}
	return 100
}

// producerFor 按数据源类型返回对应抓取器（tier 分发）。
func (c *Crawler) producerFor(src *store.KBScrapeSource) (Producer, error) {
	switch src.Kind {
	case "official_api":
		return &wiktionaryProducer{st: c.St, src: src}, nil
	case "limited_web":
		return &htmlTableProducer{st: c.St, src: src}, nil
	case "llm_gen":
		if c.LLM == nil {
			return nil, fmt.Errorf("LLM 客户端未配置，无法运行 llm_gen 源")
		}
		return &llmProducer{st: c.St, src: src, llm: c.LLM}, nil
	default:
		return nil, fmt.Errorf("未知数据源类型: %s", src.Kind)
	}
}

// Producer 抓取器接口：按游标产出下一批条目/安全句。
type Producer interface {
	// Next 返回下一块产出；nextCursor 供断点续传；done=true 表示源已采集完。
	Next(ctx context.Context, deps *SourceDeps, cursor string, offset int) (
		entries []*store.KBStagedEntry, phrases []*store.KBStagedPhrase,
		nextCursor string, done bool, err error)
}

// SortLangs 排序语言列表（确定性输出用）。
func SortLangs(in []string) []string {
	out := append([]string{}, in...)
	sort.Strings(out)
	return out
}
