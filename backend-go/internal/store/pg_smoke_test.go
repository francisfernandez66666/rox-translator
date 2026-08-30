// ============ 本文件职责中文说明 ============
// PostgreSQL 端到端冒烟测试（store_test 外部测试包）：用真实 PostgreSQL 启动完整
// Store 迁移链并验证基础读写；需要 PG_TEST_DSN 环境变量，未设置则跳过。
// 目的：验证全部 SQL 在 PG 方言下（? 占位符、AUTOINCREMENT、INSERT OR IGNORE、
// PRAGMA 补列分支）能正确执行，是查询层方言化的最终验收。
// =============================================
package store_test

import (
	"os"
	"testing"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/iam"
	"translator/internal/store"
	"translator/internal/tenant"

	_ "github.com/lib/pq"
)

// TestStoreBootstrapOnPG 端到端冒烟：用真实 PostgreSQL 启动完整 Store 迁移链，
// 并验证基础读写。需要 PG_TEST_DSN 环境变量；未设置则跳过。
// 这是 P0-3 查询层方言化的最终验证——所有 ? 占位符、AUTOINCREMENT、
// INSERT OR IGNORE、PRAGMA 补列分支均需在 PG 下正确工作。
func TestStoreBootstrapOnPG(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://postgres@127.0.0.1:55432/pgtest?sslmode=disable"
	}
	// 全局方言切到 postgres（db.CurrentDialect 据此返回）；测试结束恢复，避免污染同包其他用例。
	prevC := config.C
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PostgreSQL，跳过：%v", err)
	}
	defer conn.Close()
	config.C = &config.Config{DatabaseDriver: "postgres"}
	defer func() { config.C = prevC }()

	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New 在 PostgreSQL 下启动失败：%v", err)
	}

	// 基础读写验证：确保账户 + 查余额（走 INSERT OR IGNORE 翻译路径）
	if err := s.EnsureBalance(1); err != nil {
		t.Fatalf("EnsureBalance(1) 失败：%v", err)
	}
	b, err := s.GetBalance(1)
	if err != nil {
		t.Fatalf("GetBalance(1) 失败：%v", err)
	}
	if b == nil {
		t.Fatal("GetBalance 返回 nil")
	}
	t.Logf("PG 启动成功，租户 1 余额=%d", b.Balance)

	// 租户存储与 IAM 存储同样需在 PostgreSQL 下完成建表补列
	if _, err := tenant.NewStore(conn); err != nil {
		t.Fatalf("tenant.NewStore 在 PostgreSQL 下启动失败：%v", err)
	}
	if st := iam.NewStore(conn); st == nil {
		t.Fatal("iam.NewStore 返回 nil")
	}
	t.Logf("tenant/iam 在 PostgreSQL 下启动成功")
}
