// 职责：#59 store ctx 穿透的地基断言——db 包的带上下文执行入口（ExecContext /
// QueryContext / QueryRowContext / InsertIDContext）必须（1）真的把 ctx 交给驱动，
// 取消后立刻返回错误；（2）在 PostgreSQL 方言下仍完成 ? → $n 占位符改写。
// 第 (1) 条是这次穿透存在的意义：请求断开后读库要能被中断；第 (2) 条防止将来
// 有人给无 ctx 版本加了方言处理却漏掉 ctx 版本，导致 PG 下语法错。
package db

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// openMemSQLite 开一个内存库并建一张测试表，返回连接（用例结束自动关闭）。
func openMemSQLite(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := Open(Config{Driver: DriverSQLite, DSN: ":memory:"})
	if err != nil {
		t.Fatalf("打开内存库失败：%v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Exec(`CREATE TABLE t (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL DEFAULT '')`); err != nil {
		t.Fatalf("建表失败：%v", err)
	}
	return conn
}

// TestCtxHelpersRoundTrip 验证四个 ctx 入口在 SQLite 下与无 ctx 版本行为一致。
func TestCtxHelpersRoundTrip(t *testing.T) {
	conn := openMemSQLite(t)
	ctx := context.Background()
	d := DialectSQLite

	if _, err := ExecContext(ctx, conn, d, `INSERT INTO t (name) VALUES (?)`, `alpha`); err != nil {
		t.Fatalf("ExecContext 失败：%v", err)
	}
	id, err := InsertIDContext(ctx, conn, d, `id`, `INSERT INTO t (name) VALUES (?)`, `beta`)
	if err != nil || id <= 0 {
		t.Fatalf("InsertIDContext 应返回自增主键，得到 id=%d err=%v", id, err)
	}

	rows, err := QueryContext(ctx, conn, d, `SELECT name FROM t ORDER BY id`)
	if err != nil {
		t.Fatalf("QueryContext 失败：%v", err)
	}
	var got []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("扫描失败：%v", err)
		}
		got = append(got, s)
	}
	rows.Close()
	if strings.Join(got, ",") != "alpha,beta" {
		t.Fatalf("QueryContext 结果不符：%v", got)
	}

	var name string
	if err := QueryRowContext(ctx, conn, d, `SELECT name FROM t WHERE id = ?`, 1).Scan(&name); err != nil {
		t.Fatalf("QueryRowContext 失败：%v", err)
	}
	if name != "alpha" {
		t.Fatalf("QueryRowContext 结果不符：%q", name)
	}
}

// TestCtxHelpersHonorCancellation 取消的 ctx 必须让读写立即失败——这是 #59 的全部理由；
// 若哪天有人把 ctx 版又退回成调用无 ctx 版本，本用例立刻红灯。
func TestCtxHelpersHonorCancellation(t *testing.T) {
	conn := openMemSQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := ExecContext(ctx, conn, DialectSQLite, `INSERT INTO t (name) VALUES (?)`, `x`); !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecContext 未尊重取消，得到 err=%v", err)
	}
	if _, err := QueryContext(ctx, conn, DialectSQLite, `SELECT name FROM t`); !errors.Is(err, context.Canceled) {
		t.Fatalf("QueryContext 未尊重取消，得到 err=%v", err)
	}
	if _, err := InsertIDContext(ctx, conn, DialectSQLite, `id`, `INSERT INTO t (name) VALUES (?)`, `x`); !errors.Is(err, context.Canceled) {
		t.Fatalf("InsertIDContext 未尊重取消，得到 err=%v", err)
	}
}

// fakeCtxExecer 记录驱动收到的 SQL，用于验证 PG 方言下占位符改写没有被 ctx 版本漏掉。
type fakeCtxExecer struct{ got string }

// ExecContext 记录带 ctx 的执行路径收到的 SQL：验证 db.ExecContext 与 Exec 走同一套方言改写。
func (f *fakeCtxExecer) ExecContext(_ context.Context, query string, _ ...interface{}) (sql.Result, error) {
	f.got = query
	return nil, nil //nolint:nilnil // 桩实现：本用例只关心改写后的 SQL 文本，不读返回值
}

// TestCtxHelpersRewriteForPostgres 断言 PG 方言下 ctx 版本同样走 pgTranslate。
func TestCtxHelpersRewriteForPostgres(t *testing.T) {
	f := &fakeCtxExecer{}
	if _, err := ExecContext(context.Background(), f, DialectPostgres, `UPDATE t SET name = ? WHERE id = ?`); err != nil {
		t.Fatalf("ExecContext 桩调用不应报错：%v", err)
	}
	if !strings.Contains(f.got, "$1") || !strings.Contains(f.got, "$2") || strings.Contains(f.got, "?") {
		t.Fatalf("ExecContext 未做 PG 占位符改写：%q", f.got)
	}
}
