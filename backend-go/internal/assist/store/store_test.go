// store_test.go — 存储层单测：建表/CRUD/配置/会话消息全链路（临时库，跑完即删）
package store

import (
	"path/filepath"
	"testing"
)

// newTestDB 建临时测试库
func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// TestKBCRUD 知识库增改查删与白名单列过滤
func TestKBCRUD(t *testing.T) {
	db := newTestDB(t)
	id, err := db.Create("kb_entries", map[string]any{
		"key": "k1", "category": "usage", "title": "T1", "content": "C1",
		"keywords": "a,b", "link_keys": "f1", "priority": 8, "enabled": 1,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id <= 0 {
		t.Fatal("id must be positive")
	}
	// 非法列应被白名单拦截（不报错但不生效）
	if _, err := db.Create("kb_entries", map[string]any{"evil": "x"}); err == nil {
		t.Fatal("no valid columns should error")
	}
	rows, _ := db.List("kb_entries", false)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	if err := db.Update("kb_entries", id, map[string]any{"enabled": 0, "title": "T1x"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	row, _ := db.Get("kb_entries", id)
	if row["enabled"].(int64) != 0 || row["title"] != "T1x" {
		t.Fatalf("update not applied: %v", row)
	}
	if err := db.Delete("kb_entries", id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if rows, _ := db.List("kb_entries", false); len(rows) != 0 {
		t.Fatal("delete failed")
	}
}

// TestConfigUpDown 配置 upsert 与读取
func TestConfigUpDown(t *testing.T) {
	db := newTestDB(t)
	if db.GetConfig("k", "def") != "def" {
		t.Fatal("default expected")
	}
	_ = db.SetConfig("k", "v1")
	_ = db.SetConfig("k", "v2") // upsert 覆盖
	if db.GetConfig("k", "") != "v2" {
		t.Fatalf("want v2, got %s", db.GetConfig("k", ""))
	}
	cfgs, _ := db.AllConfigs()
	if len(cfgs) != 1 {
		t.Fatalf("want 1 config, got %d", len(cfgs))
	}
}

// TestSessionAndMessages 会话幂等创建/流程状态/消息历史正序
func TestSessionAndMessages(t *testing.T) {
	db := newTestDB(t)
	_ = db.EnsureSession("s1", "/home")
	_ = db.EnsureSession("s1", "/home") // 幂等
	s, _ := db.SessionRow("s1")
	if s == nil || s["page_url"] != "/home" {
		t.Fatalf("session bad: %v", s)
	}
	_ = db.SetFlow("s1", "onboard", 2)
	s, _ = db.SessionRow("s1")
	if s["in_flow"] != "onboard" || toI64(s["flow_step"]) != 2 {
		t.Fatalf("flow state bad: %v", s)
	}
	acts := []map[string]string{{"key": "f", "name": "F", "url": "/", "ftype": "route"}}
	_ = db.AddMessage("s1", "user", "hi", nil)
	_ = db.AddMessage("s1", "assistant", "hello", acts)
	_ = db.TouchSession("s1")
	hs, _ := db.History("s1", 10)
	if len(hs) != 2 || hs[0]["role"] != "user" || hs[1]["role"] != "assistant" {
		t.Fatalf("history bad: %+v", hs)
	}
	if hs[1]["actions"] == "" || hs[1]["actions"] == "[]" {
		t.Fatal("actions json missing")
	}
	// 越界 limit 取最近 N 条后仍正序
	hs, _ = db.History("s1", 1)
	if len(hs) != 1 || hs[0]["role"] != "assistant" {
		t.Fatalf("limit history bad: %+v", hs)
	}
	if db.SessionCount() != 1 || db.MessageCount() != 2 {
		t.Fatal("stats bad")
	}
}

// TestListOrdering 列表排序（feature 按 sort，kb 按 priority）
func TestListOrdering(t *testing.T) {
	db := newTestDB(t)
	_, _ = db.Create("feature_links", map[string]any{"key": "b", "name": "B", "url": "/", "sort": 20, "enabled": 1})
	_, _ = db.Create("feature_links", map[string]any{"key": "a", "name": "A", "url": "/", "sort": 10, "enabled": 1})
	rows, _ := db.List("feature_links", true)
	if len(rows) != 2 || rows[0]["key"] != "a" {
		t.Fatalf("sort order bad: %+v", rows)
	}
}

func toI64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// TestCleanupExpiredAnonymousRemovesExpiredMessages 锁 〇-AP 现网缺陷（过期匿名会话清理每小时报错）。
//
// 旧形态 `CleanupExpiredAnonymous` 写了 `DELETE FROM messages_base WHERE session_id IN (…) LIMIT ?`，
// 而本包用的驱动是 modernc.org/sqlite（**与生产二进制同发行**），默认没编 SQLITE_ENABLE_UPDATE_DELETE_LIMIT
// ⇒ `DELETE … LIMIT` 在 SQLite 里根本非法 ⇒ 每小时整点一条 ERROR「过期匿名会话清理失败：near "LIMIT": syntax error」，
// 自 10-02 起过期匿名会话与孤儿消息一条都没清过、只在库里堆。修法＝把 LIMIT 收进**子查询的 SELECT**。
//
// ★ 反证：把 store.go 那句写回 `DELETE … ) LIMIT ?`（LIMIT 挂回 DELETE），本用例即在
//
//	`CleanupExpiredAnonymous` 的 err 上红——正是复现现网那条语法错；改回子查询形态即绿。
func TestCleanupExpiredAnonymousRemovesExpiredMessages(t *testing.T) {
	db := newTestDB(t)

	// ① 已过期的匿名会话（expires_at 为 NULL 即按已过期处理，见清理谓词）＋两条待清消息。
	if err := db.CreateSession("s-exp", "/x", 0, 0, "hash-expired"); err != nil {
		t.Fatalf("建过期匿名会话失败: %v", err)
	}
	_ = db.AddMessage("s-exp", "user", "旧问题", nil)
	_ = db.AddMessage("s-exp", "assistant", "旧回答", nil)

	// ② 仍在效期的匿名会话（expires_at 置到未来）＋一条消息，必须原样保留（防把在效客户会话一起清了）。
	if err := db.CreateSession("s-live", "/y", 0, 0, "hash-live"); err != nil {
		t.Fatalf("建在效匿名会话失败: %v", err)
	}
	if _, err := db.sql.Exec("UPDATE sessions_base SET expires_at=datetime(CURRENT_TIMESTAMP,'+1 day') WHERE id='s-live'"); err != nil {
		t.Fatalf("置未来到期时间失败: %v", err)
	}
	_ = db.AddMessage("s-live", "user", "在效问题", nil)

	// 清理：旧形态此处直接 err≠nil（near "LIMIT" 语法错），修法落地后应正常返回、无错。
	// ★ 〇-AR 第 8 波（52）：这一条现在同时锁**会话回收的读数**——旧形态把 DELETE 会话的
	//
	//	RowsAffected 写成 `_, _ =` 丢掉了，"消息清了、壳也回收了"在出参里只剩消息那一条。
	//	反证：把 store.go 里会话回收那一条的条数改回丢弃（`_, _ =` ＋ return 0），本用例当场红。
	n, sess, err := db.CleanupExpiredAnonymous(100)
	if err != nil {
		t.Fatalf("清理不应报语法错（旧 `DELETE … LIMIT` 形态此处必红）: %v", err)
	}
	if n != 2 {
		t.Fatalf("应清掉过期会话的 2 条消息，实际清 %d 条", n)
	}
	if sess != 1 {
		t.Fatalf("应回收 1 个过期空会话（s-exp），实际读数 %d＝会话腿又静默了（52）", sess)
	}
	if hs, _ := db.History("s-exp", 10); len(hs) != 0 {
		t.Fatalf("过期会话的消息应被清空: %+v", hs)
	}
	if hs, _ := db.History("s-live", 10); len(hs) != 1 {
		t.Fatalf("在效会话的消息不许被误删: %+v", hs)
	}
}
