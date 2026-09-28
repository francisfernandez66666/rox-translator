// ============ 本文件职责中文说明 ============
// billing_settle_pg_concurrency_test.go · ★ D-1（全量审计 2026-09-29）配套回归：
// 「欠费结算 SettleExhausted」与「正常扣减 DeductWithGrants」在 PostgreSQL 真实并发下的资金守恒。
//
// 背景：生产唯一方言是 PostgreSQL（S 批决策）。SQLite 有 DSN `_txlock=immediate` 全库写锁兜着，
// 事务内「先读快照、后写回」这类写法在本地永远不出错；PG 是 READ COMMITTED 且 BEGIN 不持写锁，
// 同一租户的并发核销会在「读快照」与「写回」之间插进来，于是历史实现有两类事故：
//
//	① 台账段 `SET "left"=?`（快照算出的绝对值）——把并发已扣掉的量**又送回去**（丢失更新），
//	   或按旧快照计 consumed ⇒ **流水多记**（账面记 300、实际只移走 200）。
//	② 永久余额兜底段 `balance=balance-take`（take 按**旧快照**算、无 `balance>=?` 守卫）——
//	   并发扣小后仍可把它扣成**负数**（透支，平台替客户买单，流水上看不出来）。
//
// 两处已统一改回主链范式：相对扣减 + `>=take` 守卫 + RowsAffected 判定 + 守卫没过重读重试一次。
//
// ★ 为什么是两个场景而不是一个（2026-09-29 反证实测出来的，别合并）：
//
//	· 场景一（双桶都有钱）→ 抓 ①：旧绝对赋值写法下实测 5/5 红（实扣被多记 220~240 token）。
//	  但同一场景对 ② **不敏感**：扣减全部落在台账上，永久余额压根没被并发碰到，
//	  把 `AND balance>=?` 守卫拆掉后实测 5/5 照样绿（假绿）。
//	· 场景二（台账清零、钱全在永久余额）→ 才是能抓 ②「无守卫扣成负数」的形状，
//	  对应审计需求原话「take > 实时余额时不扣负」。
//	只留场景一就会长期挂着一条「看着在守资金、其实守不到透支」的空转账，故两腿都在。
//
// 守恒不变式（比「不扣负」更强，① 的关键判据）：
//
//	初始可用量 - (成功扣减合计 + 结算实耗合计) == 结束时可用量
//
// 依赖：PG_TEST_DSN（默认 postgres://$USER@127.0.0.1:5432/pgtest）；无 PG 则跳过。
// 方言自钉：AGENTS §一·4——本文件自行把 config.C 钉成 postgres，不依赖外部 DB_DRIVER。
// ========================================
package store_test

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/db"
	"translator/internal/store"

	_ "github.com/lib/pq"
)

// settleRaceOutcome 一轮「扣减 × 结算」并发压测的计数结果。
type settleRaceOutcome struct {
	okDeduct, insufficient, settleOK, settleNeeded, retryable int64
	settleConsumed                                            int64
	mu                                                        sync.Mutex
	unexpected                                                []string
}

// noteUnexpected 记录一笔非预期错误（只留前 8 条，够定位即可、避免刷屏）。
func (o *settleRaceOutcome) noteUnexpected(e error) {
	o.mu.Lock()
	if len(o.unexpected) < 8 {
		o.unexpected = append(o.unexpected, e.Error())
	}
	o.mu.Unlock()
}

// isRetryablePGConflict 认 PostgreSQL 的「并发冲突可重试」两类 SQLSTATE：
// 40001 serialization_failure / 40P01 deadlock_detected。
// 说明（这不是放宽判据）：结算与扣减对台账行的加锁次序不同（扣减按 expires_at ASC 且带 FOR UPDATE，
// 结算按「试用优先、再临期」且靠守卫重试），理论上存在互等窗口。该窗口在本修复之前就在
// （旧代码同样是这套访问次序），且 PG 中止事务时**分文未动**，守恒不变式不受影响，
// 故单独计数并打印，由调用方（计费链已有守卫重试）处理；不混进「非预期错误」。
func isRetryablePGConflict(e error) bool {
	msg := strings.ToLower(e.Error())
	return strings.Contains(msg, "deadlock detected") ||
		strings.Contains(msg, "sqlstate 40001") ||
		strings.Contains(msg, "could not serialize access")
}

