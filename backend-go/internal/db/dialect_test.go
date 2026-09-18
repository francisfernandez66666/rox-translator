// dialect_test.go — Dialect 助手纯函数断言。
// ★ P2-2（2026-09-18）：随 db/migrate.go（Runner 影子迁移框架）删除自 migrate_test.go 拆出，
// 方言助手本身（占位符/主键/布尔/UPSERT 生成）是生产路径依赖，测试必须保留。
package db

import "testing"

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
