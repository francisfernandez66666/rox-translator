// ============ trial_grant.go · 职责说明 ============
// 账号级免费体验额度发放（★ 2026-10-21 M3 用户令：注册/首登自动获取，一个账号仅一次）。
//
// 为什么单独成表：历史上的体验额度按**租户**发放（quota_grants.source='register', ref_id=0），
// 同一租户的受邀成员不会各自获得；而用户令要求“一个账号仅一次”。本表以 user_id 为主键，
// 用唯一约束把“同一账号并发登录/注册重试”收敛成一次，发放与标记在同一事务内完成：
//   - 先插入 user_trial_grants（主键冲突=已经发过，直接返回 false）；
//   - 再写 quota_grants（真正的额度台账）；
//   - 任一步失败整体回滚，下一次登录还能补发。
//
// 存量账号在启动迁移时一律标记为 backfill（tokens=0），避免上线后给老账号再补一份。
// =============================================
package store

import (
	"database/sql"
	"strings"
	"time"

	"translator/internal/db"
)

// UserTrialGrantMigrate 建账号级体验发放台账并回填存量账号（幂等）。
// 回填语义：迁移那一刻已经存在的账号视为“历史已发过”，只写一行 tokens=0 的占位，
// 不补发额度；上线后新建的账号才走 GrantUserTrialOnce。
func (s *Store) UserTrialGrantMigrate() {
	d := db.CurrentDialect()
	_, _ = db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS user_trial_grants (
		user_id INTEGER PRIMARY KEY,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		tokens INTEGER NOT NULL DEFAULT 0,
		source TEXT NOT NULL DEFAULT '',
		granted_at TEXT NOT NULL DEFAULT ''
	)`)
	// 只补不存在的账号；已发放（含并发迁移）的账号不动。
	_, _ = db.Exec(s.db, d, `INSERT INTO user_trial_grants (user_id, tenant_id, tokens, source, granted_at)
		SELECT u.id, u.tenant_id, 0, 'backfill', ?
		  FROM users u
		 WHERE NOT EXISTS (SELECT 1 FROM user_trial_grants g WHERE g.user_id=u.id)`,
		time.Now().UTC().Format(time.RFC3339))
}

// GrantUserTrialOnce 给一个账号发放一次体验额度。
// 返回 granted=true 表示本次真的写了额度；false 表示该账号已经发过（含历史回填）。
// 参数：tid=租户 ID，uid=用户 ID，tokens=内部计量 token，expires=到期时间，source=来源标记。
func (s *Store) GrantUserTrialOnce(tid, uid, tokens int64, expires time.Time, source string) (bool, error) {
	if tid <= 0 || uid <= 0 {
		return false, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT INTO user_trial_grants (user_id, tenant_id, tokens, source, granted_at) VALUES (?,?,?,?,?)",
		uid, tid, tokens, source, now); err != nil {
		if IsUniqueViolation(err) {
			return false, nil
		}
		return false, err
	}
	if tokens > 0 {
		// ★ 2026-10-30 三事合一批：这里**知道是谁**，所以把 uid 一并落到行的 user_id 上
		// （W6：个人档只该成员核销；旧写法只有 ref_id=uid 这个"线索"，扣减侧读不到归属）。
		if err := createQuotaGrantTxTiered(tx, tid, uid, "trial", tokens, expires, source, uid, ""); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	// 与 CreateQuotaGrant 的副作用对齐：新体验进入“到期前 3 天提醒”窗口。
	// 这一步在事务外、best-effort；失败只影响提醒去重，不影响额度本身。
	if tokens > 0 {
		s.resetTrialNotified(tid)
	}
	return tokens > 0, nil
}

// userTrialGranted 只读探测（供排障/测试使用；业务写路径不要用它做“先查后写”）。
func (s *Store) userTrialGranted(uid int64) (bool, error) {
	var one int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT 1 FROM user_trial_grants WHERE user_id=?", uid).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AccountHasRealTrialGrant 问「这一个账户（跨全部身份行）有没有拿到过**真实**体验补贴」。
//
// ★ 2026-10-25 多身份方案 R4 补腿：纯企业账户登录时会自动补建一条个人身份行，
// 那一行要不要发体验额度，判据不能只看新行自己（它必然没领过），要问**账户级**事实。
//
// 「真实补贴」＝台账里 tokens>0 的那一行。两档占位不算拿到过：
//   - 'backfill'（启动迁移给存量账号写的 tokens=0 占位）；
//   - 'membership_no_trial'（企业身份不送额度那条款记的 tokens=0 占位）。
//
// ⚠️ 这一条与上面 userTrialGranted 不同：那个是「这一行有没有台账记录」（含占位），
// 本函数是「这个账户有没有真领过钱」。它依然**只是补贴去重读数**，不是并发闸门——
// 同一行的发放原子性仍由 `user_trial_grants.user_id` 主键保证（GrantUserTrialOnce 里先插后写）。
// 账户号空 ⇒ 直接 false（不拿空串去比，避免把历史 account_id 未回填的行误判成同一个账户）。
func (s *Store) AccountHasRealTrialGrant(accountID string) (bool, error) {
	if strings.TrimSpace(accountID) == "" {
		return false, nil
	}
	var one int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT 1 FROM user_trial_grants g JOIN users u ON u.id=g.user_id
		  WHERE u.account_id=? AND g.tokens>0 LIMIT 1`, strings.TrimSpace(accountID)).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
