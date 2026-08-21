package store

import "translator/internal/iam"

// User 用户实体（类型别名，实际定义在 iam 包）
type User = iam.User

// 角色常量
const (
	RoleUser        = iam.RoleUser
	RoleDeptAdmin   = iam.RoleDeptAdmin
	RoleTenantAdmin = iam.RoleTenantAdmin
	RoleSuperAdmin  = iam.RoleSuperAdmin
	RoleApprover    = iam.RoleApprover
	RoleAdmin       = iam.RoleAdmin
)

const (
	UserActive   = iam.UserActive
	UserDisabled = iam.UserDisabled
)

// IAM 返回 iam.Store 实例（用户/组织数据访问层）
func (s *Store) IAM() *iam.Store { return s.iam }

// CreateUser 委托 iam.Store
func (s *Store) CreateUser(tid int64, username, passHash, displayName, role string, createdBy, orgID int64) (*User, error) {
	return s.iam.CreateUser(tid, username, passHash, displayName, role, createdBy, orgID)
}

// GetUser 委托 iam.Store
func (s *Store) GetUser(id, tid int64) (*User, error) {
	return s.iam.GetUser(id, tid)
}

// GetUserByUsername 委托 iam.Store
func (s *Store) GetUserByUsername(tid int64, username string) (*User, error) {
	return s.iam.GetUserByUsername(tid, username)
}

// GetUserByEmail 委托 iam.Store
func (s *Store) GetUserByEmail(email string) (*User, error) {
	return s.iam.GetUserByEmail(email)
}

// SetUserEmail 委托 iam.Store
func (s *Store) SetUserEmail(id, tid int64, email string) error {
	return s.iam.SetUserEmail(id, tid, email)
}

// GetSuperAdminByUsername 委托 iam.Store
func (s *Store) GetSuperAdminByUsername(username string) (*User, error) {
	return s.iam.GetSuperAdminByUsername(username)
}

// GetUserByUsernameGlobal 委托 iam.Store
func (s *Store) GetUserByUsernameGlobal(username string) ([]*User, error) {
	return s.iam.GetUserByUsernameGlobal(username)
}

// ListUsers 委托 iam.Store
func (s *Store) ListUsers(tid int64) ([]*User, error) {
	return s.iam.ListUsers(tid)
}

// UpdateUser 委托 iam.Store
func (s *Store) UpdateUser(id, tid int64, displayName, role, status string, orgID int64) error {
	return s.iam.UpdateUser(id, tid, displayName, role, status, orgID)
}

// SetUserOrg 委托 iam.Store
func (s *Store) SetUserOrg(id, tid, orgID int64) error {
	return s.iam.SetUserOrg(id, tid, orgID)
}

// ListUsersByOrg 委托 iam.Store
func (s *Store) ListUsersByOrg(tid int64, orgIDs []int64) ([]*User, error) {
	return s.iam.ListUsersByOrg(tid, orgIDs)
}

// ResetPassword 委托 iam.Store
func (s *Store) ResetPassword(id, tid int64, passHash string) error {
	return s.iam.ResetPassword(id, tid, passHash)
}

// TouchLogin 委托 iam.Store
func (s *Store) TouchLogin(id int64) {
	s.iam.TouchLogin(id)
}

// EnsureAdmin 委托 iam.Store
func (s *Store) EnsureAdmin(tid int64, username, passHash, displayName string) error {
	return s.iam.EnsureAdmin(tid, username, passHash, displayName)
}
// ListAllUsers 列出全部租户（含平台 0）的所有用户（超管平台根视图用）。委托 iam.Store。
func (s *Store) ListAllUsers() ([]*User, error) {
	return s.iam.ListAllUsers()
}

// OrgNameMap 全部组织 ID→名称映射。委托 iam.Store。
func (s *Store) OrgNameMap() (map[int64]string, error) {
	return s.iam.OrgNameMap()
}

// DeleteUser 删除用户账号。委托 iam.Store。
func (s *Store) DeleteUser(id, tid int64) error {
	return s.iam.DeleteUser(id, tid)
}
