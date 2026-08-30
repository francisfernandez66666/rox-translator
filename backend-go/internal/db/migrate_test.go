// 职责：迁移执行器与方言助手测试，验证 Dialect 占位符/自增/UPSERT 改写、
// Runner 幂等应用、splitStmts 拆分与核心模板 DDL 在 SQLite 下的可执行性。
package db

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// TestDialectHelpers 验证 Dialect 各助手（占位符/自增主键/布尔字面量/NOW/UPSERT）在
// SQLite 与 PostgreSQL 下的返回差异符合预期。
func TestDialectHelpers(t *testing.T) {
	sq := DialectSQLite
	pg := DialectPostgres

	if sq.Placeholder(1) != "?" || sq.Placeholder(2) != "?" {
		t.Fatal("sqlite placeholder should be ?")
	}
	if pg.Placeholder(1) != "$1" || pg.Placeholder(3) != "$3" {
		t.Fatalf("pg placeholder mismatch: %s %s", pg.Placeholder(1), pg.Placeholder(3))
	}
	if sq.AutoIncrementPK() != "INTEGER PRIMARY KEY AUTOINCREMENT" {
		t.Fatal("sqlite pk mismatch")
	}
	if pg.AutoIncrementPK() != "BIGSERIAL PRIMARY KEY" {
		t.Fatal("pg pk mismatch")
	}
	if sq.BoolLiteral(true) != "1" || sq.BoolLiteral(false) != "0" {
		t.Fatal("sqlite bool mismatch")
	}
	if pg.BoolLiteral(true) != "TRUE" || pg.BoolLiteral(false) != "FALSE" {
		t.Fatal("pg bool mismatch")
	}
	if sq.NowFn() != "CURRENT_TIMESTAMP" || pg.NowFn() != "NOW()" {
		t.Fatal("nowfn mismatch")
	}
	if sq.UpsertIgnore("t", []string{"a"}) != "ON CONFLICT" {
		t.Fatal("sqlite upsert ignore prefix mismatch")
	}
	if got := pg.UpsertIgnore("t", []string{"a", "b"}); got != "ON CONFLICT (a, b) DO NOTHING" {
		t.Fatalf("pg upsert ignore mismatch: %s", got)
	}
	got := pg.UpsertUpdate([]string{"id"}, []string{"name", "updated_at"})
	want := "ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, updated_at = EXCLUDED.updated_at"
	if got != want {
		t.Fatalf("pg upsert update mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// TestMigrationUpSQLSelection 验证 Migration.UpSQL 按当前方言返回对应升级语句。
func TestMigrationUpSQLSelection(t *testing.T) {
	m := Migration{ID: "0001", SQLiteUp: "SQLITE_SQL", PostgresUp: "PG_SQL"}
	if m.UpSQL(DialectSQLite) != "SQLITE_SQL" {
		t.Fatal("sqlite up selection")
	}
	if m.UpSQL(DialectPostgres) != "PG_SQL" {
		t.Fatal("pg up selection")
	}
}

// TestRunnerIdempotent 验证迁移在 SQLite 上可应用且重复调用幂等。
func TestRunnerIdempotent(t *testing.T) {
	conn, err := sql.Open("sqlite", "file::memory:?cache=shared&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	migs := []Migration{
		{
			ID:        "0001_users",
			SQLiteUp:  "CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL);",
			PostgresUp: "CREATE TABLE users (id BIGSERIAL PRIMARY KEY, name TEXT NOT NULL);",
		},
		{
			ID:        "0002_tickets",
			SQLiteUp:  "CREATE TABLE tickets (id INTEGER PRIMARY KEY AUTOINCREMENT, uid INTEGER NOT NULL);",
			PostgresUp: "CREATE TABLE tickets (id BIGSERIAL PRIMARY KEY, uid INTEGER NOT NULL);",
		},
	}

	r := NewRunner(conn, DialectSQLite)
	if err := r.Up(migs); err != nil {
		t.Fatalf("first Up failed: %v", err)
	}
	// 表已创建
	if !tableExists(t, conn, "users") || !tableExists(t, conn, "tickets") {
		t.Fatal("tables not created")
	}
	// 重复执行应无错误（幂等）
	if err := r.Up(migs); err != nil {
		t.Fatalf("second Up (idempotent) failed: %v", err)
	}
	// applied 记录数应为 2
	var n int
	if err := conn.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("expected 2 applied migrations, got %d", n)
	}
}

// tableExists 测试辅助：查询 sqlite_master 判断指定表是否存在。
func tableExists(t *testing.T, conn *sql.DB, name string) bool {
	t.Helper()
	rows, err := conn.Query("SELECT name FROM sqlite_master WHERE type='table' AND name=?", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	return rows.Next()
}

// TestRegisteredMigrationsApplies 验证核心表模板 DDL 在 SQLite 上可执行且幂等。
func TestRegisteredMigrationsApplies(t *testing.T) {
	conn, err := sql.Open("sqlite", "file::memory:?cache=shared&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	migs := RegisteredMigrations()
	r := NewRunner(conn, DialectSQLite)
	if err := r.Up(migs); err != nil {
		t.Fatalf("apply registered migrations failed: %v", err)
	}
	for _, name := range []string{"users", "tickets"} {
		if !tableExists(t, conn, name) {
			t.Fatalf("table %s not created by template", name)
		}
	}
	// 幂等：重复应用不报错
	if err := r.Up(migs); err != nil {
		t.Fatalf("idempotent re-apply failed: %v", err)
	}
}


// TestSplitStmts 验证 splitStmts 按分号拆分多语句 SQL 并剥离 -- 行注释。
func TestSplitStmts(t *testing.T) {
	in := "CREATE TABLE a (id INTEGER);\n-- comment\nCREATE TABLE b (id INTEGER);\n"
	got := splitStmts(in)
	if len(got) != 2 {
		t.Fatalf("expected 2 stmts, got %d: %#v", len(got), got)
	}
	if got[0] != "CREATE TABLE a (id INTEGER)" || got[1] != "CREATE TABLE b (id INTEGER)" {
		t.Fatalf("split mismatch: %#v", got)
	}
}
