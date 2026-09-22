// ============ 本文件职责中文说明 ============
// 续费宽限期 + 自动续费重试台账（★ #74，2026-09-23）数据层单测：
//
//	A) renewal_attempts 同日唯一键：同一 (租户, 包, 日期) 只能抢到一次建单资格，
//	   换日可再抢——「同日不重复建单」的数据库级保证（多实例降级本地执行也吃在这一格）；
//	B) 宽限期两键（grace_expires_at / notified_grace）走 JSONPatchSet 单字段原子写，
//	   不得吞掉 permissions 里的订阅身份与句数镜像（B1 整改口径回归）；重新起算宽限期须复位
//	   通知去重位；与 SetTenantAutoRenew 交替写互不覆盖；
//	C) ExpirePackage 摘除身份时一并清零宽限期两键（★ 防「下一期宽限期形同虚设」的回归锁），
//	   同时保留句数余额买断资产；
//
//	D) 入参非法（packageID<=0 / attemptDate 空）时既不建格也不给资格，防止「空日期格子」把
//	   唯一键变成一个无限复用的万能闸门；
//	E) 台账格子按 (租户, 包, 日) 三维定位——回写只影响今日那一格，跨租户/跨包互不串台；
//	F) 并发抢占（模拟多实例降级本地执行同时跑）只有一个赢家，这是「同日只建一张」的最后一道保险；
//	G) uniq_renewal_attempt_day 唯一索引本身能挡住绕过业务方法的直插（闸门在数据库不在代码）。
//
// 内存 SQLite + newTestStoreWithTenants（与 packages_*_test.go 同一套夹具）。
// ★ 单测自钉 sqlite 方言（AGENTS.md §4）：config.Default() 会读 DB_DRIVER 并副作用写全局
//
//	config.C，run_uat 的 PG 模式下会泄漏方言给同包内存库用例，故显式钉住并 Cleanup 复原。
//
// ==========================================
package store

import (
	"sync"
	"testing"
	"time"

	"translator/internal/config"
	"translator/internal/tenant"
)

// pinSQLiteDialect 把全局方言钉成 sqlite 并在用例结束时复原（AGENTS.md §4 模板）。
func pinSQLiteDialect(t *testing.T) {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
}

// A：同日只可抢占一次建单资格，跨日恢复。
func TestRenewalAttemptClaimIsUniquePerDay(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStoreWithTenants(t)
	day1 := time.Now().Format("2006-01-02")
	day2 := time.Now().Add(24 * time.Hour).Format("2006-01-02")

	ok, err := s.ClaimRenewalAttempt(1, 77, day1)
	if err != nil || !ok {
		t.Fatalf("首次建单资格应抢到: ok=%v err=%v", ok, err)
	}
	// 同一格再抢一次必须失败（模拟多实例降级本地执行 / 手工重复触发扫描）
	ok2, err := s.ClaimRenewalAttempt(1, 77, day1)
	if err != nil {
		t.Fatalf("重复抢占不应报错: %v", err)
	}
	if ok2 {
		t.Fatal("同一天同一包重复抢占应返回 false（同日不重复建单的硬闸门）")
	}
	if n := s.RenewalAttemptsToday(1, 77, day1); n != 1 {
		t.Fatalf("今日台账应为 1 条，实际 %d", n)
	}
	// 结果回写：台账要能回答「这张单建成没有、单号是什么」
	if err := s.FinishRenewalAttempt(1, 77, day1, RenewalAttemptCreated, 9001, "RO9001", ""); err != nil {
		t.Fatalf("回写建单结果失败: %v", err)
	}
	var status, orderNo string
	if err := s.db.QueryRow(
		"SELECT status, order_no FROM renewal_attempts WHERE tenant_id=1 AND package_id=77 AND attempt_date=?", day1,
	).Scan(&status, &orderNo); err != nil {
		t.Fatalf("读取台账失败: %v", err)
	}
	if status != RenewalAttemptCreated || orderNo != "RO9001" {
		t.Fatalf("台账回写异常: status=%q order_no=%q", status, orderNo)
	}
	// 换日（下一个阶梯日）应重新可抢——这就是「未到账则按日退避重试」的地基
	ok3, err := s.ClaimRenewalAttempt(1, 77, day2)
	if err != nil || !ok3 {
		t.Fatalf("次日应可重新抢占建单资格: ok=%v err=%v", ok3, err)
	}
	// 另一个包同日互不影响（去重键是「租户+包+日」，不是「租户+日」）
	if ok4, err4 := s.ClaimRenewalAttempt(1, 78, day1); err4 != nil || !ok4 {
		t.Fatalf("不同包同日应可各自建单一次: ok=%v err=%v", ok4, err4)
	}
}

