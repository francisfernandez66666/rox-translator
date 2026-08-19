package store

import (
	"time"
)

// Ticket 工单
type Ticket struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	TicketNo    string `json:"ticket_no"`
	Title       string `json:"title"`
	Status      string `json:"status"` // draft/in_progress/pending_approval/approved/rejected/completed
	SourceText  string `json:"source_text"`
	FilePath    string `json:"file_path"`
	TargetLangs string `json:"target_langs"`
	CreatedBy   int64  `json:"created_by"`
	ApproverID  int64  `json:"approver_id"`
	ReviewerID  int64  `json:"reviewer_id"`
	RejectReason string `json:"reject_reason"`
	FinalResult string `json:"final_result"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// TicketState 工单状态轨迹（Projector 物化）
type TicketState struct {
	ID        int64  `json:"id"`
	TicketID  int64  `json:"ticket_id"`
	Step      string `json:"step"`
	Status    string `json:"status"` // pending/running/success/failed/skipped
	Payload   string `json:"payload"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

// 工单状态
const (
	TicketDraft         = "draft"
	TicketInProgress    = "in_progress"
	TicketPendingAppr   = "pending_approval"
	TicketApproved      = "approved"
	TicketRejected      = "rejected"
	TicketCompleted     = "completed"
)

// CreateTicket 创建工单
func (s *Store) CreateTicket(tid, userID int64, title, sourceText, filePath, targetLangs string) (*Ticket, error) {
	now := time.Now()
	t := &Ticket{
		TenantID:    tid,
		TicketNo:    "T" + now.Format("20060102150405") + randSuffix(3),
		Title:       title,
		Status:      TicketDraft,
		SourceText:  sourceText,
		FilePath:    filePath,
		TargetLangs: targetLangs,
		CreatedBy:   userID,
		CreatedAt:   now.Format(time.RFC3339),
		UpdatedAt:   now.Format(time.RFC3339),
	}
	res, err := s.db.Exec(
		"INSERT INTO tickets (tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		t.TenantID, t.TicketNo, t.Title, t.Status, t.SourceText, t.FilePath, t.TargetLangs, t.CreatedBy, t.CreatedAt, t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	t.ID = id
	return t, nil
}

// GetTicket 按 id 查询（租户隔离）
func (s *Store) GetTicket(id, tid int64) (*Ticket, error) {
	var t Ticket
	err := s.db.QueryRow("SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, created_at, updated_at FROM tickets WHERE id=? AND tenant_id=?", id, tid).
		Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// ListTickets 工单列表（租户隔离）
func (s *Store) ListTickets(tid, userID int64, onlyMine bool) ([]*Ticket, error) {
	q := "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, created_at, updated_at FROM tickets WHERE tenant_id=?"
	args := []interface{}{tid}
	if onlyMine {
		q += " AND created_by=?"
		args = append(args, userID)
	}
	q += " ORDER BY id DESC LIMIT 200"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &t)
	}
	return out, nil
}

// ListPendingApproval 待审批工单（approver/admin）
func (s *Store) ListPendingApproval(tid int64) ([]*Ticket, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, created_at, updated_at FROM tickets WHERE tenant_id=? AND status IN ('pending_approval','approved','rejected') ORDER BY id DESC LIMIT 200", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &t)
	}
	return out, nil
}

// UpdateTicket 更新工单状态/字段
func (s *Store) UpdateTicket(t *Ticket) error {
	_, err := s.db.Exec(
		"UPDATE tickets SET title=?, status=?, target_langs=?, approver_id=?, reviewer_id=?, reject_reason=?, final_result=?, updated_at=? WHERE id=? AND tenant_id=?",
		t.Title, t.Status, t.TargetLangs, t.ApproverID, t.ReviewerID, t.RejectReason, t.FinalResult,
		time.Now().Format(time.RFC3339), t.ID, t.TenantID)
	return err
}

// SetTicketState 记录工单状态轨迹（版本递增）
func (s *Store) SetTicketState(ticketID int64, step, status, payload string) error {
	var maxVer int
	_ = s.db.QueryRow("SELECT COALESCE(MAX(version),0) FROM ticket_state WHERE ticket_id=? AND step=?", ticketID, step).Scan(&maxVer)
	_, err := s.db.Exec(
		"INSERT INTO ticket_state (ticket_id, step, status, payload, version, updated_at) VALUES (?,?,?,?,?,?)",
		ticketID, step, status, payload, maxVer+1, time.Now().Format(time.RFC3339))
	return err
}

// TicketStates 工单状态轨迹
func (s *Store) TicketStates(ticketID int64) ([]*TicketState, error) {
	rows, err := s.db.Query("SELECT id, ticket_id, step, status, payload, version, updated_at FROM ticket_state WHERE ticket_id=? ORDER BY version", ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*TicketState
	for rows.Next() {
		var st TicketState
		if err := rows.Scan(&st.ID, &st.TicketID, &st.Step, &st.Status, &st.Payload, &st.Version, &st.UpdatedAt); err != nil {
			continue
		}
		out = append(out, &st)
	}
	return out, nil
}