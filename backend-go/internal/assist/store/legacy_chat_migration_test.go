// legacy_chat_migration_test.go — 存量库旧 sessions/messages 迁进 *_base 的三条锁：
// ① 有历史的库上迁移真的跑（旧判据「新表不存在」被建表批次先满足 ⇒ 恒不跑，现网留过孤儿表）；
// ② 迁进来的匿名行进得了过期清理（兑现「匿名会话到期清除」那句对外说法）；
// ③ 旧表结构不合预期时不销毁数据、不挡启动；
// ④ 迁移在单连接池下不死锁（openUnderDeadline：5 秒不返回即判红，不再拖满 10 分钟墙钟）。
package store

import (
	"path/filepath"
	"testing"
	"time"
)

// openUnderDeadline 在墙钟里跑一次 Open，超时即判红。
//
// ★ 为什么迁移类用例都要走它而不是裸 Open：本库连接池是 `SetMaxOpenConns(1)`，
// 「一边 range 游标一边 Begin」会**死锁**而不是报错——裸调用会让整包 go test 撞满 10 分钟默认超时，
// 把一条本该 5 秒现形的缺陷拖成"整包莫名红"（首跑真踩：assist/store 600s 超时，栈顶正是迁移里的 Begin）。
// 走这一层之后失败信息直接点明「迁移死锁」这一种病，不必后人事后翻 goroutine dump。
func openUnderDeadline(t *testing.T, path string) (*DB, error) {
	t.Helper()
	type res struct {
		db  *DB
		err error
	}
	ch := make(chan res, 1) // 带缓冲：超时路径上 goroutine 仍能收尾，不卡在发送
	go func() {
		db, err := Open(path)
		ch <- res{db, err}
	}()
	select {
	case r := <-ch:
		return r.db, r.err
	case <-time.After(5 * time.Second):
		t.Fatalf("Open(%s) 5 秒未返回＝启动路径死锁（迁移里 range 游标时开事务？）", path)
		return nil, nil
	}
}

