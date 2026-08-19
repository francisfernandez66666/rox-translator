package store

import (
	"time"
)

// EvalRecord 评估记录
type EvalRecord struct {
	ID        int64   `json:"id"`
	TenantID  int64   `json:"tenant_id"`
	UserID    int64   `json:"user_id"`
	TicketID  int64   `json:"ticket_id"`
	TaskType  string  `json:"task_type"`
	Model     string  `json:"model"`
	InputText string  `json:"input_text"`
	OutputText string `json:"output_text"`
	Scores    string  `json:"scores"`
	Total     float64 `json:"total"`
	Status    string  `json:"status"` // passed/failed/retried/skipped
	CreatedAt string  `json:"created_at"`
}

// SaveEvalRecord 保存评估记录
func (s *Store) SaveEvalRecord(r *EvalRecord) (int64, error) {
	res, err := s.db.Exec(
		"INSERT INTO eval_records (tenant_id, user_id, ticket_id, task_type, model, input_text, output_text, scores, total, status, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
		r.TenantID, r.UserID, r.TicketID, r.TaskType, r.Model, r.InputText, r.OutputText, r.Scores, r.Total, r.Status, time.Now().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListEvalRecords 评估记录列表（租户隔离）
func (s *Store) ListEvalRecords(tid int64, limit int) ([]*EvalRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
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
			continue
		}
		out = append(out, &r)
	}
	return out, nil
}