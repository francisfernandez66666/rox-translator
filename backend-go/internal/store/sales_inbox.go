// ============ sales_inbox.go · 职责说明 ============
// 二期 mq 核心层的落库口：inbox_events 幂等收件箱。
// ★ 迁入即换：外仓 EnsureProcessed 是「先查后写＋二次回读」的非原子形态，
// 这里改成一条 INSERT … SELECT … WHERE NOT EXISTS 的条件写（照 sales_reserve.go 先例），
// 并发败方撞唯一索引只认 db.IsUniqueViolation（双方言一句判据），
// 禁止先 COUNT 预检、禁止字符串判 PG 状态字、禁止把冲突当故障上抛。
// ================================================
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
)

// TryProcessInboxEvent 幂等抢占：首次调用返回 true（调用方去处理），重复或并发败方返回 false。
// 参数：tid=显式租户；eventID=事件幂等键；oneID=用户主键；topic=主题；返回：是否抢到与错误。
func (s *Store) TryProcessInboxEvent(tid int64, eventID, oneID, topic string) (bool, error) {
	if tid <= 0 || strings.TrimSpace(eventID) == "" {
		return false, errors.New("收件箱抢占必须带有效租户与事件幂等键")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO inbox_events (tenant_id, event_id, one_id, topic, status, created_at, processed_at)
		 SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS (
			SELECT 1 FROM inbox_events WHERE tenant_id=? AND event_id=?)`,
		tid, eventID, oneID, topic, "pending", now, "", tid, eventID)
	if err != nil {
		if db.IsUniqueViolation(err) {
			return false, nil // 并发败方：撞唯一索引不是故障
		}
		return false, fmt.Errorf("收件箱抢占失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("收件箱抢占读数失败: %w", err)
	}
	return n == 1, nil
}

// MarkInboxProcessed 处理成功后打完成标记；重复标记返回 false（不报错）。
// 参数：tid=显式租户；eventID=事件幂等键；返回：本次是否真正写成与错误。
func (s *Store) MarkInboxProcessed(tid int64, eventID string) (bool, error) {
	if tid <= 0 || strings.TrimSpace(eventID) == "" {
		return false, errors.New("收件箱完成标记必须带有效租户与事件幂等键")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`UPDATE inbox_events SET status=?, processed_at=?
		 WHERE tenant_id=? AND event_id=? AND processed_at=?`,
		"done", now, tid, eventID, "")
	if err != nil {
		return false, fmt.Errorf("收件箱完成标记失败: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("收件箱完成标记读数失败: %w", err)
	}
	return n == 1, nil
}

// GetInboxEvent 按租户＋事件键读一行状态（供审计与回放；跨租户读不到）。
// 参数：tid=显式租户；eventID=事件幂等键；返回：状态、完成时间、是否存在、错误。
func (s *Store) GetInboxEvent(tid int64, eventID string) (status, processedAt string, found bool, err error) {
	if tid <= 0 || strings.TrimSpace(eventID) == "" {
		return "", "", false, errors.New("收件箱读取必须带有效租户与事件幂等键")
	}
	row := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT status, processed_at FROM inbox_events WHERE tenant_id=? AND event_id=?`, tid, eventID)
	if e := row.Scan(&status, &processedAt); e != nil {
		if errors.Is(e, sql.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, fmt.Errorf("收件箱读取失败: %w", e)
	}
	return status, processedAt, true, nil
}
