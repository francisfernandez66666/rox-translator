// ============ 本文件职责中文说明 ============
// 组织域薄委托层：类型别名与数据访问方法委托给 internal/iam（IAM 子系统拆分后
// 保留本文件维持历史调用方 import 路径稳定）。
// =============================================
package store

import "translator/internal/iam"

// Org 组织实体（类型别名，实际定义在 iam 包）
type Org = iam.Org

const (
	OrgTypeRoot = iam.OrgTypeRoot
	OrgTypeOrg  = iam.OrgTypeOrg
	OrgTypeDept = iam.OrgTypeDept
)

func (s *Store) CreateOrg(tid, parentID int64, name, orgType string) (*Org, error) {
	return s.iam.CreateOrg(tid, parentID, name, orgType)
}

func (s *Store) GetOrgByID(id int64) (*Org, error) {
	return s.iam.GetOrgByID(id)
}

func (s *Store) EnsureRootOrg(tid int64, name string) (*Org, error) {
	return s.iam.EnsureRootOrg(tid, name)
}

func (s *Store) GetRootOrg(tid int64) (*Org, error) {
	return s.iam.GetRootOrg(tid)
}

func (s *Store) EnsurePlatformRootOrg(name string) (*Org, error) {
	return s.iam.EnsurePlatformRootOrg(name)
}

func (s *Store) GetPlatformRootOrg() (*Org, error) {
	return s.iam.GetPlatformRootOrg()
}

func (s *Store) ListPlatformOrgs(platformRootID int64) ([]*Org, error) {
	return s.iam.ListPlatformOrgs(platformRootID)
}

func (s *Store) ListOrgs(tid int64) ([]*Org, error) {
	return s.iam.ListOrgs(tid)
}

func (s *Store) RenameOrg(id int64, name string) error {
	return s.iam.RenameOrg(id, name)
}

func (s *Store) MoveOrg(tid, id, parentID int64) error {
	return s.iam.MoveOrg(tid, id, parentID)
}

func (s *Store) DeleteOrg(id int64) error {
	return s.iam.DeleteOrg(id)
}

func (s *Store) OrgDescendantIDs(tid, orgID int64) ([]int64, error) {
	return s.iam.OrgDescendantIDs(tid, orgID)
}

func (s *Store) IsOrgInSubtree(tid, rootOrgID, targetOrgID int64) (bool, error) {
	return s.iam.IsOrgInSubtree(tid, rootOrgID, targetOrgID)
}