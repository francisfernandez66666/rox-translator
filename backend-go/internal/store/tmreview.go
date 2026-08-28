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
	"encoding/json"
	"regexp"
	"strings"
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

// ============ 数据回流治理（评审整改 D7，白皮书 §七.4） ============

// phoneRe 手机号正则（脱敏用）；maskPII 对原文/译文做最小侵入掩码后入审核池。
var phoneRe = regexp.MustCompile(`1[3-9]\d{9}`)

// maskPII 脱敏：手机号保留前三后二；邮箱本地域打码。
func maskPII(s string) string {
	if s == "" {
		return s
	}
	s = phoneRe.ReplaceAllStringFunc(s, func(m string) string {
		return m[:3] + "****" + m[len(m)-2:]
	})
	emailRe := regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+`)
	s = emailRe.ReplaceAllStringFunc(s, func(m string) string {
		at := len(m) - len(m[strings.LastIndexByte(m, '@'):])
		return m[:min(2, at)] + "***" + m[strings.LastIndexByte(m, '@'):]
	})
	return s
}

// feedbackOptOut 读取租户 policy_config.data_feedback_opt_out（store 内联解析，
// 避免 store→tenant 反向依赖）。true=该租户已关闭数据回流。
func (s *Store) feedbackOptOut(tid int64) bool {
	var raw string
	if err := s.db.QueryRow("SELECT COALESCE(policy_config,'{}') FROM tenants WHERE id=?", tid).Scan(&raw); err != nil {
		return false // 查询异常按「参与」处理（与默认开启语义一致）
	}
	var pc struct {
		DataFeedbackOptOut *int `json:"data_feedback_opt_out,omitempty"`
	}
	if json.Unmarshal([]byte(raw), &pc) != nil {
		return false
	}
	return pc.DataFeedbackOptOut != nil && *pc.DataFeedbackOptOut == 1
}

// CreateTmReview 新增候选。★ D7：opt_out 租户静默跳过（只计数不入池）；入库前 PII 脱敏。
func (s *Store) CreateTmReview(r *TmReview) error {
	if r.TenantID > 0 && s.feedbackOptOut(r.TenantID) {
		return nil // 租户关闭回流：不进入平台审核池
	}
	r.Zh = maskPII(r.Zh)
	r.Trans = maskPII(r.Trans)
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
// ★ D7：opt_out 租户只累计计数、不产生候选不入审核池（私有 TM 不受影响）。
func (s *Store) BumpTmHit(tid int64, zh, lang, trans string, threshold int64) (int64, bool, error) {
	if tid <= 0 || zh == "" || trans == "" || zh == trans {
		return 0, false, nil
	}
	zh = maskPII(zh)
	trans = maskPII(trans)
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
	if s.feedbackOptOut(tid) {
		return n, false, nil // 关闭回流：只计数不产候选
	}
	cr := &TmReview{TenantID: tid, Zh: zh, Lang: lang, Trans: trans,
		Source: "hit_threshold", RefType: "ticket", HitCount: n}
	if err := s.CreateTmReview(cr); err != nil {
		return n, false, err
	}
	return n, true, nil
}

// TmHitPair 批量计数的单条输入。
type TmHitPair struct {
	Zh    string
	Lang  string
	Trans string
}

// BumpTmHitsBatch 批量累计翻译记忆命中（性能优化 B5）：把一次工单内全部
// (原文,语言,译文) 去重后在单个事务内批量 upsert，避免逐条 INSERT+SELECT 在大型文件下
// 产生数千次 DB 往返。达到阈值且未关闭回流的条目生成待审候选。
// 返回本次新生成候选的原文预览（用于上层告警），无则空切片。
func (s *Store) BumpTmHitsBatch(tid int64, pairs []TmHitPair, threshold int64) []string {
	if tid <= 0 || len(pairs) == 0 {
		return nil
	}
	// 去重（同 (zh,lang,trans) 在文件内可能多次出现，合并计数）
	uniq := make([]TmHitPair, 0, len(pairs))
	seen := map[string]bool{}
	for _, p := range pairs {
		if p.Zh == "" || p.Trans == "" || p.Zh == p.Trans {
			continue
		}
		key := tmHash(p.Zh) + "\x00" + p.Lang + "\x00" + tmHash(p.Trans)
		if seen[key] {
			continue
		}
		seen[key] = true
		uniq = append(uniq, TmHitPair{maskPII(p.Zh), p.Lang, maskPII(p.Trans)})
	}
	if len(uniq) == 0 {
		return nil
	}
	// 单事务批量 upsert 计数
	tx, err := s.db.Begin()
	if err != nil {
		return nil
	}
	defer tx.Rollback()
	for _, p := range uniq {
		if _, e := tx.Exec(`INSERT INTO tm_hit_count (tenant_id, zh_hash, lang, trans_hash, zh, trans, n)
			VALUES (?,?,?,?,?,?,1)
			ON CONFLICT(tenant_id, zh_hash, lang, trans_hash) DO UPDATE SET n=n+1`,
			tid, tmHash(p.Zh), p.Lang, tmHash(p.Trans), p.Zh, p.Trans); e != nil {
			return nil
		}
	}
	if err := tx.Commit(); err != nil {
		return nil
	}
	// 阈值判定 + 生成待审候选（仅在首次达到阈值时）
	if s.feedbackOptOut(tid) {
		return nil
	}
	var created []string
	for _, p := range uniq {
		var n int64
		if err := s.db.QueryRow(`SELECT n FROM tm_hit_count WHERE tenant_id=? AND zh_hash=? AND lang=? AND trans_hash=?`,
			tid, tmHash(p.Zh), p.Lang, tmHash(p.Trans)).Scan(&n); err != nil {
			continue
		}
		if n < threshold {
			continue
		}
		if s.HasActiveTmReview(tid, p.Zh, p.Lang, p.Trans) {
			continue
		}
		cr := &TmReview{TenantID: tid, Zh: p.Zh, Lang: p.Lang, Trans: p.Trans,
			Source: "hit_threshold", RefType: "ticket", HitCount: n}
		if err := s.CreateTmReview(cr); err != nil {
			continue
		}
		preview := p.Zh
		if len([]rune(preview)) > 50 {
			preview = string([]rune(preview)[:50]) + "…"
		}
		created = append(created, preview)
	}
	return created
}
