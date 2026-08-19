package store

import (
	"database/sql"
	"time"
)

// InviteCode 自助注册邀请码
type InviteCode struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	Used      int    `json:"used"`
	TenantID  int64  `json:"tenant_id"` // 0=新建独立租户；>0=绑定到指定租户
	UsedBy    string `json:"used_by"`
	CreatedAt string `json:"created_at"`
	UsedAt    string `json:"used_at"`
}

// CreateInviteCode 生成邀请码
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

// GetInviteCode 按 id 查询
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

// GetInviteCodeByCode 按邀请码查询（校验使用状态）
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

// MarkInviteCodeUsed 标记邀请码已使用
func (s *Store) MarkInviteCodeUsed(id int64, usedBy string) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"UPDATE invite_codes SET used=1, used_by=?, used_at=? WHERE id=? AND used=0",
		usedBy, now, id)
	return err
}

// ListInviteCodes 邀请码列表（全部，含已使用）
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
			continue
		}
		out = append(out, &c)
	}
	return out, nil
}

var _ = sql.ErrNoRows