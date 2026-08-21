// ============ 本文件职责中文说明 ============
// 工单（tickets / ticket_state 表）数据访问层：工单 CRUD、租户隔离查询、
// 待审批列表，以及工单状态轨迹（每步运行快照，版本递增，供前端流程进度展示）。
// 工单状态机：draft → in_progress → pending_approval → approved / rejected → completed。
// =============================================
package store

import (
	"database/sql"
	"time"
)

// Ticket 工单
type Ticket struct {
	ID           int64  `json:"id"`            // 工单主键 ID
	TenantID     int64  `json:"tenant_id"`     // 所属租户 ID
	TicketNo     string `json:"ticket_no"`     // 工单号（T + 时间戳 + 随机后缀）
	Title        string `json:"title"`         // 工单标题
	Status       string `json:"status"`        // 状态：draft/in_progress/pending_approval/approved/rejected/completed
	SourceText   string `json:"source_text"`   // 待翻译源文本
	FilePath     string `json:"file_path"`     // 关联上传文件路径（空表示纯文本）
	TargetLangs  string `json:"target_langs"`  // 目标语言列表（逗号分隔）
	CreatedBy    int64  `json:"created_by"`    // 创建者用户 ID
	ApproverID   int64  `json:"approver_id"`   // 审批人用户 ID（0 表示未分配）
	ReviewerID   int64  `json:"reviewer_id"`   // 审校人用户 ID（0 表示未分配）
	RejectReason string `json:"reject_reason"` // 驳回原因（被驳回时填写，重翻时使用）
	FinalResult  string `json:"final_result"`  // 最终结果（JSON：含各语言译文及中间轨迹）
	ResultPath   string `json:"result_path,omitempty"` // 结果文件路径（原格式回写产物/xlsx 对照表；空=未生成）
	CreatedAt    string `json:"created_at"`    // 创建时间（RFC3339 字符串）
	UpdatedAt    string `json:"updated_at"`    // 更新时间（RFC3339 字符串）
}

// TicketState 工单状态轨迹（Projector 物化）
type TicketState struct {
	ID        int64  `json:"id"`         // 轨迹记录主键 ID
	TicketID  int64  `json:"ticket_id"`  // 关联工单 ID
	Step      string `json:"step"`       // 流程步骤标识（如 kb_match / gate）
	Status    string `json:"status"`     // 该步骤状态：pending/running/success/failed/skipped
	Payload   string `json:"payload"`    // 步骤轨迹快照（JSON，如命中记录）
	Version   int    `json:"version"`    // 步骤版本号（同步骤递增）
	UpdatedAt string `json:"updated_at"` // 更新时间（RFC3339 字符串）
}

// 工单状态
const (
	TicketDraft       = "draft"            // 草稿
	TicketQueued      = "queued"           // 已入队待执行（异步队列）
	TicketInProgress  = "in_progress"      // 处理中
	TicketPendingAppr = "pending_approval" // 待审批
	TicketApproved    = "approved"         // 已批准
	TicketRejected    = "rejected"         // 已驳回
	TicketCompleted   = "completed"        // 已完成
)

