// ============ artifacts.go · 职责说明 ============
// 产物/上传件归属登记表（评审整改 C1）：
// /api/download 具备 JWT+目录白名单后，仍缺「租户/用户归属」维度——白名单内任意登录
// 用户可读任意租户的产物。本表在写入点登记 path→(tenant,user,ticket) 归属，
// 下载时按归属判定；未登记的历史产物按 Phase1 灰度放行并留审计（一个产物保留周期后收紧）。
// =============================================
package store

import (
	"time"
)

// Artifact 归属登记行
type Artifact struct {
	ID        int64
	Path      string
	TenantID  int64
	UserID    int64
	TicketID  int64
	CreatedAt string
}

// ArtifactsMigrate 建表与唯一索引（幂等，Store.New 迁移链调用）。
func (s *Store) ArtifactsMigrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS output_artifacts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL UNIQUE,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		ticket_id INTEGER NOT NULL DEFAULT 0,
		created_at TEXT)`)
}

// RegisterArtifact 写入点登记（幂等：同 path 重复登记忽略）。
// 参数：path=产物/上传件绝对路径；tid=归属租户；uid=归属用户；ticketID=关联工单（可 0）。
func (s *Store) RegisterArtifact(path string, tid, uid, ticketID int64) {
	if path == "" {
		return
	}
	s.db.Exec(`INSERT OR IGNORE INTO output_artifacts (path, tenant_id, user_id, ticket_id, created_at)
		VALUES (?,?,?,?,?)`, path, tid, uid, ticketID, time.Now().Format(time.RFC3339))
}

// GetArtifactByPath 按路径查归属；不存在返回 (nil, err)。
func (s *Store) GetArtifactByPath(path string) (*Artifact, error) {
	row := s.db.QueryRow(
		"SELECT id, path, tenant_id, user_id, ticket_id, COALESCE(created_at,'') FROM output_artifacts WHERE path=?", path)
	var a Artifact
	if err := row.Scan(&a.ID, &a.Path, &a.TenantID, &a.UserID, &a.TicketID, &a.CreatedAt); err != nil {
		return nil, err
	}
	return &a, nil
}
