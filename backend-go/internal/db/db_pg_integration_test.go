// 职责：PostgreSQL 后端集成测试，验证核心表模板迁移 DDL 在真实 PG 下的可执行性、
// 幂等与 schema_migrations 跟踪；需环境变量 PG_TEST_DSN，未设置则跳过。
package db

import (
	"os"
	"testing"
)

// TestRegisteredMigrationsPostgres 在真实 PostgreSQL 上验证核心模板 DDL 可执行、
// 幂等、且 schema_migrations 跟踪表正常建立。需要环境变量 PG_TEST_DSN 指向可用实例
// （如 postgres://user@localhost:5432/db?sslmode=disable）；未设置则跳过。
//
// 用法（本地已装 Homebrew PostgreSQL 时）：
//
//	initdb -D /tmp/pgtest -U postgres --auth=trust
//	pg_ctl -D /tmp/pgtest -o "-p 55432 -k /tmp" -l /tmp/pg.log start
//	createdb -p 55432 -h 127.0.0.1 -U postgres pgtest
//	PG_TEST_DSN="postgres://postgres@127.0.0.1:55432/pgtest?sslmode=disable" \
//	  go test ./internal/db/ -run TestRegisteredMigrationsPostgres -v
// TestRegisteredMigrationsPostgres：TestRegisteredMigrationsPostgres
func TestRegisteredMigrationsPostgres(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		t.Skip("PG_TEST_DSN 未设置，跳过 PostgreSQL 集成验证")
	}
	conn, err := Open(Config{Driver: DriverPostgres, DSN: dsn, EnablePgvector: true})
	if err != nil {
		t.Fatalf("open postgres failed: %v", err)
	}
	defer conn.Close()

	migs := RegisteredMigrations()
	if err := NewRunner(conn, DialectPostgres).Up(migs); err != nil {
		t.Fatalf("apply migrations on postgres failed: %v", err)
	}
	for _, name := range []string{"users", "tickets", "schema_migrations"} {
		var cnt int
		if err := conn.QueryRow(
			"SELECT count(*) FROM information_schema.tables WHERE table_name = $1", name,
		).Scan(&cnt); err != nil {
			t.Fatalf("query table %s failed: %v", name, err)
		}
		if cnt != 1 {
			t.Fatalf("table %s not created on postgres (count=%d)", name, cnt)
		}
	}
	// 幂等：重复应用不应报错
	if err := NewRunner(conn, DialectPostgres).Up(migs); err != nil {
		t.Fatalf("idempotent re-apply on postgres failed: %v", err)
	}
}
