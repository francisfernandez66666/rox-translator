// ============ billing_uat_batchb_test.go · 职责说明 ============
// 2026-09-25 发布前 UAT 修复批 B（计费与配额域）store 层回归断言，逐钉六条最易
// 被后续改动悄悄回退的口径：
//
//	① F-34：静态码超时取消单的「我已付费」补审重建幂等——同一原单连点只留一张
//	   在途 pending 补审单（旧实现每点一次多建一张，双确认即双入账，生产实测 1→2 张）；
//	② F-36：升级闭环回收——先退新单再退旧单时，旧单退款须连带清零子单遗留的
//	   order_carry 结转份额（白嫖口子，生产实测幽灵 3,000 积分）；子单仍在 paid
//	   时 carry 必须保留（合法折抵），两向都钉；
//	③ F-37：LatestActivePaidSubscription 只认「在期」paid 订阅（paid_at+duration_days
//	   过期不算、days<=0 视为永不过期、排除被退单本身）——退款身份改挂判据的单一事实源；
//	④ F-43：不可开票订单（不存在/未支付）的报错走单一文案，不得把 sql.ErrNoRows
//	   底裤（"sql: no rows in result set"）漏给用户；
//	⑤ F-32：CreateAlertPerOrder 绕开 (tenant,kind) open 幂等闸——per-order 每单必告
//	   （旧实现同型第二单被静默吞掉，生产 id55 即此态）；
//	⑥ F-12：充值尺子重锚 33222 分/百万 token，使 3,000 积分（90 万 token）恰折算 ¥299，
//	   且 system_config 显式值仍优先于代码默认。
//
// 方言：固定 SQLite 内存库并显式钉死 config.C（AGENTS.md §4，防 run_uat 的 PG 模式泄漏）。
// 运行：env DB_DRIVER=sqlite go test -count=1 ./internal/store/ -run TestUATBatchB
// =============================================
package store

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"translator/internal/config"

	_ "modernc.org/sqlite"
)

