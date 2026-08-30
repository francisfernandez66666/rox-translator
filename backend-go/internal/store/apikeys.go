// ============ 本文件职责中文说明 ============
// 租户开放 API Key（api_keys 表）数据访问层：签发、查询、列表、启停、删除与调用统计。
// 安全设计：明文 Key 仅签发时返回一次，库内只存 SHA-256 哈希与 10 位前缀，便于开放 API 鉴权与展示脱敏。
// =============================================
package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
	"translator/internal/db"
)

// APIKey 租户开放 API Key
type APIKey struct {
	ID             int64  `json:"id"`               // API Key 主键 ID
	TenantID       int64  `json:"tenant_id"`        // 所属租户 ID
	UserID         int64  `json:"user_id"`          // ★ 签发用户 ID（0=历史租户级 Key；用于任务归属校验）
	KeyHash        string `json:"-"`                // 明文 Key 的 SHA-256 哈希（json 序列化时隐藏，不对外暴露）
	KeyPrefix      string `json:"key_prefix"`       // Key 前缀（前 10 字符），用于界面展示与人工识别
	Name           string `json:"name"`             // Key 名称/用途说明
	Perms          string `json:"perms"`            // 权限范围：translate / kb / billing / all
	Status         string `json:"status"`           // 状态：active（启用）/ disabled（停用）
	CreatedAt      string `json:"created_at"`       // 签发时间（RFC3339 字符串）
	LastUsedAt     string `json:"last_used_at"`     // 最近一次调用时间（空表示从未调用）
	CallCount      int64  `json:"call_count"`       // 累计调用次数
	KeyEnc         string `json:"-"`                // 明文 Key 的静态加密结果（AES-256-GCM，支持任意时刻复制）
	DailyCallLimit int64  `json:"daily_call_limit"` // 每日调用上限（0=不限；R4 Key 级配额）
	CallsToday     int64  `json:"calls_today"`      // 今日已调用次数
	CallsTodayDate string `json:"calls_today_date"` // 今日计数归属日期（YYYY-MM-DD，跨日自动清零）
}

// HashAPIKey 计算 Key 哈希：对明文 Key 做 SHA-256 并输出十六进制字符串。
// 参数：key=明文 API Key；返回其哈希值（用于库内存储与鉴权比对）。
func HashAPIKey(key string) string {
	h := sha256.Sum256([]byte(key))
	return hex.EncodeToString(h[:])
}

// randToken 生成 n 字节密码学随机数的十六进制串（API Key 明文专用）。
//
// ★ 安全（2026-08-26 全仓评审 A4）：Key 是鉴权凭证，必须用 crypto/rand——
//
//	此前复用 randSuffix（UnixNano 种子的线性同余伪随机，服务于订单号可读性），
//	其输出空间与种子可预测性不满足凭证强度要求。
func randToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// 随机源不可用时拒绝签发（宁可失败也不落弱密钥）
		return ""
	}
	return hex.EncodeToString(b)
}

// CreateAPIKey 签发 API Key：生成明文（rk_ 前缀 + 40 位密码学随机 hex），仅此一次返回明文。
// 参数：tid=租户 ID，userID=归属用户 ID，name=Key 名称，perms=权限范围（为空默认 translate），dailyLimit=每日调用上限（0=不限）。
// 返回：明文 Key（调用方需立即展示，之后只能查哈希）与错误。
func (s *Store) CreateAPIKey(tid, userID int64, name, perms string, dailyLimit int64) (string, error) {
	token := randToken(20) // 20 字节 = 160bit 熵
	if token == "" {
		return "", fmt.Errorf("随机源不可用，已拒绝签发 API Key") // 显式失败，不落弱密钥
	}
	plain := "rk_" + token
	if perms == "" {
		perms = "translate" // 默认只给翻译权限，最小权限原则
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO api_keys (tenant_id, user_id, key_hash, key_prefix, name, perms, status, created_at, daily_call_limit, key_enc) VALUES (?,?,?, ?,?, ?, 'active', ?, ?, ?)",
		tid, userID, HashAPIKey(plain), plain[:10], name, perms, time.Now().Format(time.RFC3339), dailyLimit, EncryptPlain(plain))
	if err != nil {
		return "", err
	}
	return plain, nil
}

// SetAPIKeyDailyLimit 设置 Key 每日调用上限（R4 Key 级配额；0=不限）。
func (s *Store) SetAPIKeyDailyLimit(id, tid, limit int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE api_keys SET daily_call_limit=? WHERE id=? AND tenant_id=?", limit, id, tid)
	return err
}

// GetAPIKeyByHash 按哈希查询 API Key（用于开放 API 鉴权：把请求携带的 Key 哈希后精确匹配）。
// 参数：hash=SHA-256 哈希；返回匹配的 API Key 记录。
func (s *Store) GetAPIKeyByHash(hash string) (*APIKey, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, COALESCE(user_id,0), key_hash, key_prefix, name, perms, status, created_at, COALESCE(last_used_at,''), call_count, COALESCE(daily_call_limit,0), COALESCE(calls_today,0), COALESCE(calls_today_date,''), COALESCE(key_enc,'') FROM api_keys WHERE key_hash=?", hash)
	var k APIKey
	if err := row.Scan(&k.ID, &k.TenantID, &k.UserID, &k.KeyHash, &k.KeyPrefix, &k.Name, &k.Perms, &k.Status, &k.CreatedAt, &k.LastUsedAt, &k.CallCount, &k.DailyCallLimit, &k.CallsToday, &k.CallsTodayDate, &k.KeyEnc); err != nil {
		return nil, err
	}
	return &k, nil
}