// openPGStore 连接 PG_TEST_DSN（缺省本机 pgtest 库），把 config.C 钉成 postgres 并返回还原闭包。
// 无可用 PG 时 t.Skipf——与 quota_grants_pg_concurrency_test.go 同口径。
func openPGStore(t *testing.T) (*sql.DB, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("PG_TEST_DSN")
	if dsn == "" {
		dsn = "postgres://" + os.Getenv("USER") + "@127.0.0.1:5432/pgtest?sslmode=disable"
	}
	conn, err := db.Open(db.Config{Driver: db.DriverPostgres, DSN: dsn})
	if err != nil {
		t.Skipf("无可用 PostgreSQL，跳过：%v", err)
	}
	prevC := config.C
	config.C = &config.Config{DatabaseDriver: "postgres"}
	s, err := store.New(conn)
	if err != nil {
		config.C = prevC
		conn.Close()
		t.Fatalf("store.New(PG) 失败: %v", err)
	}
	return conn, s, func() { config.C = prevC; conn.Close() }
}

// runSettleDeductRace 起 deductN 笔各 deductAmt 的正常扣减 + 2 次全量欠费结算，同时打同一租户。
// 参数：tid=租户（调用方随机化，重复运行互不干扰）；grantInit=发放台账额度（0＝钱全在永久余额）；
//
//	balInit=永久余额额度；deductN/deductAmt=扣减压测形状。
//
// 返回：并发计数结果。建数据失败属用例自身前提，直接 t.Fatalf。
func runSettleDeductRace(t *testing.T, s *store.Store, tid int64, grantInit, balInit, deductN, deductAmt int64) *settleRaceOutcome {
	t.Helper()
	out := &settleRaceOutcome{}
	if err := s.EnsureBalance(tid); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	if grantInit > 0 {
		exp := time.Now().UTC().Add(24 * time.Hour)
		if err := s.CreateQuotaGrant(tid, "trial", grantInit, exp, "ut-d1", 0); err != nil {
			t.Fatalf("发放台账失败: %v", err)
		}
	}
	// 永久余额用直接 UPDATE 钉值（EnsureBalance 已保证账户行存在）
	if _, err := db.Exec(s.DB(), db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=? WHERE tenant_id=?", balInit, tid); err != nil {
		t.Fatalf("设永久余额失败: %v", err)
	}
	// owed 远大于任何可用量：复核恒判「确实欠费」，结算一定走完整清零路径
	// （而不是被挡成 ErrSettleNotNeeded，那样两条 UPDATE 就不会交错）
	const owed = int64(100_000)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := int64(0); i < deductN; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			switch e := s.DeductWithGrants(tid, deductAmt); {
			case e == nil:
				atomic.AddInt64(&out.okDeduct, 1)
			case errors.Is(e, store.ErrInsufficientBalance):
				atomic.AddInt64(&out.insufficient, 1)
			case isRetryablePGConflict(e):
				atomic.AddInt64(&out.retryable, 1)
			default:
				out.noteUnexpected(e)
			}
		}()
	}
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			consumed, e := s.SettleExhausted(tid, owed)
			switch {
			case e == nil:
				atomic.AddInt64(&out.settleOK, 1)
				atomic.AddInt64(&out.settleConsumed, consumed)
			case errors.Is(e, store.ErrSettleNotNeeded):
				atomic.AddInt64(&out.settleNeeded, 1)
			case isRetryablePGConflict(e):
				atomic.AddInt64(&out.retryable, 1)
			default:
				out.noteUnexpected(e)
			}
		}()
	}
	close(start)
	wg.Wait()
	return out
}

