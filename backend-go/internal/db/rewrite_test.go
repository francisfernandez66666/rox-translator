// 职责：SQLite→PostgreSQL DDL 翻译单元测试，验证 INTEGER PRIMARY KEY AUTOINCREMENT→
// BIGSERIAL、BLOB→BYTEA、REAL→DOUBLE PRECISION，以及 ALTER 幂等补 IF NOT EXISTS。
package db

import "testing"

// TestToDialectSQLiteNoop 验证 SQLite 方言下 ToDialect 原样返回（不翻译）。
func TestToDialectSQLiteNoop(t *testing.T) {
	in := "CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, v BLOB, r REAL)"
	if got := ToDialect(in, DialectSQLite); got != in {
		t.Fatalf("sqlite should be unchanged: %s", got)
	}
}

// TestToDialectPostgres 验证 PostgreSQL 下 AUTOINCREMENT/BLOB/REAL 等类型被正确翻译。
func TestToDialectPostgres(t *testing.T) {
	in := `CREATE TABLE t (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  balance INTEGER NOT NULL DEFAULT 0,
  vec BLOB,
  price REAL NOT NULL DEFAULT 0,
  name TEXT NOT NULL DEFAULT ''
)`
	want := `CREATE TABLE t (
  id BIGSERIAL PRIMARY KEY,
  balance INTEGER NOT NULL DEFAULT 0,
  vec BYTEA,
  price DOUBLE PRECISION NOT NULL DEFAULT 0,
  name TEXT NOT NULL DEFAULT ''
)`
	if got := ToDialect(in, DialectPostgres); got != want {
		t.Fatalf("ToDialect mismatch:\n got=%s\nwant=%s", got, want)
	}
}

// TestRunAlterPostgresAddsIfNotExists 验证 PostgreSQL 下 ALTER 补列自动追加 IF NOT EXISTS 使其幂等。
func TestRunAlterPostgresAddsIfNotExists(t *testing.T) {
	in := "ALTER TABLE users ADD COLUMN org_id INTEGER NOT NULL DEFAULT 0"
	want := "ALTER TABLE users ADD COLUMN IF NOT EXISTS org_id INTEGER NOT NULL DEFAULT 0"
	if got := RewriteAlter(in, DialectPostgres); got != want {
		t.Fatalf("RunAlter transform mismatch:\n got=%s\nwant=%s", got, want)
	}
}
