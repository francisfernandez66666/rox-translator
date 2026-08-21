// ============ 本文件职责中文说明 ============
// 组织层级数据访问单元测试：根组织行创建/幂等、组织/部门类型、根组织删除保护、列表根优先。
// =============================================
package store

import "testing"

func TestOrgHierarchy(t *testing.T) {
	s := newTestStore(t)

	// 根组织创建与幂等
	root, err := s.EnsureRootOrg(1, "测试租户")
	if err != nil {
		t.Fatalf("EnsureRootOrg 失败: %v", err)
	}
	if root.Type != OrgTypeRoot || root.ParentID != 0 {
		t.Fatalf("根组织类型/父级错误: type=%s parent=%d", root.Type, root.ParentID)
	}
	root2, err := s.EnsureRootOrg(1, "测试租户")
	if err != nil || root2.ID != root.ID {
		t.Fatalf("EnsureRootOrg 非幂等: %v (id %d -> %d)", err, root.ID, root2.ID)
	}

	// 组织（根组织下）与部门（组织下）
	org, err := s.CreateOrg(1, 0, "研发部", OrgTypeOrg)
	if err != nil {
		t.Fatalf("创建组织失败: %v", err)
	}
	if org.Type != OrgTypeOrg {
		t.Fatalf("组织类型错误: %s", org.Type)
	}
	dept, err := s.CreateOrg(1, org.ID, "前端组", OrgTypeDept)
	if err != nil {
		t.Fatalf("创建部门失败: %v", err)
	}
	if dept.Type != OrgTypeDept || dept.ParentID != org.ID {
		t.Fatalf("部门类型/父级错误: type=%s parent=%d", dept.Type, dept.ParentID)
	}

	// 列表：根组织排最前
	all, err := s.ListOrgs(1)
	if err != nil {
		t.Fatalf("ListOrgs 失败: %v", err)
	}
	if len(all) < 3 || all[0].Type != OrgTypeRoot {
		t.Fatalf("列表未以根组织开头: %+v", all)
	}

	// 根组织不可删除
	if err := s.DeleteOrg(root.ID); err == nil {
		t.Fatalf("根组织不应可删除")
	}
	// 组织/部门可删除
	if err := s.DeleteOrg(dept.ID); err != nil {
		t.Fatalf("删除部门失败: %v", err)
	}
	if err := s.DeleteOrg(org.ID); err != nil {
		t.Fatalf("删除组织失败: %v", err)
	}
}

func TestOrgMove(t *testing.T) {
	s := newTestStore(t)
	root, _ := s.EnsureRootOrg(1, "测试租户")
	orgA, _ := s.CreateOrg(1, 0, "组织A", OrgTypeOrg)
	orgB, _ := s.CreateOrg(1, 0, "组织B", OrgTypeOrg)
	dept, _ := s.CreateOrg(1, orgA.ID, "部门A", OrgTypeDept)

	// 把组织A移动到组织B下（组织嵌套组织）
	if err := s.MoveOrg(1, orgA.ID, orgB.ID); err != nil {
		t.Fatalf("移动组织A到B下失败: %v", err)
	}
	got, _ := s.GetOrgByID(orgA.ID)
	if got.ParentID != orgB.ID {
		t.Fatalf("移动后 parent 错误: %d != %d", got.ParentID, orgB.ID)
	}

	// 根组织不可移动
	if err := s.MoveOrg(1, root.ID, orgB.ID); err == nil {
		t.Fatalf("根组织不应可移动")
	}

	// 成环校验：不能把组织B移动到它的子孙(组织A)下
	if err := s.MoveOrg(1, orgB.ID, dept.ID); err == nil {
		t.Fatalf("移动到自身子孙下应报错(成环)")
	}

	// 移到根组织下（parent_id=0）
	if err := s.MoveOrg(1, dept.ID, 0); err != nil {
		t.Fatalf("移动部门到根下失败: %v", err)
	}
}
