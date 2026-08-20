// ============ 本文件职责中文说明 ============
// 用户体系（users 表）数据访问层：用户 CRUD、登录查询、角色/状态管理。
// 三级角色：普通用户 < 租户管理员 < 超级管理员（平台级，不挂租户）；
// 提供租户隔离查询、密码重置、最后登录记录，以及初始超级管理员保障（EnsureAdmin）。
// =============================================
package store

import (
	"database/sql"
	"time"
)

// User 用户实体
type User struct {
	ID           int64  `json:"id"`            // 用户主键 ID
	TenantID     int64  `json:"tenant_id"`     // 所属租户 ID（0 表示平台级超级管理员）
	Username     string `json:"username"`      // 登录用户名（同租户内唯一）
	PasswordHash string `json:"-"`             // 密码哈希（json 序列化时隐藏，不对外暴露）
	DisplayName  string `json:"display_name"`  // 显示名称
	Role         string `json:"role"`          // 角色：user / tenant_admin / super_admin（兼容 approver / admin）
	Status       string `json:"status"`        // 状态：active / disabled
	CreatedBy    int64  `json:"created_by"`    // 创建者用户 ID（0 表示系统创建）
	LastLoginAt  string `json:"last_login_at"` // 最近登录时间（空表示从未登录）
	OrgID        int64  `json:"org_id"`        // 所属组织 ID（0=未分配/根组织）
	CreatedAt    string `json:"created_at"`    // 创建时间（RFC3339 字符串）
	UpdatedAt    string `json:"updated_at"`    // 更新时间（RFC3339 字符串）
}

// 角色常量（三级：普通用户 < 租户管理员 < 超级管理员）
const (
	RoleUser        = "user"         // 普通用户
	RoleTenantAdmin = "tenant_admin" // 租户管理员
	RoleSuperAdmin  = "super_admin"  // 超级管理员（平台级）
	// 兼容旧数据
	RoleApprover = "approver" // 视为租户管理员
	RoleAdmin    = "admin"    // 视为超级管理员
)

// 用户状态
const (
	UserActive   = "active"   // 启用
	UserDisabled = "disabled" // 停用
)

// userCols 用户表查询列清单（统一使用，避免遗漏新增列）
const userCols = "id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, org_id, created_at, updated_at"

