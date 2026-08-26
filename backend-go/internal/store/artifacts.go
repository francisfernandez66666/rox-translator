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
	var tableExists int
	if err := s.db.QueryRow(`SELECT 1 FROM sqlite_master WHERE type='table' AND name='output_artifacts'`).Scan(&tableExists); err != nil {
		tableExists = 0
	}
	needRebuild := false
	if tableExists == 1 {
		rows, err := s.db.Query(`PRAGMA index_list('output_artifacts')`)
		if err == nil {
			for rows.Next() {
				var seq, unique, partial int
				var name, origin string
				if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
					continue
				}
				if unique != 1 {
					continue
				}
				irows, err := s.db.Query(`PRAGMA index_info(?)`, name)
				if err != nil {
					continue
				}
				cols := []string{}
				for irows.Next() {
					var seqno, cid int
					var colName string
					if err := irows.Scan(&seqno, &cid, &colName); err == nil {
						cols = append(cols, colName)
					}
				}
				irows.Close()
				if len(cols) == 1 && cols[0] == "path" {
					needRebuild = true
					break
				}
			}
			rows.Close()
		}
	}
	if needRebuild {
		tx, err := s.db.Begin()
		if err != nil {
			return
		}
		tx.Exec(`CREATE TABLE output_artifacts_new (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			ticket_id INTEGER NOT NULL DEFAULT 0,
			created_at TEXT,
			UNIQUE(tenant_id, path))`)
		tx.Exec(`INSERT INTO output_artifacts_new SELECT * FROM output_artifacts`)
		tx.Exec(`DROP TABLE output_artifacts`)
		tx.Exec(`ALTER TABLE output_artifacts_new RENAME TO output_artifacts`)
		tx.Commit()
		return
	}
	s.db.Exec(`CREATE TABLE IF NOT EXISTS output_artifacts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		path TEXT NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		user_id INTEGER NOT NULL DEFAULT 0,
		ticket_id INTEGER NOT NULL DEFAULT 0,
		created_at TEXT,
		UNIQUE(tenant_id, path))`)
}

// RegisterArtifact 写入点登记（幂等：同租户内同 path 重复登记忽略）。
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
