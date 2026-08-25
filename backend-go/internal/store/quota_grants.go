// ============ quota_grants.go · 职责说明 ============
// 额度发放台账：带到期日的体验包/订阅额度（可多行叠加、各自独立过期）。
// 永久余额仍存 balance_accounts.balance；扣减顺序 = 未过期额度行(按到期日升序) → 永久余额。
// 设计依据：《TOKEN双桶改造实施方案.md》§二/§三。
// =============================================
package store

import (
	"time"
)

// QuotaGrant 一条额度发放记录
type QuotaGrant struct {
	ID        int64
	TenantID  int64
	Kind      string // trial | plan
	Total     int64
	Left      int64
	ExpiresAt string
	Source    string
	RefID     int64
	CreatedAt string
}

// QuotaGrantMigrate 建表与索引（幂等，随 Store.New 调用）。
func (s *Store) QuotaGrantMigrate() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS quota_grants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		kind TEXT NOT NULL DEFAULT 'trial',
		total INTEGER NOT NULL, left INTEGER NOT NULL,
		expires_at TEXT NOT NULL,
		source TEXT DEFAULT '', ref_id INTEGER DEFAULT 0, created_at TEXT)`)
	s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_qg_tid_exp ON quota_grants(tenant_id, expires_at)`)
}

// CreateQuotaGrant 发放一条额度。
func (s *Store) CreateQuotaGrant(tid int64, kind string, total int64, expires time.Time, source string, refID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := s.db.Exec(
		"INSERT INTO quota_grants (tenant_id, kind, total, left, expires_at, source, ref_id, created_at) VALUES (?,?,?,?,?,?,?,?)",
		tid, kind, total, total, expires.UTC().Format(time.RFC3339), source, refID, now)
	return err
}

// SumActiveGrants 未过期额度剩余合计（低额提醒/展示用）。
func (s *Store) SumActiveGrants(tid int64) int64 {
	var n int64
	s.db.QueryRow("SELECT COALESCE(SUM(left),0) FROM quota_grants WHERE tenant_id=? AND left>0 AND expires_at>?",
		tid, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	return n
}

// DeductWithGrants 双部分顺序扣减（事务）：
//
//	① 未过期 grants 按 expires_at ASC 逐行核销（可拆分多行）
//	② 不足部分从永久余额原子扣减
//
// 任一环节不足 → 回滚返回 ErrInsufficientBalance。
//
// ★ 并发安全（2026-08-26 P0-4 止血）：
//   - 连接 DSN 已启用 _txlock=immediate：本事务 BEGIN 时即持有写锁，
//     SELECT 快照与后续 UPDATE 之间不可能有其他写事务提交，拆行核销基于一致视图；
//   - 每条核销 UPDATE 额外携带 AND left>=? 守卫 + RowsAffected 校验，
//     即使未来有人回退事务锁模式，也不会把台账扣成负数（双保险）。
func (s *Store) DeductWithGrants(tid int64, tokens int64) error {
	if err := s.EnsureBalance(tid); err != nil {
		return err
	}
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ 实际为 BEGIN IMMEDIATE
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := tx.Query(
		"SELECT id, left FROM quota_grants WHERE tenant_id=? AND left>0 AND expires_at>? ORDER BY expires_at ASC",
		tid, now)
	if err != nil {
		return err
	}
	type row struct {
		id   int64
		left int64
	}
	var grants []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.left); err == nil {
			grants = append(grants, r)
		}
	}
	rows.Close()

	need := tokens
	for _, g := range grants {
		if need <= 0 {
			break
		}
		use := g.left
		if use > need {
			use = need
		}
		// ★ 守卫式核销：AND left>=use 保证不扣负；影响行数为 0 说明并发下该行余额
		//   已发生变化（理论上是防御性分支，IMMEDIATE 锁下不应触发），整体回滚报余额不足。
		res, err := tx.Exec("UPDATE quota_grants SET left=left-? WHERE id=? AND left>=?", use, g.id, use)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrInsufficientBalance
		}
		need -= use
	}
	if need > 0 {
		res, err := tx.Exec("UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
			need, time.Now().Format(time.RFC3339), tid, need)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrInsufficientBalance
		}
	}
	return tx.Commit()
}