// B：宽限期两键原子写不吞订阅身份；标记与清理各自只动自己那两格。
func TestSubscriptionGracePermsKeysAreAtomic(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStoreWithTenants(t)
	base := &tenant.Perms{PackageCode: "m74", PackageExpires: "2026-09-01T00:00:00Z", SentenceBalance: 66, AutoRenew: true}
	if err := s.SaveTenantPerms(1, base); err != nil {
		t.Fatalf("预置权限失败: %v", err)
	}
	graceEnd := time.Now().Add(72 * time.Hour)
	if err := s.SetSubscriptionGrace(1, graceEnd); err != nil {
		t.Fatalf("落宽限期截止时刻失败: %v", err)
	}
	p := s.readPermsForGrace(t)
	if p.GraceExpiresAt == "" || p.NotifiedGrace {
		t.Fatalf("宽限期键未落库: %+v", p)
	}
	// 关键回归：permissions 其余字段（订阅身份/句数镜像/开关）必须原样保留
	if p.PackageCode != "m74" || p.PackageExpires != "2026-09-01T00:00:00Z" || p.SentenceBalance != 66 || !p.AutoRenew {
		t.Fatalf("宽限期写入覆盖了 permissions 其余字段: %+v", p)
	}
	if err := s.MarkGraceNotified(1); err != nil {
		t.Fatalf("置位宽限期通知标记失败: %v", err)
	}
	if p2 := s.readPermsForGrace(t); !p2.NotifiedGrace || p2.PackageCode != "m74" || p2.GraceExpiresAt != p.GraceExpiresAt {
		t.Fatalf("通知标记置位异常: %+v", p2)
	}
	// 重新起算宽限期（新一期到期再次进入）必须顺带复位通知去重位，否则永远收不到「进入宽限期」提醒
	if err := s.SetSubscriptionGrace(1, graceEnd.Add(24*time.Hour)); err != nil {
		t.Fatalf("重新落宽限期截止时刻失败: %v", err)
	}
	if p2b := s.readPermsForGrace(t); p2b.NotifiedGrace {
		t.Fatalf("重新起算宽限期应复位通知标记，否则新一期到期收不到提醒: %+v", p2b)
	}
	// 与其它单字段写路径交替：任何一侧都不得把对方刚写的值抹掉（B1 丢失更新回归）
	if err := s.SetTenantAutoRenew(1, false); err != nil {
		t.Fatalf("关闭自动续费失败: %v", err)
	}
	if err := s.MarkGraceNotified(1); err != nil {
		t.Fatalf("交替置位宽限期标记失败: %v", err)
	}
	if p2c := s.readPermsForGrace(t); p2c.AutoRenew || !p2c.NotifiedGrace || p2c.PackageCode != "m74" {
		t.Fatalf("交替单字段写入互相覆盖: %+v", p2c)
	}
	if err := s.ClearSubscriptionGrace(1); err != nil {
		t.Fatalf("清宽限期键失败: %v", err)
	}
	if p3 := s.readPermsForGrace(t); p3.GraceExpiresAt != "" || p3.NotifiedGrace || p3.PackageCode != "m74" {
		t.Fatalf("宽限期清理异常: %+v", p3)
	}
}

