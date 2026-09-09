// ============ 本文件职责中文说明 ============
// 商业包数据层单元测试：包 CRUD、句数余额读写、付费包/增量包发放。
package store

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newTestStoreWithTenants 创建基于内存 SQLite 的 Store（含 tenants 表与测试租户，供句数余额读写）。
func newTestStoreWithTenants(t *testing.T) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS tenants (
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
	// 测试租户标记为个人用户（is_personal=1）：邀请裂变付费奖励仅个人用户可得，
	// 缺省 0 会导致 RewardPaidPermanent 的 is_personal 校验静默跳过奖励，使交易类用例误红。
	if _, err := db.Exec(`INSERT INTO tenants (code, name, status, expires_at, permissions, is_personal) VALUES ('test','测试租户','active','','{}',1)`); err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
	s, err := New(db)
	if err != nil {
		t.Fatalf("创建测试 Store 失败: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return s
}

// TestPackageCRUD 商业包创建/查询/更新/删除全链路。
func TestPackageCRUD(t *testing.T) {
	s := newTestStoreWithTenants(t)
	// 创建付费包
	p, err := s.CreatePackage(&Package{Code: "monthly_100", Name: "包月 100 句", PType: PackagePaid, Sentences: 100, PriceMoney: 29, DurationDays: 30, Enabled: 1})
	if err != nil {
		t.Fatalf("CreatePackage 失败: %v", err)
	}
	if p.ID <= 0 || p.Code != "monthly_100" {
		t.Fatalf("CreatePackage 返回异常: %+v", p)
	}
	// 查询
	got, err := s.GetPackageByCode(0, "monthly_100")
	if err != nil || got.Sentences != 100 {
		t.Fatalf("GetPackageByCode 失败: %v", err)
	}
	// 更新启停
	got.Enabled = 0
	if err := s.UpdatePackage(got); err != nil {
		t.Fatalf("UpdatePackage 失败: %v", err)
	}
	// 上架列表不应包含下架包
	enabled, _ := s.ListEnabledCommercialPackages()
	for _, e := range enabled {
		if e.Code == "monthly_100" {
			t.Fatalf("下架包仍在上架列表")
		}
	}
	// 删除
	if err := s.DeletePackage(got.ID); err != nil {
		t.Fatalf("DeletePackage 失败: %v", err)
	}
	if _, err := s.GetPackage(got.ID); err == nil {
		t.Fatalf("删除后仍可查到")
	}
}

// TestSentenceBalance 句数余额增/减/发放链路。
func TestSentenceBalance(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	// 初始 0
	bal, err := s.GetSentenceBalance(1)
	if err != nil || bal != 0 {
		t.Fatalf("初始句数应为 0，实际 %d err=%v", bal, err)
	}
	// 发放付费包句数
	paid, _ := s.CreatePackage(&Package{Code: "paid100", Name: "包月 100 句", PType: PackagePaid, Sentences: 100})
	if _, err := s.GrantPackageSentences(1, paid); err != nil {
		t.Fatalf("GrantPackageSentences 失败: %v", err)
	}
	bal, _ = s.GetSentenceBalance(1)
	if bal != 100 {
		t.Fatalf("发放后应为 100，实际 %d", bal)
	}
	// 增量包追加
	inc, _ := s.CreatePackage(&Package{Code: "inc50", Name: "增量 50 句", PType: PackageIncrement, Sentences: 50})
	if _, err := s.GrantPackageSentences(1, inc); err != nil {
		t.Fatalf("增量包发放失败: %v", err)
	}
	bal, _ = s.GetSentenceBalance(1)
	if bal != 150 {
		t.Fatalf("增量后应为 150，实际 %d", bal)
	}
	// 扣减
	if _, err := s.DeductSentences(1, 40); err != nil {
		t.Fatalf("DeductSentences 失败: %v", err)
	}
	bal, _ = s.GetSentenceBalance(1)
	if bal != 110 {
		t.Fatalf("扣减后应为 110，实际 %d", bal)
	}
	// 超扣应报 ErrSentenceExhausted
	if _, err := s.DeductSentences(1, 500); err != ErrSentenceExhausted {
		t.Fatalf("超扣应返回 ErrSentenceExhausted，实际 %v", err)
	}
}

// TestIndustryPackage 行业包查找与新租户行业包开通。
func TestIndustryPackage(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	// 行业包宿主=平台共享租户0（SharedHostTenant，2026-09-04 权限澄清）；
	// FindIndustryByCode 仅查宿主租户0 的行业包，故须在宿主创建。
	if _, err := s.CreateKBPackage(SharedHostTenant, 0, "automotive", "汽车行业", PackIndustry, PackRoleSource); err != nil {
		t.Fatalf("创建行业包失败: %v", err)
	}
	// 查找行业包
	p, err := s.FindIndustryByCode("automotive")
	if err != nil || p.Name != "汽车行业" {
		t.Fatalf("FindIndustryByCode 失败: %v", err)
	}
	// 新租户开通行业包（幂等）
	if err := s.EnsureIndustryPackage(99, p.Code, p.Name); err != nil {
		t.Fatalf("EnsureIndustryPackage 失败: %v", err)
	}
	if err := s.EnsureIndustryPackage(99, p.Code, p.Name); err != nil {
		t.Fatalf("EnsureIndustryPackage 幂等失败: %v", err)
	}
	// 新租户行业包存在
	pkgs, _ := s.ListKBPackages(99)
	found := false
	for _, k := range pkgs {
		if k.Code == "automotive" && k.PackType == PackIndustry {
			found = true
		}
	}
	if !found {
		t.Fatalf("新租户未创建行业包")
	}
}

// TestPackageOrderManualConfirm 包订阅订单 + 静态码人工确认全链路：
// 创建付费包订单 → 用户点「我已付费」置 manual_confirm → 超管确认到账发放句数。
func TestPackageOrderManualConfirm(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if err := s.EnsureBalance(1); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	// 创建付费包
	paid, err := s.CreatePackage(&Package{Code: "paid500", Name: "包月 500 句", PType: PackagePaid, Sentences: 500, PriceMoney: 99})
	if err != nil {
		t.Fatalf("CreatePackage 失败: %v", err)
	}
	// 创建 manual 渠道订阅订单
	o, err := s.CreatePackageOrder(1, paid, 1, "manual")
	if err != nil {
		t.Fatalf("CreatePackageOrder 失败: %v", err)
	}
	if o.Channel != "manual" || o.PackageID != paid.ID {
		t.Fatalf("订单渠道/包关联异常: %+v", o)
	}
	// 初始句数为 0
	if bal, _ := s.GetSentenceBalance(1); bal != 0 {
		t.Fatalf("初始句数应为 0，实际 %d", bal)
	}
	// 用户点「我已付费」
	if err := s.MarkOrderManualConfirm(o.ID, 1); err != nil {
		t.Fatalf("MarkOrderManualConfirm 失败: %v", err)
	}
	// 待人工确认订单列表应包含该单
	list, _ := s.ListManualConfirmOrders()
	if len(list) != 1 || list[0].ID != o.ID {
		t.Fatalf("待确认订单列表异常: %+v", list)
	}
	// 超管确认到账 → ★ CommitC 分流：付费包（ptype=paid）入 t+30 台账（quota_grants），不发句数
	if err := s.MarkOrderPaid(o.ID, 1); err != nil {
		t.Fatalf("MarkOrderPaid 失败: %v", err)
	}
	// 句数镜像照常记录（订阅身份+展示镜像）；token 走台账通道
	if bal, _ := s.GetSentenceBalance(1); bal != paid.Sentences {
		t.Fatalf("确认后句数镜像应为 %d，实际 %d", paid.Sentences, bal)
	}
	// 台账额度按 token 口径（句数×折算率×可配均摊系数 markup，与扣费侧同单位）
	wantGrants := int64(float64(paid.Sentences*s.TokenSentenceRate()) * s.MarkupMultiplier())
	if g := s.SumActiveGrants(1); g != wantGrants {
		t.Fatalf("确认后台账额度应为 %d，实际 %d", wantGrants, g)
	}
	// 确认后不再出现在待确认列表
	list, _ = s.ListManualConfirmOrders()
	if len(list) != 0 {
		t.Fatalf("已确认订单仍出现在待确认列表")
	}
}

// TestPackageUpgrade 套餐升级全链路（2026-09-09）：
// 旧付费包订阅并消耗部分 token → 升级到更高价付费包：
//
//	① ComputeUpgradeCredit 按旧包剩余台账折算抵扣；
//	② CreateUpgradeOrder 应付 = 新价 − 抵扣；
//	③ MarkOrderPaid 升级分流：旧包剩余台账作废 + 等价转入新台账、新包即时生效。
func TestPackageUpgrade(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if err := s.EnsureBalance(1); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	// 两个付费包：低价 old、高价 new
	oldPkg, _ := s.CreatePackage(&Package{Code: "paid500", Name: "包月 500 句", PType: PackagePaid, Sentences: 500, PriceMoney: 99, DurationDays: 30})
	newPkg, _ := s.CreatePackage(&Package{Code: "paid2000", Name: "包月 2000 句", PType: PackagePaid, Sentences: 2000, PriceMoney: 299, DurationDays: 30})

	// ① 先订阅低价包（mock 渠道自动到账）
	o1, err := s.CreatePackageOrder(1, oldPkg, 1, "mock")
	if err != nil {
		t.Fatalf("CreatePackageOrder 失败: %v", err)
	}
	if err := s.MarkOrderPaid(o1.ID, 1); err != nil {
		t.Fatalf("MarkOrderPaid 旧包失败: %v", err)
	}
	oldTokens := int64(float64(oldPkg.Sentences*s.TokenSentenceRate()) * s.MarkupMultiplier())
	if g := s.SumActiveGrants(1); g != oldTokens {
		t.Fatalf("订阅后台账应为 %d，实际 %d", oldTokens, g)
	}
	perms, _ := s.GetTenantPerms(1)
	if perms.PackageCode != "paid500" {
		t.Fatalf("订阅后包编码应为 paid500，实际 %q", perms.PackageCode)
	}

	// ② 消耗部分旧包 token（模拟用量）
	consumed := oldTokens / 4
	if err := s.DeductWithGrants(1, consumed); err != nil {
		t.Fatalf("DeductWithGrants 失败: %v", err)
	}
	remain := s.SumActiveGrants(1)
	if remain != oldTokens-consumed {
		t.Fatalf("消耗后剩余台账应为 %d，实际 %d", oldTokens-consumed, remain)
	}

	// ③ 计算升级抵扣：应退 = 旧包实付 × 剩余率
	credit, err := s.ComputeUpgradeCredit(1, newPkg)
	if err != nil {
		t.Fatalf("ComputeUpgradeCredit 失败: %v", err)
	}
	wantCredit := float64(int(oldPkg.PriceMoney*float64(remain)/float64(oldTokens)*100+0.5)) / 100.0
	if credit.CreditMoney != wantCredit {
		t.Fatalf("抵扣金额应为 %.2f，实际 %.2f", wantCredit, credit.CreditMoney)
	}
	if credit.OldOrderID != o1.ID {
		t.Fatalf("升级来源订单应为 %d，实际 %d", o1.ID, credit.OldOrderID)
	}

	// ④ 创建升级订单：应付 = 新价 − 抵扣
	up, err := s.CreateUpgradeOrder(1, newPkg, credit, 1, "mock")
	if err != nil {
		t.Fatalf("CreateUpgradeOrder 失败: %v", err)
	}
	if up.AmountMoney != newPkg.PriceMoney-credit.CreditMoney {
		t.Fatalf("升级订单应付应为 %.2f，实际 %.2f", newPkg.PriceMoney-credit.CreditMoney, up.AmountMoney)
	}
	if up.UpgradeFromOrder != o1.ID {
		t.Fatalf("升级订单来源应关联旧订单 %d，实际 %d", o1.ID, up.UpgradeFromOrder)
	}

	// ⑤ 支付升级订单：旧包剩余台账作废 + 等价转入新台账
	if err := s.MarkOrderPaid(up.ID, 1); err != nil {
		t.Fatalf("MarkOrderPaid 升级失败: %v", err)
	}
	// 旧台账作废（ref_id=旧订单 的 plan 台账 left 归零）
	var zeroed int64
	s.db.QueryRow("SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=1 AND source='order' AND ref_id=? AND kind='plan'", o1.ID).Scan(&zeroed)
	if zeroed != 0 {
		t.Fatalf("旧包台账应作废为 0，实际 %d", zeroed)
	}
	// 新台账 = 新包 token + 旧包剩余（等价转入）
	newTokens := int64(float64(newPkg.Sentences*s.TokenSentenceRate()) * s.MarkupMultiplier())
	if g := s.SumActiveGrants(1); g != newTokens+remain {
		t.Fatalf("升级后总台账应为新包 %d + 旧剩 %d = %d，实际 %d", newTokens, remain, newTokens+remain, g)
	}
	// 新包即时生效：PackageCode 换新
	perms2, _ := s.GetTenantPerms(1)
	if perms2.PackageCode != "paid2000" {
		t.Fatalf("升级后包编码应为 paid2000，实际 %q", perms2.PackageCode)
	}
}

// TestPackageUpgradeRejections 升级边界：无订阅/相同包/非付费目标应拒绝。
func TestPackageUpgradeRejections(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if err := s.EnsureBalance(1); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	oldPkg, _ := s.CreatePackage(&Package{Code: "paid500", Name: "包月 500 句", PType: PackagePaid, Sentences: 500, PriceMoney: 99, DurationDays: 30})
	newPkg, _ := s.CreatePackage(&Package{Code: "paid2000", Name: "包月 2000 句", PType: PackagePaid, Sentences: 2000, PriceMoney: 299, DurationDays: 30})
	inc, _ := s.CreatePackage(&Package{Code: "inc500", Name: "增量 500 句", PType: PackageIncrement, Sentences: 500, PriceMoney: 50})

	// 未订阅 → 拒绝
	if _, err := s.ComputeUpgradeCredit(1, newPkg); err == nil {
		t.Fatal("无订阅应拒绝升级")
	}
	// 先订阅低价包
	o1, _ := s.CreatePackageOrder(1, oldPkg, 1, "mock")
	if err := s.MarkOrderPaid(o1.ID, 1); err != nil {
		t.Fatalf("MarkOrderPaid 失败: %v", err)
	}
	// 同包 → 拒绝
	if _, err := s.ComputeUpgradeCredit(1, oldPkg); err == nil {
		t.Fatal("同包不应允许升级")
	}
	// 目标为增量包 → 拒绝
	if _, err := s.ComputeUpgradeCredit(1, inc); err == nil {
		t.Fatal("非付费目标应拒绝升级")
	}
}
