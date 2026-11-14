// ============ gate_helpers_test.go · 职责说明 ============
// W4 六个断言的共享夹具：内存 SQLite（多连接 cache=shared，DDL 走 db.ExecDDL）、
// 可注入的 fake DeliveryQueue（逐字复刻 internal/queue/direct.go 的 Reserve/MarkDone/MarkFailed
// 重试与死信语义）、信封/事件构造、计数工具。
// ★ fake 只实现 relay 依赖的 DeliveryQueue 窄接口，不碰真实 jobs 表，故六个断言彼此隔离。
// ================================================
package outbox

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"translator/internal/db"
	"translator/internal/mq"
	"translator/internal/queue"

	_ "modernc.org/sqlite"
)

var testDBSeq atomic.Int64

// newTestDB 每个用例一条独立的共享内存库；多连接 + 短 busy_timeout，保证
// 「把事件写移到事务外」这类变异体不会在单连接下死锁，而是快速以可辨错误暴露。
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	n := testDBSeq.Add(1)
	dsn := "file:w4mem_" + strconv.Itoa(os.Getpid()) + "_" + strconv.FormatInt(n, 10) +
		"?mode=memory&cache=shared&_pragma=busy_timeout(300)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	conn.SetMaxOpenConns(4)
	t.Cleanup(func() { conn.Close() })
	if err := Migrate(conn, db.DialectSQLite); err != nil {
		t.Fatalf("outbox 域迁移失败: %v", err)
	}
	// 业务写点测试用的临时业务表（DDL 仍走唯一入口 db.ExecDDL）。
	if err := db.ExecDDL(conn, db.DialectSQLite,
		"CREATE TABLE IF NOT EXISTS wp_biz (id INTEGER PRIMARY KEY AUTOINCREMENT, note TEXT NOT NULL DEFAULT '')"); err != nil {
		t.Fatalf("建 wp_biz 临时表失败: %v", err)
	}
	return conn
}

// ---- fake DeliveryQueue：逐字复刻 DirectQueue 的领取/完成/失败-重试-死信语义 ----

type fakeJob struct {
	id          int64
	typ         string
	payload     []byte
	status      string // queued / running / done / dead
	attempts    int
	maxAttempts int
}

type fakeQueue struct {
	mu     sync.Mutex
	jobs   []*fakeJob
	nextID int64
}

var _ DeliveryQueue = (*fakeQueue)(nil)

func newFakeQueue() *fakeQueue { return &fakeQueue{} }

func (f *fakeQueue) Enqueue(_ context.Context, jobType string, payload []byte, maxAttempts int) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if maxAttempts <= 0 {
		maxAttempts = queue.DefaultMaxAttempts
	}
	f.nextID++
	f.jobs = append(f.jobs, &fakeJob{id: f.nextID, typ: jobType, payload: append([]byte(nil), payload...),
		status: "queued", maxAttempts: maxAttempts})
	return f.nextID, nil
}

func (f *fakeQueue) Reserve(_ context.Context, _ string, _ int) (*queue.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.status == "queued" {
			j.status = "running"
			j.attempts++ // 与 DirectQueue.Reserve 一致：领取即 attempts+1
			return &queue.Job{ID: j.id, Type: j.typ, Payload: append([]byte(nil), j.payload...),
				Status: "running", Attempts: j.attempts, MaxAttempts: j.maxAttempts}, nil
		}
	}
	return nil, nil
}

func (f *fakeQueue) MarkDone(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.id == id {
			j.status = "done"
		}
	}
	return nil
}

func (f *fakeQueue) MarkFailed(_ context.Context, id int64, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.id == id {
			// 与 DirectQueue.MarkFailed 的 CASE WHEN 完全同构：attempts>=max ⇒ dead，否则回 queued。
			if j.attempts >= j.maxAttempts {
				j.status = "dead"
			} else {
				j.status = "queued"
			}
		}
	}
	return nil
}

func (f *fakeQueue) snapshot() []fakeJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]fakeJob, 0, len(f.jobs))
	for _, j := range f.jobs {
		out = append(out, *j)
	}
	return out
}

// ---- 信封 / 事件 / 业务写 / 计数工具 ----

func testEnv(t *testing.T, topic, eventID string, tid int64, traceID string) mq.Envelope {
	t.Helper()
	env, err := mq.NewEnvelope(topic, tid, "one-1", eventID, traceID, map[string]any{"k": "v"})
	if err != nil {
		t.Fatalf("构造测试信封失败: %v", err)
	}
	return env
}

func testStoredEvent(t *testing.T, env mq.Envelope) StoredEvent {
	t.Helper()
	raw, err := env.Marshal()
	if err != nil {
		t.Fatalf("序列化测试信封失败: %v", err)
	}
	return StoredEvent{
		Topic:    env.Topic,
		EventID:  env.Header.EventID,
		TenantID: env.Header.TenantID,
		OneID:    env.Header.OneID,
		TraceID:  env.Header.TraceID,
		Payload:  raw,
	}
}

// bizInsert 返回一个「在同一 tx 内写业务行」的回调（WritePoint 的 business 参数）。
func bizInsert(note string) func(*sql.Tx) error {
	return func(tx *sql.Tx) error {
		_, err := db.Exec(tx, db.DialectSQLite, "INSERT INTO wp_biz(note) VALUES(?)", note)
		return err
	}
}

func countRows(t *testing.T, conn *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(conn, db.DialectSQLite, "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("计数 %s 失败: %v", table, err)
	}
	return n
}

// scalarString 读单列字符串，(值, 是否存在)。
func scalarString(t *testing.T, conn *sql.DB, query string, args ...interface{}) (string, bool) {
	t.Helper()
	var s string
	err := db.QueryRow(conn, db.DialectSQLite, query, args...).Scan(&s)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("标量查询失败: %v", err)
	}
	return s, true
}
