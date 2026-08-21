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
		"created_at" TEXT,
		"updated_at" TEXT
	)`); err != nil {
		t.Fatalf("建 tenants 表失败: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO tenants (code, name, status, expires_at, permissions) VALUES ('test','测试租户','active','','{}')`); err != nil {
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
	got, err := s.GetPackageByCode("monthly_100")
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
	// 在默认租户创建行业包
	if _, err := s.CreateKBPackage(1, 0, "automotive", "汽车行业", PackIndustry, PackRoleSource); err != nil {
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
	// 超管确认到账 → 发放句数
	if err := s.MarkOrderPaid(o.ID, 1); err != nil {
		t.Fatalf("MarkOrderPaid 失败: %v", err)
	}
	if bal, _ := s.GetSentenceBalance(1); bal != 500 {
		t.Fatalf("确认后句数应为 500，实际 %d", bal)
	}
	// 确认后不再出现在待确认列表
	list, _ = s.ListManualConfirmOrders()
	if len(list) != 0 {
		t.Fatalf("已确认订单仍出现在待确认列表")
	}
}
