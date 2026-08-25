// ============ 本文件职责中文说明 ============
// 用户反馈数据访问层（feedbacks 表）：前台翻译结果反馈的写入与超管查询/处理。
// 反馈链路：前台（文本气泡/工单详情）→ POST /api/feedback → feedbacks 表
// → CreateAlert(warning,"feedback") 触达超管（复用告警邮件/群机器人链路）。
// =============================================
package store

import (
	"database/sql"
	"time"
)

// Feedback 用户反馈记录
type Feedback struct {
	ID           int64  `json:"id"`           // 主键 ID
	TenantID     int64  `json:"tenant_id"`    // 所属租户 ID
	UserID       int64  `json:"user_id"`      // 反馈用户 ID
	TargetType   string `json:"target_type"`  // 反馈对象：text | ticket
	TicketID     int64  `json:"ticket_id"`    // 工单 ID（text 类型为 0）
	SourceText   string `json:"source_text"`  // 源文上下文（with_context=1 时有值）
	Translations string `json:"translations"` // 译文 JSON 上下文（with_context=1 时有值）
	TargetLangs  string `json:"target_langs"` // 目标语言列表
	Mode         string `json:"mode"`         // 翻译模式：fast | pro
	Content      string `json:"content"`      // 反馈意见
	WithContext  bool   `json:"with_context"` // 是否附带上下文
	Replies      string `json:"replies"`      // BBS 回复线程 JSON：[{u,role,content,at}]
	Status       string `json:"status"`       // open(反馈中) | resolved(已完成)
	HandleNote   string `json:"handle_note"`  // 超管处理备注
	CreatedAt    string `json:"created_at"`   // 反馈时间
	HandledAt    string `json:"handled_at"`   // 处理时间（空=未处理）
}

// feedbackCols 反馈表查询列清单（Scan 顺序契约；replies/handled_at 为老库可空列，COALESCE 兜底）。
const feedbackCols = "id, tenant_id, user_id, target_type, ticket_id, source_text, translations, target_langs, mode, content, with_context, status, handle_note, COALESCE(replies,'[]'), created_at, COALESCE(handled_at,'')"

// CreateFeedback 写入一条用户反馈。
// 参数：f=反馈对象（Content 必填）；返回错误。
func (s *Store) CreateFeedback(f *Feedback) error {
	now := time.Now().Format(time.RFC3339)
	f.CreatedAt = now
	f.Status = "open"
	ctxInt := 0
	if f.WithContext {
		ctxInt = 1
	}
	res, err := s.db.Exec(
		`INSERT INTO feedbacks (tenant_id, user_id, target_type, ticket_id, source_text, translations, target_langs, mode, content, with_context, status, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,'open',?)`,
		f.TenantID, f.UserID, f.TargetType, f.TicketID, f.SourceText, f.Translations,
		f.TargetLangs, f.Mode, f.Content, ctxInt, now)
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	f.ID = id
	return nil
}

// ListFeedbacks 反馈列表（超管用；status 为空返回全部；按 ID 倒序最多 200 条）。
func (s *Store) ListFeedbacks(status string) ([]*Feedback, error) {
	q := "SELECT " + feedbackCols + " FROM feedbacks"
	args := []interface{}{}
	if status != "" {
		q += " WHERE status=?"
		args = append(args, status)
	}
	q += " ORDER BY id DESC LIMIT 200"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Feedback
	for rows.Next() {
		f, perr := scanFeedback(rows)
		if perr != nil {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// feedbackScanner 抽象行扫描来源（*sql.Rows）
type feedbackScanner interface {
	Scan(dest ...interface{}) error
}

// scanFeedback 扫描单行反馈记录。
func scanFeedback(row feedbackScanner) (*Feedback, error) {
	var f Feedback
	var ctxInt int
	if err := row.Scan(&f.ID, &f.TenantID, &f.UserID, &f.TargetType, &f.TicketID,
		&f.SourceText, &f.Translations, &f.TargetLangs, &f.Mode, &f.Content,
		&ctxInt, &f.Status, &f.HandleNote, &f.Replies, &f.CreatedAt, &f.HandledAt); err != nil {
		return nil, err
	}
	f.WithContext = ctxInt == 1
	return &f, nil
}

// ResolveFeedback 超管标记反馈已处理并附备注。
func (s *Store) ResolveFeedback(id int64, note string) error {
	res, err := s.db.Exec(
		"UPDATE feedbacks SET status='resolved', handle_note=?, handled_at=? WHERE id=?",
		note, time.Now().Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// CountFeedbacksToday 统计用户当日已提交反馈数（限流用：同用户每天 ≤20 条）。
func (s *Store) CountFeedbacksToday(userID int64) int64 {
	day := time.Now().Format("2006-01-02")
	var n int64
	_ = s.db.QueryRow(
		"SELECT COUNT(*) FROM feedbacks WHERE user_id=? AND created_at>=?", userID, day+"T00:00:00").Scan(&n)
	return n
}

// feedbackMigrate 老库补 replies 列（幂等）。
func (s *Store) feedbackMigrate() {
	_, _ = s.db.Exec("ALTER TABLE feedbacks ADD COLUMN replies TEXT NOT NULL DEFAULT '[]'")
}

// ListFeedbacksByUser 查询某用户提交的全部反馈（BBS 我的反馈视图）。status 为空=全部。
func (s *Store) ListFeedbacksByUser(userID int64, status string) ([]*Feedback, error) {
	q := "SELECT " + feedbackCols + " FROM feedbacks WHERE user_id=?"
	args := []interface{}{userID}
	if status != "" {
		q += " AND status=?"
		args = append(args, status)
	}
	q += " ORDER BY id DESC LIMIT 200"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Feedback
	for rows.Next() {
		f, perr := scanFeedback(rows)
		if perr != nil {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// AppendFeedbackReply 向回复线程追加一条（BBS 模式；超管与提交者均可写）。
// 参数：id=反馈 ID；replyJSON=完整线程 JSON（调用方读改写，避免并发覆盖场景复杂化——当前量级可接受）。
func (s *Store) AppendFeedbackReply(id int64, replyJSON string) error {
	_, err := s.db.Exec("UPDATE feedbacks SET replies=? WHERE id=?", replyJSON, id)
	return err
}

// GetFeedback 按 ID 取单条反馈（不存在返回 nil, err）。
func (s *Store) GetFeedback(id int64) (*Feedback, error) {
	row := s.db.QueryRow("SELECT " + feedbackCols + " FROM feedbacks WHERE id=?", id)
	f, err := scanFeedback(row)
	if err != nil {
		return nil, err
	}
	return f, nil
}
