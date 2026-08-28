// ============ 本文件职责中文说明 ============
// 频率护栏（rate_limits 表）数据访问层：基于滑动窗口的计数与封锁机制，
// 用于登录失败、接口刷量等场景的限流与锁定。状态以 (scope,key) 维度隔离，
// 支持读取、原子记录动作、设置封锁截止、重置计数。
// =============================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
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
	row := s.db.QueryRow("SELECT count, window_start, lock_until FROM rate_limits WHERE scope=? AND key=?", scope, key)
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
	row := tx.QueryRow("SELECT count, window_start FROM rate_limits WHERE scope=? AND key=?", scope, key)
	scanErr := row.Scan(&count, &ws)
	if errors.Is(scanErr, sql.ErrNoRows) {
		if _, e := tx.Exec("INSERT INTO rate_limits(scope,key,count,window_start,lock_until) VALUES(?,?,1,?,0)", scope, key, now); e != nil {
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
	if _, e := tx.Exec("UPDATE rate_limits SET count=?, window_start=? WHERE scope=? AND key=?", count, ws, scope, key); e != nil {
		return RateState{}, e
	}
	if e := tx.Commit(); e != nil {
		return RateState{}, e
	}
	return RateState{Count: count, WindowStart: ws}, nil
}

// RateSetLock 设置（延长）封锁截止时刻（unix 秒）。
func (s *Store) RateSetLock(scope, key string, lockUntil int64) error {
	if s.db == nil {
		return nil
	}
	if _, err := s.db.Exec("UPDATE rate_limits SET lock_until=? WHERE scope=? AND key=?", lockUntil, scope, key); err != nil {
		return fmt.Errorf("设置频率封锁失败: %w", err)
	}
	return nil
}

// RateReset 清零某 (scope,key) 的计数与封锁（如登录成功）。
func (s *Store) RateReset(scope, key string) error {
	if s.db == nil {
		return nil
	}
	if _, err := s.db.Exec("DELETE FROM rate_limits WHERE scope=? AND key=?", scope, key); err != nil {
		return fmt.Errorf("重置频率护栏失败: %w", err)
	}
	return nil
}
