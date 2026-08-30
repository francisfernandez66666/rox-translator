// ============ 本文件职责中文说明 ============
// 对照编辑器数据层：translation_edits 表的读写，以及术语高亮所需的租户术语列表查询。
// 与工单（tickets）/知识库条目（kb_entries）解耦，仅依赖 store 的连接与方言适配。
// =============================================
package store

import (
	"time"

	"translator/internal/db"
)

// TranslationEdit 对照编辑器单段编辑记录。
type TranslationEdit struct {
	ID          int64  `json:"id"`
	TenantID    int64  `json:"tenant_id"`
	TicketID    int64  `json:"ticket_id"`
	Lang        string `json:"lang"`
	SegIndex    int    `json:"seg_index"`
	SourceText  string `json:"source_text"`
	TargetText  string `json:"target_text"`
	EditedText  string `json:"edited_text"`
	Status      string `json:"status"` // pending / approved / rejected
	Note        string `json:"note"`
	ReviewerID  int64  `json:"reviewer_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// UpsertTranslationEdit 写入或更新单段编辑（按 ticket_id+lang+seg_index 唯一）。
// 参数：租户/工单/语言/段序号/源文/系统译文/修订译文/状态/批注/操作人。
func (s *Store) UpsertTranslationEdit(tenantID, ticketID int64, lang string, segIndex int,
	source, target, edited, status, note string, reviewerID int64) error {
	now := time.Now().Format(time.RFC3339)
	// SQLite 用 INSERT ... ON CONFLICT；PostgreSQL 用 ON CONFLICT DO UPDATE（同语义）。
	q := `INSERT INTO translation_edits
		(tenant_id, ticket_id, lang, seg_index, source_text, target_text, edited_text, status, note, reviewer_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(ticket_id, lang, seg_index) DO UPDATE SET
		source_text=excluded.source_text, target_text=excluded.target_text,
		edited_text=excluded.edited_text, status=excluded.status,
		note=excluded.note, reviewer_id=excluded.reviewer_id, updated_at=excluded.updated_at`
	if db.CurrentDialect() == db.DialectPostgres {
		q = `INSERT INTO translation_edits
			(tenant_id, ticket_id, lang, seg_index, source_text, target_text, edited_text, status, note, reviewer_id, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT(ticket_id, lang, seg_index) DO UPDATE SET
			source_text=EXCLUDED.source_text, target_text=EXCLUDED.target_text,
			edited_text=EXCLUDED.edited_text, status=EXCLUDED.status,
			note=EXCLUDED.note, reviewer_id=EXCLUDED.reviewer_id, updated_at=EXCLUDED.updated_at`
	}
	_, err := db.Exec(s.db, db.CurrentDialect(), q, tenantID, ticketID, lang, segIndex, source, target, edited, status, note, reviewerID, now, now)
	return err
}

// GetTranslationEdits 读取某工单某语言的全部段编辑（含系统译文与用户修订）。
func (s *Store) GetTranslationEdits(ticketID int64, lang string) ([]TranslationEdit, error) {
	rows, err := s.db.Query(
		`SELECT id, tenant_id, ticket_id, lang, seg_index, source_text, target_text, edited_text, status, note, reviewer_id, created_at, updated_at
		 FROM translation_edits WHERE ticket_id=? AND lang=? ORDER BY seg_index ASC`, ticketID, lang)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TranslationEdit
	for rows.Next() {
		var e TranslationEdit
		if err := rows.Scan(&e.ID, &e.TenantID, &e.TicketID, &e.Lang, &e.SegIndex,
			&e.SourceText, &e.TargetText, &e.EditedText, &e.Status, &e.Note, &e.ReviewerID, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ListKBTerms 返回租户可见的术语表（source_text），供前端高亮。
// 参数 tenantID=租户；lang=目标语言（置空则不限）；limit=上限。返回去重后的术语串。
func (s *Store) ListKBTerms(tenantID int64, lang string, limit int) ([]string, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT DISTINCT source_text FROM kb_entries WHERE tenant_id=? AND source_text <> ''`
	var args []interface{}
	args = append(args, tenantID)
	if lang != "" {
		q += ` AND target_lang=?`
		args = append(args, lang)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var terms []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	return terms, rows.Err()
}