// CreateTicket 创建工单（初始状态为草稿）。
// 参数：tid=租户 ID，userID=创建者 ID，title=标题，sourceText=源文本，
// filePath=文件路径，targetLangs=目标语言列表（逗号分隔）。
// 返回：新工单对象。
func (s *Store) CreateTicket(tid, userID int64, title, sourceText, filePath, targetLangs string) (*Ticket, error) {
	now := time.Now()
	t := &Ticket{
		TenantID:    tid,
		TicketNo:    "T" + now.Format("20060102150405") + randSuffix(3), // 生成唯一工单号
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
	t.ID = id // 回填自增主键
	return t, nil
}

// GetTicket 按 id 查询工单（租户隔离校验）。
// 参数：id=工单主键 ID，tid=租户 ID；返回工单对象。
func (s *Store) GetTicket(id, tid int64) (*Ticket, error) {
	var t Ticket
	err := s.db.QueryRow("SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), created_at, updated_at FROM tickets WHERE id=? AND tenant_id=?", id, tid).
		Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTicketGlobal 按 ID 查询工单（不带租户过滤，worker 异步上下文用）。
func (s *Store) GetTicketGlobal(id int64) (*Ticket, error) {
	row := s.db.QueryRow("SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), created_at, updated_at FROM tickets WHERE id=?", id)
	return scanTicketFull(row)
}

// SetTicketResultPath 写入结果文件路径。
func (s *Store) SetTicketResultPath(id int64, path string) error {
	_, err := s.db.Exec("UPDATE tickets SET result_path=?, updated_at=? WHERE id=?", path, time.Now().Format(time.RFC3339), id)
	return err
}


// ListTickets 工单列表（租户隔离；onlyMine=true 时只返回当前用户创建的）。
// 参数：tid=租户 ID，userID=用户 ID，onlyMine=是否仅我的工单。
// 返回：工单列表（最多 200 条，按 ID 倒序）。
func (s *Store) ListTickets(tid, userID int64, onlyMine bool) ([]*Ticket, error) {
	q := "SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), created_at, updated_at FROM tickets WHERE tenant_id=?"
	args := []interface{}{tid}
	if onlyMine {
		q += " AND created_by=?" // 只看自己创建的
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
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &t)
	}
	return out, nil
}

// ListPendingApproval 待审批工单列表（供 approver/admin 审批台使用）。
// 参数：tid=租户 ID；返回状态为 pending_approval/approved/rejected 的工单。
func (s *Store) ListPendingApproval(tid int64) ([]*Ticket, error) {
	rows, err := s.db.Query("SELECT id, tenant_id, ticket_no, title, status, source_text, file_path, target_langs, created_by, approver_id, reviewer_id, reject_reason, final_result, COALESCE(result_path,''), created_at, updated_at FROM tickets WHERE tenant_id=? AND status IN ('pending_approval','approved','rejected') ORDER BY id DESC LIMIT 200", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.CreatedAt, &t.UpdatedAt); err != nil {
			continue // 单行解析失败跳过
		}
		out = append(out, &t)
	}
	return out, nil
}

// UpdateTicket 更新工单状态与字段。
// 参数：t=待更新工单对象（以 ID+TenantID 定位）；返回错误。
func (s *Store) UpdateTicket(t *Ticket) error {
	_, err := s.db.Exec(
		"UPDATE tickets SET title=?, status=?, target_langs=?, approver_id=?, reviewer_id=?, reject_reason=?, final_result=?, updated_at=? WHERE id=? AND tenant_id=?",
		t.Title, t.Status, t.TargetLangs, t.ApproverID, t.ReviewerID, t.RejectReason, t.FinalResult,
		time.Now().Format(time.RFC3339), t.ID, t.TenantID)
	return err
}

// SetTicketState 记录工单状态轨迹（同步骤版本递增）。
// 参数：ticketID=工单 ID，step=步骤标识，status=步骤状态，payload=轨迹快照 JSON。
func (s *Store) SetTicketState(ticketID int64, step, status, payload string) error {
	// 取当前步骤最大版本号，新记录版本 +1
	var maxVer int
	_ = s.db.QueryRow("SELECT COALESCE(MAX(version),0) FROM ticket_state WHERE ticket_id=? AND step=?", ticketID, step).Scan(&maxVer)
	_, err := s.db.Exec(
		"INSERT INTO ticket_state (ticket_id, step, status, payload, version, updated_at) VALUES (?,?,?,?,?,?)",
		ticketID, step, status, payload, maxVer+1, time.Now().Format(time.RFC3339))
	return err
}

// TicketStates 查询工单状态轨迹（按版本升序）。
// 参数：ticketID=工单 ID；返回该工单全部步骤轨迹。
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
			continue // 单行解析失败跳过
		}
		out = append(out, &st)
	}
	return out, nil
}


// scanTicketFull 扫描全列工单行（GetTicketGlobal 专用，含 result_path）。
func scanTicketFull(row *sql.Row) (*Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.TenantID, &t.TicketNo, &t.Title, &t.Status, &t.SourceText, &t.FilePath, &t.TargetLangs, &t.CreatedBy, &t.ApproverID, &t.ReviewerID, &t.RejectReason, &t.FinalResult, &t.ResultPath, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
