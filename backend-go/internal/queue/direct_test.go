// ============ 本文件职责中文说明 ============
// DirectQueue 单元测试：内存 SQLite 建表 → 入队 → 领取 → 完成/重试全链路。
// 回归锚点：2026-08-23 生产事故——Reserve 占位符 4 个仅绑 3 参，工单永久滞留 queued；
// 本测试保证「入队即可被领取」这一核心契约不再回归。
// =============================================
package queue

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestQueue 内存库 + jobs 表 + DirectQueue。
func newTestQueue(t *testing.T) *DirectQueue {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS jobs (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		type TEXT NOT NULL,
		payload TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT 'queued',
		attempts INTEGER NOT NULL DEFAULT 0,
		max_attempts INTEGER NOT NULL DEFAULT 3,
		leased_by TEXT NOT NULL DEFAULT '',
		leased_at TEXT NOT NULL DEFAULT '',
		timeout_sec INTEGER NOT NULL DEFAULT 1800,
		error TEXT NOT NULL DEFAULT '',
		created_at TEXT, updated_at TEXT
	)`)
	if err != nil {
		t.Fatalf("建 jobs 表失败: %v", err)
	}
	return NewDirect(db)
}

// TestEnqueueAndReserve 入队后必须能被领取（回归：占位符缺参导致永久空手）。
func TestEnqueueAndReserve(t *testing.T) {
	q := newTestQueue(t)
	jobID, err := q.Enqueue(context.Background(), "ticket_run", NewTicketPayload(42), DefaultMaxAttempts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	job, rerr := q.Reserve(ctx, "worker-1", DefaultLeaseSec)
	if rerr != nil {
		t.Fatalf("Reserve 报错（历史缺陷：占位符缺参）: %v", rerr)
	}
	if job == nil {
		t.Fatal("存在 queued 任务却领取失败——占位符绑定缺陷回归！")
	}
	if job.ID != jobID || job.Type != "ticket_run" {
		t.Fatalf("领取到错误任务: %+v", job)
	}
	// 二次领取应为空（唯一任务已转 running 且租约未过期）
	job2, _ := q.Reserve(ctx, "worker-2", DefaultLeaseSec)
	if job2 != nil {
		t.Fatalf("running 未过期不应再被领取")
	}
}

// TestLeaseExpiryReclaim 租约过期后可被其他 worker 重新领取。
func TestLeaseExpiryReclaim(t *testing.T) {
	q := newTestQueue(t)
	q.Enqueue(context.Background(), "ticket_run", NewTicketPayload(7), DefaultMaxAttempts)
	ctx := context.Background()
	if _, err := q.Reserve(ctx, "w1", DefaultLeaseSec); err != nil {
		t.Fatal(err)
	}
	// 手工把租约时间回拨 30 分钟（模拟 worker 崩溃后租约超时）
	past := time.Now().Add(-30 * time.Minute).Format(time.RFC3339)
	if _, err := q.db.Exec("UPDATE jobs SET leased_at=? WHERE status='running'", past); err != nil {
		t.Fatal(err)
	}
	job, rerr := q.Reserve(ctx, "w2", DefaultLeaseSec)
	if rerr != nil || job == nil {
		t.Fatalf("过期任务应可被重新领取 (err=%v)", rerr)
	}
}

// TestMarkDone 领取后标记完成。
func TestMarkDone(t *testing.T) {
	q := newTestQueue(t)
	id, _ := q.Enqueue(context.Background(), "t", NewTicketPayload(1), DefaultMaxAttempts)
	ctx := context.Background()
	q.Reserve(ctx, "w1", DefaultLeaseSec)
	if err := q.MarkDone(ctx, id); err != nil {
		t.Fatal(err)
	}
	job, _ := q.Reserve(ctx, "w2", DefaultLeaseSec)
	if job != nil {
		t.Fatal("done 任务不应再被领取")
	}
}
