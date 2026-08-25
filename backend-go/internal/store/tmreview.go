// ============ tmreview.go · 职责说明 ============
// TM 自闭环待审池：系统不再自动写入 tm_segments；所有候选先进 tm_review，
// 仅超管「通过」后才落正式库（SaveBack module='manual'）。
// 触发来源：① 用户反馈修正（ref_type=feedback）② 相同原文+译文对累计达阈值
// ③ bitext/tmx 人工导入（import，不豁免审核）。权限：全部接口仅超管。
// =============================================
package store

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// TmReview 待审候选
type TmReview struct {
	ID         int64  `json:"id"`
	TenantID   int64  `json:"tenant_id"`
	Zh         string `json:"zh"`
	Lang       string `json:"lang"`
	Trans      string `json:"trans"`
	Source     string `json:"source"`   // bitext | tmx | hit_threshold | feedback
	RefType    string `json:"ref_type"` // feedback | ticket | import
	RefID      int64  `json:"ref_id"`
	HitCount   int64  `json:"hit_count"`
	Status     string `json:"status"` // pending | approved | rejected
	Reviewer   string `json:"reviewer"`
	ReviewedAt string `json:"reviewed_at"`
	CreatedAt  string `json:"created_at"`
}

// tmReviewCols TM 待审池查询列清单（Scan 顺序契约；hit_count/reviewer/reviewed_at 为可空列，COALESCE 兜底）。
const tmReviewCols = "id, tenant_id, zh, lang, trans, source, ref_type, ref_id, COALESCE(hit_count,0), status, COALESCE(reviewer,''), COALESCE(reviewed_at,''), created_at"

// tmHash 原文指纹（SHA256 前 8 字节 hex）：同句去重与 hit_count 累加键。
func tmHash(v string) string { h := sha256.Sum256([]byte(v)); return hex.EncodeToString(h[:8]) }

// TmReviewMigrate 建表（幂等）。
func (s *Store) TmReviewMigrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS tm_review (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		zh TEXT NOT NULL, lang TEXT NOT NULL DEFAULT 'en', trans TEXT NOT NULL,
		source TEXT DEFAULT '', ref_type TEXT DEFAULT '', ref_id INTEGER DEFAULT 0,
		hit_count INTEGER DEFAULT 0, status TEXT DEFAULT 'pending',
		reviewer TEXT DEFAULT '', reviewed_at TEXT DEFAULT '', created_at TEXT)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_tm_review_status ON tm_review(status, id DESC)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS tm_hit_count (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		zh_hash TEXT NOT NULL, lang TEXT NOT NULL, trans_hash TEXT NOT NULL,
		zh TEXT, trans TEXT, n INTEGER DEFAULT 0,
		UNIQUE(tenant_id, zh_hash, lang, trans_hash))`)
}

// CreateTmReview 新增候选。
func (s *Store) CreateTmReview(r *TmReview) error {
	if r.CreatedAt == "" {
		r.CreatedAt = time.Now().Format(time.RFC3339)
	}
	if r.Status == "" {
		r.Status = "pending"
	}
	res, err := s.db.Exec(
		"INSERT INTO tm_review (tenant_id, zh, lang, trans, source, ref_type, ref_id, hit_count, status, created_at) VALUES (?,?,?,?,?,?,?,?,?,?)",
		r.TenantID, r.Zh, r.Lang, r.Trans, r.Source, r.RefType, r.RefID, r.HitCount, r.Status, r.CreatedAt)
	if err != nil {
		return err
	}
	r.ID, _ = res.LastInsertId()
	return nil
}

// ListTmReviews 列表。
func (s *Store) ListTmReviews(status string) ([]*TmReview, error) {
	q := "SELECT " + tmReviewCols + " FROM tm_review"
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
	out := []*TmReview{}
	for rows.Next() {
		var r TmReview
		if err := rows.Scan(&r.ID, &r.TenantID, &r.Zh, &r.Lang, &r.Trans, &r.Source,
			&r.RefType, &r.RefID, &r.HitCount, &r.Status, &r.Reviewer, &r.ReviewedAt, &r.CreatedAt); err != nil {
			continue
		}
		out = append(out, &r)
	}
	return out, nil
}

// GetTmReview 单条。
func (s *Store) GetTmReview(id int64) (*TmReview, error) {
	row := s.db.QueryRow("SELECT "+tmReviewCols+" FROM tm_review WHERE id=?", id)
	var r TmReview
	err := row.Scan(&r.ID, &r.TenantID, &r.Zh, &r.Lang, &r.Trans, &r.Source,
		&r.RefType, &r.RefID, &r.HitCount, &r.Status, &r.Reviewer, &r.ReviewedAt, &r.CreatedAt)
	return &r, err
}

// SetTmReviewStatus 审核状态变更。
func (s *Store) SetTmReviewStatus(id int64, status, reviewer string) error {
	_, err := s.db.Exec("UPDATE tm_review SET status=?, reviewer=?, reviewed_at=? WHERE id=?",
		status, reviewer, time.Now().Format(time.RFC3339), id)
	return err
}

// HasActiveTmReview 同对已有 pending/approved。
func (s *Store) HasActiveTmReview(tid int64, zh, lang, trans string) bool {
	var one int
	return s.db.QueryRow("SELECT 1 FROM tm_review WHERE tenant_id=? AND zh=? AND lang=? AND trans=? AND status IN ('pending','approved') LIMIT 1",
		tid, zh, lang, trans).Scan(&one) == nil
}

// BumpTmHit 计数自增；达阈值自动建候选。返回 (计数, 是否新建候选)。
func (s *Store) BumpTmHit(tid int64, zh, lang, trans string, threshold int64) (int64, bool, error) {
	if tid <= 0 || zh == "" || trans == "" || zh == trans {
		return 0, false, nil
	}
	now := time.Now().Format(time.RFC3339)
	if _, err := s.db.Exec(`INSERT INTO tm_hit_count (tenant_id, zh_hash, lang, trans_hash, zh, trans, n)
		VALUES (?,?,?,?,?,?,1)
		ON CONFLICT(tenant_id, zh_hash, lang, trans_hash) DO UPDATE SET n=n+1`,
		tid, tmHash(zh), lang, tmHash(trans), zh, trans); err != nil {
		return 0, false, err
	}
	var n int64
	if err := s.db.QueryRow(`SELECT n FROM tm_hit_count WHERE tenant_id=? AND zh_hash=? AND lang=? AND trans_hash=?`,
		tid, tmHash(zh), lang, tmHash(trans)).Scan(&n); err != nil {
		return 0, false, err
	}
	_ = now
	if n < threshold || s.HasActiveTmReview(tid, zh, lang, trans) {
		return n, false, nil
	}
	cr := &TmReview{TenantID: tid, Zh: zh, Lang: lang, Trans: trans,
		Source: "hit_threshold", RefType: "ticket", HitCount: n}
	if err := s.CreateTmReview(cr); err != nil {
		return n, false, err
	}
	return n, true, nil
}
