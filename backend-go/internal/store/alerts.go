// ============ 本文件职责中文说明 ============
// 监控告警（alerts 表）数据访问层：记录、查询、关闭平台级与租户级监控告警。
// 告警类型涵盖余额不足（balance）、模型异常（model）、错误率过高（error_rate）等；
// 同一租户同类 open 告警幂等去重，避免每次健康检查都重复刷屏。
// =============================================
package store

import (
	"time"
)

// Alert 监控告警记录
type Alert struct {
	ID         int64  `json:"id"`          // 告警主键 ID
	TenantID   int64  `json:"tenant_id"`   // 所属租户 ID（0 表示平台级告警）
	Level      string `json:"level"`       // 告警级别：info / warning / critical
	Kind       string `json:"kind"`        // 告警类型：balance / model / error_rate
	Message    string `json:"message"`     // 告警消息内容
	Status     string `json:"status"`      // 告警状态：open（未处理）/ resolved（已关闭）
	CreatedAt  string `json:"created_at"`  // 告警创建时间（RFC3339 格式字符串）
	ResolvedAt string `json:"resolved_at"` // 告警关闭时间（空字符串表示尚未关闭）
}

// CreateAlert 记录告警；同类型+租户的 open 告警不重复创建（幂等去重）。
// 参数：tid=租户 ID，level=告警级别，kind=告警类型，message=告警描述。
func (s *Store) CreateAlert(tid int64, level, kind, message string) error {
	// 幂等：同 tenant+kind 已有 open 告警时跳过（避免每次检查都刷屏）
	var cnt int
	err := s.db.QueryRow("SELECT COUNT(*) FROM alerts WHERE tenant_id=? AND kind=? AND status='open'", tid, kind).Scan(&cnt)
	if err == nil && cnt > 0 {
		return nil // 已存在未处理同类型告警，直接返回，不再重复写入
	}
	_, err = s.db.Exec(
		"INSERT INTO alerts (tenant_id, level, kind, message, status, created_at) VALUES (?,?,?,?,'open',?)",
		tid, level, kind, message, time.Now().Format(time.RFC3339))
	return err
}

// ListAlerts 查询告警列表；tenant_id<=0 表示全平台，status 为空时默认返回全部状态。
// 参数：tid=租户 ID（<=0 查询全平台），status=状态过滤（open/resolved/空），limit=条数上限（默认 100，最大 500）。
// 返回：告警列表，按 ID 倒序排列。
func (s *Store) ListAlerts(tid int64, status string, limit int) ([]*Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100 // 非法或不合理的 limit 收敛到默认 100
	}
	query := "SELECT id, tenant_id, level, kind, message, status, created_at, COALESCE(resolved_at,'') FROM alerts"
	args := []interface{}{}
	conds := []string{}
	if tid > 0 {
		conds = append(conds, "tenant_id=?") // 租户隔离过滤
		args = append(args, tid)
	}
	if status != "" {
		conds = append(conds, "status=?") // 按状态过滤
		args = append(args, status)
	}
	if len(conds) > 0 {
		query += " WHERE " + joinConds(conds) // 拼接过滤条件
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
		// resolved_at 可能为 NULL，用 COALESCE 兜底为空字符串
		if err := rows.Scan(&a.ID, &a.TenantID, &a.Level, &a.Kind, &a.Message, &a.Status, &a.CreatedAt, &a.ResolvedAt); err != nil {
			continue // 单行解析失败跳过，不中断整个查询
		}
		out = append(out, &a)
	}
	return out, nil
}

// ResolveAlert 关闭指定告警：将状态置为 resolved 并记录关闭时间。
// 参数：id=告警主键 ID。
func (s *Store) ResolveAlert(id int64) error {
	_, err := s.db.Exec(
		"UPDATE alerts SET status='resolved', resolved_at=? WHERE id=?",
		time.Now().Format(time.RFC3339), id)
	return err
}

// joinConds 把多个 SQL 条件用 AND 连接成 "c1 AND c2" 字符串。
// 参数：conds=条件表达式切片；返回拼接后的条件串（无元素时返回空串）。
func joinConds(conds []string) string {
	out := ""
	for i, c := range conds {
		if i > 0 {
			out += " AND " // 除首个条件外都补 AND 连接符
		}
		out += c
	}
	return out
}
