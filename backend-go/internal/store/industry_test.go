// ============ 本文件职责中文说明 ============
// 行业字典管理（2026-09-10 超管可创建/维护行业）store 层单元测试：
//   - ListIndustries：按平台宿主租户0 列出全部行业包（pack_type=industry）
//   - IndustryCodeExists / IndustryReferenced：code 唯一性 / 租户引用校验
//   - UpdateIndustry / ToggleIndustry / DeleteIndustry：编辑名/启停/删除（连带条目清理）
// 复用 newTestStoreWithTenants 内存 SQLite 基建，不依赖业务库。
// ========================================
package store

import "testing"

// TestIndustriesCRUD 行业字典增改启停删全链路。
func TestIndustriesCRUD(t *testing.T) {
	s := newTestStoreWithTenants(t)

	// 创建行业（平台宿主租户0）
	p, err := s.CreateKBPackage(SharedHostTenant, 0, "testind", "测试行业", PackIndustry, PackRoleSource)
	if err != nil {
		t.Fatalf("创建行业失败: %v", err)
	}
	if p.Code != "testind" || p.PackType != PackIndustry || p.TenantID != SharedHostTenant {
		t.Fatalf("行业创建字段异常: %+v", p)
	}

	// code 唯一性
	exists, _ := s.IndustryCodeExists("testind")
	if !exists {
		t.Fatalf("期望 testind 已存在")
	}
	exists2, _ := s.IndustryCodeExists("nonexist")
	if exists2 {
		t.Fatalf("期望 nonexist 不存在")
	}

	// 列表包含新行业
	inds, err := s.ListIndustries()
	if err != nil {
		t.Fatalf("ListIndustries 失败: %v", err)
	}
	found := false
	for _, x := range inds {
		if x.Code == "testind" {
			found = true
		}
	}
	if !found {
		t.Fatalf("列表未包含新行业 testind")
	}

	// 编辑名称
	if err := s.UpdateIndustry(p.ID, "测试行业-改名"); err != nil {
		t.Fatalf("UpdateIndustry 失败: %v", err)
	}
	got, _ := s.GetKBPackage(p.ID, SharedHostTenant)
	if got == nil || got.Name != "测试行业-改名" {
		t.Fatalf("行业名未更新: %+v", got)
	}

	// 停用 / 启用
	if err := s.ToggleIndustry(p.ID, 0); err != nil {
		t.Fatalf("停用行业失败: %v", err)
	}
	if got, _ = s.GetKBPackage(p.ID, SharedHostTenant); got.Enabled != 0 {
		t.Fatalf("行业停用失败: enabled=%d", got.Enabled)
	}
	if err := s.ToggleIndustry(p.ID, 1); err != nil {
		t.Fatalf("启用行业失败: %v", err)
	}
	if got, _ = s.GetKBPackage(p.ID, SharedHostTenant); got.Enabled != 1 {
		t.Fatalf("行业启用失败: enabled=%d", got.Enabled)
	}

	// 删除
	if err := s.DeleteIndustry(p.ID); err != nil {
		t.Fatalf("DeleteIndustry 失败: %v", err)
	}
	if got, _ := s.GetKBPackage(p.ID, SharedHostTenant); got != nil {
		t.Fatalf("行业删除后仍存在: %+v", got)
	}
}

// TestIndustryReferenced 行业被租户引用检测：tenants.industry 指向该 code 时返回 true。
func TestIndustryReferenced(t *testing.T) {
	s := newTestStoreWithTenants(t)
	// 测试库 tenants 表缺少 industry 列（生产由 tenant.Store 启动补列），此处手动补
	if _, err := s.db.Exec(`ALTER TABLE tenants ADD COLUMN industry TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("补 industry 列失败: %v", err)
	}
	// 默认测试租户无 industry；另插一个 auto 租户
	if _, err := s.db.Exec(`INSERT INTO tenants (id, code, name, status, industry) VALUES (99,'tauto','汽车租户','active','auto')`); err != nil {
		t.Fatalf("插入测试租户失败: %v", err)
	}
	used, err := s.IndustryReferenced("auto")
	if err != nil {
		t.Fatalf("IndustryReferenced 失败: %v", err)
	}
	if !used {
		t.Fatalf("期望 auto 被引用")
	}
	used2, _ := s.IndustryReferenced("media")
	if used2 {
		t.Fatalf("期望 media 未被引用")
	}
}
