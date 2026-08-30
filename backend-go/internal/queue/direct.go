// ============ 本文件职责中文说明 ============
// direct 队列驱动：基于 jobs 表的进程内实现（SQLite 持久化）。
//   - Enqueue：INSERT queued
//   - Reserve：原子领取（queued 或 租约过期的 running → running + 刷新租约）
//   - MarkDone/MarkFailed：完成置 done；失败用单条 CASE WHEN 原子更新，
//     attempts<max 回 queued（延迟由 updated_at 排序天然实现），超限置 dead
//   - RecoverStale：启动/巡检时把租约过期的 running 重置回 queued（崩溃自愈）
//
// 未来 Kafka 驱动：实现同一 Queue 接口；jobs 表仍作为状态账本共用。
// =============================================
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
	"translator/internal/db"
)

// DirectQueue 基于 SQL 存储的队列实现（与业务共用同一 SQLite 连接）。
type DirectQueue struct {
	db *sql.DB
}

// NewDirect 创建 direct 队列。参数 db=平台数据库连接（jobs 表所在库）。
func NewDirect(db *sql.DB) *DirectQueue {
	return &DirectQueue{db: db}
}

// jobCols 任务查询列清单（与 scanJob 严格对应）。
const jobCols = "id, type, payload, status, attempts, max_attempts, COALESCE(error,'')"

// Enqueue 入队：INSERT queued，返回任务 ID。
func (q *DirectQueue) Enqueue(ctx context.Context, jobType string, payload []byte, maxAttempts int) (int64, error) {
	if maxAttempts <= 0 {
		maxAttempts = DefaultMaxAttempts
	}
	now := Now().Format(time.RFC3339)
	return db.InsertID(q.db, db.CurrentDialect(), "id",
		"INSERT INTO jobs (type, payload, status, attempts, max_attempts, leased_by, leased_at, timeout_sec, error, created_at, updated_at) VALUES (?,?, 'queued',0,?,'','',?, '', ?, ?)",
		jobType, string(payload), maxAttempts, DefaultLeaseSec, now, now)
}

// Reserve 原子领取下一个可执行任务：
// 条件 = status='queued' 或 (status='running' AND 租约已过期)；领取后置 running 并刷新租约。
func (q *DirectQueue) Reserve(ctx context.Context, workerID string, leaseSec int) (*Job, error) {
	now := Now()
	nowStr := now.Format(time.RFC3339)
	leaseUntil := now.Add(-time.Duration(leaseSec) * time.Second).Format(time.RFC3339)
	// 单条 UPDATE 完成领取（依赖 SQLite 写锁保证原子性）。
	// ⚠️ 历史缺陷（2026-08-23 E2E 发现）：占位符 4 个仅绑定 3 个参数（updated_at 被误绑
	// 租约时间、子查询无参可绑），驱动报参数不足 → Reserve 永远空手 → 工单永久滞留 queued。
	// 已修正为按序绑定：leased_by / leased_at / updated_at / 租约阈值。
	res, err := db.ExecContext(ctx, q.db, db.CurrentDialect(), `
		UPDATE jobs SET status='running', leased_by=?, leased_at=?, attempts=attempts+1, updated_at=?
		WHERE id = (
			SELECT id FROM jobs
			WHERE status='queued' OR (status='running' AND leased_at<=?)
			ORDER BY id LIMIT 1
		)`, workerID, nowStr, nowStr, leaseUntil)
	if err != nil {
		return nil, fmt.Errorf("reserve claim: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, nil // 无可执行任务
	}
	var j Job
	var payload string // modernc/sqlite TEXT 返回 string；json.RawMessage 直接扫描会报 unsupported Scan
	err = db.QueryRow(q.db, db.CurrentDialect(), "SELECT "+jobCols+" FROM jobs WHERE leased_by=? AND status='running' ORDER BY id DESC LIMIT 1", workerID).
		Scan(&j.ID, &j.Type, &payload, &j.Status, &j.Attempts, &j.MaxAttempts, &j.Error)
	if err != nil {
		return nil, fmt.Errorf("reserve scan: %w", err)
	}
	j.Payload = json.RawMessage(payload)
	return &j, nil
}

// MarkDone 标记任务完成。
func (q *DirectQueue) MarkDone(ctx context.Context, jobID int64) error {
	_, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET status='done', error='', updated_at=? WHERE id=?", Now().Format(time.RFC3339), jobID)
	return err
}

// MarkFailed 标记失败：未达上限回 queued（等待下轮领取），达上限置 dead 死信。
// ★ 整改 D6：单条 CASE WHEN 条件 UPDATE——此前「SELECT attempts → 独立 UPDATE」
// 两语句无事务，与巡检 RecoverStale/其他 worker 并发时基于过期计数决策，
// 可能把该 dead 的毒丸反复回队或覆盖他方刚写入的状态。
func (q *DirectQueue) MarkFailed(ctx context.Context, jobID int64, errMsg string) error {
	_, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET status=(CASE WHEN attempts>=max_attempts THEN 'dead' ELSE 'queued' END), "+
			"error=?, updated_at=? WHERE id=?",
		errMsg, Now().Format(time.RFC3339), jobID)
	return err
}

// RecoverStale 回收中断任务（服务启动/巡检调用）：running 且租约过期 → queued。
func (q *DirectQueue) RecoverStale(ctx context.Context) (int64, error) {
	leaseUntil := Now().Add(-time.Duration(DefaultLeaseSec) * time.Second).Format(time.RFC3339)
	res, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET status='queued', leased_by='', leased_at='', updated_at=? WHERE status='running' AND leased_at<=?",
		Now().Format(time.RFC3339), leaseUntil)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// 状态常量（导出供 service 层使用）。
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusDead    = "dead"
)