// C：ExpirePackage 摘除身份时清零宽限期两键（否则下一期到期会被遗留值判定「宽限期已过」）。
func TestExpirePackageClearsGraceKeys(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStoreWithTenants(t)
	pkg, err := s.CreatePackage(&Package{Code: "m74", Name: "月包74", PType: PackagePaid, Sentences: 30, DurationDays: 30, Enabled: 1})
	if err != nil {
		t.Fatalf("CreatePackage: %v", err)
	}
	if _, err := s.GrantPackageSentences(1, pkg); err != nil {
		t.Fatalf("GrantPackageSentences: %v", err)
	}
	if err := s.SetSubscriptionGrace(1, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("落宽限期键失败: %v", err)
	}
	if err := s.MarkGraceNotified(1); err != nil {
		t.Fatalf("置位通知标记失败: %v", err)
	}
	if _, err := s.ExpirePackage(1); err != nil {
		t.Fatalf("ExpirePackage: %v", err)
	}
	p := s.readPermsForGrace(t)
	if p.PackageCode != "" || p.PackageExpires != "" {
		t.Fatalf("订阅身份未摘除: %+v", p)
	}
	if p.GraceExpiresAt != "" || p.NotifiedGrace {
		t.Fatalf("摘除后宽限期键必须清零，否则下一期到期即刻被摘: %+v", p)
	}
	if p.SentenceBalance != 30 {
		t.Fatalf("句数余额是买断资产应保留 30，实际 %d", p.SentenceBalance)
	}
}

// D：入参非法时不得建格。attemptDate 是空串的话，唯一键退化成「(租户, 包) 永久一格」，
// 一旦某轮扫描拿到空日期抢到格子，之后所有阶梯日都会被它挡住建不了单（静默断供）。
func TestRenewalClaimRejectsInvalidArgs(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStoreWithTenants(t)
	today := time.Now().Format("2006-01-02")

	if ok, err := s.ClaimRenewalAttempt(1, 0, today); err != nil || ok {
		t.Fatalf("packageID<=0 应直接判无资格且不报错: ok=%v err=%v", ok, err)
	}
	if ok, err := s.ClaimRenewalAttempt(1, -7, today); err != nil || ok {
		t.Fatalf("负包 ID 应判无资格: ok=%v err=%v", ok, err)
	}
	if ok, err := s.ClaimRenewalAttempt(1, 77, ""); err != nil || ok {
		t.Fatalf("attemptDate 为空应判无资格: ok=%v err=%v", ok, err)
	}
	if n := graceAttemptRowCount(t, s); n != 0 {
		t.Fatalf("非法入参不应留下台账格子，实际 %d 行", n)
	}
	// 非法参数下的回写/计数同样应为无害空操作（不能污染别的数据）
	if err := s.FinishRenewalAttempt(1, 0, today, RenewalAttemptFailed, 0, "", "boom"); err != nil {
		t.Fatalf("非法包 ID 的回写应无害: %v", err)
	}
	if err := s.FinishRenewalAttempt(1, 77, "", RenewalAttemptFailed, 0, "", "boom"); err != nil {
		t.Fatalf("空日期的回写应无害: %v", err)
	}
	if n := s.RenewalAttemptsToday(1, 77, today); n != 0 {
		t.Fatalf("非法入参链路后台账仍应为空，实际 %d", n)
	}
	// 合法入参对照：确认上面的「0 行」不是因为表根本没建（防假绿）
	if ok, err := s.ClaimRenewalAttempt(1, 77, today); err != nil || !ok {
		t.Fatalf("合法入参应抢到资格: ok=%v err=%v", ok, err)
	}
	if n := graceAttemptRowCount(t, s); n != 1 {
		t.Fatalf("合法入参应留下 1 格，实际 %d", n)
	}
}