// batchBEnv 批 B 测试环境：钉 SQLite 方言 + 独立内存库 Store（每用例一套，互不污染）。
// tenants 表需先于 New 建好（订阅发放路径 getTenantPermsTx 要读它，口径同 newTestStoreWithTenants）。
func batchBEnv(t *testing.T) *Store {
	t.Helper()
	old := config.C
	cfg := config.Default()
	cfg.DatabaseDriver = "sqlite"
	config.C = cfg
	t.Cleanup(func() { config.C = old })
	conn, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS tenants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		"code" TEXT UNIQUE NOT NULL,
		"name" TEXT NOT NULL DEFAULT '',
		"status" TEXT NOT NULL DEFAULT 'active',
		"expires_at" TEXT NOT NULL DEFAULT '',
		"permissions" TEXT NOT NULL DEFAULT '{}',
		"is_personal" INTEGER NOT NULL DEFAULT 0,
		"created_at" TEXT,
		"updated_at" TEXT
	)`); err != nil {
		t.Fatalf("建 tenants 表失败: %v", err)
	}
	s, err := New(conn)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return s
}

// setOrderCol 测试辅助：直改订单单列（模拟超时取消/回溯支付时间等外部触发路径）。
func setOrderCol(t *testing.T, s *Store, orderID int64, col, val string) {
	t.Helper()
	if _, err := s.db.Exec("UPDATE orders SET "+col+"=? WHERE id=?", val, orderID); err != nil {
		t.Fatalf("改订单 %d 列 %s 失败: %v", orderID, col, err)
	}
}

// countPendingReopens 统计指向某原单的在途（pending）补审单张数。
func countPendingReopens(t *testing.T, s *Store, srcOrderID int64) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM orders WHERE reopen_from_order=? AND status='pending'", srcOrderID).Scan(&n); err != nil {
		t.Fatalf("统计补审单失败: %v", err)
	}
	return n
}

// carryLeft 查询某订单升级结转（order_carry）份额的剩余合计 token。
func carryLeft(t *testing.T, s *Store, tid, childOrderID int64) int64 {
	t.Helper()
	var left int64
	if err := s.db.QueryRow(`SELECT COALESCE(SUM("left"),0) FROM quota_grants
		WHERE tenant_id=? AND source='order_carry' AND ref_id=?`, tid, childOrderID).Scan(&left); err != nil {
		t.Fatalf("查 carry 失败: %v", err)
	}
	return left
}

// mustPaidSubOrder 测试辅助：为租户创建并支付一笔付费订阅单，返回订单。
func mustPaidSubOrder(t *testing.T, s *Store, tid, uid int64, pkg *Package) *Order {
	t.Helper()
	o, err := s.CreatePackageOrder(tid, pkg, uid, "mock")
	if err != nil {
		t.Fatalf("建订阅单失败: %v", err)
	}
	if err := s.MarkOrderPaid(o.ID, tid); err != nil {
		t.Fatalf("订阅单入账失败: %v", err)
	}
	return o
}

// TestUATBatchB_F34_ReopenManualOrderIdempotent ①：同一超时取消单连续补审只产出一张在途单。
func TestUATBatchB_F34_ReopenManualOrderIdempotent(t *testing.T) {
	s := batchBEnv(t)
	o, err := s.CreateOrderChannel(21, 300000, 0, 5, "manual", "")
	if err != nil {
		t.Fatalf("建静态码单失败: %v", err)
	}
	setOrderCol(t, s, o.ID, "status", "cancelled")
	r1, err := s.ReopenManualOrder(o.ID, 21)
	if err != nil {
		t.Fatalf("首次补审失败: %v", err)
	}
	if r1.Status != "pending" || r1.Channel != "manual" || r1.ManualConfirm != 1 {
		t.Fatalf("补审单应为 manual+pending+人工确认，got %s/%s/%d", r1.Channel, r1.Status, r1.ManualConfirm)
	}
	// 连点第二次：必须复用同一张在途单，表里在途补审单恒 1 张
	r2, err := s.ReopenManualOrder(o.ID, 21)
	if err != nil {
		t.Fatalf("重复补审失败: %v", err)
	}
	if r2.ID != r1.ID {
		t.Fatalf("重复补审须复用在途单：got %d want %d", r2.ID, r1.ID)
	}
	if n := countPendingReopens(t, s, o.ID); n != 1 {
		t.Fatalf("在途补审单必须只剩 1 张，got %d", n)
	}
	// 补审单入账后再点：上一张已不 pending，应另建新单（生命周期不被防重闸卡死）
	if err := s.MarkOrderPaid(r1.ID, 21); err != nil {
		t.Fatalf("补审单入账失败: %v", err)
	}
	r3, err := s.ReopenManualOrder(o.ID, 21)
	if err != nil {
		t.Fatalf("入账后再补审失败: %v", err)
	}
	if r3.ID == r1.ID {
		t.Fatal("已入账补审单不应被复用")
	}
	// 非 cancelled 单拒绝补审（判据不放宽）
	if _, err := s.ReopenManualOrder(r3.ID, 21); err == nil {
		t.Fatal("pending 单不允许作为补审源")
	}
}

// TestUATBatchB_F36_CarryReclaimOnParentRefund ②：先退新后退旧→旧单退款连带清零子单 carry；
// 子单仍 paid→carry 保留（合法折抵不被误收）。
func TestUATBatchB_F36_CarryReclaimOnParentRefund(t *testing.T) {
	s := batchBEnv(t)
	pkgA, err := s.CreatePackage(&Package{TenantID: 0, Code: "uat_b_a", Name: "A 包", PType: "paid", Points: 1000, PriceMoney: 99, DurationDays: 30, Enabled: 1})
	if err != nil {
		t.Fatalf("建包 A 失败: %v", err)
	}
	pkgB, err := s.CreatePackage(&Package{TenantID: 0, Code: "uat_b_b", Name: "B 包", PType: "paid", Points: 2000, PriceMoney: 199, DurationDays: 30, Enabled: 1})
	if err != nil {
		t.Fatalf("建包 B 失败: %v", err)
	}
	// —— 场景 A（租户 31）：升级闭环全退，旧单退款须回收 carry ——
	paid := mustPaidSubOrder(t, s, 31, 5, pkgA)
	up, err := s.CreateUpgradeOrder(31, pkgB, &UpgradeCredit{OldOrderID: paid.ID, CreditMoney: 49.5, RemainTokens: paid.AmountTokens}, 5, "mock")
	if err != nil {
		t.Fatalf("建升级单失败: %v", err)
	}
	if err := s.MarkOrderPaid(up.ID, 31); err != nil {
		t.Fatalf("升级单入账失败: %v", err)
	}
	carry0 := carryLeft(t, s, 31, up.ID)
	if carry0 <= 0 {
		t.Fatalf("升级入账后应存在 carry 结转份额，got %d", carry0)
	}
	if err := s.RefundOrder(up.ID, 31); err != nil {
		t.Fatalf("退子单失败: %v", err)
	}
	// A3 豁免：退子单不动 carry（其价值对应旧单已付对价）
	if carryLeft(t, s, 31, up.ID) != carry0 {
		t.Fatal("退子单不应回收 carry（A3 豁免口径）")
	}
	if err := s.RefundOrder(paid.ID, 31); err != nil {
		t.Fatalf("退旧单失败: %v", err)
	}
	// ★ F-36 闭环：旧单退款连带清零已退子单的 carry
	if left := carryLeft(t, s, 31, up.ID); left != 0 {
		t.Fatalf("旧单退款后 carry 必须归零，got %d", left)
	}
	// —— 场景 B（租户 32）：子单仍 paid，旧单退款不得误收 carry ——
	paidB := mustPaidSubOrder(t, s, 32, 5, pkgA)
	upB, err := s.CreateUpgradeOrder(32, pkgB, &UpgradeCredit{OldOrderID: paidB.ID, CreditMoney: 49.5, RemainTokens: paidB.AmountTokens}, 5, "mock")
	if err != nil {
		t.Fatalf("建升级单 B 失败: %v", err)
	}
	if err := s.MarkOrderPaid(upB.ID, 32); err != nil {
		t.Fatalf("升级单 B 入账失败: %v", err)
	}
	carryB := carryLeft(t, s, 32, upB.ID)
	if carryB <= 0 {
		t.Fatalf("场景 B 应存在 carry，got %d", carryB)
	}
	if err := s.RefundOrder(paidB.ID, 32); err != nil {
		t.Fatalf("退旧单 B 失败: %v", err)
	}
	if carryLeft(t, s, 32, upB.ID) != carryB {
		t.Fatal("子单仍 paid 时 carry 必须保留（合法折抵）")
	}
}

// TestUATBatchB_F37_LatestActivePaidSubscription ③：在期判据——过期单不算、
// days<=0 永不过期、排除被退单、全退光返回 ok=false。
func TestUATBatchB_F37_LatestActivePaidSubscription(t *testing.T) {
	s := batchBEnv(t)
	pkgX, err := s.CreatePackage(&Package{TenantID: 0, Code: "uat_c_x", Name: "X 包", PType: "paid", Points: 1000, PriceMoney: 99, DurationDays: 30, Enabled: 1})
	if err != nil {
		t.Fatalf("建包 X 失败: %v", err)
	}
	pkgY, err := s.CreatePackage(&Package{TenantID: 0, Code: "uat_c_y", Name: "Y 包（永不过期）", PType: "paid", Points: 2000, PriceMoney: 199, DurationDays: 0, Enabled: 1})
	if err != nil {
		t.Fatalf("建包 Y 失败: %v", err)
	}
	pkgZ, err := s.CreatePackage(&Package{TenantID: 0, Code: "uat_c_z", Name: "Z 包", PType: "paid", Points: 3000, PriceMoney: 299, DurationDays: 30, Enabled: 1})
	if err != nil {
		t.Fatalf("建包 Z 失败: %v", err)
	}
	ox := mustPaidSubOrder(t, s, 33, 5, pkgX)
	oy := mustPaidSubOrder(t, s, 33, 5, pkgY)
	oz := mustPaidSubOrder(t, s, 33, 5, pkgZ)
	// paid_at 手工拉开梯度：X 最新、Y 次新、Z 回溯到 2020（已过期）
	setOrderCol(t, s, oy.ID, "paid_at", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339))
	setOrderCol(t, s, oz.ID, "paid_at", "2020-01-01T00:00:00Z")
	// 排除 X：应改挂次新的在期 Y（Z 虽面值大但已过期，不算）
	code, exp, ok, err := s.LatestActivePaidSubscription(33, ox.ID)
	if err != nil || !ok {
		t.Fatalf("应存在在期订阅: ok=%v err=%v", ok, err)
	}
	if code != "uat_c_y" {
		t.Fatalf("应改挂 Y 包，got %q", code)
	}
	if exp != "" {
		t.Fatalf("days<=0 包到期时间应为空串（永不过期），got %q", exp)
	}
	// 排除 Y：X 仍在期且最新
	if code2, _, ok2, _ := s.LatestActivePaidSubscription(33, oy.ID); !ok2 || code2 != "uat_c_x" {
		t.Fatalf("排除 Y 后应返回 X，got %q ok=%v", code2, ok2)
	}
	// X/Y 都置 refunded 后：只剩过期 Z → ok=false（身份必须清空而非挂过期包）
	setOrderCol(t, s, ox.ID, "status", "refunded")
	setOrderCol(t, s, oy.ID, "status", "refunded")
	if _, _, ok3, _ := s.LatestActivePaidSubscription(33, oz.ID); ok3 {
		t.Fatal("过期订阅不得作为在期身份来源")
	}
}

// TestUATBatchB_F43_InvoiceErrorMessage ④：不可开票（不存在/未支付）回单一文案，不漏驱动层错误。
func TestUATBatchB_F43_InvoiceErrorMessage(t *testing.T) {
	s := batchBEnv(t)
	_, err := s.CreateInvoice(41, 999999, "某某公司", "TAX01")
	if err == nil {
		t.Fatal("不存在订单应拒开")
	}
	if !strings.Contains(err.Error(), "不可开票") {
		t.Fatalf("应回业务文案，got %q", err.Error())
	}
	if strings.Contains(err.Error(), "sql:") {
		t.Fatalf("驱动层错误泄漏给用户: %q", err.Error())
	}
	// pending 单同样拒开
	o, err := s.CreateOrderChannel(41, 300000, 3, 5, "mock", "")
	if err != nil {
		t.Fatalf("建 pending 单失败: %v", err)
	}
	if _, err := s.CreateInvoice(41, o.ID, "某某公司", "TAX01"); err == nil || !strings.Contains(err.Error(), "不可开票") {
		t.Fatalf("未支付订单应拒开并回单一文案，got %v", err)
	}
}

// TestUATBatchB_F32_AlertPerOrderBypassesDedupe ⑤：CreateAlertPerOrder 不吃 (tenant,kind)
// open 幂等闸，每单必告；CreateAlertEx 同型仍去重（对照组，防改错幂等本体）。
func TestUATBatchB_F32_AlertPerOrderBypassesDedupe(t *testing.T) {
	s := batchBEnv(t)
	for i := 0; i < 2; i++ {
		if err := s.CreateAlert(0, "critical", "pay_manual", "去重组"); err != nil {
			t.Fatalf("CreateAlert 失败: %v", err)
		}
	}
	var deduped int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE kind='pay_manual' AND message='去重组'").Scan(&deduped); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if deduped != 1 {
		t.Fatalf("CreateAlert 幂等去重应保留，got %d", deduped)
	}
	for i := 0; i < 3; i++ {
		if err := s.CreateAlertPerOrder(0, "critical", "pay_manual", "每单必告组"); err != nil {
			t.Fatalf("CreateAlertPerOrder 失败: %v", err)
		}
	}
	var perOrder int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE kind='pay_manual' AND message='每单必告组'").Scan(&perOrder); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if perOrder != 3 {
		t.Fatalf("per-order 告警每单必落，got %d", perOrder)
	}
}

// TestUATBatchB_F12_PriceRulerRealigned ⑥：尺子重锚 33222（3,000 积分=¥299），
// system_config 显式值优先；旧值 29900 可复现历史 ¥269.10 差。
func TestUATBatchB_F12_PriceRulerRealigned(t *testing.T) {
	s := batchBEnv(t)
	if got := s.PriceFenPerMillionTokens(); got != 33222 {
		t.Fatalf("默认尺子应为 33222 分/百万 token，got %d", got)
	}
	// 90 万 token（=3,000 积分 × 300）折应收：恰 ¥299.00
	if fen := s.TokensToFen(900000); fen != 29900 {
		t.Fatalf("3,000 积分应折 ¥299.00（29900 分），got %d", fen)
	}
	// 显式配置优先（模拟未改值的生产库复现旧差）
	if err := s.SetConfig("price_fen_per_million_tokens", "29900"); err != nil {
		t.Fatalf("写配置失败: %v", err)
	}
	if fen := s.TokensToFen(900000); fen != 26910 {
		t.Fatalf("旧尺子 29900 下应复现 26910 分（历史差源），got %d", fen)
	}
}
