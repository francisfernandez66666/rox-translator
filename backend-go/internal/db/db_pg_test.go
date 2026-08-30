// 职责：PostgreSQL 路径单元测试，验证占位符改写、INSERT OR IGNORE 翻译、
// EnsureColumns/HasColumn/UniqueColumnSets 在 PG 下的行为与幂等；依赖 lib/pq 驱动，
// 无可用 PG 实例时自动跳过（仅校验驱动已注册）。
package db

import (
	"database/sql"
	"os"
	"strings"
	"testing"

	// 注册 PostgreSQL 驱动，验证连接器在 postgres 选型下可被正常识别。
	_ "github.com/lib/pq"
)

// MustExec 在测试中执行 DDL，失败即 fatal。
func MustExec(t *testing.T, conn *sql.DB, sqlText string) {
	t.Helper()
	if _, err := conn.Exec(sqlText); err != nil {
		t.Fatalf("执行 SQL 失败: %s -> %v", sqlText, err)
	}
}

// pgTestDSN 返回集成测试用的 PostgreSQL 连接串，未设置环境变量时回退到本机默认实例。
func pgTestDSN() string {
	if v := os.Getenv("PG_TEST_DSN"); v != "" {
		return v
	}
	return "postgres://postgres@127.0.0.1:55432/pgtest?sslmode=disable"
}

// openTestPG 打开一个真实 PostgreSQL 连接；无可用实例时跳过测试。
func openTestPG(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(Config{Driver: DriverPostgres, DSN: pgTestDSN()})
	if err != nil {
		t.Skipf("无可用 PostgreSQL 实例，跳过集成测试：%v", err)
	}
	return conn
}

// TestInsertOrIgnoreTranslationOnPG 验证 INSERT OR IGNORE 在 PostgreSQL 下被改写为
// ON CONFLICT DO NOTHING，重复插入被安全忽略而非报错。
func TestInsertOrIgnoreTranslationOnPG(t *testing.T) {
	conn := openTestPG(t)
	defer conn.Close()
	MustExec(t, conn, `DROP TABLE IF EXISTS pg_ignore_demo`)
	MustExec(t, conn, `CREATE TABLE pg_ignore_demo (id INTEGER PRIMARY KEY, code TEXT UNIQUE)`)
	d := DialectPostgres
	if _, err := Exec(conn, d, "INSERT OR IGNORE INTO pg_ignore_demo (id, code) VALUES (?,?)", 1, "a"); err != nil {
		t.Fatalf("首次插入失败：%v", err)
	}
	// 第二次同种冲突（code 唯一）应被忽略而不报错
	if _, err := Exec(conn, d, "INSERT OR IGNORE INTO pg_ignore_demo (id, code) VALUES (?,?)", 2, "a"); err != nil {
		t.Fatalf("OR IGNORE 未翻译为 ON CONFLICT DO NOTHING：%v", err)
	}
	var n int
	if err := conn.QueryRow("SELECT COUNT(*) FROM pg_ignore_demo").Scan(&n); err != nil || n != 1 {
		t.Fatalf("期望仅 1 行，实际 %d（err=%v）", n, err)
	}
}

// TestEnsureColumnsOnPG 验证 PostgreSQL 下 EnsureColumns 使用 ADD COLUMN IF NOT EXISTS 幂等补列。
func TestEnsureColumnsOnPG(t *testing.T) {
	conn := openTestPG(t)
	defer conn.Close()
	MustExec(t, conn, `DROP TABLE IF EXISTS pg_cols_demo`)
	MustExec(t, conn, `CREATE TABLE pg_cols_demo (id BIGSERIAL PRIMARY KEY, a TEXT)`)
	d := DialectPostgres
	if err := EnsureColumns(conn, d, "pg_cols_demo", map[string]string{
		"b": "TEXT NOT NULL DEFAULT ''",
		"c": "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		t.Fatalf("EnsureColumns 失败：%v", err)
	}
	// 二次调用应幂等不报错（IF NOT EXISTS）
	if err := EnsureColumns(conn, d, "pg_cols_demo", map[string]string{
		"b": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		t.Fatalf("EnsureColumns 二次调用未幂等：%v", err)
	}
	has, err := HasColumn(conn, d, "pg_cols_demo", "b")
	if err != nil || !has {
		t.Fatalf("列 b 应存在（has=%v err=%v）", has, err)
	}
}

// TestUniqueColumnSetsOnPG 验证 PostgreSQL 下唯一约束列集合可被正确识别。
func TestUniqueColumnSetsOnPG(t *testing.T) {
	conn := openTestPG(t)
	defer conn.Close()
	MustExec(t, conn, `DROP TABLE IF EXISTS pg_uniq_demo`)
	MustExec(t, conn, `CREATE TABLE pg_uniq_demo (a TEXT, b TEXT, c TEXT, UNIQUE(a, b))`)
	d := DialectPostgres
	sets, err := UniqueColumnSets(conn, d, "pg_uniq_demo")
	if err != nil {
		t.Fatalf("UniqueColumnSets 失败：%v", err)
	}
	found := false
	for _, s := range sets {
		if len(s) == 2 {
			m := map[string]bool{}
			for _, c := range s {
				m[c] = true
			}
			if m["a"] && m["b"] {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("未发现复合唯一 (a,b)，实际 %v", sets)
	}
}

// TestPlaceholderRewriteOnPG 验证 PostgreSQL 下 ? 占位符被改写为 $n。
func TestPlaceholderRewriteOnPG(t *testing.T) {
	conn := openTestPG(t)
	defer conn.Close()
	MustExec(t, conn, `DROP TABLE IF EXISTS pg_ph_demo`)
	MustExec(t, conn, `CREATE TABLE pg_ph_demo (id BIGSERIAL PRIMARY KEY, name TEXT, age INTEGER)`)
	d := DialectPostgres
	if _, err := Exec(conn, d, "INSERT INTO pg_ph_demo (name, age) VALUES (?,?)", "alice", 30); err != nil {
		t.Fatalf("插入失败：%v", err)
	}
	row := QueryRow(conn, d, "SELECT name FROM pg_ph_demo WHERE age=? AND name=?", 30, "alice")
	var name string
	if err := row.Scan(&name); err != nil {
		t.Fatalf("查询失败：%v", err)
	}
	if name != "alice" {
		t.Fatalf("期望 alice，实际 %s", name)
	}
}


// TestOpenPostgresDriverRegistered 验证 postgres 驱动已注册：无服务器时应返回
// 连接类错误，而非 "unknown driver"。若本机装有 PG 则可连接成功（跳过断言）。
func TestOpenPostgresDriverRegistered(t *testing.T) {
	_, err := Open(Config{
		Driver:         DriverPostgres,
		DSN:            "postgres://localhost:5432/none?sslmode=disable",
		EnablePgvector: true,
	})
	if err == nil {
		t.Skip("本机存在 PostgreSQL，跳过负向断言")
	}
	if strings.Contains(err.Error(), "unknown driver") {
		t.Fatalf("postgres 驱动未注册：%v", err)
	}
	t.Logf("postgres 驱动已注册（无服务器时连接失败属预期）：%v", err)
}
