package store

import (
	"time"
)

// Alert 监控告警记录
type Alert struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	Level      string `json:"level"`   // info/warning/critical
	Kind       string `json:"kind"`    // balance/model/error_rate
	Message    string `json:"message"`
	Status     string `json:"status"`  // open/resolved
	CreatedAt  string `json:"created_at"`
	ResolvedAt string `json:"resolved_at"`
}

// CreateAlert 记录告警（同类型+租户的 open 告警不重复创建）
func (s *Store) CreateAlert(tid int64, level, kind, message string) error {
	// 幂等：同 tenant+kind 已有 open 告警时跳过（避免每次检查都刷屏）
	var cnt int
	err := s.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE tenant_id=? AND kind=? AND status='open'", tid, kind).Scan(&cnt)
	if err == nil && cnt > 0 {
		return nil
	}
	_, err = s.db.Exec(
		"INSERT INTO alerts (tenant_id, level, kind, message, status, created_at) VALUES (?,?,?,?,'open',?)",
		tid, level, kind, message, time.Now().Format(time.RFC3339))
	return err
}

// ListAlerts 查询告警（tenant_id<=0 表示全平台；默认仅 open）
func (s *Store) ListAlerts(tid int64, status string, limit int) ([]*Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := "SELECT id, tenant_id, level, kind, message, status, created_at, COALESCE(resolved_at,'') FROM alerts"
	args := []interface{}{}
	conds := []string{}
	if tid > 0 {
		conds = append(conds, "tenant_id=?")
		args = append(args, tid)
	}
	if status != "" {
		conds = append(conds, "status=?")
		args = append(args, status)
	}
	if len(conds) > 0 {
		query += " WHERE " + joinConds(conds)
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Level, &a.Kind, &a.Message, &a.Status, &a.CreatedAt, &a.ResolvedAt); err != nil {
			continue
		}
		out = append(out, &a)
	}
	return out, nil
}

// ResolveAlert 关闭告警
func (s *Store) ResolveAlert(id int64) error {
	_, err := s.db.Exec(
		"UPDATE alerts SET status='resolved', resolved_at=? WHERE id=?",
		time.Now().Format(time.RFC3339), id)
	return err
}

func joinConds(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND "
		}
		out += c
	}
	return out
}