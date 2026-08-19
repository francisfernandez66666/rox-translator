package store

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// APIKey 租户开放 API Key
type APIKey struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	KeyHash    string `json:"-"`
	KeyPrefix  string `json:"key_prefix"`
	Name       string `json:"name"`
	Perms      string `json:"perms"` // translate / kb / billing / all
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at"`
	CallCount  int64  `json:"call_count"`
}

// HashAPIKey 计算 Key 哈希
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// CreateAPIKey 签发 API Key（返回明文，仅此一次）
func (s *Store) CreateAPIKey(tid int64, name, perms string) (string, error) {
	plain := "rk_" + randSuffix(24)
	if perms == "" {
		perms = "translate"
	}
	_, err := s.db.Exec(
		"INSERT INTO api_keys (tenant_id, key_hash, key_prefix, name, perms, status, created_at) VALUES (?,?,?,?,?, 'active', ?)",
		tid, HashAPIKey(plain), plain[:10], name, perms, time.Now().Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return plain, nil
}

// GetAPIKeyByHash 按哈希查询（用于开放 API 鉴权）
func (s *Store) GetAPIKeyByHash(hash string) (*APIKey, error) {
	row := s.db.QueryRow("SELECT id, tenant_id, key_hash, key_prefix, name, perms, status, created_at, COALESCE(last_used_at,''), call_count FROM api_keys WHERE key_hash=?", hash)
	var k APIKey
	if err := row.Scan(&k.ID, &k.TenantID, &k.KeyHash, &k.KeyPrefix, &k.Name, &k.Perms, &k.Status, &k.CreatedAt, &k.LastUsedAt, &k.CallCount); err != nil {
		return nil, err
	}
	return &k, nil
}

// ListAPIKeys 租户 API Key 列表
func (s *Store) ListAPIKeys(tid int64) ([]*APIKey, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, key_hash, key_prefix, name, perms, status, created_at, COALESCE(last_used_at,''), call_count FROM api_keys WHERE tenant_id=? ORDER BY id DESC", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.TenantID, &k.KeyHash, &k.KeyPrefix, &k.Name, &k.Perms, &k.Status, &k.CreatedAt, &k.LastUsedAt, &k.CallCount); err != nil {
			continue
		}
		out = append(out, &k)
	}
	return out, nil
}

// SetAPIKeyStatus 启用/停用 API Key
func (s *Store) SetAPIKeyStatus(id, tid int64, status string) error {
	_, err := s.db.Exec("UPDATE api_keys SET status=? WHERE id=? AND tenant_id=?", status, id, tid)
	return err
}

// DeleteAPIKey 删除 API Key
func (s *Store) DeleteAPIKey(id, tid int64) error {
	_, err := s.db.Exec("DELETE FROM api_keys WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// TouchAPIKey 记录调用
func (s *Store) TouchAPIKey(id int64) {
	_, _ = s.db.Exec("UPDATE api_keys SET call_count=call_count+1, last_used_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
}