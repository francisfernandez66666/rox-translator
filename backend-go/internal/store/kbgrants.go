// ============================================================================
// H3 KB 包级权限矩阵：五档可见范围（PackScope 继承链）之上，补按用户授权的
// 读/写/管理三级（read ⊂ write ⊂ manage）。部门管理员及以上天然具备 manage；
// 普通成员需持有目标包 grants 才能操作——读写条目、管理包与再授权。
// 表：kb_pack_grants（每用户每包一条，UNIQUE 幂等 upsert）。
// ============================================================================
package store

import (
	"time"
	"translator/internal/db"
)

// KBPackGrant 包级授权行。Role: read | write | manage。
type KBPackGrant struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	PackID      int64  `json:"pack_id"`
	UserID      int64  `json:"user_id"`
	Role        string `json:"role"`
	Username    string `json:"username"`     // join 展示用
	DisplayName string `json:"display_name"` // join 展示用
	CreatedAt   string `json:"created_at"`
}

// KBPackGrantsMigrate H3 授权表建表（幂等，store.New 启动调用）。
func (s *Store) KBPackGrantsMigrate() {
	db.Exec(s.db, db.CurrentDialect(), `CREATE TABLE IF NOT EXISTS kb_pack_grants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		pack_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL,
		role TEXT NOT NULL DEFAULT 'read',
		created_at TEXT,
		UNIQUE(pack_id, user_id))`)
	db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_kb_pack_grants_user ON kb_pack_grants(tenant_id, user_id)`)
}

// KBRoleRank 三级角色序：” = 0（无授权），read=1 < write=2 < manage=3。
func KBRoleRank(role string) int {
	switch role {
	case "read":
		return 1
	case "write":
		return 2
	case "manage":
		return 3
	}
	return 0
}

// GrantKBPack 授予/更新某用户对某包的角色（role 须为 read/write/manage）。
func (s *Store) GrantKBPack(tid, packID, userID int64, role string) error {
	if KBRoleRank(role) == 0 {
		return s.RevokeKBPack(tid, packID, userID)
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM kb_pack_grants WHERE pack_id=? AND user_id=?", packID, userID)
	if err != nil {
		return err
	}
	_, err = db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO kb_pack_grants (tenant_id, pack_id, user_id, role, created_at) VALUES (?,?,?,?,?)",
		tid, packID, userID, role, time.Now().Format(time.RFC3339))
	return err
}

// RevokeKBPack 撤销某用户对某包的授权。
func (s *Store) RevokeKBPack(tid, packID, userID int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"DELETE FROM kb_pack_grants WHERE pack_id=? AND user_id=?", packID, userID)
	return err
}

// ListKBPackGrants 某包全部授权（带用户名/昵称，按角色降序）。
func (s *Store) ListKBPackGrants(tid, packID int64) ([]*KBPackGrant, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT g.id, g.tenant_id, g.pack_id, g.user_id, g.role,
		        COALESCE(u.username,''), COALESCE(u.display_name,''), COALESCE(g.created_at,'')
		   FROM kb_pack_grants g LEFT JOIN users u ON u.id = g.user_id
		  WHERE g.pack_id=? AND (g.tenant_id=? OR ?<=0) ORDER BY g.role, g.id`, packID, tid, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackGrant
	for rows.Next() {
		g := &KBPackGrant{}
		if err := rows.Scan(&g.ID, &g.TenantID, &g.PackID, &g.UserID, &g.Role, &g.Username, &g.DisplayName, &g.CreatedAt); err != nil {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}

// KBPackRoleOf 某用户对某包的角色（无授权返回 ""）。
func (s *Store) KBPackRoleOf(tid, packID, userID int64) string {
	var role string
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT role FROM kb_pack_grants WHERE pack_id=? AND user_id=? AND (tenant_id=? OR ?<=0) LIMIT 1",
		packID, userID, tid, tid).Scan(&role)
	if err != nil {
		return ""
	}
	return role
}

// ListKBPackGrantsByUser 某用户全部包授权（前端导航门控 /api/admin/kb-packages/mine）。
func (s *Store) ListKBPackGrantsByUser(tid, userID int64) ([]*KBPackGrant, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT g.id, g.tenant_id, g.pack_id, g.user_id, g.role, '', '', COALESCE(g.created_at,'')
		   FROM kb_pack_grants g
		  WHERE g.user_id=? AND (g.tenant_id=? OR ?<=0) ORDER BY g.pack_id`, userID, tid, tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*KBPackGrant
	for rows.Next() {
		g := &KBPackGrant{}
		if err := rows.Scan(&g.ID, &g.TenantID, &g.PackID, &g.UserID, &g.Role, &g.Username, &g.DisplayName, &g.CreatedAt); err != nil {
			continue
		}
		out = append(out, g)
	}
	return out, nil
}
