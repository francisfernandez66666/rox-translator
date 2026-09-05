// ============ quota_grants.go · 职责说明 ============
// store 包额度发放台账实现。
// 带到期日的体验包/订阅额度（可多行叠加、各自独立过期）。
// 永久余额仍存 balance_accounts.balance；扣减顺序 = 未过期额度行(按到期日升序) → 永久余额。
// 设计依据：《TOKEN双桶改造实施方案.md》§二/§三。
// =============================================
package store

import (
	"database/sql"
	"time"
	"translator/internal/db"
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
	db.Exec(s.db, db.CurrentDialect(), `CREATE TABLE IF NOT EXISTS quota_grants (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		kind TEXT NOT NULL DEFAULT 'trial',
		total INTEGER NOT NULL, "left" INTEGER NOT NULL,
		expires_at TEXT NOT NULL,
		source TEXT DEFAULT '', ref_id INTEGER DEFAULT 0, created_at TEXT)`)
	db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_qg_tid_exp ON quota_grants(tenant_id, expires_at)`)
}

// CreateQuotaGrant 发放一条额度。
func (s *Store) CreateQuotaGrant(tid int64, kind string, total int64, expires time.Time, source string, refID int64) error {
	now := time.Now().Format(time.RFC3339)
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"INSERT INTO quota_grants (tenant_id, kind, total, \"left\", expires_at, source, ref_id, created_at) VALUES (?,?,?,?,?,?,?,?)",
		tid, kind, total, total, expires.UTC().Format(time.RFC3339), source, refID, now)
	if err != nil {
		return err
	}
	// ★ 任务2.5：发放 trial 体验台账时，复位「到期前 3 天提醒」去重标记——
	//   新体验重新进入提醒窗口（注册礼包 / 超管重新发放均自动复位）。
	if kind == "trial" {
		_, _ = db.Exec(s.db, db.CurrentDialect(),
			"UPDATE tenants SET permissions=json_set(COALESCE(permissions,'{}'), '$.notified_exp3', json('false')), updated_at=? WHERE id=?",
			time.Now().Format(time.RFC3339), tid)
	}
	return nil
}

// SumActiveGrants 未过期额度剩余合计（低额提醒/展示用）。
func (s *Store) SumActiveGrants(tid int64) int64 {
	var n int64
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=? AND \"left\">0 AND expires_at>?",
		tid, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	return n
}

// ResetCurrentPackageGrants 重置当前套餐期用量（GPT 式重制）：
// quota_grants 中 kind='plan'、未过期（expires_at>now）、left<total 的行恢复 left=total。
// 参数：tid=租户 ID；返回重置行数（幂等：无待重置行返回 0）。
// 供 /api/admin/billing/package/reset（运营策略引擎 package.monthly_reset_enabled 因子）调用。
func (s *Store) ResetCurrentPackageGrants(tid int64) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE quota_grants SET \"left\"=total WHERE tenant_id=? AND kind='plan' AND expires_at>? AND \"left\"<total",
		tid, now)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// TenantRemainTotal 双部分可用余额一次聚合（2026-08-26 评审整改 A1）：
// 返回 (未过期台账合计, 永久余额)。全系统「可用额度」唯一口径——
// 展示（balancePayload）、预检（CheckBalance / worker 快速失败）必须同时覆盖两桶，// 否则会出现「台账有 30 万体验 token、永久余额为 0 却被 fail-closed 拒绝」的口径分裂。
func (s *Store) TenantRemainTotal(tid int64) (grants, permanent int64, err error) {
	if err := s.EnsureBalance(tid); err != nil {
		return 0, 0, err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	err = db.QueryRow(s.db, db.CurrentDialect(), `
		SELECT b.balance,
		       COALESCE((SELECT SUM(g."left") FROM quota_grants g
		                 WHERE g.tenant_id=b.tenant_id AND g."left">0 AND g.expires_at>?),0)
		FROM balance_accounts b WHERE b.tenant_id=?`, now, tid).Scan(&permanent, &grants)
	if err != nil {
		return 0, 0, err
	}
	return grants, permanent, nil
}

// EarliestActiveTrialExpiry 返回租户最早到期的未过期试用台账到期时间（任务2.5 到期提醒用）。
// 返回 (expiresAt RFC3339, found)。仅统计 kind='trial' 且 left>0 且未过期；无则 found=false。
func (s *Store) EarliestActiveTrialExpiry(tid int64) (string, bool) {
	var exp string
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT MIN(expires_at) FROM quota_grants WHERE tenant_id=? AND kind='trial' AND \"left\">0 AND expires_at>?",
		tid, time.Now().UTC().Format(time.RFC3339)).Scan(&exp)
	if err != nil || exp == "" {
		return "", false
	}
	return exp, true
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
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ 实际为 BEGIN IMMEDIATE
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deductWithGrantsTx(tx, tid, tokens); err != nil {
		return err
	}
	return tx.Commit()
}

// deductWithGrantsTx 双部分顺序扣减核心（供外部事务复用，★ 整改 B4）：
// RecordUsage 需把「扣减」与「台账落账」放进同一 IMMEDIATE 事务——此前扣减独立提交、
// ledger INSERT 失败即产生「扣了钱无流水」的对账缺口。
// 约束：tx 必须已由调用方 Begin（IMMEDIATE）；函数内禁止触碰 s.db。
func deductWithGrantsTx(tx *sql.Tx, tid int64, tokens int64) error {
	// 确保账户行存在（等价 EnsureBalance 的 tx 内联版）
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := db.Query(tx, db.CurrentDialect(),
		"SELECT id, \"left\" FROM quota_grants WHERE tenant_id=? AND \"left\">0 AND expires_at>? ORDER BY expires_at ASC",
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
		res, err := db.Exec(tx, db.CurrentDialect(), "UPDATE quota_grants SET \"left\"=\"left\"-? WHERE id=? AND \"left\">=?", use, g.id, use)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrInsufficientBalance
		}
		need -= use
	}
	if need > 0 {
		res, err := db.Exec(tx, db.CurrentDialect(), "UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
			need, time.Now().Format(time.RFC3339), tid, need)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrInsufficientBalance
		}
	}
	return nil
}
