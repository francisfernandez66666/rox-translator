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
	Revoked   int    `json:"revoked"`    // ★ G9（2026-10-19）：撤销状态 0=未撤销 / 1=已撤销
	RevokedAt string `json:"revoked_at"` // ★ G9：撤销时间（未撤销为空字符串）
	Status    string `json:"status"`     // ★ G9：派生展示状态 active/used/revoked（只读，不回库）
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
	c, err := scanInviteCode(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+inviteCodeCols+" FROM invite_codes WHERE id=?", id))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetInviteCodeByCode 按邀请码字符串查询（注册流程校验使用状态用）。
// 参数：code=邀请码字符串；返回邀请码对象。
func (s *Store) GetInviteCodeByCode(code string) (*InviteCode, error) {
	c, err := scanInviteCode(db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT "+inviteCodeCols+" FROM invite_codes WHERE code=?", code))
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// MarkInviteCodeUsed 标记邀请码已使用（原子条件更新：仅当 used=0 且未撤销时生效，防止并发重复使用）。
// 参数：id=邀请码主键 ID，usedBy=使用者的标识。
// 返回 (claimed, err)：claimed=true 表示成功占用；claimed=false 表示该码已被并发请求抢先占用，
// **或该码已被撤销**（★ G9：撤销语义收进同一条 UPDATE 的 WHERE，避免「查过撤销位、标记时又被撤销」
// 的窗口）——两种情况下调用方都必须拒绝本次注册。
func (s *Store) MarkInviteCodeUsed(id int64, usedBy string) (bool, error) {
	now := time.Now().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE invite_codes SET used=1, used_by=?, used_at=? WHERE id=? AND used=0 AND revoked=0",
		usedBy, now, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// ListInviteCodes 列出全部邀请码（含已使用/已撤销，按 ID 倒序）。
// 返回：邀请码列表。
func (s *Store) ListInviteCodes() ([]*InviteCode, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT "+inviteCodeCols+" FROM invite_codes ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*InviteCode
	for rows.Next() {
		c, serr := scanInviteCode(rows)
		if serr != nil {
			continue // 单行解析失败跳过
		}
		cc := c
		out = append(out, &cc)
	}
	return out, nil
}

// 占位引用：保留 database/sql 导入供邀请码扫描扩展使用（编译期防 unused 撤销）
var _ = sql.ErrNoRows