// seedLegacyChatTables 在已建好 *_base 的库上造出「旧版挂件库」形态：
// 旧 sessions（2 行，其中 1 行 last_at 在过去）＋旧 messages（3 行）。
// 这正是现网那张库的形状：base 表已存在（新二进制自己建的），旧表里躺着 09-16~10-01 的历史行。
func seedLegacyChatTables(t *testing.T, db *DB) {
	t.Helper()
	if _, err := db.sql.Exec(`CREATE TABLE sessions(
		id TEXT PRIMARY KEY, page_url TEXT DEFAULT '', in_flow TEXT DEFAULT '',
		flow_step INTEGER DEFAULT 0, msg_count INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP, last_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("造旧 sessions 失败: %v", err)
	}
	if _, err := db.sql.Exec(`CREATE TABLE messages(
		id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, role TEXT NOT NULL,
		content TEXT NOT NULL, actions TEXT DEFAULT '[]',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`); err != nil {
		t.Fatalf("造旧 messages 失败: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO sessions(id,page_url,msg_count,last_at)
		VALUES('lg-1','/a',2,'2026-09-16 10:00:00'),
		       ('lg-2','/b',1,'2026-10-01 15:20:00')`); err != nil {
		t.Fatalf("灌旧 sessions 失败: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO messages(id,session_id,role,content)
		VALUES(9001,'lg-1','user','旧问题一'),(9002,'lg-1','assistant','旧回答一'),
		       (9003,'lg-2','user','旧问题二')`); err != nil {
		t.Fatalf("灌旧 messages 失败: %v", err)
	}
}

// TestLegacyChatTablesMigrateIntoBaseOnReopen ① 迁移在存量库上真的跑一次（走 Open 这条真链，
// 不复用函数内部调用——旧形态的坑恰恰在「建表批次排在守卫之前」这个顺序上）。
func TestLegacyChatTablesMigrateIntoBaseOnReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// base 表里先放一条现役会话，验证迁移不碰它
	if err := db.CreateSession("live-1", "/live", 7, 7, "hash-live"); err != nil {
		t.Fatalf("建现役会话失败: %v", err)
	}
	seedLegacyChatTables(t, db)
	baseSessBefore := db.tableRowCount("sessions_base")
	db.Close()

	// 重新打开＝新二进制在存量库上启动那一次
	db2, err := openUnderDeadline(t, path)
	if err != nil {
		t.Fatalf("重开存量库失败: %v", err)
	}
	defer db2.Close()

	if has, _ := db2.tableExists("sessions"); has {
		t.Fatal("旧 sessions 已迁完仍被留着＝孤儿表没清掉（DROP 腿未跑）")
	}
	if has, _ := db2.tableExists("messages"); has {
		t.Fatal("旧 messages 已迁完仍被留着＝孤儿表没清掉（DROP 腿未跑）")
	}
	if got := db2.tableRowCount("sessions_base"); got != baseSessBefore+2 {
		t.Fatalf("sessions_base 应多 2 行，实际 %d（迁前 %d）", got, baseSessBefore)
	}
	if got := db2.tableRowCount("messages_base"); got != 3 {
		t.Fatalf("messages_base 应有 3 行旧消息，实际 %d", got)
	}
	// 旧行内容按列搬过来（含原 id，幂等与「未搬走」判据都靠它）
	row, err := db2.SessionRow("lg-1")
	if err != nil || row == nil {
		t.Fatalf("旧会话 lg-1 没迁进 base：%v row=%v", err, row)
	}
	if got := row["anonym_hash"]; got != legacyAnonymMarker {
		t.Fatalf("迁进来的匿名行必须带来源标记 %q，实际 %v", legacyAnonymMarker, got)
	}
	if hs, _ := db2.History("lg-1", 10); len(hs) != 2 {
		t.Fatalf("旧会话 lg-1 的消息应 2 条，实际 %d", len(hs))
	}
	// 现役行一字未动
	if live, err := db2.SessionRow("live-1"); err != nil || live == nil {
		t.Fatalf("现役会话被迁移影响: %v", err)
	}
}

// TestLegacyMigratedRowsArePurgeable ② 迁进来的匿名历史行必须进得了过期清理——
// 旧形态「数据只留不删」与对外那句「匿名会话到期清除」相抵，这一条把它兑现掉。
func TestLegacyMigratedRowsArePurgeable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy2.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	seedLegacyChatTables(t, db)
	db.Close()
	db, err = openUnderDeadline(t, path)
	if err != nil {
		t.Fatalf("重开: %v", err)
	}
	defer db.Close()

	if n := db.tableRowCount("messages_base"); n != 3 {
		t.Fatalf("前置：期望 3 行旧消息，实际 %d", n)
	}
	// 清理谓词按「匿名且有/无到期时刻」收，历史 last_at 早已过 ⇒ 这一轮就该排空
	// ★ 52：清理出参现在是两条腿各自的条数，这里顺带核一次「迁进来的壳会话也被回收」。
	deleted, sess, err := db.CleanupExpiredAnonymous(100)
	if err != nil {
		t.Fatalf("清理报错: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("旧匿名消息应被清掉 3 条，实际 deleted=%d（迁进行的匿名档没打上＝对外说法仍兑现不了）", deleted)
	}
	if n := db.tableRowCount("messages_base"); n != 0 {
		t.Fatalf("messages_base 应清空，实际 %d", n)
	}
	if sess != 2 {
		t.Fatalf("迁进来的 2 个过期匿名会话壳都应被回收，读数 %d＝会话腿静默（52）", sess)
	}
}

// TestLegacyMigrationKeepsDataOnUnexpectedShape ③ 旧表结构不合预期（缺列）⇒ 迁移在 DROP 之前
// 就返回：旧表连行一起留着（数据不丢），且**不挡启动**（下一次启动自动重试）。
// 反证：把 migrateLegacyChatTables 里的「迁移失败即 return」改成继续往下走 ⇒ 旧表被 DROP、本用例红。
func TestLegacyMigrationKeepsDataOnUnexpectedShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy3.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 只给 id 列：链头 SELECT 一定失败
	if _, err := db.sql.Exec(`CREATE TABLE sessions(id TEXT PRIMARY KEY)`); err != nil {
		t.Fatalf("造畸形旧表失败: %v", err)
	}
	if _, err := db.sql.Exec(`INSERT INTO sessions(id) VALUES('odd-1')`); err != nil {
		t.Fatalf("灌畸形行失败: %v", err)
	}
	db.Close()

	db2, err := openUnderDeadline(t, path) // 必须起得来
	if err != nil {
		t.Fatalf("畸形旧表不许挡启动: %v", err)
	}
	defer db2.Close()
	if has, _ := db2.tableExists("sessions"); !has {
		t.Fatal("迁移没跑成却把旧表删了＝历史数据被销毁")
	}
	if n := db2.tableRowCount("sessions"); n != 1 {
		t.Fatalf("旧表行必须原样留着，实际 %d", n)
	}
}

// TestLegacyChatMigrationIdempotentOnNextStart 幂等：迁移完成后再启一次（此时根本没有旧表）
// 不报错、行数不变。
func TestLegacyChatMigrationIdempotentOnNextStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy4.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	seedLegacyChatTables(t, db)
	db.Close()
	if _, err := openUnderDeadline(t, path); err != nil {
		t.Fatalf("第一次重开（应迁移）: %v", err)
	}
	db2, err := openUnderDeadline(t, path) // 第二次重开（旧表已不存在）
	if err != nil {
		t.Fatalf("第二次重开报错: %v", err)
	}
	defer db2.Close()
	if n := db2.tableRowCount("messages_base"); n != 3 {
		t.Fatalf("重复启动不许把行迁丢或迁重，实际 %d", n)
	}
}
