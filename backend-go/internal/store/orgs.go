// ============ 本文件职责中文说明 ============
// 组织域薄委托层：类型别名与数据访问方法委托给 internal/iam（IAM 子系统拆分后
// 保留本文件维持历史调用方 import 路径稳定）。
// =============================================
package store

import "translator/internal/iam"

// Org 组织实体（类型别名，实际定义在 iam 包）
type Org = iam.Org

// 组织类型常量别名（透传 iam 包：root=平台根 / org=组织 / dept=部门）。
const (
	OrgTypeRoot = iam.OrgTypeRoot
	OrgTypeOrg  = iam.OrgTypeOrg
	OrgTypeDept = iam.OrgTypeDept
)

// CreateOrg 创建新记录/资源。
func (s *Store) CreateOrg(tid, parentID int64, name, orgType string) (*Org, error) {
	return s.iam.CreateOrg(tid, parentID, name, orgType)
}

// GetOrgByID 查询并返回单条记录/资源。
func (s *Store) GetOrgByID(id int64) (*Org, error) {
	return s.iam.GetOrgByID(id)
}

// EnsureRootOrg 确保目标存在，不存在则创建（幂等）。
func (s *Store) EnsureRootOrg(tid int64, name string) (*Org, error) {
	return s.iam.EnsureRootOrg(tid, name)
}

// GetRootOrg 查询并返回单条记录/资源。
func (s *Store) GetRootOrg(tid int64) (*Org, error) {
	return s.iam.GetRootOrg(tid)
}

// EnsurePlatformRootOrg 确保目标存在，不存在则创建（幂等）。
func (s *Store) EnsurePlatformRootOrg(name string) (*Org, error) {
	return s.iam.EnsurePlatformRootOrg(name)
}

// GetPlatformRootOrg 查询并返回单条记录/资源。
func (s *Store) GetPlatformRootOrg() (*Org, error) {
	return s.iam.GetPlatformRootOrg()
}

// ListPlatformOrgs 按条件查询列表。
func (s *Store) ListPlatformOrgs(platformRootID int64) ([]*Org, error) {
	return s.iam.ListPlatformOrgs(platformRootID)
}

// ListOrgs 按条件查询列表。
func (s *Store) ListOrgs(tid int64) ([]*Org, error) {
	return s.iam.ListOrgs(tid)
}

// RenameOrg 组织重命名（跨租户按 ID 直改，调用方需先做租户归属校验）。
func (s *Store) RenameOrg(id int64, name string) error {
	return s.iam.RenameOrg(id, name)
}

// MoveOrg 移动组织节点到新父级（tid 用于父级合法性校验，防跨租户挂载）。
func (s *Store) MoveOrg(tid, id, parentID int64) error {
	return s.iam.MoveOrg(tid, id, parentID)
}

// DeleteOrg 删除指定记录（含关联清理）。
func (s *Store) DeleteOrg(id int64) error {
	return s.iam.DeleteOrg(id)
}

// OrgDescendantIDs 取组织子树全部后代 ID（含自身；部门预算聚合/数据范围过滤用）。
func (s *Store) OrgDescendantIDs(tid, orgID int64) ([]int64, error) {
	return s.iam.OrgDescendantIDs(tid, orgID)
}

// OrgAncestorIDs 取组织祖先链（自底向上 [自身,...,根]；KB 部门包继承链用，2026-08-26）。
func (s *Store) OrgAncestorIDs(tid, orgID int64) ([]int64, error) {
	return s.iam.OrgAncestorIDs(tid, orgID)
}

// IsOrgInSubtree 判断 targetOrgID 是否位于 rootOrgID 子树内（层级越权防护）。
func (s *Store) IsOrgInSubtree(tid, rootOrgID, targetOrgID int64) (bool, error) {
	return s.iam.IsOrgInSubtree(tid, rootOrgID, targetOrgID)
}
