// ============ wallet_pg_recon_test.go · 职责说明 ============
// 组3 W23：**PostgreSQL 真实并发**下的钱包对账等式（本批是唯一会静默扣错钱的一组，
// SQLite 的 _txlock=immediate 会把并发顺带串行化，对这条判据没有证明力）。
//
// 判据三件同时成立（缺一条就等于"账对不上"）：
//
//	① 无负余额（每一档 left ≥ 0）；
//	② 成功笔数 × 单笔 = 台账总减少量（不双花、不丢更新）；
//	③ SUM(usage_debit_lines.tokens) == 台账总减少量（拆扣明细与实扣是同一件事的两面，
//	   这条等式是"按档退款"能成立的唯一前提）。
//
// 依赖：PG_TEST_DSN（默认 postgres://$USER@127.0.0.1:5432/pgtest）；无 PG 则跳过——
// 跳过不是通过：发布闸门里这条必须**真跑过**（run_uat.sh 的 PG 方言段）。
// =============================================
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

func TestWalletPGRecon_DebitLinesMatchLedger(t *testing.T) {
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://" + os.Getenv("USER") + "@127.0.0.1:5432/pgtest?sslmode=disable"
	}
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PostgreSQL，跳过（发布闸门必须真跑一次）：%v", err)
	}
	defer conn.Close()
	prevC := config.C
	config.C = &config.Config{DatabaseDriver: "postgres"}
	defer func() { config.C = prevC }()

	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New(PG) 失败: %v", err)
	}
	tid := time.Now().Unix()%5_000_000 + 50_000_000 // 随机化租户，避免多次运行互踩
	if err := s.EnsureBalance(tid); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	exp := time.Now().UTC().Add(24 * time.Hour)
	if err := s.CreateQuotaGrant(tid, "plan", 9000, exp, "ut", 0); err != nil {
		t.Fatalf("发放台账失败: %v", err)
	}
	if err := s.CreateQuotaGrant(tid, "trial", 6000, exp.Add(time.Hour), "ut", 0); err != nil {
		t.Fatalf("发放台账失败: %v", err)
	}

	var ok, insufficient int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := s.DeductWithGrants(tid, 0, 1000); err == nil {
				atomic.AddInt64(&ok, 1)
			} else {
				atomic.AddInt64(&insufficient, 1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if ok != 15 || insufficient != 5 {
		t.Fatalf("并发拆扣期望 15 成功 / 5 欠费，实得 %d / %d", ok, insufficient)
	}
	d := db.CurrentDialect()
	var leftSum, debitSum, minLeft int64
	if err := db.QueryRow(conn, d,
		`SELECT COALESCE(SUM("left"),0) FROM quota_grants WHERE tenant_id=?`, tid).Scan(&leftSum); err != nil {
		t.Fatalf("读台账剩余失败: %v", err)
	}
	if err := db.QueryRow(conn, d,
		`SELECT COALESCE(SUM(tokens),0) FROM usage_debit_lines WHERE tenant_id=? AND grant_id>0`, tid).Scan(&debitSum); err != nil {
		t.Fatalf("读扣档明细失败: %v", err)
	}
	if err := db.QueryRow(conn, d,
		`SELECT COALESCE(MIN("left"),0) FROM quota_grants WHERE tenant_id=?`, tid).Scan(&minLeft); err != nil {
		t.Fatalf("读最小剩余失败: %v", err)
	}
	if minLeft < 0 {
		t.Fatalf("W23：出现负余额（min left=%d）", minLeft)
	}
	if leftSum != 0 {
		t.Fatalf("W23：15000 全被扣光后剩余应恰为 0，实得 %d", leftSum)
	}
	if debitSum != 15000 {
		t.Fatalf("W23：SUM(debit)=SUM(实扣) 不成立（debit=%d，期望 15000）", debitSum)
	}
}
