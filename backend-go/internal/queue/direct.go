// ============ direct.go · 职责说明 ============
// queue 包 direct 队列驱动实现。
// 基于 jobs 表的进程内实现（SQLite 持久化）。
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
// ★ S2/B12 改造（2026-09-12，生产统一 PG）：
//   - 旧实现「UPDATE ... WHERE id=(SELECT ... 无锁)」在 PG 并发下两 worker 可选同一行，
//     后者等待行锁释放后仍按旧子查询结果覆盖 leased_by → 双跑双扣费；SQLite 靠写锁侥幸安全。
//   - 新实现：单事务内 SELECT ... FOR UPDATE SKIP LOCKED 锁住首行（PG 多 worker 互不阻塞、
//     各取各行）→ 按 id 精确 UPDATE → COMMIT；任务内容随锁定的 SELECT 直接返回，
//     消除旧「按 leased_by + ORDER BY id DESC 反查」在持有多任务时错拿的隐患。
func (q *DirectQueue) Reserve(ctx context.Context, workerID string, leaseSec int) (*Job, error) {
	d := db.CurrentDialect()
	now := Now()
	nowStr := now.Format(time.RFC3339)
	leaseUntil := now.Add(-time.Duration(leaseSec) * time.Second).Format(time.RFC3339)
	lock := ""
	if d.IsPostgres() {
		lock = " FOR UPDATE SKIP LOCKED"
	}
	tx, err := q.db.BeginTx(ctx, nil) // SQLite DSN _txlock=immediate ⇒ BEGIN IMMEDIATE
	if err != nil {
		return nil, fmt.Errorf("reserve begin: %w", err)
	}
	defer tx.Rollback()
	var j Job
	var payload string // modernc/sqlite TEXT 返回 string；json.RawMessage 直接扫描会报 unsupported Scan
	err = db.QueryRow(tx, d,
		"SELECT "+jobCols+" FROM jobs WHERE status='queued' OR (status='running' AND leased_at<=?) ORDER BY id LIMIT 1"+lock,
		leaseUntil).Scan(&j.ID, &j.Type, &payload, &j.Status, &j.Attempts, &j.MaxAttempts, &j.Error)
	if err == sql.ErrNoRows {
		return nil, nil // 无可执行任务
	}
	if err != nil {
		return nil, fmt.Errorf("reserve scan: %w", err)
	}
	if _, err := db.Exec(tx, d,
		"UPDATE jobs SET status='running', leased_by=?, leased_at=?, attempts=attempts+1, updated_at=? WHERE id=?",
		workerID, nowStr, nowStr, j.ID); err != nil {
		return nil, fmt.Errorf("reserve claim: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("reserve commit: %w", err)
	}
	j.Payload = json.RawMessage(payload)
	j.Status = "running"
	j.Attempts++
	return &j, nil
}

// MarkDone 标记任务完成。
func (q *DirectQueue) MarkDone(ctx context.Context, jobID int64) error {
	_, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET status='done', error='', updated_at=? WHERE id=?", Now().Format(time.RFC3339), jobID)
	return err
}

// MarkFailed 标记失败：未达上限回 queued（等待下轮领取），达上限置 dead 死信。
// 单条 CASE WHEN 条件 UPDATE——此前「SELECT attempts → 独立 UPDATE」
// 两语句无事务，与巡检 RecoverStale/其他 worker 并发时基于过期计数决策，
// 可能把该 dead 的毒丸反复回队或覆盖他方刚写入的状态。
func (q *DirectQueue) MarkFailed(ctx context.Context, jobID int64, errMsg string) error {
	_, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET status=(CASE WHEN attempts>=max_attempts THEN 'dead' ELSE 'queued' END), "+
			"error=?, updated_at=? WHERE id=?",
		errMsg, Now().Format(time.RFC3339), jobID)
	return err
}

// Heartbeat 租约续期：仅刷新当前 worker 持有的 running 任务租约。
// 条件带 leased_by=? 防止误续其他 worker/已回队任务；RowsAffected=0 表示任务
// 已被巡检重排/其他 worker 领走，worker 应尽快收尾（收尾守卫已拦截双扣费）。
func (q *DirectQueue) Heartbeat(ctx context.Context, jobID int64, workerID string) error {
	_, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET leased_at=?, updated_at=? WHERE id=? AND status='running' AND leased_by=?",
		Now().Format(time.RFC3339), Now().Format(time.RFC3339), jobID, workerID)
	return err
}

// RecoverStale 回收中断任务（服务启动/巡检调用）：running 且租约过期 → queued。
// ★ 2026-09-04 加固：RecoverStale 与 Reserve 并发时，仅回收「持有者已停止心跳」的
//
//	running 任务——先显式把匹配行的 leased_by 置空，再按置空结果回队，避免在
//	多实例下把仍存活 worker 在跑的任务重置回 queued 造成双跑。
func (q *DirectQueue) RecoverStale(ctx context.Context) (int64, error) {
	leaseUntil := Now().Add(-time.Duration(DefaultLeaseSec) * time.Second).Format(time.RFC3339)
	now := Now().Format(time.RFC3339)
	// 第一步：仅对租约过期的 running 任务清空持有者（条件带 leased_at<=，原子抢断）。
	res, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET leased_by='', leased_at='', updated_at=? WHERE status='running' AND leased_at<=?",
		now, leaseUntil)
	if err != nil {
		return 0, err
	}
	cleared, _ := res.RowsAffected()
	if cleared == 0 {
		return 0, nil
	}
	// 第二步：把已被清空的 running 任务回队。此处 leased_at 已为空，即使与
	// Reserve 并发，Reserve 的 WHERE (status='queued' OR (running AND leased_at<=))
	// 不会误领本步骤尚未回队的任务。
	res2, err := db.ExecContext(ctx, q.db, db.CurrentDialect(),
		"UPDATE jobs SET status='queued', updated_at=? WHERE status='running' AND leased_by='' AND leased_at=''",
		now)
	if err != nil {
		return 0, err
	}
	return res2.RowsAffected()
}

// 状态常量（导出供 service 层使用）。
const (
	StatusQueued  = "queued"
	StatusRunning = "running"
	StatusDone    = "done"
	StatusFailed  = "failed"
	StatusDead    = "dead"
)
