// ============ coupons_pg_concurrency_test.go · 职责说明 ============
// 优惠券核销的 PostgreSQL 真实并发回归（★ #41 商业洞三，2026-09-21）。
// 为什么单独要一份 PG 用例：SQLite 内存库靠 DSN `_txlock=immediate` 把写事务全库串行化，
// 「两人同时用掉同一张限量券」这类竞争在 SQLite 下**永远复现不出来**；生产统一 PostgreSQL 后
// 靠的是 ApplyCouponToOrder 里的 `SELECT ... FOR UPDATE` 行锁 + 带守卫的
// `UPDATE coupons SET used_count=used_count+1 WHERE ... used_count<max_uses`。
// 本用例用 20 并发抢一张 max_uses=5 的券，钉死三件事：
//
//	① 恰好 5 笔成功（不超发）；② used_count 与流水行数一致（不错计）；
//	③ 其余 15 笔只能拿到「该券已抢完」这一可回显业务码，而不是 500/SQL 异常。
//
// 依赖：PG_TEST_DSN（默认 postgres://$USER@127.0.0.1:5432/pgtest），无 PG 则跳过
// （与 quota_grants_pg_concurrency_test.go 同一口径）。
// ==========================================
package store_test

import (
	"errors"
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

func TestCouponRedeemConcurrencyOnPG(t *testing.T) {
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
	cfg := config.Default()
	cfg.DatabaseDriver = "postgres"
	config.C = cfg
	defer func() { config.C = prevC }()

	s, err := store.New(conn)
	if err != nil {
		t.Fatalf("store.New(PG) 失败: %v", err)
	}
	// 随机化租户 ID，避免多次运行互踩（券码也带同一后缀，防唯一索引冲突）
	tid := time.Now().Unix()%5_000_000 + 60_000_000
	code := "PGCOUP" + time.Now().Format("150405")
	if _, err := s.CreateCoupon(&store.Coupon{
		Code: code, Name: "并发限量券", Kind: store.CouponKindAny,
		DiscountType: store.CouponTypeAmount, DiscountValue: 10, MaxUses: 5, Enabled: 1,
	}); err != nil {
		t.Fatalf("建券失败: %v", err)
	}

	const attempts = 20
	orders := make([]*store.Order, 0, attempts)
	for i := 0; i < attempts; i++ {
		o, oerr := s.CreateOrderChannel(tid, s.TokensFromPoints(1000), 100, 1, "mock", "")
		if oerr != nil {
			t.Fatalf("建单失败: %v", oerr)
		}
		orders = append(orders, o)
	}

	var ok, soldOut, other int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(o *store.Order) {
			defer wg.Done()
			<-start
			_, _, _, aerr := s.ApplyCouponToOrder(o.ID, tid, code, store.CouponKindRecharge)
			switch {
			case aerr == nil:
				atomic.AddInt64(&ok, 1)
			case errors.Is(aerr, store.ErrCouponInvalid):
				var ce *store.CouponError
				if errors.As(aerr, &ce) && ce.Code == store.CouponErrSoldOut {
					atomic.AddInt64(&soldOut, 1)
					return
				}
				atomic.AddInt64(&other, 1)
				t.Logf("非预期券业务错误: %v", aerr)
			default:
				atomic.AddInt64(&other, 1)
				t.Logf("非预期错误: %v", aerr)
			}
		}(orders[i])
	}
	close(start)
	wg.Wait()

	if other != 0 {
		t.Fatalf("出现非预期错误 %d 笔（应仅为 成功/已抢完 两类），ok=%d soldOut=%d", other, ok, soldOut)
	}
	if ok != 5 {
		t.Fatalf("max_uses=5 应恰好 5 笔成功，实得 ok=%d soldOut=%d（超发或误拒均为资金事故）", ok, soldOut)
	}
	c, gerr := s.GetCouponByCode(code)
	if gerr != nil {
		t.Fatalf("取券失败: %v", gerr)
	}
	if c.UsedCount != 5 {
		t.Fatalf("used_count 应为 5，实际 %d", c.UsedCount)
	}
	d := db.CurrentDialect()
	var rows int
	if qerr := db.QueryRow(conn, d, `SELECT COUNT(1) FROM coupon_redemptions WHERE code=?`, code).Scan(&rows); qerr != nil {
		t.Fatalf("统计流水失败: %v", qerr)
	}
	if rows != 5 {
		t.Fatalf("核销流水应 5 条（与计数一致），实际 %d", rows)
	}
	// 成功的那 5 张单必须确实按折后金额落库（90=100-10），失败单保持原价
	var discounted int
	if qerr := db.QueryRow(conn, d, `SELECT COUNT(1) FROM orders WHERE tenant_id=? AND amount_money=90`, tid).Scan(&discounted); qerr != nil {
		t.Fatalf("统计折后单失败: %v", qerr)
	}
	if discounted != 5 {
		t.Fatalf("应有 5 张单改写为折后 90 元，实际 %d", discounted)
	}
	var touched int
	if qerr := db.QueryRow(conn, d, `SELECT COUNT(1) FROM orders WHERE tenant_id=? AND coupon_code<>''`, tid).Scan(&touched); qerr != nil {
		t.Fatalf("统计带券单失败: %v", qerr)
	}
	if touched != 5 {
		t.Fatalf("只允许成功核销的 5 张单挂券码，实际 %d", touched)
	}
	// 清理本次随机租户的残留（同一 PG 库会被多轮用例复用）
	if _, err := db.Exec(conn, d, `DELETE FROM coupon_redemptions WHERE code=?`, code); err != nil {
		t.Logf("清理核销流水失败（不影响断言）: %v", err)
	}
	if _, err := db.Exec(conn, d, `DELETE FROM orders WHERE tenant_id=?`, tid); err != nil {
		t.Logf("清理订单失败（不影响断言）: %v", err)
	}
	if _, err := db.Exec(conn, d, `DELETE FROM coupons WHERE code=?`, code); err != nil {
		t.Logf("清理券失败（不影响断言）: %v", err)
	}
}
