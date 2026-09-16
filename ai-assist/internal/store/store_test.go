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