// CreateUser 创建用户（初始状态为 active）。
// 参数：tid=租户 ID，username=用户名，passHash=密码哈希，displayName=显示名，
// role=角色，createdBy=创建者 ID，orgID=所属组织 ID（0=根组织）。
// 返回：新用户对象。
func (s *Store) CreateUser(tid int64, username, passHash, displayName, role string, createdBy, orgID int64) (*User, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO users (tenant_id, username, password_hash, display_name, role, status, created_by, org_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		tid, username, passHash, displayName, role, UserActive, createdBy, orgID, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUser(id, tid)
}

// GetUser 按 id 查询用户（校验租户归属）。
// 参数：id=用户主键 ID，tid=租户 ID；返回用户对象。
func (s *Store) GetUser(id, tid int64) (*User, error) {
	row := s.db.QueryRow(
		"SELECT "+userCols+" FROM users WHERE id=? AND tenant_id=?",
		id, tid)
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByUsername 按用户名查询用户（同租户内唯一）。
// 参数：tid=租户 ID，username=用户名；返回用户对象。
func (s *Store) GetUserByUsername(tid int64, username string) (*User, error) {
	row := s.db.QueryRow(
		"SELECT "+userCols+" FROM users WHERE username=? AND tenant_id=?",
		username, tid)
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetSuperAdminByUsername 按用户名查询超级管理员（平台级，不挂租户）。
// 参数：username=用户名；返回平台级管理员用户对象（role 为 admin 或 super_admin）。
func (s *Store) GetSuperAdminByUsername(username string) (*User, error) {
	row := s.db.QueryRow(
		"SELECT "+userCols+" FROM users WHERE username=? AND role IN ('admin','super_admin') LIMIT 1",
		username)
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByUsernameGlobal 跨租户按用户名查询所有匹配用户（登录用；多租户重名时需指定租户）。
// 参数：username=用户名；返回所有租户下同名用户列表（按租户 ID 排序）。
// 注意：保留 PasswordHash 供登录校验使用。
func (s *Store) GetUserByUsernameGlobal(username string) ([]*User, error) {
	rows, err := s.db.Query(
		"SELECT "+userCols+" FROM users WHERE username=? ORDER BY tenant_id", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &u)
	}
	return out, nil
}

// ListUsers 列出租户内全部用户。
// 参数：tid=租户 ID；返回用户列表（按 ID 排序，PasswordHash 已清空脱敏）。
func (s *Store) ListUsers(tid int64) ([]*User, error) {
	rows, err := s.db.Query(
		"SELECT "+userCols+" FROM users WHERE tenant_id=? ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		u.PasswordHash = "" // 列表接口脱敏：清除密码哈希
		out = append(out, &u)
	}
	return out, nil
}

// UpdateUser 更新用户（显示名/角色/状态/组织）。
// 参数：id=用户主键 ID，tid=租户 ID，displayName=显示名，role=角色，status=状态，orgID=所属组织 ID。
func (s *Store) UpdateUser(id, tid int64, displayName, role, status string, orgID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE users SET display_name=?, role=?, status=?, org_id=?, updated_at=? WHERE id=? AND tenant_id=?",
		displayName, role, status, orgID, now, id, tid)
	return err
}

// SetUserOrg 仅更新用户所属组织（组织树调整时用）。
// 参数：id=用户主键 ID，tid=租户 ID，orgID=目标组织 ID（0=根组织）。
func (s *Store) SetUserOrg(id, tid, orgID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE users SET org_id=?, updated_at=? WHERE id=? AND tenant_id=?",
		orgID, now, id, tid)
	return err
}

// ListUsersByOrg 列出指定组织及其子孙组织内的全部用户（组织级数据归集）。
// 参数：tid=租户 ID，orgIDs=目标组织及子孙组织 ID 集合；空集合表示不限组织（全部用户）。
// 返回：用户列表（按 ID 排序，密码哈希已脱敏）。
func (s *Store) ListUsersByOrg(tid int64, orgIDs []int64) ([]*User, error) {
	var rows *sql.Rows
	var err error
	if len(orgIDs) == 0 {
		rows, err = s.db.Query("SELECT "+userCols+" FROM users WHERE tenant_id=? ORDER BY id", tid)
	} else {
		// 构建 IN 占位符（仅限定给定组织 ID）
		placeholders := ""
		args := []interface{}{tid}
		for i, id := range orgIDs {
			if i > 0 {
				placeholders += ","
			}
			placeholders += "?"
			args = append(args, id)
		}
		rows, err = s.db.Query("SELECT "+userCols+" FROM users WHERE tenant_id=? AND org_id IN ("+placeholders+") ORDER BY id", args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		u.PasswordHash = "" // 列表接口脱敏：清除密码哈希
		out = append(out, &u)
	}
	return out, nil
}

// ResetPassword 重置用户密码（写入新密码哈希）。
// 参数：id=用户主键 ID，tid=租户 ID，passHash=新密码哈希。
func (s *Store) ResetPassword(id, tid int64, passHash string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE users SET password_hash=?, updated_at=? WHERE id=? AND tenant_id=?",
		passHash, now, id, tid)
	return err
}

// TouchLogin 记录最后登录时间（登录成功时调用）。
// 参数：id=用户主键 ID；忽略错误。
func (s *Store) TouchLogin(id int64) {
	_, _ = s.db.Exec("UPDATE users SET last_login_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
}

// EnsureAdmin 确保平台存在超级管理员初始账号（默认 admin/admin123，平台级不挂租户，幂等）。
// 参数：tid=租户 ID，username=管理员用户名，passHash=初始密码哈希，displayName=显示名。
func (s *Store) EnsureAdmin(tid int64, username, passHash, displayName string) error {
	// 已存在平台级管理员则直接返回
	if su, err := s.GetSuperAdminByUsername(username); err == nil && su != nil {
		return nil
	}
	// 已存在于任意租户（旧库兼容）→ 迁移为平台级
	if u, err := s.GetUserByUsername(tid, username); err == nil && u != nil {
		_, _ = s.db.Exec("UPDATE users SET tenant_id=0, role=? WHERE id=?", RoleAdmin, u.ID)
		return nil
	}
	// 不存在则创建平台级管理员（tenant_id=0）
	_, err := s.CreateUser(0, username, passHash, displayName, RoleAdmin, 0, 0)
	return err
}
