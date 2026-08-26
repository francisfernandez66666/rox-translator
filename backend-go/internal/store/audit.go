// ============ 本文件职责中文说明 ============
// 审计日志（audit_logs 表）数据访问层：记录用户关键操作日志（写入/批量导出）。
// 支持结构化变更轨迹：before_val / after_val 保存操作前后的 JSON 值，便于比对变更内容；
// 查询提供租户隔离与动作/资源/用户/时间范围多条件过滤。
// =============================================
package store

import (
	"time"
)

// AuditLog 审计日志
type AuditLog struct {
	ID         int64  `json:"id"`                    // 审计日志主键 ID
	TenantID   int64  `json:"tenant_id"`             // 所属租户 ID（0 表示平台级操作）
	UserID     int64  `json:"user_id"`               // 操作者用户 ID（0 表示系统/未登录操作）
	Action     string `json:"action"`                // 操作动作，如 create/update/delete/login
	Resource   string `json:"resource"`              // 操作资源类型，如 user/ticket/kb_package/order
	Detail     string `json:"detail"`                // 操作详情描述（人类可读）
	BeforeVal  string `json:"before_val"`            // 操作前值（JSON 字符串，用于变更轨迹比对）
	AfterVal   string `json:"after_val"`             // 操作后值（JSON 字符串，用于变更轨迹比对）
	CreatedAt  string `json:"created_at"`            // 日志创建时间（RFC3339 字符串）
	TenantName string `json:"tenant_name,omitempty"` // 归属租户名（超管全量视图 JOIN 填充）
	Username   string `json:"username,omitempty"`    // 操作者用户名（JOIN users 填充）
}

// LogAudit 记录审计日志（不含前后值快照的便捷入口）。
// 参数：tid=租户 ID，userID=操作者 ID，action=动作，resource=资源类型，detail=详情描述。
func (s *Store) LogAudit(tid, userID int64, action, resource, detail string) {
	s.LogAuditDiff(tid, userID, action, resource, detail, "", "")
}

// LogAuditDiff 记录审计日志（含结构化前后值，供变更轨迹比对）。
// 参数：tid/userID=租户与操作者，action=动作，resource=资源，detail=描述，
// beforeVal=操作前值（JSON），afterVal=操作后值（JSON）。
func (s *Store) LogAuditDiff(tid, userID int64, action, resource, detail, beforeVal, afterVal string) {
	// 写入审计表；失败静默忽略（审计不应阻塞业务主流程）
	_, _ = s.db.Exec(
		"INSERT INTO audit_logs (tenant_id, user_id, action, resource, detail, before_val, after_val, created_at) VALUES (?,?,?,?,?,?,?,?)",
		tid, userID, action, resource, detail, beforeVal, afterVal, time.Now().Format(time.RFC3339))
}

// ListAuditFilter 查询审计日志（租户隔离；可按动作/资源/用户/时间范围过滤）。
// 参数：tid=租户 ID（必填），action/resource=过滤条件（空=不过滤），userID=操作者（<=0 不过滤），
// from/to=时间范围（RFC3339，to 自动补到当日 23:59:59），limit=条数上限（默认 100，最大 1000）。
// 返回：审计日志列表，按 ID 倒序。
func (s *Store) ListAuditFilter(tid int64, action, resource string, userID int64, from, to string, limit int) ([]*AuditLog, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100 // 非法 limit 收敛到默认 100
	}
	var query string
	var args []interface{}
	if tid <= 0 {
		// 平台全量视图（超管）：附租户名与操作者用户名
		query = "SELECT a.id, a.tenant_id, a.user_id, a.action, a.resource, a.detail, a.before_val, a.after_val, a.created_at, COALESCE(t.name,'平台'), COALESCE(u.username,'') " +
			"FROM audit_logs a LEFT JOIN tenants t ON t.id=a.tenant_id LEFT JOIN users u ON u.id=a.user_id WHERE 1=1"
	} else {
		query = "SELECT a.id, a.tenant_id, a.user_id, a.action, a.resource, a.detail, a.before_val, a.after_val, a.created_at, '', '' " +
			"FROM audit_logs a WHERE a.tenant_id=?"
		args = append(args, tid)
	}
	if action != "" {
		query += " AND action=?" // 按动作过滤
		args = append(args, action)
	}
	if resource != "" {
		query += " AND resource=?" // 按资源类型过滤
		args = append(args, resource)
	}
	if userID > 0 {
		query += " AND user_id=?" // 按操作者过滤
		args = append(args, userID)
	}
	if from != "" {
		query += " AND created_at>=?" // 起始时间下限
		args = append(args, from)
	}
	if to != "" {
		query += " AND created_at<=?" // 截止时间上限（补足当日 23:59:59，覆盖整天）
		args = append(args, to+"T23:59:59Z")
	}
	query += " ORDER BY a.id DESC LIMIT ?"
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*AuditLog
	for rows.Next() {
		var a AuditLog
		if err := rows.Scan(&a.ID, &a.TenantID, &a.UserID, &a.Action, &a.Resource, &a.Detail, &a.BeforeVal, &a.AfterVal, &a.CreatedAt, &a.TenantName, &a.Username); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &a)
	}
	return out, nil
}

// ListAudit 查询审计日志（租户隔离；可按动作过滤）。
// 参数：tid=租户 ID，limit=条数上限；返回审计日志列表（内部复用 ListAuditFilter 全条件通配）。
func (s *Store) ListAudit(tid int64, limit int) ([]*AuditLog, error) {
	return s.ListAuditFilter(tid, "", "", 0, "", "", limit)
}
