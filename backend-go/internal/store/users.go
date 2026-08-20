// ============ 本文件职责中文说明 ============
// 用户体系（users 表）数据访问层：用户 CRUD、登录查询、角色/状态管理。
// 三级角色：普通用户 < 租户管理员 < 超级管理员（平台级，不挂租户）；
// 提供租户隔离查询、密码重置、最后登录记录，以及初始超级管理员保障（EnsureAdmin）。
// =============================================
package store

import (
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

// CreateUser 创建用户（初始状态为 active）。
// 参数：tid=租户 ID，username=用户名，passHash=密码哈希，displayName=显示名，
// role=角色，createdBy=创建者 ID。
// 返回：新用户对象。
func (s *Store) CreateUser(tid int64, username, passHash, displayName, role string, createdBy int64) (*User, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO users (tenant_id, username, password_hash, display_name, role, status, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?)",
		tid, username, passHash, displayName, role, UserActive, createdBy, now, now)
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
		"SELECT id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, created_at, updated_at FROM users WHERE id=? AND tenant_id=?",
		id, tid)
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.CreatedBy, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUserByUsername 按用户名查询用户（同租户内唯一）。
// 参数：tid=租户 ID，username=用户名；返回用户对象。
func (s *Store) GetUserByUsername(tid int64, username string) (*User, error) {
	row := s.db.QueryRow(
		"SELECT id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, created_at, updated_at FROM users WHERE username=? AND tenant_id=?",
		username, tid)
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.CreatedBy, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetSuperAdminByUsername 按用户名查询超级管理员（平台级，不挂租户）。
// 参数：username=用户名；返回平台级管理员用户对象（role 为 admin 或 super_admin）。
func (s *Store) GetSuperAdminByUsername(username string) (*User, error) {
	row := s.db.QueryRow(
		"SELECT id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, created_at, updated_at FROM users WHERE username=? AND role IN ('admin','super_admin') LIMIT 1",
		username)
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.CreatedBy, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt)
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
		"SELECT id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, created_at, updated_at FROM users WHERE username=? ORDER BY tenant_id", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
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
		"SELECT id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, created_at, updated_at FROM users WHERE tenant_id=? ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		u.PasswordHash = "" // 列表接口脱敏：清除密码哈希
		out = append(out, &u)
	}
	return out, nil
}

// UpdateUser 更新用户（显示名/角色/状态）。
// 参数：id=用户主键 ID，tid=租户 ID，displayName=显示名，role=角色，status=状态。
func (s *Store) UpdateUser(id, tid int64, displayName, role, status string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE users SET display_name=?, role=?, status=?, updated_at=? WHERE id=? AND tenant_id=?",
		displayName, role, status, now, id, tid)
	return err
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
	_, err := s.CreateUser(0, username, passHash, displayName, RoleAdmin, 0)
	return err
}
