// ============ 本文件职责中文说明 ============
// 组织层级（管理结构展示层）：orgs 表数据访问。
// 结构：根组织 = 租户本身（type='root'，parent_id=0，每个租户一行，名称可自定义），
// 其下可挂组织（type='org'）与部门（type='dept'），parent_id 指向父节点。
// 用途：管理后台按「根组织 → 组织 → 部门 → 用户」树形浏览；用户通过 org_id 挂到组织。
// 数据隔离仍以租户为边界（组织仅用于结构展示与归集）。
// =============================================
package store

import (
	"fmt"
	"time"
)

// Org 组织实体（管理结构展示层）
type Org struct {
	ID        int64  `json:"id"`         // 组织主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID
	ParentID  int64  `json:"parent_id"`  // 父组织 ID（0=根组织，即租户本身）
	Name      string `json:"name"`       // 组织/部门/根组织名称（可自定义）
	Type      string `json:"type"`       // 类型：root(根组织)/org(组织)/dept(部门)
	CreatedAt string `json:"created_at"` // 创建时间（RFC3339）
	UpdatedAt string `json:"updated_at"` // 更新时间（RFC3339）
}

// 组织类型常量
const (
	OrgTypeRoot = "root" // 根组织（租户本身）
	OrgTypeOrg  = "org"  // 组织
	OrgTypeDept = "dept" // 部门
)

// orgCols 组织表查询列清单
const orgCols = "id, tenant_id, parent_id, name, type, created_at, updated_at"

// CreateOrg 创建组织/部门。
// 参数：tid=租户 ID，parentID=父组织 ID（0=根组织之下），name=名称，orgType=类型（org/dept）。
// 返回：新组织对象。
func (s *Store) CreateOrg(tid, parentID int64, name, orgType string) (*Org, error) {
	if orgType == "" {
		orgType = OrgTypeOrg
	}
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO orgs (tenant_id, parent_id, name, type, created_at, updated_at) VALUES (?,?,?,?,?,?)",
		tid, parentID, name, orgType, now, now)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetOrgByID(id)
}

// GetOrgByID 按 id 查询组织。
// 参数：id=组织主键 ID；返回组织对象。
func (s *Store) GetOrgByID(id int64) (*Org, error) {
	row := s.db.QueryRow("SELECT "+orgCols+" FROM orgs WHERE id=?", id)
	var o Org
	if err := row.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// EnsureRootOrg 确保租户存在根组织行（type='root'，parent_id=0，名称默认租户名，可后续重命名）。
// 幂等：已存在则跳过。返回根组织对象。
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

// GetRootOrg 查询租户的根组织行。
// 参数：tid=租户 ID；返回根组织对象（不存在返回错误）。
func (s *Store) GetRootOrg(tid int64) (*Org, error) {
	row := s.db.QueryRow("SELECT "+orgCols+" FROM orgs WHERE tenant_id=? AND type='root' ORDER BY id LIMIT 1", tid)
	var o Org
	if err := row.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.Type, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOrgs 列出租户下全部组织（含根组织行，扁平列表，前端组装树）。
// 参数：tid=租户 ID；返回组织列表（根组织优先，按 parent_id, id 排序）。
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

// RenameOrg 重命名组织。
// 参数：id=组织 ID，name=新名称。
func (s *Store) RenameOrg(id int64, name string) error {
	_, err := s.db.Exec(
		"UPDATE orgs SET name=?, updated_at=? WHERE id=?", name, time.Now().Format(time.RFC3339), id)
	return err
}

// DeleteOrg 删除组织：先回收其下用户到根组织（org_id=0），再把子组织挂到父组织下（防止孤儿），最后删除本组织。
// 不允许删除根组织（type='root'）。
// 参数：id=组织 ID；返回错误。
func (s *Store) DeleteOrg(id int64) error {
	// 读取组织信息（获取其租户与父组织）
	org, err := s.GetOrgByID(id)
	if err != nil {
		return err
	}
	// 根组织不可删除
	if org.Type == OrgTypeRoot {
		return fmt.Errorf("根组织不可删除")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 1. 本组织用户回收至根组织
	if _, err := tx.Exec("UPDATE users SET org_id=0 WHERE org_id=?", id); err != nil {
		return err
	}
	// 2. 子孙组织上移：挂在父组织下（父为 0 则挂根）
	if _, err := tx.Exec("UPDATE orgs SET parent_id=?, updated_at=? WHERE parent_id=?", org.ParentID, time.Now().Format(time.RFC3339), id); err != nil {
		return err
	}
	// 3. 删除本组织
	if _, err := tx.Exec("DELETE FROM orgs WHERE id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}

// OrgDescendantIDs 计算指定组织及其全部子孙组织 ID（组织级数据归集用）。
// 参数：tid=租户 ID，orgID=起始组织 ID（0=根组织，返回全部组织 ID）。
// 返回：组织 ID 集合（含起始组织与全部子孙）。
func (s *Store) OrgDescendantIDs(tid, orgID int64) ([]int64, error) {
	all, err := s.ListOrgs(tid)
	if err != nil {
		return nil, err
	}
	// 构建 parent -> children 映射
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
