// ============================================================================
// ★ H10 SCIM 2.0 组织同步——存储层
//
//	scim_config：每租户一条（token 即 IdP 侧 Bearer 凭证；enabled 总开关）。
//	users.scim_external_id：IdP 稳定标识映射（幂等 upsert 依据）。
//
// ============================================================================
package store

import (
	"crypto/rand"
	"encoding/hex"
	"time"
	"translator/internal/db"
)

// SCIMConfig 租户级 SCIM 服务端配置。
type SCIMConfig struct {
	TenantID  int64  `json:"tenant_id"`
	Token     string `json:"token"`
	Enabled   bool   `json:"enabled"`
	RootOrgID int64  `json:"root_org_id"` // Group 同步挂载点（0=租户组织根）
	CreatedAt string `json:"created_at"`
}

// SCIMMigrate H10 建表/加列（幂等，store.New 启动调用）。
func (s *Store) SCIMMigrate() {
	db.Exec(s.db, db.CurrentDialect(), `CREATE TABLE IF NOT EXISTS scim_config (
		tenant_id INTEGER PRIMARY KEY,
		token TEXT NOT NULL DEFAULT '',
		enabled INTEGER NOT NULL DEFAULT 0,
		root_org_id INTEGER NOT NULL DEFAULT 0,
		created_at TEXT)`)
	db.Exec(s.db, db.CurrentDialect(), "CREATE UNIQUE INDEX IF NOT EXISTS idx_scim_token ON scim_config(token) WHERE token<>''")
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE users ADD COLUMN scim_external_id TEXT NOT NULL DEFAULT ''")
	db.Exec(s.db, db.CurrentDialect(), "CREATE INDEX IF NOT EXISTS idx_users_scim_ext ON users(tenant_id, scim_external_id) WHERE scim_external_id<>''")
}

// NewSCIMToken 生成租户 SCIM 令牌（48 hex）。
func NewSCIMToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// GetSCIMConfigByTenant 租户配置（不存在返回 nil,nil）。
func (s *Store) GetSCIMConfigByTenant(tid int64) (*SCIMConfig, error) {
	c := &SCIMConfig{}
	var en, root int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT tenant_id, COALESCE(token,''), COALESCE(enabled,0), COALESCE(root_org_id,0), COALESCE(created_at,'') FROM scim_config WHERE tenant_id=?", tid).
		Scan(&c.TenantID, &c.Token, &en, &root, &c.CreatedAt)
	if err != nil {
		return nil, nil //nolint:nilerr // 无配置=未开通
	}
	c.Enabled, c.RootOrgID = en == 1, root
	return c, nil
}

// GetSCIMConfigByToken IdP 请求鉴权：Bearer token → 配置。
func (s *Store) GetSCIMConfigByToken(token string) (*SCIMConfig, error) {
	if token == "" {
		return nil, nil
	}
	c := &SCIMConfig{}
	var en, root int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT tenant_id, token, COALESCE(enabled,0), COALESCE(root_org_id,0), COALESCE(created_at,'') FROM scim_config WHERE token=?", token).
		Scan(&c.TenantID, &c.Token, &en, &root, &c.CreatedAt)
	if err != nil {
		return nil, nil
	}
	c.Enabled, c.RootOrgID = en == 1, root
	return c, nil
}

// UpsertSCIMConfig 保存/更新租户配置（token 为空时自动生成）。
func (s *Store) UpsertSCIMConfig(c *SCIMConfig) error {
	if c.Token == "" {
		c.Token = NewSCIMToken()
	}
	if c.CreatedAt == "" {
		c.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	en := 0
	if c.Enabled {
		en = 1
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO scim_config (tenant_id, token, enabled, root_org_id, created_at) VALUES (?,?,?,?,?)
		 ON CONFLICT(tenant_id) DO UPDATE SET token=excluded.token, enabled=excluded.enabled, root_org_id=excluded.root_org_id`,
		c.TenantID, c.Token, en, c.RootOrgID, c.CreatedAt)
	return err
}

// SCIMFindUserByExternalId externalId 映射查询。
func (s *Store) SCIMFindUserByExternalId(tid int64, ext string) *User {
	if ext == "" {
		return nil
	}
	var id int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT id FROM users WHERE tenant_id=? AND scim_external_id=? LIMIT 1", tid, ext).Scan(&id)
	if err != nil {
		return nil
	}
	u, _ := s.GetUser(id, tid)
	return u
}

// SCIMSetUserExternalId 绑定 externalId（覆盖式）。
func (s *Store) SCIMSetUserExternalId(uid, tid int64, ext string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE users SET scim_external_id=? WHERE id=? AND tenant_id=?", ext, uid, tid)
	return err
}

// SCIMExternalID 用户当前的 IdP externalId（无则 ""）。
func (s *Store) SCIMExternalID(uid int64) string {
	var ext string
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(scim_external_id,'') FROM users WHERE id=?", uid).Scan(&ext)
	return ext
}

// UserIDsByOrg 组织内成员用户 ID（SCIM Group members 视图）。
func (s *Store) UserIDsByOrg(tid, orgID int64) []int64 {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id FROM users WHERE tenant_id=? AND org_id=? ORDER BY id", tid, orgID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}
