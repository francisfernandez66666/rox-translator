package iam

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"
)

const userCols = "id, tenant_id, username, password_hash, display_name, role, status, created_by, last_login_at, org_id, COALESCE(email,''), created_at, updated_at"
const orgCols = "id, tenant_id, parent_id, name, type, created_at, updated_at"

// Store IAM 数据访问层（用户 + 组织）
type Store struct {
	db *sql.DB
	mu sync.Mutex // 写操作互斥：SQLite 单写者模型下降低并发 SQLITE_BUSY 概率
}

// write 串行化执行写操作（事务/Exec 统一入口）。
func (s *Store) write(fn func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fn()
}

// execW 互斥执行单条写 SQL。
func (s *Store) execW(query string, args ...interface{}) (sql.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Exec(query, args...)
}

func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func scanUser(row *sql.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
		&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.Email, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// ============ 用户 ============

func (s *Store) CreateUser(tid int64, username, passHash, displayName, role string, createdBy, orgID int64) (*User, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.execW(
		"INSERT INTO users (tenant_id, username, password_hash, display_name, role, status, created_by, org_id, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		tid, username, passHash, displayName, role, UserActive, createdBy, orgID, now, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetUser(id, tid)
}

func (s *Store) GetUser(id, tid int64) (*User, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE id=? AND tenant_id=?", id, tid))
}

func (s *Store) GetUserByUsername(tid int64, username string) (*User, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE username=? AND tenant_id=?", username, tid))
}

func (s *Store) GetUserByEmail(email string) (*User, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE lower(email)=? ORDER BY tenant_id LIMIT 1", strings.ToLower(strings.TrimSpace(email))))
}

func (s *Store) SetUserEmail(id, tid int64, email string) error {
	_, err := s.execW("UPDATE users SET email=? WHERE id=? AND tenant_id=?", email, id, tid)
	return err
}

func (s *Store) GetSuperAdminByUsername(username string) (*User, error) {
	return scanUser(s.db.QueryRow("SELECT "+userCols+" FROM users WHERE username=? AND role IN ('admin','super_admin') LIMIT 1", username))
}

func (s *Store) GetUserByUsernameGlobal(username string) ([]*User, error) {
	rows, err := s.db.Query("SELECT "+userCols+" FROM users WHERE username=? ORDER BY tenant_id", username)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.Email, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &u)
	}
	return out, nil
}

func (s *Store) ListUsers(tid int64) ([]*User, error) {
	rows, err := s.db.Query("SELECT "+userCols+" FROM users WHERE tenant_id=? ORDER BY id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.Email, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue
		}
		u.PasswordHash = ""
		out = append(out, &u)
	}
	return out, nil
}

func (s *Store) UpdateUser(id, tid int64, displayName, role, status string, orgID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.execW(
		"UPDATE users SET display_name=?, role=?, status=?, org_id=?, updated_at=? WHERE id=? AND tenant_id=?",
		displayName, role, status, orgID, now, id, tid)
	return err
}

func (s *Store) SetUserOrg(id, tid, orgID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.execW("UPDATE users SET org_id=?, updated_at=? WHERE id=? AND tenant_id=?", orgID, now, id, tid)
	return err
}

func (s *Store) ListUsersByOrg(tid int64, orgIDs []int64) ([]*User, error) {
	var rows *sql.Rows
	var err error
	if len(orgIDs) == 0 {
		rows, err = s.db.Query("SELECT "+userCols+" FROM users WHERE tenant_id=? ORDER BY id", tid)
	} else {
		phs := ""
		args := []interface{}{tid}
		for i, id := range orgIDs {
			if i > 0 {
				phs += ","
			}
			phs += "?"
			args = append(args, id)
		}
		rows, err = s.db.Query("SELECT "+userCols+" FROM users WHERE tenant_id=? AND org_id IN ("+phs+") ORDER BY id", args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.Email, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue
		}
		u.PasswordHash = ""
		out = append(out, &u)
	}
	return out, nil
}

func (s *Store) ResetPassword(id, tid int64, passHash string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.execW("UPDATE users SET password_hash=?, updated_at=? WHERE id=? AND tenant_id=?", passHash, now, id, tid)
	return err
}

