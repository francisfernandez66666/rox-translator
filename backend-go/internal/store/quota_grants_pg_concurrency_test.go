// ============ 本文件职责中文说明 ============
// quota_grants_pg_concurrency_test.go · A1/S2 配套：PostgreSQL 真实并发扣减回归。
// 背景：生产统一 PostgreSQL（S 批决策）后，扣费事务不再享有 SQLite _txlock=immediate
// 的全库写锁，READ COMMITTED 下多实例并发核销同一租户台账必须靠 SELECT ... FOR UPDATE
// 行锁 + 守卫重试保证：① 不超扣（无双花）；② 不扣负；③ 不误报 ErrInsufficientBalance
// 把在余量的批次判成欠费（误报经 sink 会触发双桶清零，属资金事故）。
// 依赖：PG_TEST_DSN（默认 postgres://$USER@127.0.0.1:5432/pgtest）；无 PG 则跳过。
// ========================================
package store_test

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"

	_ "github.com/lib/pq"
)

func TestDeductWithGrantsConcurrencyOnPG(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://" + os.Getenv("USER") + "@127.0.0.1:5432/pgtest?sslmode=disable"
	}
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PostgreSQL，跳过：%v", err)
	}
	defer conn.Close()
	prevC := config.C
	config.C = &config.Config{DatabaseDriver: "postgres"}
	defer func() { config.C = prevC }()

	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New(PG) 失败: %v", err)
	}
	tid := time.Now().Unix()%5_000_000 + 40_000_000 // 随机化租户 ID，避免多次运行互踩
	if err := s.EnsureBalance(tid); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	// 两条台账（6000+9000=15000，无永久余额）+ 20 并发各扣 1000
	exp := time.Now().UTC().Add(24 * time.Hour)
	if err := s.CreateQuotaGrant(tid, "plan", 9000, exp, "ut", 0); err != nil {
		t.Fatalf("发放台账失败: %v", err)
	}
	if err := s.CreateQuotaGrant(tid, "trial", 6000, exp.Add(time.Hour), "ut", 0); err != nil {
		t.Fatalf("发放台账失败: %v", err)
	}
	var ok, insufficient, other int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			err := s.DeductWithGrants(tid, 1000)
			switch err {
			case nil:
				atomic.AddInt64(&ok, 1)
			case store.ErrInsufficientBalance:
				atomic.AddInt64(&insufficient, 1)
			default:
				atomic.AddInt64(&other, 1)
				t.Logf("非预期错误: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if other != 0 {
		t.Fatalf("出现非预期错误 %d 笔（应仅为 成功/ErrInsufficientBalance 两类）", other)
	}
	if ok != 15 {
		t.Fatalf("应恰好 15 笔成功，实得 ok=%d insufficient=%d（1000×20 vs 总额 15000）", ok, insufficient)
	}
	if grants, perm, err := s.TenantRemainTotal(tid); err != nil || grants != 0 || perm != 0 {
		t.Fatalf("扣尽后双桶应归零：grants=%d perm=%d err=%v", grants, perm, err)
	}
	// 台账无负值
	var neg int64
	if err := db.QueryRow(conn, db.CurrentDialect(),
		`SELECT COUNT(*) FROM quota_grants WHERE tenant_id=? AND "left"<0`, tid).Scan(&neg); err != nil || neg != 0 {
		t.Fatalf("出现负库存台账 %d 行 (err=%v)", neg, err)
	}
}
