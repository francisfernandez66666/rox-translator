// 职责：jsonops.go 双方言助手测试——SQLite 路径功能验证（真实执行）+
// PG 路径 SQL 形态验证（字符串断言，避免依赖 PG 实例）；PG 实例可用时追加真实执行验证。
package db

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// TestJSONNumAddSQLite 真实执行：自增/守卫自减/置 false 在 SQLite(JSON1) 下语义正确。
func TestJSONNumAddSQLite(t *testing.T) {
	conn, err := Open(Config{Driver: DriverSQLite, DSN: ":memory:"})
	if err != nil {
		t.Fatalf("打开内存库失败: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TABLE tenants (id INTEGER PRIMARY KEY, permissions TEXT, updated_at TEXT)"); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	seeds := []struct {
		id int64
		pm string
	}{{1, ""}, {2, `{"sentence_balance":100}`}, {3, `{}`}}
	for _, sd := range seeds {
		if _, err := conn.Exec("INSERT INTO tenants (id, permissions, updated_at) VALUES (?,?,?)",
			sd.id, sd.pm, "t0"); err != nil {
			t.Fatalf("种子失败: %v", err)
		}
	}
	// 自增 +50（空串按 0 起算，id=1 行）
	if _, err := conn.Exec("UPDATE tenants SET " + JSONNumAdd(DialectSQLite, "permissions", "sentence_balance") + ", updated_at=? WHERE id=?", 50, "t1", 1); err != nil {
		t.Fatalf("自增失败: %v", err)
	}
	var v int64
	if err := conn.QueryRow("SELECT " + JSONExtractNum(DialectSQLite, "permissions", "sentence_balance") + " FROM tenants WHERE id=1").Scan(&v); err != nil || v != 50 {
		t.Fatalf("空串自增应得 50，实得 %d (err=%v)", v, err)
	}
	// 守卫自减：id=2 行 100，扣 150 应 0 行命中；扣 30 应成功得 70
	res, err := conn.Exec("UPDATE tenants SET "+JSONNumAdd(DialectSQLite, "permissions", "sentence_balance")+", updated_at=? WHERE id=? AND "+JSONNumGE(DialectSQLite, "permissions", "sentence_balance"), -150, "t2", 2, 150)
	if err != nil {
		t.Fatalf("守卫自减失败: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("余额不足时守卫应拦截（0 行），实得 %d", n)
	}
	if _, err := conn.Exec("UPDATE tenants SET "+JSONNumAdd(DialectSQLite, "permissions", "sentence_balance")+", updated_at=? WHERE id=? AND "+JSONNumGE(DialectSQLite, "permissions", "sentence_balance"), -30, "t3", 2, 30); err != nil {
		t.Fatalf("扣减失败: %v", err)
	}
	if err := conn.QueryRow("SELECT " + JSONExtractNum(DialectSQLite, "permissions", "sentence_balance") + " FROM tenants WHERE id=2").Scan(&v); err != nil || v != 70 {
		t.Fatalf("扣减后应得 70，实得 %d (err=%v)", v, err)
	}
	// 置 false（id=3 行 {}）
	if _, err := conn.Exec("UPDATE tenants SET "+JSONSetFalse(DialectSQLite, "permissions", "notified_exp3")+", updated_at=? WHERE id=?", "t4", 3); err != nil {
		t.Fatalf("置 false 失败: %v", err)
	}
	var s string
	_ = conn.QueryRow("SELECT permissions FROM tenants WHERE id=3").Scan(&s)
	if !strings.Contains(s, `"notified_exp3":false`) {
		t.Fatalf("置 false 结果异常: %s", s)
	}
}

// TestJSONOpsPGShape PG 形态断言：jsonb 语法要素齐备、无 JSON1 残留、标识符白名单生效。
func TestJSONOpsPGShape(t *testing.T) {
	add := JSONNumAdd(DialectPostgres, "permissions", "sentence_balance")
	for _, want := range []string{"jsonb_set", "::jsonb", "->>", "to_jsonb", "?"} {
		if !strings.Contains(add, want) {
			t.Fatalf("PG 自增片段缺少 %s: %s", want, add)
		}
	}
	if strings.Contains(add, "json_set") || strings.Contains(add, "json_extract") {
		t.Fatalf("PG 片段混入 JSON1: %s", add)
	}
	guard := JSONNumGE(DialectPostgres, "permissions", "sentence_balance")
	if !strings.Contains(guard, "::numeric >=") {
		t.Fatalf("PG 守卫形态异常: %s", guard)
	}
	tid := JSONTicketIDExpr(DialectPostgres, "payload")
	if !strings.Contains(tid, "->>'ticket_id'") {
		t.Fatalf("PG ticket_id 表达式异常: %s", tid)
	}
	// 占位符改写联动：片段经 RewritePlaceholders 后 $n 连续
	merged := "UPDATE tenants SET " + add + ", updated_at=? WHERE id=? AND " + guard
	rw := RewritePlaceholders(merged)
	for _, want := range []string{"$1", "$2", "$3", "$4"} {
		if !strings.Contains(rw, want) {
			t.Fatalf("占位符改写缺 %s: %s", want, rw)
		}
	}
	// 白名单：非法标识符必须 panic
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("非法列名应 panic")
			}
		}()
		JSONNumAdd(DialectPostgres, "perm; DROP TABLE users", "k")
	}()
}

// TestJSONOpsPGReal 可选：有 PG 实例时真实执行 jsonb 片段（形态断言的兜底）。
func TestJSONOpsPGReal(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://" + os.Getenv("USER") + "@127.0.0.1:5432/postgres?sslmode=disable"
	}
	conn, err := Open(Config{Driver: DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PG 实例，跳过真实执行: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Exec("CREATE TEMP TABLE tenants (id bigint, permissions text, updated_at text)"); err != nil {
		t.Skipf("临时表创建失败: %v", err)
	}
	if _, err := conn.Exec("INSERT INTO tenants VALUES (1,'','t0'),(2,'{\"sentence_balance\":100}','t0')"); err != nil {
		t.Fatalf("种子失败: %v", err)
	}
	// 经 Exec 助手执行（SQLite 真源 SQL → PG 自动改写占位符），与生产路径一致
	if _, err := Exec(conn, DialectPostgres,
		"UPDATE tenants SET "+JSONNumAdd(DialectPostgres, "permissions", "sentence_balance")+", updated_at=? WHERE id=?",
		50, "t1", int64(1)); err != nil {
		t.Fatalf("PG 自增真实执行失败: %v", err)
	}
	var v int64
	if err := conn.QueryRow("SELECT (NULLIF(permissions,'')::jsonb->>'sentence_balance')::bigint FROM tenants WHERE id=1").Scan(&v); err != nil || v != 50 {
		t.Fatalf("PG 空串自增应得 50，实得 %d (err=%v)", v, err)
	}
	res, err := Exec(conn, DialectPostgres,
		"UPDATE tenants SET "+JSONNumAdd(DialectPostgres, "permissions", "sentence_balance")+", updated_at=? WHERE id=? AND "+JSONNumGE(DialectPostgres, "permissions", "sentence_balance"),
		int64(-150), "t2", int64(2), int64(150))
	if err != nil {
		t.Fatalf("PG 守卫自减失败: %v", err)
	}
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("PG 余额不足守卫应 0 行，实得 %d", n)
	}
	var _ *sql.DB
}
