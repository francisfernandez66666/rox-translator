// ============ ratelimit_day.go · 职责说明 ============
// 「按 UTC 自然日计一次量」的数据访问层（★ 2026-10-30 三事合一批 组2a，D-1 的免费档计数器）。
//
// 为什么**不复用**同表那套 `RateRecord`／`RateReserve`（AGENTS §一·10 的既有实现）：
//
//	`ratelimit.go` 的窗口语义是「**首笔起固定窗**」——`now-window_start >= windowSec` 才重置，
//	窗口起点跟着第一笔走。而 D-1 要的是「第二天 UTC 0 点重制」这一对外口径（用户原话），
//	两种语义在跨日那一档必然给出不同答案：23:59 用掉 100 积分的用户，按固定窗到次日 23:59 才恢复，
//	按自然日 0 点就恢复。**不许**把既有固定窗改掉——注册防薅与 trial 发放都依赖它（改了就是动现网防刷闸）。
//	所以这里新增一条**并列而不覆盖**的日窗口实现，物理形态仍沿用 `rate_limits` 表。
//
// 物理形态（一条都不许漂）：
//   - `window_start` 存**当日 UTC 00:00 的 unix 秒**（不是首笔时刻！判据全部围着那一个值做等值）；
//   - `count` 存当日已用量（单位由 scope 决定：`free_pt_*` 是积分、`free_ip`/`free_wall` 是次数）；
//   - `(scope,key)` 是主键 ⇒ **一个 (scope,key) 永远只有一行**，跨日只是把那一行的 window_start 推前，
//     不新增行。⚠️ 严禁改用「key 里塞日期」（`uid|20261030`）那种"每天一行"的写法：
//     现仓**没有任何 `rate_limits` 清理腿**（已实读确认），那是把内存泄漏换成库泄漏，
//     而库泄漏没有症状——直到某天下意识去 vacuum。K1 的"行数不随日增加"那条断言专门盯它。
//
// 并发取向沿用 `RateReserve`：**判据与计数同一条带条件的 UPDATE**（WHERE 里带 window_start 等值），
// 影响 1 行才算加上；影响 0 行才回读分诊（无行→INSERT／旧日→整行重置并保持条件）。
// 「先读后写」的形态在并发下会把当日格子放大 N 倍，而免费档本来就是一笔钱，放大＝白送积分。
// 不用 RETURNING（SQLite 与 PG 支持口径不一致，AGENTS §一·4）。
//
// fail-open 的方向（AGENTS §一·10「新增防护不拿可用性交押金」）：
//
//	本文件只负责**如实报错**；「读不到就放行＋出声」是调用侧（组4 的准入函数）的判据，
//	不在这里偷偷返回一个"看起来像 0 用量"的成功态——那会把一次库故障写成"今天还没用过"。
//
// =============================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
	"translator/internal/db"
)

// secondsPerUTCDay 一个 UTC 自然日的秒数（日界的唯一尺度，禁止调用方自己拼 86400）。
const secondsPerUTCDay int64 = 86400

// DayState 某 (scope,key) 在**当日（UTC）**的已用量与当日起点（unix 秒）。
// DayStart 为 0 只可能出现在「还没落过行」的读态；写态一律带当日真实起点。
type DayState struct {
	Used     int64
	DayStart int64
}

// utcDayStart 唯一的日界算法：把任意时刻折到**它所在 UTC 日的 00:00**。
// 参数 now: 任意时区的时间（内部先转 UTC，所以宿主机时区不影响结果——
// 现网服务器是 UTC、开发机是 +08，拿本地日历切一次就会让两边各红一半）。
func utcDayStart(now time.Time) int64 {
	// 唯一算法在 quota_org.go 的 dayStartUnix（月界也在同一个文件）——这里只做委托，
	// 否则"日界"会长出第二份实现，而两份在跨零点的表现不同（§一·2 那条日历边界锁会判红）。
	return dayStartUnix(now)
}

// RateDayLoad 读当日已用量（**不改库**）。
// 三种情况都回「今日零用量」而不报错：行不存在、行属于往前的某一日（跨日自动归零）、库未初始化。
// 跨日那一档**故意不顺手把 window_start 写回**：读路径不写库是这条链的边界，
// 归零由真正发生用量的那一次 `RateDayAdd` 落（否则每次看板查询都在改计数器表，
// 读写放大之外还会把"谁开的这一窗"变成读的人）。
func (s *Store) RateDayLoad(scope, key string) (DayState, error) {
	if s.db == nil {
		return DayState{DayStart: utcDayStart(time.Now())}, nil
	}
	var count, ws int64
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT count, window_start FROM rate_limits WHERE scope=? AND key=?", scope, key)
	err := row.Scan(&count, &ws)
	today := utcDayStart(time.Now())
	if errors.Is(err, sql.ErrNoRows) {
		return DayState{DayStart: today}, nil
	}
	if err != nil {
		return DayState{}, err
	}
	if ws != today {
		// 旧日（含"库里是首笔时刻那种老形态"）一律视为新的一天：用量 0。
		// 判据用**等值**而不是 `today-ws >= 86400`：等值把"窗起点被写歪"也一并归零，
		// 而差值判据对"window_start 在未来"（时钟回拨/脏写）会算出负数并当成"今天已用过很多"。
		return DayState{DayStart: today}, nil
	}
	return DayState{Used: count, DayStart: today}, nil
}

