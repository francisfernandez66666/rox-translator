// ============ packscraper.go · 职责说明 ============
// api 包「行业包/语言文化包自动采集」调度接线（2026-09-01 新功能）。
// 纯低占用驱动：后台每 scrape_poll_sec 探测一次，低占用（无进行中翻译任务、
// LLM 错误率低、RSS 低于水位）才触发采集；高占用自动暂停（Crawler 内部按块续传）。
// 首日铺底：scrape_seed_once=1 且无每日完成标记时，启动即跑一轮全量。
// 采集产生新待审条目后通知全部超管（复用 notifications 站内信）。
// =============================================
package api

import (
	"context"
	"log"
	"strconv"
	"sync/atomic"
	"time"

	"translator/internal/crawler"
)

// startPackScraper 启动数据采集调度器（由 startWatchdog 调用）。
// 后台 goroutine 生命周期与服务进程一致；Store/Engine 缺失时直接跳过。
func (s *Server) startPackScraper() {
	if s.Store == nil {
		return
	}
	go func() {
		// 探测周期（默认 300 秒；0/非法回退默认）
		poll := s.Store.ConfigInt("scrape_poll_sec", 300)
		if poll <= 0 {
			poll = 300
		}
		var running int32 // 防止并发重入（单飞）
		runOnce := func() {
			if !atomic.CompareAndSwapInt32(&running, 0, 1) {
				return
			}
			defer atomic.StoreInt32(&running, 0)
			if err := s.runPackScrapeOnce(); err != nil {
				log.Printf("[crawler] 采集轮失败: %v", err)
			}
		}
		// 首日铺底：scrape_seed_once=1（默认）且尚未有每日完成标记 → 启动立即跑一轮
		if s.Store.ConfigInt("scrape_seed_once", 1) == 1 && s.Store.DailyMarker() == "" {
			log.Println("[crawler] 首日铺底：启动即采集一轮全量")
			runOnce()
		}
		// 之后按探测周期轮询：低占用才触发（RunDaily 内部逐块检测，高占用自动暂停）
		ticker := time.NewTicker(time.Duration(poll) * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			if s.Store == nil {
				continue
			}
			runOnce()
		}
	}()
	log.Println("数据采集调度器已启动（低占用驱动，scrape_poll_sec 可配）")
}

// runPackScrapeOnce 执行一轮采集并处理通知。
// 返回错误（仅记录日志，不中断调度循环）。
func (s *Server) runPackScrapeOnce() error {
	c := crawler.New(s.Store)
	if s.Engine != nil {
		c.LLM = s.Engine.LLM
	}
	// 低占用探针：无进行中翻译任务 + LLM 错误率低 + RSS 低于水位
	// （Probe 返回 true=低占用可采集，与 Crawler.Idle 契约一致）
	c.Probe = func() bool { return s.lowOccupancyForScrape() }
	before := s.Store.ScrapeStagedSummary().PendingEntries
	done, err := c.RunDaily(context.Background())
	if err != nil {
		return err
	}
	// 采集有新待审条目 → 通知全部超管（仅当有新增时，避免每日轰炸）
	after := s.Store.ScrapeStagedSummary().PendingEntries
	if after > before && after > 0 {
		s.notifySuperAdmins("行业/语言文化包采集完成",
			"自动采集新增 "+itoa(after-before)+" 条待审数据（累计待审 "+itoa(after)+" 条），请前往「数据源」面板审批后热加载。")
	}
	_ = done
	return nil
}

// lowOccupancyForScrape 判定当前是否低占用（可采集）。
// 三条同时满足才算低占用：①无进行中/排队翻译任务 ②LLM 错误率低于阈值 ③RSS 低于水位。
// 任一不满足即高占用，采集暂停（断点续传）。
func (s *Server) lowOccupancyForScrape() bool {
	// ① 无进行中翻译任务（jobs 表 ticket_run 无 running/queued）
	if s.Store != nil {
		var n int64
		if err := s.Store.DB().QueryRow(
			"SELECT COUNT(*) FROM jobs WHERE type='ticket_run' AND status IN ('queued','running')").Scan(&n); err == nil && n > 0 {
			return false
		}
	}
	// ② LLM 错误率低于阈值（默认 5%）
	if s.Engine != nil {
		threshold := s.Store.ConfigFloat("scrape_idle_err_rate", 0.05)
		if s.Engine.ErrorRate() >= threshold {
			return false
		}
	}
	// ③ RSS 低于水位（默认 600MB；非 Linux 读取为 0 → 视为低占用）
	memMB := s.Store.ConfigInt("scrape_idle_mem_mb", 600)
	if rssKB := selfRSSKB(); rssKB > 0 && rssKB/1024 >= int64(memMB) {
		return false
	}
	return true
}

// notifySuperAdmins 向全部超管（role='super_admin' 或 admin）发送站内信。
// 参数 title/body：通知标题与正文。
func (s *Server) notifySuperAdmins(title, body string) {
	rows, err := s.Store.DB().Query("SELECT id FROM users WHERE role IN ('super_admin','admin')")
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var uid int64
		if rows.Scan(&uid) == nil && uid > 0 {
			_ = s.Store.CreateNotification(uid, title, body, "kb_scrape", 0)
		}
	}
}

// itoa 便捷 int→string（strconv 薄包装，通知文案拼接用）。
func itoa(v int) string {
	return strconv.Itoa(v)
}
