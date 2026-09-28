// ============ 本文件职责中文说明 ============
// 商业包数据层单元测试：包 CRUD、句数余额读写、付费包/增量包发放。
package store

import (
	"database/sql"
	"testing"
	"time"

	"translator/internal/db"

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
	// ★ C26：句数=发放镜像（只增），真实消耗扣 token 台账；镜像不再支持扣减
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

// TestPackageUpgradeExhausted ★ F-80（2026-09-28 〇-Z）：旧包 token 全部耗尽后仍必须能升级。
//
//	线上实测形态（演示站 langcross_demo 租户 1）：basic_m 那笔已支付订单发放 1,200,000 token，
//	台账 quota_grants(kind='plan',source='order',ref_id=该订单) 的 SUM("left") = 0，而订阅有效期
//	要到 10-17 才结束。旧实现在 ComputeUpgradeCredit 的 ratio<=0 这一支直接报
//	「当前套餐已无剩余价值，无法抵扣升级」，handler 包成 409 → 客户点「升级到 专业·年」就是死路：
//	**越是把套餐用满、越想加钱的客户，越被系统挡在门口**。
//	本用例钉住修好的口径：剩余 0 ⇒ 抵扣 0、按全价出升级单、支付后新包即时生效，
//	并且**不多发一分额度**（旧包没有剩余可转移，台账里不该出现 order_carry 转入行）。
//
//	反向对照（防「把校验整段放宽」这种过度修复）：
//	  · 部分消耗仍按比例抵扣 → 见同文件 TestPackageUpgrade 第 ③④⑤ 步，本用例不重复；
//	  · 无生效套餐／同包／非付费目标／目标价不高于当前包 仍须拒绝 → 见 TestPackageUpgradeRejections。
func TestPackageUpgradeExhausted(t *testing.T) {
	s := newTestStoreWithTenants(t)
	if err := s.EnsureBalance(1); err != nil {
		t.Fatalf("EnsureBalance 失败: %v", err)
	}
	oldPkg, _ := s.CreatePackage(&Package{Code: "ex_old", Name: "基础·月", PType: PackagePaid, Sentences: 500, PriceMoney: 99, DurationDays: 30})
	newPkg, _ := s.CreatePackage(&Package{Code: "ex_new", Name: "专业·年", PType: PackagePaid, Sentences: 2000, PriceMoney: 2999, DurationDays: 365})

	// ① 订阅旧包并**把 token 全部用光**（复现演示站读数：台账 left=0 但订阅未过期）
	o1, err := s.CreatePackageOrder(1, oldPkg, 1, "mock")
	if err != nil {
		t.Fatalf("CreatePackageOrder 失败: %v", err)
	}
	if err := s.MarkOrderPaid(o1.ID, 1); err != nil {
		t.Fatalf("MarkOrderPaid 旧包失败: %v", err)
	}
	oldTokens := s.PackageTokenAmount(oldPkg)
	if err := s.DeductWithGrants(1, oldTokens); err != nil {
		t.Fatalf("耗尽旧包额度失败: %v", err)
	}
	if g := s.SumActiveGrants(1); g != 0 {
		t.Fatalf("前置读数：旧包额度应已耗尽（剩余 0），实际剩余 %d", g)
	}
	// 订阅期仍在有效期内（演示站就是这一形态：钱花完了、天还没到）
	if perms, _ := s.GetTenantPerms(1); perms.PackageCode != "ex_old" {
		t.Fatalf("前置读数：当前生效包应为 ex_old，实际 %q", perms.PackageCode)
	}

	// ② 核心判据：额度耗尽**不得**再成为拒绝升级的理由
	credit, err := s.ComputeUpgradeCredit(1, newPkg)
	if err != nil {
		t.Fatalf("F-80 回归：旧包额度耗尽应可按全价升级，实际被拒：%v", err)
	}
	if credit.CreditMoney != 0 {
		t.Fatalf("额度耗尽时抵扣金额应为 0，实际 %.2f", credit.CreditMoney)
	}
	if credit.RemainTokens != 0 {
		t.Fatalf("额度耗尽时剩余 token 应为 0，实际 %d", credit.RemainTokens)
	}
	if credit.OldOrderID != o1.ID {
		t.Fatalf("升级来源订单仍应关联旧订单 %d，实际 %d", o1.ID, credit.OldOrderID)
	}

	// ③ 抵扣 0 ⇒ 应付就是全价（本测试租户 created_at 为空 ⇒ 不触发首月半价，取挂牌价）
	up, err := s.CreateUpgradeOrder(1, newPkg, credit, 1, "mock")
	if err != nil {
		t.Fatalf("CreateUpgradeOrder 失败: %v", err)
	}
	if up.AmountMoney != newPkg.PriceMoney {
		t.Fatalf("全价升级应付应为 %.2f，实际 %.2f", newPkg.PriceMoney, up.AmountMoney)
	}
	if up.CreditMoney != 0 {
		t.Fatalf("升级单记录的抵扣金额应为 0，实际 %.2f", up.CreditMoney)
	}

	// ④ 支付确认后：新包即时生效，且额度只进新包那份（无旧包转入）
	if err := s.MarkOrderPaid(up.ID, 1); err != nil {
		t.Fatalf("MarkOrderPaid 升级失败: %v", err)
	}
	perms2, _ := s.GetTenantPerms(1)
	if perms2.PackageCode != "ex_new" {
		t.Fatalf("升级后包编码应为 ex_new，实际 %q", perms2.PackageCode)
	}
	newTokens := s.PackageTokenAmount(newPkg)
	if g := s.SumActiveGrants(1); g != newTokens {
		t.Fatalf("升级后台账应恰为新包 %d（旧包已耗尽、无可转移），实际 %d", newTokens, g)
	}
	var carryRows int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM quota_grants WHERE tenant_id=1 AND source='order_carry'").Scan(&carryRows); err != nil {
		t.Fatalf("统计转入台账失败: %v", err)
	}
	if carryRows != 0 {
		t.Fatalf("旧包剩余为 0 时不该写出 order_carry 转入行（会凭空多发额度），实际 %d 行", carryRows)
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

// TestFindTermsBySubstring KB 术语子串匹配：源文长句内嵌的 L1 术语应被检索命中，
// 非 L1 条目（L2 翻译记忆）不参与术语匹配，目标语言不匹配的术语不返回。
func TestFindTermsBySubstring(t *testing.T) {
	s := newTestStoreWithTenants(t)
	// 建一个 source 角色的行业包（包类型全员可见）
	pkgID := s.dbInsertPkg(t, 1, "industry")
	// 插入术语：极石→ar→ROX / 极石→ru→ROX（L1 术语）
	s.dbInsertEntry(t, 1, pkgID, 1, "极石", "ar", "ROX")
	s.dbInsertEntry(t, 1, pkgID, 1, "极石", "ru", "ROX")
	// 插入一条 L2 翻译记忆（不应被术语子串匹配返回）
	s.dbInsertEntry(t, 1, pkgID, 2, "山海无界，极石致远", "en", "Boundless mountains and seas, Jishi reaches far.")

	// 源文长句内嵌「极石」→ 应命中 ar/ru 两条 L1 术语
	terms, err := s.FindTermsBySubstring(1, 0, "zh", "山海无界，极石致远。国际标准赋能制造，极石汽车驰骋全球山海")
	if err != nil {
		t.Fatalf("FindTermsBySubstring 失败: %v", err)
	}
	if len(terms) != 2 {
		t.Fatalf("应命中 2 条术语（ar/ru），实得 %d 条: %+v", len(terms), terms)
	}
	langs := map[string]string{}
	for _, tm := range terms {
		langs[tm.TargetLang] = tm.TargetText
	}
	if langs["ar"] != "ROX" || langs["ru"] != "ROX" {
		t.Fatalf("术语译文不符: %+v", langs)
	}

	// 源文不含术语 → 空结果
	empty, err := s.FindTermsBySubstring(1, 0, "zh", "今天天气不错")
	if err != nil || len(empty) != 0 {
		t.Fatalf("无术语源文应返回空: err=%v n=%d", err, len(empty))
	}

	// 只取目标语言 ar → 仅 1 条（workflow 按语言过滤，此处验证返回包含多语言由上层过滤）
	arTerms, err := s.FindTermsBySubstring(1, 0, "zh", "极石汽车")
	if err != nil || len(arTerms) != 2 {
		t.Fatalf("极石汽车应命中 2 条术语: err=%v n=%d", err, len(arTerms))
	}
}

// dbInsertPkg 插入测试知识库包，返回包 ID。
func (s *Store) dbInsertPkg(t *testing.T, tid int64, packType string) int64 {
	t.Helper()
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(), "INSERT INTO kb_packages (tenant_id, code, name, pack_type, role, org_id, created_at, updated_at) VALUES (?,?,?,?,?,0,?,?)",
		tid, "t"+packType+now, "测试包", packType, "source", now, now)
	if err != nil {
		t.Fatalf("插入测试包失败: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// dbInsertEntry 插入测试知识库条目，返回条目 ID。
func (s *Store) dbInsertEntry(t *testing.T, tid, pkgID int64, layer int, src, lang, tgt string) int64 {
	t.Helper()
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(), "INSERT INTO kb_entries (tenant_id, package_id, layer, source_lang, source_text, target_lang, target_text, module, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		tid, pkgID, layer, "zh", src, lang, tgt, "brand", now, now)
	if err != nil {
		t.Fatalf("插入测试条目失败: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// TestPackageOrderPointsPricing ★ S1 积分制定价链路：积分×汇率折算内部 token、
// 注册 30 天内订阅包首月半价、充值包（价格尺子）不打折、老租户挂牌全价。
func TestPackageOrderPointsPricing(t *testing.T) {
	s := newTestStoreWithTenants(t)
	setReg := func(when time.Time) {
		if _, err := db.Exec(s.db, db.CurrentDialect(),
			"UPDATE tenants SET created_at=? WHERE id=1", when.Format(time.RFC3339)); err != nil {
			t.Fatalf("设置注册时间失败: %v", err)
		}
	}
	paid, err := s.CreatePackage(&Package{Code: "basic_m", Name: "基础·月", PType: PackagePaid, Points: 3000, PriceMoney: 99, DurationDays: 30})
	if err != nil {
		t.Fatalf("CreatePackage 失败: %v", err)
	}
	topup, err := s.CreatePackage(&Package{Code: "topup_s", Name: "充值包·小", PType: PackageIncrement, Points: 3000, PriceMoney: 299})
	if err != nil {
		t.Fatalf("CreatePackage 失败: %v", err)
	}
	// 新租户（注册 1 天）：订阅五折 + token=积分×出厂汇率（★ F-78 起 1:400）
	setReg(time.Now().Add(-24 * time.Hour))
	o, err := s.CreatePackageOrder(1, paid, 1, "manual")
	if err != nil {
		t.Fatalf("订阅下单失败: %v", err)
	}
	if o.AmountMoney != 49.5 {
		t.Fatalf("首月半价应为 49.5，实际 %v", o.AmountMoney)
	}
	if o.AmountTokens != 3000*DefaultPointsTokensRate {
		t.Fatalf("积分折算 token 应为 %d（3,000×汇率），实际 %d", 3000*DefaultPointsTokensRate, o.AmountTokens)
	}
	// 充值包：尺子价不打折
	o2, err := s.CreatePackageOrder(1, topup, 1, "manual")
	if err != nil {
		t.Fatalf("充值下单失败: %v", err)
	}
	if o2.AmountMoney != 299 {
		t.Fatalf("充值包应全价 299，实际 %v", o2.AmountMoney)
	}
	// 老租户（注册 31 天）：订阅恢复挂牌价
	setReg(time.Now().Add(-31 * 24 * time.Hour))
	if m := s.PackageOrderPrice(paid, 1); m != 99 {
		t.Fatalf("老租户订阅应为全价 99，实际 %v", m)
	}
}