// RateDayAdd 在当日窗口内原子累加 delta，返回**累加后**的当日状态。
// 参数 delta: 增量（积分或次数，由 scope 定口径）；<=0 视为"这一笔不需要记账"，只回读不写。
// 跨 UTC 日时整行重置（count=delta、window_start=今日 00:00），**不新增行**（见文件头那条主键纪律）。
func (s *Store) RateDayAdd(scope, key string, delta int64) (DayState, error) {
	if s.db == nil {
		return DayState{DayStart: utcDayStart(time.Now())}, nil
	}
	if delta <= 0 {
		return s.RateDayLoad(scope, key)
	}
	d := db.CurrentDialect()
	for attempt := 0; attempt < 3; attempt++ {
		today := utcDayStart(time.Now())
		tx, err := s.db.Begin()
		if err != nil {
			return DayState{}, err
		}
		// ① 当日内累加：WHERE 带 window_start 等值 ⇒ 旧日的那一行不可能被这一枪加上
		//    （旧行要累加必须先经③整行重置，重置的 WHERE 又带它自己读到的旧起点——
		//     两步都带条件，并发开新窗的两笔里只有一笔的 count 成立）。
		res, err := db.Exec(tx, d, "UPDATE rate_limits SET count=count+? WHERE scope=? AND key=? AND window_start=?", delta, scope, key, today)
		if err != nil {
			_ = tx.Rollback()
			return DayState{}, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			var c int64
			if e := db.QueryRow(tx, d, "SELECT count FROM rate_limits WHERE scope=? AND key=?", scope, key).Scan(&c); e != nil {
				_ = tx.Rollback()
				return DayState{}, e
			}
			if e := tx.Commit(); e != nil {
				return DayState{}, e
			}
			return DayState{Used: c, DayStart: today}, nil
		}
		// ② 没影响任何行 ⇒ 分诊：无行 / 旧行 / 被并发抢先改了起点
		var old int64
		scanErr := db.QueryRow(tx, d, "SELECT window_start FROM rate_limits WHERE scope=? AND key=?", scope, key).Scan(&old)
		if errors.Is(scanErr, sql.ErrNoRows) {
			if _, e := db.Exec(tx, d, "INSERT INTO rate_limits(scope,key,count,window_start,lock_until) VALUES(?,?,?,?,0)", scope, key, delta, today); e != nil {
				_ = tx.Rollback()
				if attempt < 2 {
					continue // 并发下另一笔同时 INSERT：主键冲突即重来，第二圈会走①累加
				}
				return DayState{}, e
			}
			if e := tx.Commit(); e != nil {
				return DayState{}, e
			}
			return DayState{Used: delta, DayStart: today}, nil
		}
		if scanErr != nil {
			_ = tx.Rollback()
			return DayState{}, scanErr
		}
		if old != today {
			// ③ 跨日重置：把这一行推到今日起点，count 直接落成本次增量（旧日用量**不带进今天**）。
			res2, e := db.Exec(tx, d, "UPDATE rate_limits SET count=?, window_start=? WHERE scope=? AND key=? AND window_start=?", delta, today, scope, key, old)
			if e != nil {
				_ = tx.Rollback()
				return DayState{}, e
			}
			if n, _ := res2.RowsAffected(); n == 1 {
				if e := tx.Commit(); e != nil {
					return DayState{}, e
				}
				return DayState{Used: delta, DayStart: today}, nil
			}
			_ = tx.Rollback()
			continue // 另一笔刚把起点改成今天：下一圈走①
		}
		_ = tx.Rollback()
		continue // 起点恰是今日却影响 0 行 ⇒ 纯竞争，重来
	}
	return DayState{}, fmt.Errorf("当日频率计数器累加失败（并发抢先三圈未落地）: scope=%s key=%s", scope, key)
}

// RateDayCeilReached 判「当日是否已触顶」。
// 参数 limit: 当日档位上限；**limit<=0 恒 false**（0＝这一档不限，负数＝关档由调用侧判，
// 两种"不拦"都在这里同一条腿，禁止调用方再造一次 0/负数分支——那是 §一·11 那一族「多把尺子」）。
// 纯函数：不读库，所以库故障那一档由调用侧决定 fail-open（见文件头），这里不替它做主。
func RateDayCeilReached(st DayState, limit int64) bool {
	if limit <= 0 {
		return false
	}
	return st.Used >= limit
}
