package store

import (
	"time"
)

// AuditLog 审计日志
type AuditLog struct {
	ID        int64  `json:"id"`
	TenantID  int64  `json:"tenant_id"`
	UserID    int64  `json:"user_id"`
	Action    string `json:"action"`
	Resource  string `json:"resource"`
	Detail    string `json:"detail"`
	BeforeVal string `json:"before_val"` // 操作前值（JSON 字符串）
	AfterVal  string `json:"after_val"`  // 操作后值（JSON 字符串）
	CreatedAt string `json:"created_at"`
}

// LogAudit 记录审计日志
func (s *Store) LogAudit(tid, userID int64, action, resource, detail string) {
	s.LogAuditDiff(tid, userID, action, resource, detail, "", "")
}

// LogAuditDiff 记录审计日志（含结构化前后值，供变更轨迹比对）
func (s *Store) LogAuditDiff(tid, userID int64, action, resource, detail, beforeVal, afterVal string) {
	_, _ = s.db.Exec(
		"INSERT INTO audit_logs (tenant_id, user_id, action, resource, detail, before_val, after_val, created_at) VALUES (?,?,?,?,?,?,?,?)",
		tid, userID, action, resource, detail, beforeVal, afterVal, time.Now().Format(time.RFC3339))
}

// ListAuditFilter 查询审计日志（租户隔离；可按动作/资源/用户/时间范围过滤，limit 最大 1000）
func (s *Store) ListAuditFilter(tid int64, action, resource string, userID int64, from, to string, limit int) ([]*AuditLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	query := "SELECT id, tenant_id, user_id, action, resource, detail, before_val, after_val, created_at FROM audit_logs WHERE tenant_id=?"
	args := []interface{}{tid}
	if action != "" {
		query += " AND action=?"
		args = append(args, action)
	}
	if resource != "" {
		query += " AND resource=?"
		args = append(args, resource)
	}
	if userID > 0 {
		query += " AND user_id=?"
		args = append(args, userID)
	}
	if from != "" {
		query += " AND created_at>=?"
		args = append(args, from)
	}
	if to != "" {
		query += " AND created_at<=?"
		args = append(args, to+"T23:59:59Z")
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.TenantID, &a.UserID, &a.Action, &a.Resource, &a.Detail, &a.BeforeVal, &a.AfterVal, &a.CreatedAt); err != nil {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}

// ListAudit 查询审计日志（租户隔离；可按动作过滤）
func (s *Store) ListAudit(tid int64, limit int) ([]*AuditLog, error) {
	return s.ListAuditFilter(tid, "", "", 0, "", "", limit)
}