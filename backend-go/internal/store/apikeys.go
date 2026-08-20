// ============ 本文件职责中文说明 ============
// 租户开放 API Key（api_keys 表）数据访问层：签发、查询、列表、启停、删除与调用统计。
// 安全设计：明文 Key 仅签发时返回一次，库内只存 SHA-256 哈希与 10 位前缀，便于开放 API 鉴权与展示脱敏。
// =============================================
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// APIKey 租户开放 API Key
type APIKey struct {
	ID         int64  `json:"id"`           // API Key 主键 ID
	TenantID   int64  `json:"tenant_id"`    // 所属租户 ID
	KeyHash    string `json:"-"`            // 明文 Key 的 SHA-256 哈希（json 序列化时隐藏，不对外暴露）
	KeyPrefix  string `json:"key_prefix"`   // Key 前缀（前 10 字符），用于界面展示与人工识别
	Name       string `json:"name"`         // Key 名称/用途说明
	Perms      string `json:"perms"`        // 权限范围：translate / kb / billing / all
	Status     string `json:"status"`       // 状态：active（启用）/ disabled（停用）
	CreatedAt  string `json:"created_at"`   // 签发时间（RFC3339 字符串）
	LastUsedAt string `json:"last_used_at"` // 最近一次调用时间（空表示从未调用）
	CallCount  int64  `json:"call_count"`   // 累计调用次数
}

// HashAPIKey 计算 Key 哈希：对明文 Key 做 SHA-256 并输出十六进制字符串。
// 参数：key=明文 API Key；返回其哈希值（用于库内存储与鉴权比对）。
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// CreateAPIKey 签发 API Key：生成明文（rk_ 前缀 + 24 位随机串），仅此一次返回明文。
// 参数：tid=租户 ID，name=Key 名称，perms=权限范围（为空默认 translate）。
// 返回：明文 Key（调用方需立即展示，之后只能查哈希）与错误。
func (s *Store) CreateAPIKey(tid int64, name, perms string) (string, error) {
	plain := "rk_" + randSuffix(24) // 生成随机明文 Key
	if perms == "" {
		perms = "translate" // 默认只给翻译权限，最小权限原则
	}
	_, err := s.db.Exec(
		"INSERT INTO api_keys (tenant_id, key_hash, key_prefix, name, perms, status, created_at) VALUES (?,?,?,?,?, 'active', ?)",
		tid, HashAPIKey(plain), plain[:10], name, perms, time.Now().Format(time.RFC3339))
	if err != nil {
		return "", err
	}
	return plain, nil
}

// GetAPIKeyByHash 按哈希查询 API Key（用于开放 API 鉴权：把请求携带的 Key 哈希后精确匹配）。
// 参数：hash=SHA-256 哈希；返回匹配的 API Key 记录。
func (s *Store) GetAPIKeyByHash(hash string) (*APIKey, error) {
	row := s.db.QueryRow("SELECT id, tenant_id, key_hash, key_prefix, name, perms, status, created_at, COALESCE(last_used_at,''), call_count FROM api_keys WHERE key_hash=?", hash)
	var k APIKey
	if err := row.Scan(&k.ID, &k.TenantID, &k.KeyHash, &k.KeyPrefix, &k.Name, &k.Perms, &k.Status, &k.CreatedAt, &k.LastUsedAt, &k.CallCount); err != nil {
		return nil, err
	}
	return &k, nil
}

// GetAPIKey 按 ID+租户查询 API Key（用于管理端轮换：必须同时校验租户归属，防止越权访问）。
// 参数：id=Key 主键 ID，tid=租户 ID。
func (s *Store) GetAPIKey(id, tid int64) (*APIKey, error) {
	row := s.db.QueryRow("SELECT id, tenant_id, key_hash, key_prefix, name, perms, status, created_at, COALESCE(last_used_at,''), call_count FROM api_keys WHERE id=? AND tenant_id=?", id, tid)
	var k APIKey
	if err := row.Scan(&k.ID, &k.TenantID, &k.KeyHash, &k.KeyPrefix, &k.Name, &k.Perms, &k.Status, &k.CreatedAt, &k.LastUsedAt, &k.CallCount); err != nil {
		return nil, err
	}
	return &k, nil
}

// ListAPIKeys 列出租户全部 API Key（按 ID 倒序）。
// 参数：tid=租户 ID；返回该租户的 Key 列表。
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
			continue // 单行解析失败跳过
		}
		out = append(out, &k)
	}
	return out, nil
}

// SetAPIKeyStatus 启用/停用 API Key（管理端轮换/吊销）。
// 参数：id=Key 主键 ID，tid=租户 ID，status=新状态（active/disabled）。
func (s *Store) SetAPIKeyStatus(id, tid int64, status string) error {
	_, err := s.db.Exec("UPDATE api_keys SET status=? WHERE id=? AND tenant_id=?", status, id, tid)
	return err
}

// DeleteAPIKey 删除 API Key（永久吊销）。
// 参数：id=Key 主键 ID，tid=租户 ID（租户隔离校验）。
func (s *Store) DeleteAPIKey(id, tid int64) error {
	_, err := s.db.Exec("DELETE FROM api_keys WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// TouchAPIKey 记录一次 API 调用：调用次数 +1 并刷新最近使用时间。
// 参数：id=Key 主键 ID；忽略错误（统计失败不影响业务主流程）。
func (s *Store) TouchAPIKey(id int64) {
	_, _ = s.db.Exec("UPDATE api_keys SET call_count=call_count+1, last_used_at=? WHERE id=?", time.Now().Format(time.RFC3339), id)
}
