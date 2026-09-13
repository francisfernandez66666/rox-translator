// ============ quota_grants.go · 职责说明 ============
// store 包额度发放台账实现。
// 带到期日的体验包/订阅额度（可多行叠加、各自独立过期）。
// 永久余额仍存 balance_accounts.balance；扣减顺序 = 未过期额度行(按到期日升序) → 永久余额。
// 设计依据：《TOKEN双桶改造实施方案.md》§二/§三。
// =============================================
package store

import (
	"database/sql"
	"encoding/json"
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
	// ★ C22（2026-09-12）：quota_grants 时间列（expires_at/created_at）统一 UTC RFC3339，
	//   与全部「expires_at > now」比较（同为 UTC）同一基准；旧实现 created_at 存本地时区，
	//   字典序跨格式比较存在 ±时区偏移误差。
	if err := createQuotaGrantTx(s.db, tid, kind, total, expires, source, refID); err != nil {
		return err
	}
	// ★ 任务2.5：发放 trial 体验台账时，复位「到期前 3 天提醒」去重标记——
	//   新体验重新进入提醒窗口（注册礼包 / 超管重新发放均自动复位）。
	// ★ 2026-09-12 PG 方言修复：JSON1 改经 db.JSONSetFalse 助手；失败不再静默吞掉（仅记日志，不阻断发放）。
	if kind == "trial" {
		s.resetTrialNotified(tid)
	}
	return nil
}

// ResetPackageMonthly ★ C7（2026-09-12）：套餐月度重置原子化。
//
//	① 限额计数与执行同事务（tenants 行锁串行化，杜绝「读计数→重置→写计数」三步
//	   竞态下的并发击穿）；② 只重置 kind='plan' 且 source='order' 的本周期台账行——
//	   升级转入行（source='order_carry'）left=total 拉回会把旧包价值通胀复制。
//
// 参数：tid=租户；limit=当月次数上限（<=0 表示不限）。
// 返回：resetRows=重置行数，count=重置后当月计数，rejected=已达上限被拒。
func (s *Store) ResetPackageMonthly(tid int64, limit int) (resetRows int64, count int, rejected bool, err error) {
	d := db.CurrentDialect()
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, false, err
	}
	defer tx.Rollback()
	var raw string
	q := "SELECT COALESCE(policy_config,'{}') FROM tenants WHERE id=?"
	if d.IsPostgres() {
		q += " FOR UPDATE"
	}
	if err := db.QueryRow(tx, d, q, tid).Scan(&raw); err != nil && err != sql.ErrNoRows {
		return 0, 0, false, err
	}
	var pc struct {
		OpsResets string `json:"ops_resets"`
	}
	_ = json.Unmarshal([]byte(raw), &pc)
	type rcT struct {
		Month string `json:"month"`
		Count int    `json:"count"`
	}
	rc := rcT{Month: time.Now().Format("2006-01")}
	if pc.OpsResets != "" {
		_ = json.Unmarshal([]byte(pc.OpsResets), &rc)
	}
	month := time.Now().Format("2006-01")
	if rc.Month != month {
		rc.Month, rc.Count = month, 0
	}
	if limit > 0 && rc.Count >= limit {
		return 0, rc.Count, true, nil
	}
	res, err := db.Exec(tx, d,
		"UPDATE quota_grants SET \"left\"=total\n\t\t WHERE tenant_id=? AND kind='plan' AND source='order' AND expires_at>? AND \"left\"<total",
		tid, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return 0, 0, false, err
	}
	resetRows, _ = res.RowsAffected()
	rc.Count++
	patch, _ := json.Marshal(map[string]interface{}{"ops_resets": marshalJSONCompactC7(rc)})
	if _, err := db.Exec(tx, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "policy_config")+", updated_at=? WHERE id=?",
		string(patch), time.Now().Format(time.RFC3339), tid); err != nil {
		return 0, 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, false, err
	}
	return resetRows, rc.Count, false, nil
}

// marshalJSONCompactC7 紧凑 JSON 序列化（失败返回 "{}"）。
func marshalJSONCompactC7(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// SumActiveGrants 未过期额度剩余合计（低额提醒/展示用）。
func (s *Store) SumActiveGrants(tid int64) int64 {
	var n int64
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(SUM(\"left\"),0) FROM quota_grants WHERE tenant_id=? AND \"left\">0 AND expires_at>?",
		tid, time.Now().UTC().Format(time.RFC3339)).Scan(&n)
	return n
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
// ★ 并发安全（2026-08-26 P0-4 止血；2026-09-12 A1 升级为 PG 主路径）：
//   - SQLite：DSN _txlock=immediate，BEGIN 即持写锁，SELECT 快照与后续 UPDATE 间无其他写事务提交；
//   - PostgreSQL（★ S 批决策：生产唯一方言）：READ COMMITTED 下 BEGIN 不持写锁，SELECT 台账行
//     必须 FOR UPDATE 按序（expires_at ASC）加行锁，把同租户并发扣减在行级串行化；
//   - 守卫 UPDATE 冲突不再武断报「余额不足」：重读该行真实余额重试（≤2 次），真实耗尽才返回
//     ErrInsufficientBalance——误报经 billing/sink 会被解释为欠费结算并清零双桶，属事故级。
//   - 每条核销 UPDATE 仍携带 AND left>=? 守卫 + RowsAffected 校验（双保险，不扣负）。
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
	d := db.CurrentDialect()
	// 确保账户行存在（等价 EnsureBalance 的 tx 内联版）
	if _, err := db.Exec(tx, d,
		"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().Format(time.RFC3339)); err != nil {
		return err
	}
	lock := ""
	if d.IsPostgres() {
		lock = " FOR UPDATE" // 行锁串行化：后到事务在首行等待前者提交，再读到最新余额
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := db.Query(tx, d,
		"SELECT id, \"left\" FROM quota_grants WHERE tenant_id=? AND \"left\">0 AND expires_at>? ORDER BY expires_at ASC"+lock,
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
		// ★ A1 守卫冲突→重读→重试：SQLite IMMEDIATE 下守卫恒真（行为不变）；
		//   PG 多实例并发下冲突属瞬态，重取该行真实余额继续核销而非误报欠费。
		left := g.left
		for attempt := 0; attempt < 3 && need > 0 && left > 0; attempt++ {
			use := left
			if use > need {
				use = need
			}
			res, err := db.Exec(tx, d, "UPDATE quota_grants SET \"left\"=\"left\"-? WHERE id=? AND \"left\">=?", use, g.id, use)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n > 0 {
				need -= use
				break
			}
			if err := db.QueryRow(tx, d, "SELECT \"left\" FROM quota_grants WHERE id=?", g.id).Scan(&left); err != nil {
				return err
			}
		}
	}
	if need > 0 {
		res, err := db.Exec(tx, d, "UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
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
