// P0 修复回归测试（2026-09-16 双实例 e2e 实测）：
//
//	① acquireMigrateLock：PG 迁移单飞锁真实互斥（第二实例排队等锁，释放后方可获取），
//	   消除双实例并发启动时 kb.Open DDL 竞态（23505 → 退化启动 → 假活 + 周期协程 crash）。
//	② isLoopbackListen：S1 生产判定（SQLite 仅回环监听）。
//
// PG 不可达时 ① 自动跳过（sqlite 单机开发无 PG 也能跑测试）。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
)

// openPGForTest 按 UAT 同口径探测本机 PG（DB_DSN → PG_ADMIN_DSN → postgres://$USER@...）。
func openPGForTest(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DB_DSN")
	if dsn == "" {
		dsn = os.Getenv("PG_ADMIN_DSN")
	}
	if dsn == "" {
		dsn = fmt.Sprintf("postgres://%s@127.0.0.1:5432/postgres?sslmode=disable", os.Getenv("USER"))
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Skipf("PG 不可用（跳过迁移锁测试）: %v", err)
	}
	db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		t.Skipf("本机 PG 不可连（跳过迁移锁测试）: %v", err)
	}
	return db
}

// advisoryLockHeld 查询当前是否已有本测试键的 session 级 advisory 锁（objid=低 32 位）。
func advisoryLockHeld(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid=$1`,
		uint32(migrateAdvisoryKey)).Scan(&n); err != nil {
		t.Fatalf("查询 pg_locks 失败: %v", err)
	}
	return n
}

// TestAcquireMigrateLockMutualExclusion 锁的独占性与释放：
// A 持锁期间任意连接观察 pg_locks 应见 1 把；release 后归 0。
func TestAcquireMigrateLockMutualExclusion(t *testing.T) {
	db := openPGForTest(t)
	defer db.Close()

	release := acquireMigrateLock(db)
	if advisoryLockHeld(t, db) == 0 {
		release()
		t.Fatal("持锁后 pg_locks 未见迁移单飞锁（key 未生效？）")
	}
	release()
	if n := advisoryLockHeld(t, db); n != 0 {
		t.Fatalf("release 后锁未释放（pg_locks=%d），将污染后续轮询", n)
	}
}

// TestAcquireMigrateLockSerializesInstances 模拟双实例并发启动：
// 第二实例 acquireMigrateLock 必须阻塞至第一实例释放（轮询间隔 1s，给 15s 宽限）。
func TestAcquireMigrateLockSerializesInstances(t *testing.T) {
	db := openPGForTest(t)
	defer db.Close()

	firstRelease := acquireMigrateLock(db)

	bAcquired := make(chan func(), 1)
	go func() {
		// 独立连接模拟第二实例（同一 sql.DB 连接池会拿到别的空闲连接，不能复用 session 锁）
		d2 := db
		rel := acquireMigrateLock(d2)
		bAcquired <- rel
	}()

	select {
	case rel := <-bAcquired:
		rel()
		t.Fatal("第二实例在第一实例持锁期间获取成功——单飞锁失效")
	case <-time.After(1500 * time.Millisecond):
		// 预期仍在排队（轮询周期 1s）
	}

	firstRelease()
	select {
	case rel := <-bAcquired:
		rel() // 立即释放，避免影响后续
	case <-time.After(15 * time.Second):
		t.Fatal("第一实例释放后，第二实例 15s 内仍未获得锁（排队逻辑异常）")
	}
}

// TestIsLoopbackListen S1 生产判定表：回环=放行，全网卡/对外=非回环。
func TestIsLoopbackListen(t *testing.T) {
	cases := []struct {
		addr string
		want bool
	}{
		{"127.0.0.1:8787", true},
		{"localhost:8787", true},
		{"[::1]:8787", true},
		{":8787", false},              // 空 host = 全网卡
		{"0.0.0.0:8787", false},       // 显式全网卡
		{"43.108.86.140:8787", false}, // 公网 IP
		{"example.com:8787", false},   // 域名一律视为对外
	}
	for _, c := range cases {
		if got := isLoopbackListen(c.addr); got != c.want {
			t.Errorf("isLoopbackListen(%q)=%v 期望 %v", c.addr, got, c.want)
		}
	}
}
