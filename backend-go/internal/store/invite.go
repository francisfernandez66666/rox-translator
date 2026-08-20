// ============ 本文件职责中文说明 ============
// 自助注册邀请码（invite_codes 表）数据访问层：生成、查询、标记已使用、列表。
// 邀请码可绑定指定租户（tenant_id>0）或新建独立租户（tenant_id=0），用于平台自助开通流程。
// =============================================
package store

import (
	"database/sql"
	"time"
)

// InviteCode 自助注册邀请码
type InviteCode struct {
	ID        int64  `json:"id"`         // 邀请码主键 ID
	Code      string `json:"code"`       // 邀请码字符串（唯一）
	Used      int    `json:"used"`       // 是否已使用：0=未使用，1=已使用
	TenantID  int64  `json:"tenant_id"`  // 绑定租户 ID：0=新建独立租户；>0=绑定到指定租户
	UsedBy    string `json:"used_by"`    // 使用者的标识（用户名或邮箱，未使用为空）
	CreatedAt string `json:"created_at"` // 生成时间（RFC3339 字符串）
	UsedAt    string `json:"used_at"`    // 使用时间（未使用为空字符串）
}

// CreateInviteCode 生成一条邀请码记录。
// 参数：code=邀请码字符串，tenantID=绑定租户 ID（0=新建独立租户）。
// 返回：新邀请码对象。
func (s *Store) CreateInviteCode(code string, tenantID int64) (*InviteCode, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.Exec(
		"INSERT INTO invite_codes (code, used, tenant_id, created_at) VALUES (?,0,?,?)",
		code, tenantID, now)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return s.GetInviteCode(id)
}

// GetInviteCode 按主键 ID 查询邀请码。
// 参数：id=邀请码主键 ID；返回邀请码对象。
func (s *Store) GetInviteCode(id int64) (*InviteCode, error) {
	row := s.db.QueryRow(
		"SELECT id, code, used, tenant_id, COALESCE(used_by,''), COALESCE(created_at,''), COALESCE(used_at,'') FROM invite_codes WHERE id=?", id)
	var c InviteCode
	err := row.Scan(&c.ID, &c.Code, &c.Used, &c.TenantID, &c.UsedBy, &c.CreatedAt, &c.UsedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetInviteCodeByCode 按邀请码字符串查询（注册流程校验使用状态用）。
// 参数：code=邀请码字符串；返回邀请码对象。
func (s *Store) GetInviteCodeByCode(code string) (*InviteCode, error) {
	row := s.db.QueryRow(
		"SELECT id, code, used, tenant_id, COALESCE(used_by,''), COALESCE(created_at,''), COALESCE(used_at,'') FROM invite_codes WHERE code=?", code)
	var c InviteCode
	err := row.Scan(&c.ID, &c.Code, &c.Used, &c.TenantID, &c.UsedBy, &c.CreatedAt, &c.UsedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MarkInviteCodeUsed 标记邀请码已使用（原子条件更新：仅当 used=0 时生效，防止并发重复使用）。
// 参数：id=邀请码主键 ID，usedBy=使用者的标识。
func (s *Store) MarkInviteCodeUsed(id int64, usedBy string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE invite_codes SET used=1, used_by=?, used_at=? WHERE id=? AND used=0",
		usedBy, now, id)
	return err
}

// ListInviteCodes 列出全部邀请码（含已使用，按 ID 倒序）。
// 返回：邀请码列表。
func (s *Store) ListInviteCodes() ([]*InviteCode, error) {
	rows, err := s.db.Query(
		"SELECT id, code, used, tenant_id, COALESCE(used_by,''), COALESCE(created_at,''), COALESCE(used_at,'') FROM invite_codes ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InviteCode
	for rows.Next() {
		var c InviteCode
		if err := rows.Scan(&c.ID, &c.Code, &c.Used, &c.TenantID, &c.UsedBy, &c.CreatedAt, &c.UsedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &c)
	}
	return out, nil
}

var _ = sql.ErrNoRows
