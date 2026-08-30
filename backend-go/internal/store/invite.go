// ============ invite.go · 职责说明 ============
// store 包自助注册邀请码（invite_codes 表）数据访问层。
// 生成、查询、标记已使用、列表。
// 邀请码可绑定指定租户（tenant_id>0）或新建独立租户（tenant_id=0），用于平台自助开通流程。
// =============================================
package store

import (
	"database/sql"
	"time"
	"translator/internal/db"
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
	OrgID     int64  `json:"org_id"`     // ★ 绑定组织（四期）：受邀用户归入该组织层级（0=不绑定）
}

// CreateInviteCode 生成一条邀请码记录。
// 参数：code=邀请码字符串，tenantID=绑定租户 ID（0=新建独立租户）。
// 返回：新邀请码对象。
func (s *Store) CreateInviteCode(code string, tenantID int64, orgID ...int64) (*InviteCode, error) {
	now := time.Now().Format(time.RFC3339)
	org := int64(0)
	if len(orgID) > 0 {
		org = orgID[0]
	}
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		"INSERT INTO invite_codes (code, used, tenant_id, org_id, created_at) VALUES (?,0,?,?,?)",
		code, tenantID, org, now)
	if err != nil {
		return nil, err
	}
	return s.GetInviteCode(id)
}

// GetInviteCode 按主键 ID 查询邀请码。
// 参数：id=邀请码主键 ID；返回邀请码对象。
func (s *Store) GetInviteCode(id int64) (*InviteCode, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id, code, used, tenant_id, COALESCE(used_by,''), COALESCE(created_at,''), COALESCE(used_at,''), COALESCE(org_id,0) FROM invite_codes WHERE id=?", id)
	var c InviteCode
	err := row.Scan(&c.ID, &c.Code, &c.Used, &c.TenantID, &c.UsedBy, &c.CreatedAt, &c.UsedAt, &c.OrgID)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetInviteCodeByCode 按邀请码字符串查询（注册流程校验使用状态用）。
// 参数：code=邀请码字符串；返回邀请码对象。
func (s *Store) GetInviteCodeByCode(code string) (*InviteCode, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id, code, used, tenant_id, COALESCE(used_by,''), COALESCE(created_at,''), COALESCE(used_at,''), COALESCE(org_id,0) FROM invite_codes WHERE code=?", code)
	var c InviteCode
	err := row.Scan(&c.ID, &c.Code, &c.Used, &c.TenantID, &c.UsedBy, &c.CreatedAt, &c.UsedAt, &c.OrgID)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MarkInviteCodeUsed 标记邀请码已使用（原子条件更新：仅当 used=0 时生效，防止并发重复使用）。
// 参数：id=邀请码主键 ID，usedBy=使用者的标识。
// 返回 (claimed, err)：claimed=true 表示成功占用；claimed=false 表示该码已被并发请求抢先占用——
// 调用方必须据此拒绝本次注册（2026-08-26 整改 A4：此前返回值被丢弃，「先查后标」窗口内并发同码双双放行）。
func (s *Store) MarkInviteCodeUsed(id int64, usedBy string) (bool, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE invite_codes SET used=1, used_by=?, used_at=? WHERE id=? AND used=0",
		usedBy, now, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListInviteCodes 列出全部邀请码（含已使用，按 ID 倒序）。
// 返回：邀请码列表。
func (s *Store) ListInviteCodes() ([]*InviteCode, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id, code, used, tenant_id, COALESCE(used_by,''), COALESCE(created_at,''), COALESCE(used_at,''), COALESCE(org_id,0) FROM invite_codes ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InviteCode
	for rows.Next() {
		var c InviteCode
		if err := rows.Scan(&c.ID, &c.Code, &c.Used, &c.TenantID, &c.UsedBy, &c.CreatedAt, &c.UsedAt, &c.OrgID); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &c)
	}
	return out, nil
}

var _ = sql.ErrNoRows