// GetAPIKey 按 ID+租户查询 API Key（用于管理端轮换：必须同时校验租户归属，防止越权访问）。
// 参数：id=Key 主键 ID，tid=租户 ID。
func (s *Store) GetAPIKey(id, tid int64) (*APIKey, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id, COALESCE(user_id,0), key_hash, key_prefix, name, perms, status, created_at, COALESCE(last_used_at,''), call_count, COALESCE(daily_call_limit,0), COALESCE(calls_today,0), COALESCE(calls_today_date,''), COALESCE(key_enc,'') FROM api_keys WHERE id=? AND tenant_id=?", id, tid)
	var k APIKey
	if err := row.Scan(&k.ID, &k.TenantID, &k.UserID, &k.KeyHash, &k.KeyPrefix, &k.Name, &k.Perms, &k.Status, &k.CreatedAt, &k.LastUsedAt, &k.CallCount, &k.DailyCallLimit, &k.CallsToday, &k.CallsTodayDate, &k.KeyEnc); err != nil {
		return nil, err
	}
	return &k, nil
}

// ListAPIKeys 列出租户全部 API Key（按 ID 倒序）。
// 参数：tid=租户 ID；返回该租户的 Key 列表。
func (s *Store) ListAPIKeys(tid int64) ([]*APIKey, error) {
	// tid<=0：跨租户全量（超管平台视角聚合）
	q := "SELECT id, tenant_id, COALESCE(user_id,0), key_hash, key_prefix, name, perms, status, created_at, COALESCE(last_used_at,''), call_count, COALESCE(daily_call_limit,0), COALESCE(calls_today,0), COALESCE(calls_today_date,''), COALESCE(key_enc,'') FROM api_keys"
	if tid > 0 {
		q += " WHERE tenant_id=?"
	}
	q += " ORDER BY id DESC"
	var rows *sql.Rows
	var err error
	if tid > 0 {
		rows, err = db.Query(s.db, db.CurrentDialect(), q, tid)
	} else {
		rows, err = db.Query(s.db, db.CurrentDialect(), q)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.TenantID, &k.UserID, &k.KeyHash, &k.KeyPrefix, &k.Name, &k.Perms, &k.Status, &k.CreatedAt, &k.LastUsedAt, &k.CallCount, &k.DailyCallLimit, &k.CallsToday, &k.CallsTodayDate, &k.KeyEnc); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &k)
	}
	return out, nil
}

// SetAPIKeyStatus 启用/停用 API Key（管理端轮换/吊销）。
// 参数：id=Key 主键 ID，tid=租户 ID，status=新状态（active/disabled）。
func (s *Store) SetAPIKeyStatus(id, tid int64, status string) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE api_keys SET status=? WHERE id=? AND tenant_id=?", status, id, tid)
	return err
}

// DeleteAPIKey 删除 API Key（永久吊销）。
// 参数：id=Key 主键 ID，tid=租户 ID（租户隔离校验）。
func (s *Store) DeleteAPIKey(id, tid int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "DELETE FROM api_keys WHERE id=? AND tenant_id=?", id, tid)
	return err
}

// TouchAPIKey 记录一次 API 调用：调用次数 +1 并刷新最近使用时间。
// 参数：id=Key 主键 ID；忽略错误（统计失败不影响业务主流程）。
func (s *Store) TouchAPIKey(id int64) {
	today := time.Now().Format("2006-01-02")
	_, _ = db.Exec(s.db, db.CurrentDialect(), `UPDATE api_keys SET call_count=call_count+1, last_used_at=?,
		calls_today = CASE WHEN calls_today_date=? THEN calls_today+1 ELSE 1 END,
		calls_today_date=?
		WHERE id=?`, time.Now().Format(time.RFC3339), today, today, id)
}

// GetAPIKeyPlain 解密返回 Key 明文（租户隔离校验；供「固定复制」能力使用）。
func (s *Store) GetAPIKeyPlain(id, tid int64) (string, error) {
	var enc string
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(key_enc,'') FROM api_keys WHERE id=? AND tenant_id=?", id, tid).Scan(&enc)
	if err != nil {
		return "", err
	}
	return DecryptPlain(enc), nil
}

// DisableAPIKeysByUser 停用指定用户名下全部 API Key（自助注销联动，2026-08-26 需求）。
// 注销即收回其签发 Key 的调用权限（status=disabled 即时生效）；账号恢复启用后
// 可由管理员在 API Key 面板逐条重新启用。返回停用条数。
func (s *Store) DisableAPIKeysByUser(tid, userID int64) int64 {
	res, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE api_keys SET status='disabled' WHERE tenant_id=? AND user_id=? AND status='active'", tid, userID)
	if err != nil {
		return 0
	}
	n, _ := res.RowsAffected()
	return n
}
