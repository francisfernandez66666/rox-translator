// ============ 本文件职责中文说明 ============
// 用户域薄委托层：类型别名与数据访问方法委托给 internal/iam（IAM 子系统拆分后
// 保留本文件维持历史调用方 import 路径稳定）。
// =============================================
package store

import (
	"translator/internal/db"
	"translator/internal/iam"
)

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

// 用户状态常量别名（透传 iam 包：active=启用 / disabled=停用）。
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

// UserDeactivating 注销宽限期状态（当日仍可用，次日等效停用）。
const UserDeactivating = iam.UserDeactivating

// DeactivateSelf 自助注销（2026-08-26 需求）：
// 仅普通用户（role=user）且当前为 active 可发起；置 deactivating 并记录请求日期。
// 宽限语义：当日仍可登录使用，次日起按 disabled 处理；后台数据保留不删除。
// 返回错误：非普通用户 / 状态不允许。
func (s *Store) DeactivateSelf(id, tid int64) error {
	return s.iam.DeactivateSelf(id, tid)
}

// FinalizeDeactivation 宽限期届满落停用态（幂等，登录路径惰性调用，best-effort）。
func (s *Store) FinalizeDeactivation(id, tid int64) {
	s.iam.FinalizeDeactivation(id, tid)
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

// SetUserAgreed 委托 iam.Store：记录用户协议+隐私协议签署时间。
func (s *Store) SetUserAgreed(id, tid int64, at string) error {
	return s.iam.SetUserAgreed(id, tid, at)
}

// EnsureAdmin 委托 iam.Store
func (s *Store) EnsureAdmin(tid int64, username, passHash, displayName, email string) error {
	return s.iam.EnsureAdmin(tid, username, passHash, displayName, email)
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

// ListUsersByRole 按租户+角色列出活跃用户（通知投递用；tid=0 表示平台层超管）。
//
// ★ 修复（2026-08-26 全仓评审 C1）：SELECT 补第 14 列 COALESCE(deactivate_at,”)——
//
//	此前 SELECT 13 列却 Scan 14 目标，每行 Scan 必败被 continue 吞掉，
//	函数恒返回空列表 → 超管通知链路（反馈回复/告警触达）整体静默失效。
func (s *Store) ListUsersByRole(tid int64, role string) []*User {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, tenant_id, username, password_hash, display_name, role, status, created_by, COALESCE(last_login_at,''), COALESCE(org_id,0), COALESCE(email,''), created_at, updated_at, COALESCE(deactivate_at,'') FROM users WHERE tenant_id=? AND role=? AND status='active'",
		tid, role)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName,
			&u.Role, &u.Status, &u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.Email, &u.CreatedAt, &u.UpdatedAt, &u.DeactivatedAt); err != nil {
			continue
		}
		out = append(out, &u)
	}
	return out
}
