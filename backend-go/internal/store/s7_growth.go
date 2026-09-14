// ============================================================================
// store/s7_growth.go — S7 商业化触达数据层（★ 2026-09-14 拍板）
// 一张 s7_watchlist 盯两类时机：
//
//	① 挽回：体验/订阅额度耗尽 ≥48h 且从未付费 → 一次性挽回礼包（167 积分=5万
//	   内部 token / 7 天），每租户终身一次（rescued_at 置位）；
//	② 续费回访：订阅到期后 72h（T+3）未续订 → 老客回归触达（lapsed3_sent 去重）。
//
// 扫描调度在 api/watchdog（每日轮）；本文件只管状态与判定，SQL 双库兼容。
// ============================================================================
package store

import (
	"time"

	"translator/internal/db"
)

// S7Watch s7_watchlist 行（耗尽起点/挽回时间/订阅到期时间/T+3 回访标记）。
type S7Watch struct {
	TenantID    int64
	ZeroSince   string // 本轮余额清零起点（回补后清空重新计时）
	RescuedAt   string // 挽回礼包发放时间（非空=终身一次已用）
	LapsedAt    string // 最近一次订阅到期时间（续订后保留，回访判定以它+标记为准）
	Lapsed3Sent int    // T+3 老客回访已发送（1=已发，新一次到期后由扫描侧重置）
}

// ensureS7Watch 惰性建表（ migrate 主列表之外独立幂等，老库启动兼容）。
func (s *Store) ensureS7Watch() {
	db.Exec(s.db, db.CurrentDialect(), `CREATE TABLE IF NOT EXISTS s7_watchlist (
		tenant_id INTEGER PRIMARY KEY,
		zero_since TEXT NOT NULL DEFAULT '',
		rescued_at TEXT NOT NULL DEFAULT '',
		lapsed_at TEXT NOT NULL DEFAULT '',
		lapsed3_sent INTEGER NOT NULL DEFAULT 0)`)
}

// getS7WatchRow 读取观察行（无行返回零值结构）。
func (s *Store) getS7WatchRow(tid int64) (*S7Watch, error) {
	s.ensureS7Watch()
	w := &S7Watch{TenantID: tid}
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT zero_since, rescued_at, lapsed_at, lapsed3_sent FROM s7_watchlist WHERE tenant_id=?", tid).
		Scan(&w.ZeroSince, &w.RescuedAt, &w.LapsedAt, &w.Lapsed3Sent)
	if err != nil {
		return w, nil // 无行=零值（sql.ErrNoRows 与真错误不区分：只影响调度节奏）
	}
	return w, nil
}

// S7MarkZero 记录/清除「余额清零起点」：isZero=true 且尚无起点 → 落当前时间；
// isZero=false → 清空起点（充值/发放回补后重新计时）。返回当前 zero_since。
func (s *Store) S7MarkZero(tid int64, isZero bool) string {
	s.ensureS7Watch()
	w, _ := s.getS7WatchRow(tid)
	now := time.Now().UTC().Format(time.RFC3339)
	if isZero {
		if w.ZeroSince == "" {
			w.ZeroSince = now
			s.upsertS7Watch(w)
		}
		return w.ZeroSince
	}
	if w.ZeroSince != "" {
		w.ZeroSince = ""
		s.upsertS7Watch(w)
	}
	return ""
}

// S7MarkRescued 挽回礼包已发放（置位时间戳）。
func (s *Store) S7MarkRescued(tid int64) {
	w, _ := s.getS7WatchRow(tid)
	w.RescuedAt = time.Now().UTC().Format(time.RFC3339)
	s.upsertS7Watch(w)
}

// S7MarkLapsed 订阅到期事件登记（lapsed_at=到期时刻；新一期到期重置回访标记）。
func (s *Store) S7MarkLapsed(tid int64, when time.Time) {
	w, _ := s.getS7WatchRow(tid)
	w.LapsedAt = when.UTC().Format(time.RFC3339)
	w.Lapsed3Sent = 0
	s.upsertS7Watch(w)
}

// S7MarkLapsed3Sent T+3 老客回访已发送。
func (s *Store) S7MarkLapsed3Sent(tid int64) {
	w, _ := s.getS7WatchRow(tid)
	w.Lapsed3Sent = 1
	s.upsertS7Watch(w)
}

// upsertS7Watch 插入或更新观察记录（tenant_id+kind 唯一，刷新 last_seen 与备注）。
func (s *Store) upsertS7Watch(w *S7Watch) {
	db.Exec(s.db, db.CurrentDialect(), `INSERT INTO s7_watchlist (tenant_id, zero_since, rescued_at, lapsed_at, lapsed3_sent)
		VALUES (?,?,?,?,?)
		ON CONFLICT(tenant_id) DO UPDATE SET zero_since=EXCLUDED.zero_since, rescued_at=EXCLUDED.rescued_at,
			lapsed_at=EXCLUDED.lapsed_at, lapsed3_sent=EXCLUDED.lapsed3_sent`,
		w.TenantID, w.ZeroSince, w.RescuedAt, w.LapsedAt, w.Lapsed3Sent)
}

// S7RescueCandidates 挽回礼包候选：清零已满 window（默认 48h）、从未发放、且租户
// 从未有过已付订单（首购后的耗尽走续费触达，不发挽回）。
func (s *Store) S7RescueCandidates(olderThan time.Duration) []int64 {
	s.ensureS7Watch()
	cutoff := time.Now().UTC().Add(-olderThan).Format(time.RFC3339)
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT w.tenant_id FROM s7_watchlist w
		 WHERE w.zero_since <> '' AND w.zero_since <= ? AND w.rescued_at = ''
		   AND NOT EXISTS (SELECT 1 FROM orders o WHERE o.tenant_id=w.tenant_id AND o.status='paid')`, cutoff)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// S7LapsedFollowups 订阅到期 ≥72h（T+3）且未发过回访的租户。
func (s *Store) S7LapsedFollowups(after time.Duration) []int64 {
	s.ensureS7Watch()
	cutoff := time.Now().UTC().Add(-after).Format(time.RFC3339)
	rows, err := db.Query(s.db, db.CurrentDialect(),
		`SELECT w.tenant_id FROM s7_watchlist w
		 WHERE w.lapsed_at <> '' AND w.lapsed_at <= ? AND w.lapsed3_sent = 0`, cutoff)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err == nil {
			out = append(out, id)
		}
	}
	return out
}
