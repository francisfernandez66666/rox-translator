// 职责：db 包核心能力测试，覆盖 SQLite 后端打开、内存库、加固 DSN 参数补齐
// 与未注册 PG 驱动时的明确报错。
package db

import (
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// TestOpenSQLiteTempFile 验证 SQLite 后端可正常打开并建连。
func TestOpenSQLiteTempFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite3")
	conn, err := Open(Config{Driver: DriverSQLite, DSN: path})
	if err != nil {
		t.Fatalf("Open sqlite failed: %v", err)
	}
	defer conn.Close()
	if err := conn.Ping(); err != nil {
		t.Fatalf("ping sqlite failed: %v", err)
	}
}

// TestOpenSQLiteMemory 验证内存库可被连接器打开（:memory: 特例）。
func TestOpenSQLiteMemory(t *testing.T) {
	conn, err := Open(Config{Driver: DriverSQLite, DSN: ":memory:"})
	if err != nil {
		t.Fatalf("Open :memory: failed: %v", err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow("SELECT 1").Scan(&n); err != nil {
		t.Fatalf("query :memory: failed: %v", err)
	}
	if n != 1 {
		t.Fatalf("unexpected value: %d", n)
	}
}

// TestSQLiteDSN 验证 DSN 补齐了必需的加固参数。
func TestSQLiteDSN(t *testing.T) {
	dsn := SQLiteDSN("foo.db")
	for _, want := range []string{"_txlock=immediate", "busy_timeout(5000)", "journal_mode(WAL)", "synchronous(NORMAL)"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("SQLiteDSN missing %q in %q", want, dsn)
		}
	}
}

// TestOpenPostgresUnknownDriver 验证未注册驱动时给出明确错误（而非静默成功）。
func TestOpenPostgresUnknownDriver(t *testing.T) {
	_, err := Open(Config{Driver: DriverPostgres, DSN: "postgres://localhost/none"})
	if err == nil {
		t.Fatal("expected error for unregistered postgres driver, got nil")
	}
}
