// ============ feedback.go · 职责说明 ============
// store 包用户反馈数据访问层（feedbacks 表）。
// 前台翻译结果反馈的写入与超管查询/处理。
// 反馈链路：前台（文本气泡/工单详情）→ POST /api/feedback → feedbacks 表
// → CreateAlert(warning,"feedback") 触达超管（复用告警邮件/群机器人链路）。
// =============================================
package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
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
	// ★ F-45（〇-U 批）：落库前在唯一的写入口再归一一次（api 侧已归一，这里是防将来
	// 出现第二个写路径时的兜底）。先改结构体再 INSERT，保证「返回给调用方的行」与
	// 「库里的行」是同一个值——否则调用方拿着 "null" 继续用，读侧还是同一场事故。
	f.Translations = normalizeTranslationsColumn(f.Translations)
	id, err := db.InsertID(s.db, db.CurrentDialect(), "id",
		`INSERT INTO feedbacks (tenant_id, user_id, target_type, ticket_id, source_text, translations, target_langs, mode, content, with_context, status, created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,'open',?)`,
		f.TenantID, f.UserID, f.TargetType, f.TicketID, f.SourceText,
		f.Translations, f.TargetLangs, f.Mode, f.Content, ctxInt, now)
	if err != nil {
		return err
	}
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
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
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
	res, err := db.Exec(s.db, db.CurrentDialect(),
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
	_ = db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(*) FROM feedbacks WHERE user_id=? AND created_at>=?", userID, day+"T00:00:00").Scan(&n)
	return n
}

// normalizeTranslationsColumn 译文上下文列的归一（★ F-45 〇-U 批）。
// 参数：raw=调用方给出的 JSON 串。返回：空串 / SQL NULL / 字面量 "null" 一律归一为 "{}"，
// 其余原样返回。
// 为什么要归一："null" 是合法 JSON 文本，能一路存进库、读出来、送到前端，
// `JSON.parse("null")` 不抛错而返回 null —— 前端 try/catch 拦不住它，
// 下一步 Object.entries(null) 才抛 TypeError，表现为「超管点进这条反馈，详情整块白屏」。
func normalizeTranslationsColumn(raw string) string {
	switch strings.TrimSpace(raw) {
	case "", "null":
		return "{}"
	}
	return raw
}

// feedbackMigrate 老库补 replies 列（幂等）+ 清洗译文上下文的 "null" 脏行（★ F-45）。
// 清洗写成幂等 UPDATE：老库里已经存着的 "null" 行不随代码修复消失，
// 而前端读侧的「必须是对象」守卫只管新形态渲染，历史详情仍会崩，故两侧都要动。
// 只清 'null'（真正会让 Object.entries 崩的形态），不动 ”：
// 空串是本列的合法默认值（NOT NULL DEFAULT ”，未勾选上下文的反馈与线索都是它），
// 把它整表改写成 '{}' 属于「无收益的大面积数据改动」，读侧本就把 ” 当无上下文。
// IS NULL 分支是给「后来用 EnsureColumns 补过这列的老库」留的兜底（补列可能允许 NULL）。
func (s *Store) feedbackMigrate() {
	_, _ = db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE feedbacks ADD COLUMN replies TEXT NOT NULL DEFAULT '[]'")
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE feedbacks SET translations='{}' WHERE translations='null' OR translations IS NULL")
	if err != nil {
		// 迁移失败不阻断启动（与补列同口径）：读侧已有「必须是对象」守卫兜底，界面不会再白屏。
		observability.Warn(context.Background(), "反馈译文上下文脏行清洗失败（不阻断启动，读侧已兜底）", "err", err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		observability.Info(context.Background(), "反馈译文上下文脏行已清洗", "rows", n)
	}
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
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
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
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE feedbacks SET replies=? WHERE id=?", replyJSON, id)
	return err
}

// GetFeedback 按 ID 取单条反馈（不存在返回 nil, err）。
func (s *Store) GetFeedback(id int64) (*Feedback, error) {
	row := db.QueryRow(s.db, db.CurrentDialect(), "SELECT "+feedbackCols+" FROM feedbacks WHERE id=?", id)
	f, err := scanFeedback(row)
	if err != nil {
		return nil, err
	}
	return f, nil
}
