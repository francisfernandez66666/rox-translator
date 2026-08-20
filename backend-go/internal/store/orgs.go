// ============ 本文件职责中文说明 ============
// 组织层级（管理结构展示层）：orgs 表数据访问。
// 结构：根组织 = 租户本身（parent_id=0，不单独建行），其下可挂子组织/部门（parent_id 指向父组织）。
// 用途：管理后台按「租户 → 组织 → 用户」树形浏览非敏感数据；用户通过 org_id 挂到组织。
// 数据隔离仍以租户为边界（组织仅用于结构展示与归集）。
// =============================================
package store

import (
	"time"
)

// Org 组织实体（管理结构展示层）
type Org struct {
	ID        int64  `json:"id"`         // 组织主键 ID
	TenantID  int64  `json:"tenant_id"`  // 所属租户 ID
	ParentID  int64  `json:"parent_id"`  // 父组织 ID（0=根组织，即租户本身）
	Name      string `json:"name"`       // 组织/部门名称
	CreatedAt string `json:"created_at"` // 创建时间（RFC3339）
	UpdatedAt string `json:"updated_at"` // 更新时间（RFC3339）
}

// orgCols 组织表查询列清单
const orgCols = "id, tenant_id, parent_id, name, created_at, updated_at"

// CreateOrg 创建组织（子组织/部门）。
// 参数：tid=租户 ID，parentID=父组织 ID（0=根组织之下），name=组织名称。
// 返回：新组织对象。
func (s *Store) CreateOrg(tid, parentID int64, name string) (*Org, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO orgs (tenant_id, parent_id, name, created_at, updated_at) VALUES (?,?,?,?,?)",
		tid, parentID, name, now, now)
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
	if err := row.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	return &o, nil
}

// ListOrgs 列出租户下全部组织（扁平列表，前端组装树）。
// 参数：tid=租户 ID；返回组织列表（按 parent_id, id 排序）。
func (s *Store) ListOrgs(tid int64) ([]*Org, error) {
	rows, err := s.db.Query("SELECT "+orgCols+" FROM orgs WHERE tenant_id=? ORDER BY parent_id, id", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Org
	for rows.Next() {
		var o Org
		if err := rows.Scan(&o.ID, &o.TenantID, &o.ParentID, &o.Name, &o.CreatedAt, &o.UpdatedAt); err != nil {
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
// 参数：id=组织 ID；返回错误。
func (s *Store) DeleteOrg(id int64) error {
	// 读取组织信息（获取其租户与父组织）
	org, err := s.GetOrgByID(id)
	if err != nil {
		return err
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
