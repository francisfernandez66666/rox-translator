// ============ ratelimit.go · 职责说明 ============
// store 包频率护栏（rate_limits 表）数据访问层。
// 基于滑动窗口的计数与封锁机制，
// 用于登录失败、接口刷量等场景的限流与锁定。状态以 (scope,key) 维度隔离，
// 支持读取、原子记录动作、设置封锁截止、重置计数。
// =============================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
	"translator/internal/db"
)

// RateState 某 (scope,key) 的窗口计数与锁定时刻（unix 秒）。
type RateState struct {
	Count       int64 // 窗口内累计次数
	WindowStart int64 // 窗口起点 / 最近一次动作时间（unix 秒）
	LockUntil   int64 // 封锁截止时刻（unix 秒，0=未封锁）
}

// RateLoad 读取（不修改）频率护栏状态。
func (s *Store) RateLoad(scope, key string) (RateState, error) {
	if s.db == nil {
		return RateState{}, nil
	}
	var st RateState
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT count, window_start, lock_until FROM rate_limits WHERE scope=? AND key=?", scope, key)
	err := row.Scan(&st.Count, &st.WindowStart, &st.LockUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return RateState{}, nil
	}
	if err != nil {
		return RateState{}, err
	}
	return st, nil
}

// RateRecord 原子记录一次动作：窗口(windowSec)未过期则 count+1 并保持窗口起点；
// 窗口过期则重置为 count=1、window_start=now。lock_until 不被本函数改动（封锁由 RateSetLock 控制）。
// 返回更新后的状态。
func (s *Store) RateRecord(scope, key string, windowSec int64) (RateState, error) {
	if s.db == nil {
		return RateState{}, nil
	}
	now := time.Now().Unix()
	tx, err := s.db.Begin()
	if err != nil {
		return RateState{}, err
	}
	defer tx.Rollback()
	var count, ws int64
	row := db.QueryRow(tx, db.CurrentDialect(), "SELECT count, window_start FROM rate_limits WHERE scope=? AND key=?", scope, key)
	scanErr := row.Scan(&count, &ws)
	if errors.Is(scanErr, sql.ErrNoRows) {
		if _, e := db.Exec(tx, db.CurrentDialect(), "INSERT INTO rate_limits(scope,key,count,window_start,lock_until) VALUES(?,?,1,?,0)", scope, key, now); e != nil {
			return RateState{}, e
		}
		if e := tx.Commit(); e != nil {
			return RateState{}, e
		}
		return RateState{Count: 1, WindowStart: now}, nil
	}
	if scanErr != nil {
		return RateState{}, scanErr
	}
	if now-ws >= windowSec {
		count = 1
		ws = now
	} else {
		count++
	}
	if _, e := db.Exec(tx, db.CurrentDialect(), "UPDATE rate_limits SET count=?, window_start=? WHERE scope=? AND key=?", count, ws, scope, key); e != nil {
		return RateState{}, e
	}
	if e := tx.Commit(); e != nil {
		return RateState{}, e
	}
	return RateState{Count: count, WindowStart: ws}, nil
}