func (s *Store) TouchLogin(id int64) {
	_, _ = s.execW("UPDATE users SET last_login_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
}

func (s *Store) EnsureAdmin(tid int64, username, passHash, displayName string) error {
	if su, err := s.GetSuperAdminByUsername(username); err == nil && su != nil {
		return nil
	}
	if u, err := s.GetUserByUsername(tid, username); err == nil && u != nil {
		_, _ = s.execW("UPDATE users SET tenant_id=0, role=? WHERE id=?", RoleAdmin, u.ID)
		return nil
	}
	_, err := s.CreateUser(0, username, passHash, displayName, RoleAdmin, 0, 0)
	return err
}

// ============ 组织 ============

func (s *Store) CreateOrg(tid, parentID int64, name, orgType string) (*Org, error) {
	if orgType == "" {
		orgType = OrgTypeOrg
	}
	now := time.Now().Format(time.RFC3339)
	res, err := s.execW("INSERT INTO orgs (tenant_id, parent_id, name, type, created_at, updated_at) VALUES (?,?,?,?,?,?)", tid, parentID, name, orgType, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetOrgByID(id)
}

func (s *Store) GetOrgByID(id int64) (*Org, error) {
	row := s.db.QueryRow("SELECT "+orgCols+" FROM orgs WHERE id=?", id)
	var o Org
	if err := row.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) EnsureRootOrg(tid int64, name string) (*Org, error) {
	root, err := s.GetRootOrg(tid)
	if err == nil {
		return root, nil
	}
	if name == "" {
		name = "组织"
	}
	return s.CreateOrg(tid, 0, name, OrgTypeRoot)
}

func (s *Store) GetRootOrg(tid int64) (*Org, error) {
	row := s.db.QueryRow("SELECT "+orgCols+" FROM orgs WHERE tenant_id=? AND type='root' ORDER BY id LIMIT 1", tid)
	var o Org
	if err := row.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

func (s *Store) EnsurePlatformRootOrg(name string) (*Org, error) {
	root, err := s.GetRootOrg(0)
	if err == nil {
		return root, nil
	}
	if name == "" {
		name = "平台"
	}
	return s.CreateOrg(0, 0, name, OrgTypeRoot)
}

func (s *Store) GetPlatformRootOrg() (*Org, error) {
	return s.GetRootOrg(0)
}

func (s *Store) ListPlatformOrgs(platformRootID int64) ([]*Org, error) {
	// INNER JOIN tenants：已删除租户的孤儿组织不再出现在平台树
	rows, err := s.db.Query("SELECT o.id, o.tenant_id, o.parent_id, o.name, o.type, o.created_at, o.updated_at FROM orgs o INNER JOIN tenants t ON o.tenant_id=t.id WHERE o.tenant_id>0 ORDER BY o.tenant_id, CASE o.type WHEN 'root' THEN 0 ELSE 1 END, o.parent_id, o.id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Org
	tenantRoot := map[int64]int64{} // tenant_id → 该租户根组织 ID
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt, &o.UpdatedAt); err != nil {
			continue
		}
		if o.Type == OrgTypeRoot {
			tenantRoot[o.TenantID] = o.ID
			o.ParentID = platformRootID // 租户根视图上挂在平台根下
		}
		out = append(out, &o)
	}
	// 关键：租户内直属根的组织（parent_id=0）重挂到本租户根组织下，否则平台树断链
	for _, o := range out {
		if o.Type != OrgTypeRoot && o.ParentID == 0 {
			if rid, ok := tenantRoot[o.TenantID]; ok {
				o.ParentID = rid
			}
		}
	}
	return out, nil
}

func (s *Store) ListOrgs(tid int64) ([]*Org, error) {
	rows, err := s.db.Query("SELECT "+orgCols+" FROM orgs WHERE tenant_id=? ORDER BY CASE type WHEN 'root' THEN 0 ELSE 1 END, parent_id, id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Org
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt, &o.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &o)
	}
	return out, nil
}

func (s *Store) RenameOrg(id int64, name string) error {
	_, err := s.execW("UPDATE orgs SET name=?, updated_at=? WHERE id=?", name, time.Now().Format(time.RFC3339), id)
	return err
}

func (s *Store) MoveOrg(tid, id, parentID int64) error {
	org, err := s.GetOrgByID(id)
	if err != nil {
		return err
	}
	if org.TenantID != tid {
		return fmt.Errorf("组织不属于当前租户")
	}
	if org.Type == OrgTypeRoot {
		return fmt.Errorf("根组织不可移动")
	}
	if parentID != 0 {
		p, err := s.GetOrgByID(parentID)
		if err != nil {
			return fmt.Errorf("目标组织不存在")
		}
		if p.TenantID != tid {
			return fmt.Errorf("目标组织不属于当前租户")
		}
		desc, err := s.OrgDescendantIDs(tid, id)
		if err != nil {
			return err
		}
		for _, d := range desc {
			if d == parentID {
				return fmt.Errorf("不能把组织移动到其自身或其子组织下")
			}
		}
	}
	_, err = s.execW("UPDATE orgs SET parent_id=?, updated_at=? WHERE id=?", parentID, time.Now().Format(time.RFC3339), id)
	return err
}

func (s *Store) DeleteOrg(id int64) error {
	org, err := s.GetOrgByID(id)
	if err != nil {
		return err
	}
	if org.Type == OrgTypeRoot {
		return fmt.Errorf("根组织不可删除")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("UPDATE users SET org_id=0 WHERE org_id=?", id); err != nil {
		return err
	}
	// 部门删除保护：被回收的部门管理员失去管理范围，自动降级为普通用户（防"幽灵管理员"）
	if _, err := tx.Exec("UPDATE users SET role=?, updated_at=? WHERE org_id=0 AND role=? AND tenant_id=?",
		RoleUser, time.Now().Format(time.RFC3339), RoleDeptAdmin, org.TenantID); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE orgs SET parent_id=?, updated_at=? WHERE parent_id=?", org.ParentID, time.Now().Format(time.RFC3339), id); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM orgs WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) OrgDescendantIDs(tid, orgID int64) ([]int64, error) {
	all, err := s.ListOrgs(tid)
	if err != nil {
		return nil, err
	}
	children := map[int64][]int64{}
	for _, o := range all {
		children[o.ParentID] = append(children[o.ParentID], o.ID)
	}
	result := []int64{orgID}
	queue := []int64{orgID}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, c := range children[cur] {
			result = append(result, c)
			queue = append(queue, c)
		}
	}
	return result, nil
}

func (s *Store) IsOrgInSubtree(tid, rootOrgID, targetOrgID int64) (bool, error) {
	desc, err := s.OrgDescendantIDs(tid, rootOrgID)
	if err != nil {
		return false, err
	}
	for _, id := range desc {
		if id == targetOrgID {
			return true, nil
		}
	}
	return false, nil
}
// ListAllUsers 列出全部租户（含平台 0）的所有用户（超管平台根视图用）。
// 返回：用户列表（按 tenant_id,id 排序，密码哈希脱敏）。
func (s *Store) ListAllUsers() ([]*User, error) {
	rows, err := s.db.Query("SELECT " + userCols + " FROM users ORDER BY tenant_id, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.TenantID, &u.Username, &u.PasswordHash, &u.DisplayName, &u.Role, &u.Status,
			&u.CreatedBy, &u.LastLoginAt, &u.OrgID, &u.Email, &u.CreatedAt, &u.UpdatedAt); err != nil {
			continue
		}
		u.PasswordHash = ""
		out = append(out, &u)
	}
	return out, nil
}

// OrgNameMap 全部组织 ID→名称映射（跨租户展示所属组织用）。
func (s *Store) OrgNameMap() (map[int64]string, error) {
	rows, err := s.db.Query("SELECT id, name FROM orgs")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[int64]string{}
	for rows.Next() {
		var id int64
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			continue
		}
		m[id] = name
	}
	return m, nil
}

// DeleteUser 删除用户账号。
func (s *Store) DeleteUser(id, tid int64) error {
	res, err := s.execW("DELETE FROM users WHERE id=? AND tenant_id=?", id, tid)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("用户不存在")
	}
	return nil
}
