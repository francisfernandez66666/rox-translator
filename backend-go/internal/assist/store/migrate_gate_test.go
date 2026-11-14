// ============ migrate_gate_test.go · 职责说明 ============
// 四期 §4.5.3 **A5**：存量库迁移幂等（行数 + 内容哈希；造旧库跑两遍）。
//
// 与被测代码**同包**（package store，不跨进程）：四张 KB 表从 assist 库
// 跨库拷进知识库，显式 id、逐列等值、跑两遍行数与内容哈希一字不变。
//
// 反证（M0302）：把 copyKBRows 的写入循环退化成「只建表不搬行」⇒
//
//	目标库四张表 0 行 ⇒ 红。
//
// ★ 与 A2（api/assist_session_migrate_gate_test.go）各判一处搬家，
//
//	不许合并：那边会话/消息 → 主库，这边 KB 四表 → 知识库（§4.5.5 验收 1）。
//
// ================================================
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"translator/internal/db"
)

// a5Seed 给四张 KB 表各插两行（走本包通用 Create，白名单列，id 自增）。
func a5Seed(t *testing.T, d *DB) {
	t.Helper()
	rows := []struct {
		table string
		data  map[string]any
	}{
		{"kb_entries", map[string]any{"key": "k1", "title": "知识一", "content": "内容一"}},
		{"kb_entries", map[string]any{"key": "k2", "title": "知识二", "content": "内容二", "priority": 8}},
		{"scripts", map[string]any{"key": "s1", "stype": "greeting", "content": "欢迎语"}},
		{"scripts", map[string]any{"key": "s2", "stype": "keyword", "content": "话术二"}},
		{"flows", map[string]any{"key": "f1", "name": "流程一", "steps_json": "[]"}},
		{"flows", map[string]any{"key": "f2", "name": "流程二", "steps_json": "[]"}},
		{"feature_links", map[string]any{"key": "fl1", "name": "入口一", "url": "/one"}},
		{"feature_links", map[string]any{"key": "fl2", "name": "入口二", "url": "/two"}},
	}
	for _, r := range rows {
		if _, err := d.Create(r.table, r.data); err != nil {
			t.Fatalf("A5 造旧库 %s 行失败: %v", r.table, err)
		}
	}
}

// a5Fingerprint 行数 + 逐行逐列内容哈希（SELECT * 保证「逐列」不是抄一份清单）。
func a5Fingerprint(t *testing.T, conn *sql.DB, table string) (int, string) {
	t.Helper()
	rows, err := conn.Query("SELECT * FROM " + table + " ORDER BY id")
	if err != nil {
		t.Fatalf("A5 读 %s 失败: %v", table, err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("A5 取 %s 列失败: %v", table, err)
	}
	h := sha256.New()
	n := 0
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("A5 扫描 %s 行失败: %v", table, err)
		}
		b, _ := json.Marshal(vals)
		h.Write(b)
		h.Write([]byte("\n"))
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("A5 遍历 %s 失败: %v", table, err)
	}
	return n, hex.EncodeToString(h.Sum(nil))
}

// a5Tables 四张 KB 表（与 kbMigrateSpecs 同源，刻意不复用被测变量以免锁随实现漂移）。
var a5Tables = []string{"kb_entries", "scripts", "flows", "feature_links"}

// TestA5KBMigrateIdempotent 造旧库跑两遍 + 行数 + 内容哈希 + 显式 id 落位。
func TestA5KBMigrateIdempotent(t *testing.T) {
	dir := t.TempDir()
	src, err := Open(filepath.Join(dir, "assist.db"))
	if err != nil {
		t.Fatalf("A5 造 assist 库失败: %v", err)
	}
	defer src.Close()
	a5Seed(t, src)
	knowledge, err := sql.Open("sqlite", filepath.Join(dir, "knowledge.db"))
	if err != nil {
		t.Fatalf("A5 造知识库失败: %v", err)
	}
	defer knowledge.Close()

	ctx := context.Background()
	// 第一遍：建表 + 显式 id 幂等 upsert。
	if err := src.MigrateKBToKnowledge(ctx, knowledge, db.DialectSQLite); err != nil {
		t.Fatalf("A5 第一遍跨库拷贝失败: %v", err)
	}
	type fp struct {
		n int
		h string
	}
	first := map[string]fp{}
	for _, table := range a5Tables {
		sn, sh := a5Fingerprint(t, src.sql, table)
		dn, dh := a5Fingerprint(t, knowledge, table)
		if sn != dn || sh != dh {
			t.Fatalf("A5 %s 第一遍逐列不等值：源(%d,%s) 知识库(%d,%s)", table, sn, sh, dn, dh)
		}
		if sn == 0 {
			t.Fatalf("A5 %s 源库本来就是空的：这条锁没有判别力", table)
		}
		first[table] = fp{dn, dh}
	}

	// 第二遍：行数 + 内容哈希必须一字不变（幂等自证）。
	if err := src.MigrateKBToKnowledge(ctx, knowledge, db.DialectSQLite); err != nil {
		t.Fatalf("A5 第二遍跨库拷贝失败: %v", err)
	}
	for _, table := range a5Tables {
		n, h := a5Fingerprint(t, knowledge, table)
		if n != first[table].n || h != first[table].h {
			t.Fatalf("A5 %s 跑两遍不幂等：一遍(%d,%s) 二遍(%d,%s)", table, first[table].n, first[table].h, n, h)
		}
	}

	// 显式 id 落位正确（不是重新分配自增 id）：源与目标的 id→key 映射逐行相等。
	for _, table := range a5Tables {
		srcIDs := a5IDKeyPairs(t, src.sql, table)
		dstIDs := a5IDKeyPairs(t, knowledge, table)
		if len(srcIDs) != len(dstIDs) {
			t.Fatalf("A5 %s 显式 id 行数不等：源=%d 目标=%d", table, len(srcIDs), len(dstIDs))
		}
		for id, key := range srcIDs {
			if dstIDs[id] != key {
				t.Fatalf("A5 %s id=%d 落位错：源 key=%q 目标 key=%q", table, id, key, dstIDs[id])
			}
		}
	}

	// 按契约名引用 KnowledgeMigrate：常量在位，保证「跨库拷贝」不是「改 DSN」的假搬。
	if KnowledgeMigrateContract != "knowledge.KnowledgeMigrate" {
		t.Fatal("A5 KnowledgeMigrate 契约名被改：组合根接线将失去锚点")
	}
}

// a5IDKeyPairs 读一张表的 id→key 映射（显式 id 落位判据）。
func a5IDKeyPairs(t *testing.T, conn *sql.DB, table string) map[int64]string {
	t.Helper()
	rows, err := conn.Query("SELECT id, key FROM " + table)
	if err != nil {
		t.Fatalf("A5 读 %s id/key 失败: %v", table, err)
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var key sql.NullString
		if err := rows.Scan(&id, &key); err != nil {
			t.Fatalf("A5 扫描 %s id/key 失败: %v", table, err)
		}
		out[id] = key.String
	}
	return out
}
