package store

import (
	"time"
)

// AuditLog 审计日志
type AuditLog struct {
	ID       int64  `json:"id"`
	TenantID int64  `json:"tenant_id"`
	UserID   int64  `json:"user_id"`
	Action   string `json:"action"`
	Resource string `json:"resource"`
	Detail   string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// LogAudit 记录审计日志
func (s *Store) LogAudit(tid, userID int64, action, resource, detail string) {
	_, _ = s.db.Exec(
		"INSERT INTO audit_logs (tenant_id, user_id, action, resource, detail, created_at) VALUES (?,?,?,?,?,?)",
		tid, userID, action, resource, detail, time.Now().Format(time.RFC3339))
}

// ListAudit 查询审计日志（租户隔离；可按动作过滤）
func (s *Store) ListAudit(tid int64, limit int) ([]*AuditLog, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query("SELECT id, tenant_id, user_id, action, resource, detail, created_at FROM audit_logs WHERE tenant_id=? ORDER BY id DESC LIMIT ?", tid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.TenantID, &a.UserID, &a.Action, &a.Resource, &a.Detail, &a.CreatedAt); err != nil {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}