// ============ 本文件职责中文说明 ============
// quota_grants_concurrency_test.go · 双桶台账并发扣减回归（2026-08-26 P0-4 止血配套）。
// 验证目标：多 goroutine 并发 DeductWithGrants 时——
//
//	① 台账行绝不出现负库存；② 消耗总量精确等于发放总额（无双花/丢更新）；
//	③ 永久余额守卫生效（余额不足时返回 ErrInsufficientBalance 且不产生任何扣减）。
//
// ========================================
package store

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newConcurrentTestStore 并发测试专用 Store：基于临时文件库（WAL）而非 :memory:。
// 原因：SQLite 的 :memory: 库是「每连接独立」的，连接池多开即各见各的空库，
// 无法承载真实多连接并发场景（会报 no such table）。文件库才能验证 IMMEDIATE 锁语义。
func newConcurrentTestStore(t *testing.T) *Store {
	t.Helper()
	// ★ 与生产 kb.Open 同款 DSN：busy_timeout / WAL / _txlock=immediate 均为
	//   「每连接」参数（DSN 形式才会随连接池下发到每个新连接；裸 Exec PRAGMA 只作用于单连接）。
	dsn := "file:" + filepath.Join(t.TempDir(), "conc.db") +
		"?_pragma=busy_timeout(10000)" +
		"&_pragma=journal_mode(WAL)" +
		"&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("打开测试数据库失败: %v", err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

// TestDeductWithGrantsConcurrency 并发扣减：20 个 goroutine 各扣 1000，总额度 15000。
// 期望：恰好 15 笔成功、5 笔余额不足；台账剩余合计为 0 且无负值；无永久余额被误扣。
func TestDeductWithGrantsConcurrency(t *testing.T) {
	st := newConcurrentTestStore(t)
	tid := int64(77)

	// 准备：两条台账（6000 + 9000 = 15000，近到期行在前）+ 永久余额 0
	if err := st.CreateQuotaGrant(tid, "trial", 6000, time.Now().Add(1*time.Hour), "test", 0); err != nil {
		t.Fatalf("建台账1失败: %v", err)
	}
	if err := st.CreateQuotaGrant(tid, "trial", 9000, time.Now().Add(48*time.Hour), "test", 0); err != nil {
		t.Fatalf("建台账2失败: %v", err)
	}
	if err := st.EnsureBalance(tid); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}

	const workers = 20
	const perCall = int64(1000)
	var mu sync.Mutex
	success, insufficient := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := st.DeductWithGrants(tid, perCall)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case err == ErrInsufficientBalance:
				insufficient++
			default:
				t.Errorf("意外错误: %v", err)
			}
		}()
	}
	wg.Wait()

	if success != 15 || insufficient != 5 {
		t.Fatalf("成功/不足笔数不符: success=%d insufficient=%d（期望 15/5）", success, insufficient)
	}
	// 台账剩余合计必须为 0（全部核销），且逐行不小于 0（无负库存）
	rows := st.db.QueryRow("SELECT COALESCE(SUM(left),0), COALESCE(MIN(left),0) FROM quota_grants WHERE tenant_id=?", tid)
	var sumLeft, minLeft int64
	if err := rows.Scan(&sumLeft, &minLeft); err != nil {
		t.Fatalf("读台账失败: %v", err)
	}
	if sumLeft != 0 || minLeft < 0 {
		t.Fatalf("台账剩余异常: sum=%d min=%d（期望 sum=0 且 min>=0）", sumLeft, minLeft)
	}
	// 永久余额必须保持 0：额度充足时不应动到永久桶
	bal, err := st.GetBalance(tid)
	if err != nil || bal.Balance != 0 {
		t.Fatalf("永久余额被误扣: %+v err=%v", bal, err)
	}
}

// TestDeductWithGrantsOverflow 永久桶兜底与不足拒绝：额度 5000 + 永久 3000，单笔扣 9000 应整体失败。
func TestDeductWithGrantsOverflow(t *testing.T) {
	st := newConcurrentTestStore(t)
	tid := int64(78)
	_ = st.CreateQuotaGrant(tid, "trial", 5000, time.Now().Add(24*time.Hour), "test", 0)
	_ = st.EnsureBalance(tid)
	if err := st.Charge(tid, 3000); err != nil {
		t.Fatalf("Charge 失败: %v", err)
	}
	// 请求 9000 > 台账 5000 + 永久 3000 → 整体回滚，什么都不该被扣
	if err := st.DeductWithGrants(tid, 9000); err != ErrInsufficientBalance {
		t.Fatalf("期望 ErrInsufficientBalance，实得: %v", err)
	}
	sum := st.SumActiveGrants(tid)
	if sum != 5000 {
		t.Fatalf("失败后台账应原封不动: sum=%d（期望 5000）", sum)
	}
	bal, _ := st.GetBalance(tid)
	if bal.Balance != 3000 {
		t.Fatalf("失败后永久余额应原封不动: %d（期望 3000）", bal.Balance)
	}
	// 可承受的混合扣减：7000 = 台账 5000 全核销 + 永久 2000
	if err := st.DeductWithGrants(tid, 7000); err != nil {
		t.Fatalf("混合扣减失败: %v", err)
	}
	if st.SumActiveGrants(tid) != 0 {
		t.Fatalf("台账未清零")
	}
	if bal, _ = st.GetBalance(tid); bal.Balance != 1000 {
		t.Fatalf("永久余额剩余不符: %d（期望 1000）", bal.Balance)
	}
}