// E：回写只定位今日那一格，且计数按 (租户, 包, 日) 三维隔离。
// WHY：FinishRenewalAttempt 的 WHERE 少一维就会把历史台账整片改写，
// 管理台「为什么这个客户没收到续费单」的排障线索随之失真。
func TestRenewalFinishTouchesOnlyClaimedCell(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStoreWithTenants(t)
	day1 := time.Now().Format("2006-01-02")
	day2 := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	day3 := time.Now().AddDate(0, 0, 2).Format("2006-01-02")

	for _, d := range []string{day1, day2} {
		if ok, err := s.ClaimRenewalAttempt(1, 77, d); err != nil || !ok {
			t.Fatalf("抢占 %s 格子失败: ok=%v err=%v", d, ok, err)
		}
	}
	if ok, err := s.ClaimRenewalAttempt(2, 77, day1); err != nil || !ok {
		t.Fatalf("另一租户同日应可抢占（去重键含租户）: ok=%v err=%v", ok, err)
	}
	// 只回写 day1：另两格必须保持「已抢占未回写」的 pending 原态
	if err := s.FinishRenewalAttempt(1, 77, day1, RenewalAttemptCreated, 5001, "RO5001", ""); err != nil {
		t.Fatalf("回写今日格子失败: %v", err)
	}
	if got := graceAttemptStatus(t, s, 1, 77, day2); got != RenewalAttemptPending {
		t.Fatalf("次日格子不应被今日回写波及，实际 status=%q", got)
	}
	if got := graceAttemptStatus(t, s, 2, 77, day1); got != RenewalAttemptPending {
		t.Fatalf("另一租户格子不应被波及，实际 status=%q", got)
	}
	if got := graceAttemptStatus(t, s, 1, 77, day1); got != RenewalAttemptCreated {
		t.Fatalf("今日格子应回写为 created，实际 %q", got)
	}
	// 失败格的订单号/原因只落在自己那一格
	if err := s.FinishRenewalAttempt(2, 77, day1, RenewalAttemptFailed, 0, "", "包已下架"); err != nil {
		t.Fatalf("回写失败格失败: %v", err)
	}
	var reason string
	if err := s.db.QueryRow(
		"SELECT reason FROM renewal_attempts WHERE tenant_id=2 AND package_id=77 AND attempt_date=?", day1,
	).Scan(&reason); err != nil {
		t.Fatalf("读取失败原因失败: %v", err)
	}
	if reason != "包已下架" {
		t.Fatalf("失败原因未落库，实际 %q", reason)
	}
	// 计数三维隔离：day1/day2/day3、租户 1/2、包 77/78 各不相干
	if n := s.RenewalAttemptsToday(1, 77, day1); n != 1 {
		t.Fatalf("租户 1 包 77 今日应为 1 格，实际 %d", n)
	}
	if n := s.RenewalAttemptsToday(1, 77, day3); n != 0 {
		t.Fatalf("未抢占过的日期应为 0 格，实际 %d", n)
	}
	if n := s.RenewalAttemptsToday(1, 78, day1); n != 0 {
		t.Fatalf("另一包同日应为 0 格，实际 %d", n)
	}
	if n := s.RenewalAttemptsToday(2, 77, day1); n != 1 {
		t.Fatalf("租户 2 包 77 今日应为 1 格，实际 %d", n)
	}
}

// F：并发抢占只有一个赢家（数据库唯一键兜底，多实例降级本地执行的最后防线）。
// WHY：内存标记在「Redis 抖动 → 两实例各自本地跑扫描」下必然失效，
// 这条用例锁的就是「即使同进程内两个 goroutine 同时抢，也只放行一个」。
func TestRenewalClaimConcurrentSingleWinner(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStoreWithTenants(t)
	// ★ 夹具口径：内存 SQLite 的 `:memory:` 是「每连接一个独立库」，连接池一扩容
	//   两个 goroutine 抢的是两个互不相干的库，测出来的「多个赢家」是夹具假象而非产品行为。
	//   这里把池收成单连接，让并发落在同一个库上（生产 PG/SQLite 文件库天然共享同一库）。
	s.DB().SetMaxOpenConns(1)
	t.Cleanup(func() { s.DB().SetMaxOpenConns(0) })
	// 先跑一次合法抢占把表与索引建好：避免两个 goroutine 并发执行 CREATE TABLE 干扰赢家计数
	if ok, err := s.ClaimRenewalAttempt(1, 900, "2000-01-01"); err != nil || !ok {
		t.Fatalf("预热建表失败: ok=%v err=%v", ok, err)
	}
	const day = "2000-01-02"
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		win  int
		errs int
	)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ClaimRenewalAttempt(1, 901, day)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs++
				return
			}
			if ok {
				win++
			}
		}()
	}
	wg.Wait()
	if errs > 0 {
		t.Fatalf("并发抢占不应报错（唯一键冲突要走 INSERT OR IGNORE 静默吞掉），实际 %d 个出错", errs)
	}
	if win != 1 {
		t.Fatalf("并发抢占只能有一个赢家，实际 %d 个（同日会堆 %d 张续费单）", win, win)
	}
	if n := s.RenewalAttemptsToday(1, 901, day); n != 1 {
		t.Fatalf("台账应只有 1 格，实际 %d", n)
	}
}

