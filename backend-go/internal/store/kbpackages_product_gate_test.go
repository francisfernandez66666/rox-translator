// ============ 本文件职责中文说明 ============
// 一期《确定的多语言能力》批次 2 产品包门禁（§9.1 K1–K3）：
// EnsureProductPacks 幂等、product_id=” 不参与唯一约束、个人租户可见产品包且不可见部门包。
// ★ K3 对齐 kbpackages.go:1185 的收口 SQL（产品包 pack_type=tenant 属放行档）。
// ========================================
package store

import (
	"testing"

	"translator/internal/db"
)

// K1：EnsureProductPacks 幂等——跑两次仍是一 source 一 gate 共两行。
func TestEnsureProductPacksIdempotent(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "产品包产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	src1, gate1, err := s.EnsureProductPacks(1, p.ID)
	if err != nil {
		t.Fatalf("首次建包失败: %v", err)
	}
	if src1 <= 0 || gate1 <= 0 || src1 == gate1 {
		t.Fatalf("必须建出两个不同角色的包, got src=%d gate=%d", src1, gate1)
	}
	src2, gate2, err := s.EnsureProductPacks(1, p.ID)
	if err != nil {
		t.Fatalf("重跑建包失败（幂等被破坏）: %v", err)
	}
	if src2 != src1 || gate2 != gate1 {
		t.Fatalf("重跑不得新建包, got src=%d gate=%d（首次 %d/%d）", src2, gate2, src1, gate1)
	}
	var n int
	if err := db.QueryRow(s.DB(), db.CurrentDialect(),
		"SELECT COUNT(*) FROM kb_packages WHERE product_id=?", p.ID).Scan(&n); err != nil {
		t.Fatalf("查包行数失败: %v", err)
	}
	if n != 2 {
		t.Fatalf("一产品必须恰好两个包, got %d", n)
	}
	ids, err := s.ListProductPackIDs(1)
	if err != nil || !ids[src1] || !ids[gate1] {
		t.Fatalf("ListProductPackIDs 必须含两包, got %v err=%v", ids, err)
	}
}

// K2：product_id=” 的存量包不参与唯一约束（老库升级零影响）。
func TestProductPackEmptyProductIDNotUnique(t *testing.T) {
	s := newProductTestStore(t)
	for i := 0; i < 3; i++ {
		if _, err := s.CreateKBPackage(1, 0, "legacy", "存量包", PackTenant, PackRoleSource); err != nil {
			t.Fatalf("存量空 product_id 包必须可重复建（第 %d 个）: %v", i+1, err)
		}
	}
	// ★ 只数本夹具建的 3 行：New() 出厂会种若干 product_id='' 的默认包，
	//   按全表计数会把「存量包」和「本用例包」混成一个读数（初版在这里假红过）。
	var n int
	if err := db.QueryRow(s.DB(), db.CurrentDialect(),
		"SELECT COUNT(*) FROM kb_packages WHERE product_id='' AND code='legacy'").Scan(&n); err != nil {
		t.Fatalf("查存量包行数失败: %v", err)
	}
	if n != 3 {
		t.Fatalf("空 product_id 包不得被部分唯一索引挡掉, got %d", n)
	}
	// 正向对照：同一产品的同角色包仍被唯一约束管住
	p, err := s.CreateProduct(1, ProductInput{Name: "唯一约束产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	if _, _, err := s.EnsureProductPacks(1, p.ID); err != nil {
		t.Fatalf("首次建包失败: %v", err)
	}
	if _, err := s.CreateKBPackage(1, 0, "dup", "重复包", PackTenant, PackRoleSource); err != nil {
		t.Fatalf("建普通包失败: %v", err)
	}
	// 手工把第二个包指向同一产品＋同角色 ⇒ 必须撞索引
	_, err = db.Exec(s.DB(), db.CurrentDialect(),
		"UPDATE kb_packages SET product_id=? WHERE code='dup'", p.ID)
	if err == nil {
		t.Fatal("同产品同角色的第二个包必须撞 ux_pkg_product（否则 K1 幂等没有数据库兜底）")
	}
}

// K3：个人租户可见产品包、不可见部门包（对齐 :1185 的 pack_type 收口）。
func TestProductPackVisibleToPersonalTenant(t *testing.T) {
	s := newProductTestStore(t)
	p, err := s.CreateProduct(1, ProductInput{Name: "个人租户产品"})
	if err != nil {
		t.Fatalf("建档失败: %v", err)
	}
	src, _, err := s.EnsureProductPacks(1, p.ID)
	if err != nil {
		t.Fatalf("建包失败: %v", err)
	}
	// 部门包（个人租户收口时被排除的那一档）
	if _, err := s.CreateKBPackageForOrg(1, 0, "dept", "部门包", PackDepartment, PackRoleSource, 9); err != nil {
		t.Fatalf("建部门包失败: %v", err)
	}
	briefs, err := s.ListProductPacksFor(1)
	if err != nil {
		t.Fatalf("列产品包失败: %v", err)
	}
	found := false
	for _, b := range briefs {
		if b.ID == src {
			found = true
			if b.PackType != PackTenant {
				t.Fatalf("产品包必须是 pack_type=tenant（个人租户收口放行档）, got %s", b.PackType)
			}
		}
		if b.PackType == PackDepartment || b.PackType == PackCrossDept {
			t.Fatalf("产品包出口不得混入部门/跨部门包, got %s", b.PackType)
		}
	}
	if !found {
		t.Fatal("个人租户必须看得到产品包（正向对照，防上面几条负向恒真空转）")
	}
}
