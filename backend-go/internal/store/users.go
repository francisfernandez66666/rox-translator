package store

import (
	"time"
)

// User 用户实体
type User struct {
	ID           int64  `json:"id"`
	TenantID     int64  `json:"tenant_id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	DisplayName  string `json:"display_name"`
	Role         string `json:"role"`   // user / tenant_admin / super_admin（兼容 approver / admin）
	Status       string `json:"status"` // active / disabled
	CreatedBy    int64  `json:"created_by"`
	LastLoginAt  string `json:"last_login_at"`
	CreatedAt    string `json:"created_at"`
	UpdatedAt    string `json:"updated_at"`
}

// 角色常量（三级：普通用户 < 租户管理员 < 超级管理员）
const (
	RoleUser        = "user"
	RoleTenantAdmin = "tenant_admin"
	RoleSuperAdmin  = "super_admin"
	// 兼容旧数据
	RoleApprover = "approver" // 视为租户管理员
	RoleAdmin    = "admin"    // 视为超级管理员
)

// 用户状态
const (
	UserActive   = "active"
	UserDisabled = "disabled"
)

// CreateUser 创建用户
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

// GetUser 按 id 查询（校验租户）
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

// GetUserByUsername 按用户名查询（同租户内唯一）
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

// GetSuperAdminByUsername 按用户名查询超级管理员（平台级，不挂租户）
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

// GetUserByUsernameGlobal 跨租户按用户名查询所有匹配用户（登录用；多租户重名时需指定租户）
// 注意：保留 PasswordHash 供登录校验使用
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
			continue
		}
		out = append(out, &u)
	}
	return out, nil
}

// ListUsers 列出租户内用户
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
			continue
		}
		u.PasswordHash = ""
		out = append(out, &u)
	}
	return out, nil
}

// UpdateUser 更新用户（名称/角色/状态）
func (s *Store) UpdateUser(id, tid int64, displayName, role, status string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE users SET display_name=?, role=?, status=?, updated_at=? WHERE id=? AND tenant_id=?",
		displayName, role, status, now, id, tid)
	return err
}

// ResetPassword 重置密码
func (s *Store) ResetPassword(id, tid int64, passHash string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE users SET password_hash=?, updated_at=? WHERE id=? AND tenant_id=?",
		passHash, now, id, tid)
	return err
}

// TouchLogin 记录最后登录时间
func (s *Store) TouchLogin(id int64) {
	_, _ = s.db.Exec("UPDATE users SET last_login_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
}

// EnsureAdmin 确保平台存在超级管理员初始账号（默认 admin/admin123，平台级不挂租户）
func (s *Store) EnsureAdmin(tid int64, username, passHash, displayName string) error {
	if su, err := s.GetSuperAdminByUsername(username); err == nil && su != nil {
		return nil
	}
	// 已存在于任意租户（旧库兼容）→ 迁移为平台级
	if u, err := s.GetUserByUsername(tid, username); err == nil && u != nil {
		_, _ = s.db.Exec("UPDATE users SET tenant_id=0, role=? WHERE id=?", RoleAdmin, u.ID)
		return nil
	}
	_, err := s.CreateUser(0, username, passHash, displayName, RoleAdmin, 0)
	return err
}