// G：唯一索引本身要拦住「绕过 ClaimRenewalAttempt 的直接写入」。
// WHY：业务层的 INSERT OR IGNORE 只是「优雅跳过」，真正的闸门是数据库唯一约束；
// 将来若有别的路径（补数脚本、新调用点）直接往台账插行，这条断言仍能挡同日堆单。
func TestRenewalAttemptUniqueIndexRejectsRawDuplicate(t *testing.T) {
	pinSQLiteDialect(t)
	s := newTestStoreWithTenants(t)
	const day = "2000-03-03"
	if ok, err := s.ClaimRenewalAttempt(1, 500, day); err != nil || !ok {
		t.Fatalf("抢占格子失败: ok=%v err=%v", ok, err)
	}
	// 同键直插必须被唯一索引拒绝（若索引没建成，这里会成功并留下两格）
	_, err := s.db.Exec(
		"INSERT INTO renewal_attempts (tenant_id, package_id, attempt_date, status) VALUES (?,?,?,?)",
		1, 500, day, RenewalAttemptCreated)
	if err == nil {
		t.Fatal("同日同包的重复行被写入：uniq_renewal_attempt_day 唯一索引未生效")
	}
	if n := s.RenewalAttemptsToday(1, 500, day); n != 1 {
		t.Fatalf("重复插入被拒后台账仍应为 1 格，实际 %d", n)
	}
	// 反面对照：换包/换日直插应成功（证明上面那次失败是「同日同包」而非整表禁写）
	if _, err2 := s.db.Exec(
		"INSERT INTO renewal_attempts (tenant_id, package_id, attempt_date, status) VALUES (?,?,?,?)",
		1, 501, day, RenewalAttemptCreated); err2 != nil {
		t.Fatalf("另一包同日直插应成功: %v", err2)
	}
	if _, err3 := s.db.Exec(
		"INSERT INTO renewal_attempts (tenant_id, package_id, attempt_date, status) VALUES (?,?,?,?)",
		1, 500, "2000-03-04", RenewalAttemptCreated); err3 != nil {
		t.Fatalf("另一日直插应成功: %v", err3)
	}
}

// graceAttemptRowCount 台账总行数（验证「非法入参不落格」）。
func graceAttemptRowCount(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(1) FROM renewal_attempts").Scan(&n); err != nil {
		t.Fatalf("统计台账失败: %v", err)
	}
	return n
}

// graceAttemptStatus 读取指定 (租户, 包, 日) 格子的状态（格子不存在即判失败）。
func graceAttemptStatus(t *testing.T, s *Store, tid, pkgID int64, day string) string {
	t.Helper()
	var status string
	if err := s.db.QueryRow(
		"SELECT status FROM renewal_attempts WHERE tenant_id=? AND package_id=? AND attempt_date=?",
		tid, pkgID, day).Scan(&status); err != nil {
		t.Fatalf("读取格子 (%d,%d,%s) 失败: %v", tid, pkgID, day, err)
	}
	return status
}

// readPermsForGrace 读取权限快照（失败即判用例失败）。
func (s *Store) readPermsForGrace(t *testing.T) *tenant.Perms {
	t.Helper()
	p, err := s.GetTenantPerms(1)
	if err != nil {
		t.Fatalf("读取权限失败: %v", err)
	}
	return p
}
