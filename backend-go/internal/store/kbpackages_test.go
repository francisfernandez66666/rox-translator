// ============ 本文件职责中文说明 ============
// KB 包默认结构单元测试：EnsureDefaultPackages 应幂等创建企业/行业/语言文化/部门四类默认包。
// =============================================
package store

import "testing"

// TestEnsureDefaultPackagesIncludesDepartment 验证 EnsureDefaultPackages 幂等创建企业/行业/语言文化/部门四类默认包。
func TestEnsureDefaultPackagesIncludesDepartment(t *testing.T) {
	s := newTestStore(t)
	// 初始无包，首次调用应创建 4 类默认包
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 失败: %v", err)
	}
	pkgs, err := s.ListKBPackages(1)
	if err != nil {
		t.Fatalf("ListKBPackages 失败: %v", err)
	}
	hasDepartment := false
	for _, p := range pkgs {
		if p.PackType == PackDepartment {
			hasDepartment = true
		}
	}
	if !hasDepartment {
		t.Fatalf("默认包中缺少部门包(department)，实际包类型: %+v", pkgs)
	}
	// 幂等：再次调用不应重复创建
	if err := s.EnsureDefaultPackages(1); err != nil {
		t.Fatalf("EnsureDefaultPackages 幂等失败: %v", err)
	}
	pkgs2, _ := s.ListKBPackages(1)
	if len(pkgs2) != len(pkgs) {
		t.Fatalf("幂等失败：调用前后包数不一致 %d -> %d", len(pkgs), len(pkgs2))
	}
}