// RateReserve 在窗口内为某 (scope,key) **原子预留**一格额度：只有「当前计数仍 < limit」才 +1。
// 为什么不能用「RateLoad 判断 → 通过后再 RateRecord」两步代替：那正是 C25 在邀请奖励上踩过的竞态形态
// （并发请求都读到 limit-1，双双放行，上限被击穿成 limit+N）。**刷号脚本本来就是并发打的**，
// 一道只在单线程下成立的上限等于没有上限，所以判据必须收进一条带条件的 UPDATE 里
// （WHERE 同时管「窗口未过期」与「计数未触顶」，影响 1 行即抢到格子）。
// 不用 RETURNING：SQLite 与 PG 对它的支持口径不一致，AGENTS §一·4 要求同一份 SQL 两方言都跑得通，
// 因此抢到后在同一事务里回读一次拿准确计数。
// 影响 0 行时才回读分诊，三种情况分别处理：无行（本窗首笔，INSERT）、窗口已过期（整行重置为 1 并保持条件）、
// 真触顶（返回 reserved=false，并带上现读状态供调用方算 retry_after）。
// limit<=0 视为「这一档不生效」（配 0 由调用侧按非法值回落默认；这里不再自作主张拒绝）。
func (s *Store) RateReserve(scope, key string, windowSec, limit int64) (reserved bool, st RateState, err error) {
	if s.db == nil {
		return true, RateState{}, nil
	}
	d := db.CurrentDialect()
	for attempt := 0; attempt < 2; attempt++ {
		now := time.Now().Unix()
		tx, e := s.db.Begin()
		if e != nil {
			return false, RateState{}, e
		}
		res, e := db.Exec(tx, d, "UPDATE rate_limits SET count=count+1 WHERE scope=? AND key=? AND window_start>? AND count<?", scope, key, now-windowSec, limit)
		if e != nil {
			_ = tx.Rollback()
			return false, RateState{}, e
		}
		if n, _ := res.RowsAffected(); n == 1 {
			var c, ws int64
			if e := db.QueryRow(tx, d, "SELECT count, window_start FROM rate_limits WHERE scope=? AND key=?", scope, key).Scan(&c, &ws); e != nil {
				_ = tx.Rollback()
				return false, RateState{}, e
			}
			if e := tx.Commit(); e != nil {
				return false, RateState{}, e
			}
			return true, RateState{Count: c, WindowStart: ws}, nil
		}
		var c, ws int64
		scanErr := db.QueryRow(tx, d, "SELECT count, window_start, lock_until FROM rate_limits WHERE scope=? AND key=?", scope, key).Scan(&c, &ws, &st.LockUntil)
		if errors.Is(scanErr, sql.ErrNoRows) {
			// 首笔：插一行 count=1。并发下另一笔可能同时 INSERT ⇒ 冲突则回滚重来一次（第二次仍失败才报错）
			if _, e := db.Exec(tx, d, "INSERT INTO rate_limits(scope,key,count,window_start,lock_until) VALUES(?,?,1,?,0)", scope, key, now); e != nil {
				_ = tx.Rollback()
				if attempt == 0 {
					continue
				}
				return false, RateState{}, e
			}
			if e := tx.Commit(); e != nil {
				_ = tx.Rollback()
				if attempt == 0 {
					continue
				}
				return false, RateState{}, e
			}
			return true, RateState{Count: 1, WindowStart: now}, nil
		}
		if scanErr != nil {
			_ = tx.Rollback()
			return false, RateState{}, scanErr
		}
		if now-ws >= windowSec {
			// 窗口过期：整行重置为「本窗第一笔」。WHERE 里带上读到的 window_start，
			// 免得与另一笔并发开新窗的请求互相顶掉（谁先把 window_start 改掉，谁的那一笔才算第一格）。
			res2, e := db.Exec(tx, d, "UPDATE rate_limits SET count=1, window_start=? WHERE scope=? AND key=? AND window_start=?", now, scope, key, ws)
			if e != nil {
				_ = tx.Rollback()
				return false, RateState{}, e
			}
			if n, _ := res2.RowsAffected(); n == 1 {
				if e := tx.Commit(); e != nil {
					return false, RateState{}, e
				}
				return true, RateState{Count: 1, WindowStart: now}, nil
			}
			_ = tx.Rollback()
			continue // 被别人抢先改了窗口起点，重来一次走上面那条条件 UPDATE
		}
		// 窗内且计数已达上限：真触顶
		_ = tx.Rollback()
		return false, RateState{Count: c, WindowStart: ws}, nil
	}
	// 两次都没抢到（并发抢先形态）：按「触顶」处理，宁拒不误放——放行的代价是上限被击穿，
	// 拒绝的代价只是这一次注册等下一轮，而脚本会自己重试（限流表照样记它）。
	return false, RateState{}, nil
}

// RateRelease 退回一格（预留了但最终没落地时用：租户创建撞唯一键、后续校验失败等）。
// 只在**窗口内**且 **count>0** 时才 -1：过期的窗口不该被一次退回改成"还剩格子"，
// 而 count 归零之后的负数会把后面每一笔都放行（计数下溢是限流最坏的一种坏法）。
// 单条条件 UPDATE，不需要事务。
func (s *Store) RateRelease(scope, key string, windowSec int64) error {
	if s.db == nil {
		return nil
	}
	now := time.Now().Unix()
	if _, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE rate_limits SET count=count-1 WHERE scope=? AND key=? AND count>0 AND window_start>?", scope, key, now-windowSec); err != nil {
		return fmt.Errorf("退回频率护栏一格失败: %w", err)
	}
	return nil
}

// RateSetLock 设置（延长）封锁截止时刻（unix 秒）。
func (s *Store) RateSetLock(scope, key string, lockUntil int64) error {
	if s.db == nil {
		return nil
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE rate_limits SET lock_until=? WHERE scope=? AND key=?", lockUntil, scope, key); err != nil {
		return fmt.Errorf("设置频率封锁失败: %w", err)
	}
	return nil
}

// RateReset 清零某 (scope,key) 的计数与封锁（如登录成功）。
func (s *Store) RateReset(scope, key string) error {
	if s.db == nil {
		return nil
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM rate_limits WHERE scope=? AND key=?", scope, key); err != nil {
		return fmt.Errorf("重置频率护栏失败: %w", err)
	}
	return nil
}
