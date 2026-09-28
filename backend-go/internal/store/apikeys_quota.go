// ============ apikeys_quota.go · 职责说明 ============
// ★ D-8（2026-09-29）API Key 日配额的「判定与计数合一」占用入口。
// 单独建文件而不是往 apikeys.go 里追加，是为了把这条并发语义的来龙去脉记在同一处：
// 读侧预检（admin_openapi.go validateAPIKey）与写侧自增（TouchAPIKey）分家，
// 是审计 D-8 实测出来的**限流精度缺口**，不是资金缺陷（定性见下）。
// =============================================
package store

import (
	"time"

	"translator/internal/db"
	"translator/internal/infra/ratelimit"
)

// ReserveAPICall 原子占用一次「今日调用额度」，并同时落展示字段（call_count/last_used_at）。
//
// 返回 true=额度已占用，放行本次调用；false=额度已满（或 Key 行已不在），调用方必须拒绝。
//
// ★ 为什么必须有这条而不能再走「预检读字段 → 事后 TouchAPIKey 自增」两段式：
//
//	预检读的是**那一刻**的 calls_today，自增是**另一条语句**的事。
//	同一把 Key 日限额 N、当日已用 N-1 时并发打进来 M 个请求，
//	这 M 个都会在同一刻读到 N-1 < N ⇒ 全部放行，事后各计一次 ⇒ 实际用掉 N-1+M。
//	审计 D-8 的定性（照原话记录，别夸大）：**计数本身早已是原子**的
//	（apikeys.go:167-170 是一条带 CASE 的相对自增 UPDATE，Redis 路径走 INCR），
//	所以这里漏的不是「扣多了」而是「多放行了几个请求」——限流精度问题，不是资金问题。
//	正因为如此，本函数只把**判据挪进同一条 UPDATE 的 WHERE**，
//	不引入 SELECT … FOR UPDATE（SQLite 没有它，AGENTS §一·4 要求双方言同一份 SQL）。
//
// 形态与防薅模块 store.RateReserve 同构：一条带条件的 UPDATE，
// 判据与计数在同一个语句里原子成立，RowsAffected()==0 即「没抢到格子」。
//
// Redis 分支保持权威源：启用 Redis 时额度以跨实例 INCR 为准（返回值即本次序号），
// INCR 失败则**回落本条条件 UPDATE**而不是「当作成功」——否则 Redis 抖动等于额度失守。
//
// 参数：id=Key 主键 ID，dailyLimit=该 Key 的每日上限（<=0 表示不限，仍会记展示计数）。
func (s *Store) ReserveAPICall(id int64, dailyLimit int64) bool {
	// 生产入口：计数器只从 ratelimit.Daily() 取（Redis 在位→跨实例聚合；不在位→nil 走条件 UPDATE）。
	// 真正语义在 reserveAPICall 里，本函数只负责「注入哪一个计数器」这一件事，
	// 拆开的唯一理由是让 Redis 分支能在单测里被真打到（见 apikeys_quota_test.go 的假计数器腿）。
	return s.reserveAPICall(id, dailyLimit, ratelimit.Daily())
}

// reserveAPICall ReserveAPICall 的可注入内核。
// 参数 c=nil 表示未启用 Redis（走②③档的条件 UPDATE），非 nil 表示以 INCR 序号为权威判定（走①）。
func (s *Store) reserveAPICall(id int64, dailyLimit int64, c ratelimit.Counter) bool {
	today := time.Now().Format("2006-01-02")
	now := time.Now().Format(time.RFC3339)

	// ① Redis 在位：INCR 的返回值就是「这是当日第几次」，判定与计数天然同刻成立
	if c != nil {
		n, err := c.Incr(ratelimit.KeyForAKQuota(id, today))
		if err == nil {
			if dailyLimit > 0 && n > dailyLimit {
				return false // 超出当日上限：本次不占用展示计数（拒绝的调用不该算进客户账单概览）
			}
			_, _ = db.Exec(s.db, db.CurrentDialect(),
				"UPDATE api_keys SET call_count=call_count+1, last_used_at=? WHERE id=?", now, id)
			return true
		}
		// err != nil → **不 fail-open**，往下走 SQLite/PG 的条件 UPDATE
	}

	// ② 无限额档：没有判据要守，直接相对自增（与旧 TouchAPIKey 同语义）
	if dailyLimit <= 0 {
		_, _ = db.Exec(s.db, db.CurrentDialect(),
			`UPDATE api_keys SET call_count=call_count+1, last_used_at=?,
				calls_today = CASE WHEN calls_today_date=? THEN calls_today+1 ELSE 1 END,
				calls_today_date=?
			WHERE id=?`, now, today, today, id)
		return true
	}

	// ③ 有限额档：判据写进 WHERE，与计数同一条语句、同一个行锁
	//   calls_today_date<>today 时按 0 计（跨日自动清零，与读侧预检同一口径），
	//   daily_call_limit 取**行内现值**而不是入参——限额可能刚被管理台改过，
	//   以库里的数为准才不会拿陈旧快照放行（入参只用于上面 <=0 的分档判断）。
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE api_keys SET call_count=call_count+1, last_used_at=?,
			calls_today = CASE WHEN calls_today_date=? THEN calls_today+1 ELSE 1 END,
			calls_today_date=?
		WHERE id=? AND daily_call_limit>0
			AND (CASE WHEN calls_today_date=? THEN calls_today ELSE 0 END) < daily_call_limit`,
		now, today, today, id, today)
	if err != nil {
		return false
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false
	}
	return n > 0
}
