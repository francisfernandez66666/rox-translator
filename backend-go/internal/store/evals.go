// ============ 本文件职责中文说明 ============
// 翻译质量评估记录（eval_records 表）数据访问层：保存/查询 LLM-as-Judge 的 5 维评分结果。
// 每行记录一次评估的输入文本、输出文本、各维分数（JSON）与加权总分及通过状态。
// =============================================
package store

import (
	"time"
)

// EvalRecord 评估记录
type EvalRecord struct {
	ID         int64   `json:"id"`          // 评估记录主键 ID
	TenantID   int64   `json:"tenant_id"`   // 所属租户 ID
	UserID     int64   `json:"user_id"`     // 发起评估的用户 ID
	TicketID   int64   `json:"ticket_id"`   // 关联工单 ID（0 表示不关联工单）
	TaskType   string  `json:"task_type"`   // 任务类型：translate / review / evals / gate
	Model      string  `json:"model"`       // 被评估的模型/语言标识
	InputText  string  `json:"input_text"`  // 评估输入（原文）
	OutputText string  `json:"output_text"` // 评估输出（译文）
	Scores     string  `json:"scores"`      // 各维分数（JSON：term/grammar/semantic/numunit/style）
	Total      float64 `json:"total"`       // 加权总分（0-100）
	Status     string  `json:"status"`      // 评估状态：passed / failed / retried / skipped
	CreatedAt  string  `json:"created_at"`  // 评估时间（RFC3339 字符串）
}

// SaveEvalRecord 保存一条评估记录，返回自增主键 ID。
// 参数：r=评估记录结构体（TenantID/UserID/TicketID/TaskType/Model/InputText/OutputText/Scores/Total/Status 必填）。
// 返回：新记录 ID 或错误。
func (s *Store) SaveEvalRecord(r *EvalRecord) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO eval_records (tenant_id, user_id, ticket_id, task_type, model, input_text, output_text, scores, total, status, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		r.TenantID, r.UserID, r.TicketID, r.TaskType, r.Model, r.InputText, r.OutputText, r.Scores, r.Total, r.Status, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListEvalRecords 查询评估记录列表（租户隔离）。
// 参数：tid=租户 ID，limit=条数上限（默认 100，最大 500）。
// 返回：评估记录列表，按 ID 倒序。
func (s *Store) ListEvalRecords(tid int64, limit int) ([]*EvalRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100 // 非法 limit 收敛到默认 100
	}
	rows, err := s.db.Query("SELECT id, tenant_id, user_id, ticket_id, task_type, model, input_text, output_text, scores, total, status, created_at FROM eval_records WHERE tenant_id=? ORDER BY id DESC LIMIT ?", tid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*EvalRecord
	for rows.Next() {
		var r EvalRecord
		if err := rows.Scan(&r.ID, &r.TenantID, &r.UserID, &r.TicketID, &r.TaskType, &r.Model, &r.InputText, &r.OutputText, &r.Scores, &r.Total, &r.Status, &r.CreatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &r)
	}
	return out, nil
}