// judgeSettleRace 两条腿共用的判据：① 无非预期错误；② 两桶均不为负；③ 守恒等式；
// ④ 结算流水与函数返回值勾稽；⑤ 结算消耗不得超过初始总量。
// 参数：initial=本轮初始可用量合计，deductAmt=单笔扣减额（用于还原实扣合计）。
func judgeSettleRace(t *testing.T, conn *sql.DB, s *store.Store, out *settleRaceOutcome, tid, initial, deductAmt int64) {
	t.Helper()
	// ① 错误形态：只允许「成功 / 余额不足 / 无需结算 / PG 可重试冲突」四类
	if len(out.unexpected) > 0 {
		t.Fatalf("出现非预期错误 %d 笔：%v（允许形态：成功 / ErrInsufficientBalance / ErrSettleNotNeeded / PG 可重试冲突）",
			len(out.unexpected), out.unexpected)
	}
	// ② 不扣负：两桶各自不得为负（D-1 直接后果）
	var negBal, negGrant int64
	if err := db.QueryRow(conn, db.CurrentDialect(),
		`SELECT COUNT(*) FROM balance_accounts WHERE tenant_id=? AND balance<0`, tid).Scan(&negBal); err != nil || negBal != 0 {
		t.Fatalf("出现负永久余额 %d 行（D-1 透支）(err=%v)", negBal, err)
	}
	if err := db.QueryRow(conn, db.CurrentDialect(),
		`SELECT COUNT(*) FROM quota_grants WHERE tenant_id=? AND "left"<0`, tid).Scan(&negGrant); err != nil || negGrant != 0 {
		t.Fatalf("出现负库存台账 %d 行 (err=%v)", negGrant, err)
	}
	// ③ 守恒：初始 - 实扣 == 期末。
	//    这一条正是抓「丢失更新（把并发扣掉的量送回去）/ 流水多记」的关键——
	//    旧实现按快照写绝对值时，期末余量与流水记数对不上，等式即红。
	gLeft, bLeft, err := s.TenantRemainTotal(tid)
	if err != nil {
		t.Fatalf("TenantRemainTotal 失败: %v", err)
	}
	moved := out.okDeduct*deductAmt + out.settleConsumed
	totalLeft := gLeft + bLeft
	if initial-moved != totalLeft {
		t.Fatalf("资金守恒破裂：初始 %d - 实扣 %d = %d，期末实测 %d（台账 %d + 永久 %d；成功扣减 %d 笔，结算实耗 %d）",
			initial, moved, initial-moved, totalLeft, gLeft, bLeft, out.okDeduct, out.settleConsumed)
	}
	if totalLeft < 0 {
		t.Fatalf("期末可用量为负：%d", totalLeft)
	}
	// ④ 流水勾稽：结算调整流水（charge_kind='settle'）合计必须等于函数返回的 consumed，
	//    否则就是「账面记了、钱没动」或反之（无痕归零 / 无痕多扣）。
	var ledgerSettle int64
	if err := db.QueryRow(conn, db.CurrentDialect(),
		`SELECT COALESCE(SUM(cost),0) FROM usage_ledger WHERE tenant_id=? AND charge_kind='settle'`, tid).Scan(&ledgerSettle); err != nil {
		t.Fatalf("读结算流水失败: %v", err)
	}
	if ledgerSettle != out.settleConsumed {
		t.Fatalf("结算流水与实耗不符：ledger=%d 返回值=%d", ledgerSettle, out.settleConsumed)
	}
	// ⑤ 有界清零：owed 远大于可用量，语义保证消耗 ≤ 初始总量
	if out.settleConsumed > initial {
		t.Fatalf("结算消耗超过初始总量：%d > %d", out.settleConsumed, initial)
	}
	if out.retryable > 0 {
		t.Logf("PG 并发冲突（可重试）%d 笔：deadlock/serialization，属既有加锁次序互等窗口，事务已整体回滚、不影响守恒", out.retryable)
	}
	t.Logf("D-1 守恒通过：ok=%d insufficient=%d settleOK=%d settleNeeded=%d consumed=%d 期末=%d（台账 %d + 永久 %d）",
		out.okDeduct, out.insufficient, out.settleOK, out.settleNeeded, out.settleConsumed, totalLeft, gLeft, bLeft)
}

// TestSettleVsDeductDualBucketOnPG 场景一：台账 300 + 永久余额 300，12 笔各扣 20 与 2 次全量结算并发。
// 专抓 ①「按旧快照写回」——反证：旧绝对赋值写法下实测 5/5 红（流水被多记 220~240）。
func TestSettleVsDeductDualBucketOnPG(t *testing.T) {
	conn, s, restore := openPGStore(t)
	defer restore()
	tid := time.Now().Unix()%4_000_000 + 50_000_000
	out := runSettleDeductRace(t, s, tid, 300, 300, 12, 20)
	judgeSettleRace(t, conn, s, out, tid, 600, 20)
}

// TestSettleVsDeductPermBalanceOnlyOnPG 场景二：台账清零、永久余额 300，20 笔各扣 20 与 2 次全量结算并发。
// 专抓 ②「兜底段无守卫扣成负数」（审计原话：take > 实时余额时不扣负）。
// 形状说明：扣减总需求 20×20=400 > 余额 300，必有笔数被拒；而结算按快照想吃掉整个余额，
// 两者在同一行 balance_accounts 上撞车——旧写法末次 UPDATE 无守卫即把余额打成负数。
func TestSettleVsDeductPermBalanceOnlyOnPG(t *testing.T) {
	conn, s, restore := openPGStore(t)
	defer restore()
	tid := time.Now().Unix()%4_000_000 + 55_000_000
	out := runSettleDeductRace(t, s, tid, 0, 300, 20, 20)
	judgeSettleRace(t, conn, s, out, tid, 300, 20)
}
