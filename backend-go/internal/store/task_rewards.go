// ============ task_rewards.go · 职责说明 ============
// store 包「任务系统」（需求 #33，2026-09-21 用户口径）数据层实现。
// 在既有任务中心（人工点击领取的 daily/once 任务）之上补齐「事件自动发放」链路：
//   - 每日：登录奖励 100 临时积分（一日一次，有效期 3 天，日叠加）
//   - 每周：发起一次翻译奖励 100 临时积分（每周上限 5 次、每天上限 1 次，有效期 7 天，周叠加）
//   - 长期：邀请好友注册成功 +500 临时积分（有效期 14 天，可叠加）
//   - 长期：邀请好友且任意充值 +1000 永久积分（可叠加）
//   - 长期：上传自己知识库并解析成功 +600 永久积分（一次性不叠加）
//   - 特殊：超管手动「重置已订阅全部用户的积分消耗量」——消耗完的一并重置，有效期不变
//
// 双形态落点（与全站积分口径一致，对外零 token）：
//   - 临时积分 = quota_grants 带到期日的台账行，kind='task'（不与 plan/trial 撞命名，
//     月度重置 ResetPackageMonthly 只碰 kind='plan' AND source='order'，任务台账不受其影响）；
//     扣减顺序仍走 DeductWithGrants 的「到期日升序优先」，任务积分天然先于永久余额花掉。
//   - 永久积分 = balance_accounts.balance（与充值包同桶，永不过期）。
//
// 并发与幂等：去重靠 user_task_rewards 唯一索引 (task_id,user_id,dedup_key)；
// 频次上限靠 user_task_period_cnt 的条件计数器 UPDATE（cnt<cap 守卫，与 referral_daily C25 同款），
// 触顶/重复一律整体回滚，不留「占了名额没发钱」的半状态。
// =============================================
package store

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
	"translator/internal/ops"
)

// 任务标识（task_key）常量：事件钩子与出厂任务定义共用，禁止散落字符串。
const (
	TaskKeyLoginDaily    = "login_daily"     // 每日登录
	TaskKeyTranslateWeek = "translate_week"  // 每周发起翻译
	TaskKeyInviteReg     = "invite_register" // 邀请好友注册成功
	TaskKeyInvitePaid    = "invite_paid"     // 邀请好友且任意充值
	TaskKeyKBUpload      = "kb_upload"       // 上传自己知识库并解析成功
)

// TaskGrantKind 任务临时积分在 quota_grants 台账中的 kind 命名空间。
// 独立于 'trial'/'plan'：月度套餐重置、体验到期提醒、订单折算均按各自 kind 过滤，不会误伤任务积分。
const TaskGrantKind = "task"

// TaskGrantSource 任务台账 source 前缀（source='task:<task_key>'，便于对账与运营追溯发放来源）。
const TaskGrantSource = "task:"

// TaskRewardResult 一次事件发放的结果（api 层据此回给用户/日志）。
type TaskRewardResult struct {
	Granted   bool   `json:"granted"`    // 是否实际发放
	Reason    string `json:"reason"`     // 未发放原因：disabled/duplicate/capped_day/capped_week/no_reward/bad_task
	TaskID    int64  `json:"task_id"`    // 任务 ID
	TaskKey   string `json:"task_key"`   // 任务标识
	Title     string `json:"title"`      // 任务标题
	Points    int64  `json:"points"`     // 本次发放积分（对外口径）
	Tokens    int64  `json:"-"`          // 本次发放 token（内部记账口径）
	ValidDays int    `json:"valid_days"` // 有效期天数（0=永久）
	ExpiresAt string `json:"expires_at"` // 临时积分到期时间 RFC3339（永久为空）
}

// TaskRewardStat 用户在某任务上的当前周期进度（任务中心展示用）。
type TaskRewardStat struct {
	TodayCount  int64  `json:"today_count"`  // 今日已发放次数
	WeekCount   int64  `json:"week_count"`   // 本周已发放次数
	TotalCount  int64  `json:"total_count"`  // 历史累计发放次数
	LastExpiry  string `json:"last_expiry"`  // 最近一次临时积分到期时间（永久类为空）
	LastGranted string `json:"last_granted"` // 最近一次发放时间
}

// TaskRewardMigrate 任务系统扩容迁移（幂等，随 Store.New 调用）：
// user_tasks 补事件发放列 + 新建发放流水表与周期计数表 + 种入出厂任务。
func (s *Store) TaskRewardMigrate() {
	d := db.CurrentDialect()
	// ① 老库补列（新增列一律带默认值，历史行按「人工领取」语义保持不变）
	// ★ 失败必须出声：补列失败会让下面种入撞「no such column」，静默吞掉＝任务系统整体静默缺失。
	if err := db.EnsureColumns(s.db, d, "user_tasks", map[string]string{
		"task_key":     "TEXT NOT NULL DEFAULT ''",
		"grant_mode":   "TEXT NOT NULL DEFAULT 'manual'", // manual=用户点击领取 / auto=事件自动发放
		"period":       "TEXT NOT NULL DEFAULT 'daily'",  // daily|weekly|once|event
		"valid_days":   "INTEGER NOT NULL DEFAULT 0",     // >0=临时积分有效天数，0=永久
		"stack_expiry": "INTEGER NOT NULL DEFAULT 1",     // 1=到期叠加（max(当前最晚到期,now)+days），0=固定 now+days
		"cap_per_day":  "INTEGER NOT NULL DEFAULT 0",     // 每日发放上限（0=不限）
		"cap_per_week": "INTEGER NOT NULL DEFAULT 0",     // 每周发放上限（0=不限）
	}); err != nil {
		// 不中断：后续建表/种入各自幂等，失败点会各自出声（缺列时种入必失败并告警）
		observability.Warn(context.Background(), "task_reward_migrate_ensure_columns_failed", slog.String("err", err.Error()))
	}
	// ② 发放流水（去重与审计的唯一依据）
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS user_task_rewards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		task_id INTEGER NOT NULL,
		task_key TEXT NOT NULL DEFAULT '',
		user_id INTEGER NOT NULL,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		reward_points INTEGER NOT NULL DEFAULT 0,
		reward_tokens INTEGER NOT NULL DEFAULT 0,
		valid_days INTEGER NOT NULL DEFAULT 0,
		grant_kind TEXT NOT NULL DEFAULT 'temporary',
		dedup_key TEXT NOT NULL DEFAULT '',
		period_day TEXT NOT NULL DEFAULT '',
		period_week TEXT NOT NULL DEFAULT '',
		expires_at TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL)`)
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS uniq_utr_task_user_key ON user_task_rewards(task_id,user_id,dedup_key)`)
	db.Exec(s.db, d, `CREATE INDEX IF NOT EXISTS idx_utr_user ON user_task_rewards(user_id,task_id,period_day,period_week)`)
	// ③ 周期条件计数器（上限守卫，消除「COUNT 判断→发放」竞态击穿）
	db.Exec(s.db, d, `CREATE TABLE IF NOT EXISTS user_task_period_cnt (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		task_id INTEGER NOT NULL,
		period_key TEXT NOT NULL,
		cnt INTEGER NOT NULL DEFAULT 0,
		updated_at TEXT NOT NULL)`)
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS uniq_utpc ON user_task_period_cnt(user_id,task_id,period_key)`)
	// ④ task_key 唯一（空值不参与唯一约束，兼容历史手工任务）
	db.Exec(s.db, d, `CREATE UNIQUE INDEX IF NOT EXISTS uniq_task_key ON user_tasks(task_key) WHERE task_key<>''`)
	// ⑤ 出厂任务种入（存在即跳过，超管后台可改额/停用）
	s.seedBuiltinTasks()
}

// seedBuiltinTasks 种入 #33 出厂任务定义（按 task_key 幂等）。
// 数值一律取自用户 2026-09-20 原文口径，落库按内部 token 记账（积分×汇率折算），
// 对外展示再由 PointsFromTokens 折回积分（乘法折算无损往返）。
func (s *Store) seedBuiltinTasks() {
	d := db.CurrentDialect()
	type seed struct {
		key     string
		typ     string // 兼容旧 task_type 列（daily/once）
		title   string
		desc    string
		points  int64
		mode    string
		period  string
		days    int
		stack   int
		capDay  int
		capWeek int
		sort    int
	}
	seeds := []seed{
		{TaskKeyLoginDaily, "daily", "每日登录", "每天登录一次即得 100 积分（有效期 3 天，可叠加）", 100, "auto", "daily", 3, 1, 1, 0, 10},
		{TaskKeyTranslateWeek, "daily", "每周发起翻译", "每周发起翻译奖励 100 积分（每天 1 次、每周最多 5 次，有效期 7 天，可叠加）", 100, "auto", "weekly", 7, 1, 1, 5, 20},
		{TaskKeyInviteReg, "once", "邀请好友注册", "好友通过你的邀请码注册成功 +500 积分（有效期 14 天，可叠加）", 500, "auto", "event", 14, 0, 0, 0, 30},
		{TaskKeyInvitePaid, "once", "邀请好友充值", "受邀好友任意充值成功 +1000 永久积分（可叠加）", 1000, "auto", "event", 0, 0, 0, 0, 40},
		{TaskKeyKBUpload, "once", "上传专属知识库", "上传自己的知识库并解析成功 +600 永久积分（一次性）", 600, "auto", "once", 0, 0, 0, 0, 50},
	}
	now := time.Now().Format(time.RFC3339)
	for _, sd := range seeds {
		var n int
		_ = db.QueryRow(s.db, d, "SELECT COUNT(*) FROM user_tasks WHERE task_key=?", sd.key).Scan(&n)
		if n > 0 {
			continue
		}
		_, err := db.Exec(s.db, d, `INSERT INTO user_tasks
			(task_type, title, description, reward_tokens, enabled, sort_order, created_at, updated_at,
			 task_key, grant_mode, period, valid_days, stack_expiry, cap_per_day, cap_per_week)
			VALUES (?,?,?,?,1,?,?,?,?,?,?,?,?,?,?)`,
			sd.typ, sd.title, sd.desc, s.TokensFromPoints(sd.points), sd.sort, now, now,
			sd.key, sd.mode, sd.period, sd.days, sd.stack, sd.capDay, sd.capWeek)
		if err != nil {
			// 并发首启或历史脏数据撞唯一索引：下个启动周期再补，不阻断服务。
			// ★ 但必须出声——静默跳过曾让一条 SQL 占位符错位逃过所有闸门（整批任务静默不入库）。
			observability.Warn(context.Background(), "task_seed_failed",
				slog.String("task_key", sd.key), slog.String("err", err.Error()))
			continue
		}
	}
	// ⑥ 种入后自检：出厂任务必须齐备，缺任一条即出声（防「静默半套」再次发生）
	if n := s.countBuiltinTaskKeys(); n < len(seeds) {
		observability.Warn(context.Background(), "task_builtin_seed_incomplete",
			slog.Int("want", len(seeds)), slog.Int("have", n))
	}
}

// countBuiltinTaskKeys 已入库的出厂任务条数（按 task_key 非空统计）。
func (s *Store) countBuiltinTaskKeys() int {
	var n int
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM user_tasks WHERE task_key<>''").Scan(&n)
	return n
}

// GrantTaskReward 事件触发任务奖励发放（幂等 + 周期上限 + 临时/永久双形态，单事务原子）。
// 参数：uid=触发用户（奖励入其所属租户）；taskKey=任务标识；dedup=事件去重后缀
// （period=event 必填，如 "invitee:123"；其余周期由服务端按日/周/终身自动派生）。
// 返回结果而非 error：未发放（重复/触顶/停用）属正常业务态，调用方只记日志不打断主流程。
func (s *Store) GrantTaskReward(uid int64, taskKey, dedup string) TaskRewardResult {
	res := TaskRewardResult{TaskKey: taskKey}
	if uid <= 0 || taskKey == "" {
		res.Reason = "bad_task"
		return res
	}
	d := db.CurrentDialect()
	t, err := scanTask(db.QueryRow(s.db, d, "SELECT "+taskCols+" FROM user_tasks WHERE task_key=?", taskKey))
	if err != nil {
		res.Reason = "bad_task"
		return res
	}
	res.TaskID, res.Title = t.ID, t.Title
	if t.Enabled != 1 || t.RewardTokens <= 0 {
		res.Reason = "disabled"
		return res
	}
	if t.GrantMode != "auto" {
		res.Reason = "no_reward" // 手工任务不走事件发放
		return res
	}
	today, week := taskPeriodKeys(time.Now())
	dedupKey := taskDedupKey(t, today, dedup)
	if dedupKey == "" {
		res.Reason = "bad_task"
		return res
	}
	if !s.platformTaskEnabled() {
		res.Reason = "disabled" // ★ 发放中台关闸（ops_policy.task.enabled=false）：事件发放零副作用退出
		return res
	}
	tx, err := s.db.Begin()
	if err != nil {
		res.Reason = "bad_task"
		return res
	}
	defer tx.Rollback()
	// ① 去重占位：撞唯一索引 = 本周期/本事件已发放（RowsAffected=0 判定，双方言一致）
	ins, e := db.Exec(tx, d, `INSERT OR IGNORE INTO user_task_rewards
		(task_id, task_key, user_id, tenant_id, reward_points, reward_tokens, valid_days, grant_kind,
		 dedup_key, period_day, period_week, expires_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.ID, t.TaskKey, uid, 0, s.PointsFromTokens(t.RewardTokens), t.RewardTokens, t.ValidDays,
		taskGrantKindOf(t.ValidDays), dedupKey, today, week, "", time.Now().UTC().Format(time.RFC3339))
	if e != nil {
		res.Reason = "bad_task"
		return res
	}
	if n, _ := ins.RowsAffected(); n == 0 {
		res.Reason = "duplicate" // 已发放过（每日一次/终身一次/同一好友一次）
		res.Points = s.PointsFromTokens(t.RewardTokens)
		res.ValidDays = t.ValidDays
		return res
	}
	// ② 周期上限：条件计数器 UPDATE（守卫不成立 = 触顶，整体回滚）
	if t.CapPerDay > 0 {
		if hit, e := bumpTaskCounter(tx, uid, t.ID, "D:"+today, int64(t.CapPerDay)); e != nil || !hit {
			res.Reason = "capped_day"
			return res
		}
	}
	if t.CapPerWeek > 0 {
		if hit, e := bumpTaskCounter(tx, uid, t.ID, "W:"+week, int64(t.CapPerWeek)); e != nil || !hit {
			res.Reason = "capped_week"
			return res
		}
	}
	// ③ 获奖人所属租户（个人/企业桶由其租户账户承担）
	var tid int64
	_ = db.QueryRow(tx, d, "SELECT COALESCE(tenant_id,0) FROM users WHERE id=?", uid).Scan(&tid)
	if tid <= 0 {
		res.Reason = "bad_task"
		return res
	}
	// ④ 发放：临时积分入台账（可叠加到期），永久积分入余额
	var expires string
	if t.ValidDays > 0 {
		base := time.Now().UTC()
		if t.StackExpiry == 1 {
			// 叠加口径：以本租户未过期任务台账的最晚到期日为基准（日叠加/周叠加即此语义）
			var latest string
			db.QueryRow(tx, d, "SELECT COALESCE(MAX(expires_at),'') FROM quota_grants WHERE tenant_id=? AND kind=? AND expires_at>?",
				tid, TaskGrantKind, base.Format(time.RFC3339)).Scan(&latest)
			if latest != "" {
				if tv, e := time.Parse(time.RFC3339, latest); e == nil && tv.After(base) {
					base = tv.UTC()
				}
			}
		}
		expT := base.Add(time.Duration(t.ValidDays) * 24 * time.Hour)
		if e := createQuotaGrantTx(tx, tid, TaskGrantKind, t.RewardTokens, expT, TaskGrantSource+t.TaskKey, t.ID); e != nil {
			res.Reason = "no_reward"
			return res
		}
		expires = expT.UTC().Format(time.RFC3339)
	} else {
		if e := chargePermanentTx(tx, tid, t.RewardTokens); e != nil {
			res.Reason = "no_reward"
			return res
		}
	}
	// ⑤ 回填流水（租户 + 到期时间），与发放同一事务
	if _, e := db.Exec(tx, d, "UPDATE user_task_rewards SET tenant_id=?, expires_at=? WHERE task_id=? AND user_id=? AND dedup_key=?",
		tid, expires, t.ID, uid, dedupKey); e != nil {
		res.Reason = "no_reward"
		return res
	}
	if e := tx.Commit(); e != nil {
		res.Reason = "no_reward"
		return res
	}
	res.Granted = true
	res.Points = s.PointsFromTokens(t.RewardTokens)
	res.Tokens = t.RewardTokens
	res.ValidDays = t.ValidDays
	res.ExpiresAt = expires
	return res
}

// GrantTaskRewardToInviter 以「受邀人」为事件主体，向其邀请人发放任务奖励（邀请裂变类任务共用）。
// 参数：inviteeUID=被邀请注册/充值的人；taskKey=任务标识。dedup 固定为 invitee:<id>（同一好友只发一次，不同好友可叠加）。
func (s *Store) GrantTaskRewardToInviter(inviteeUID int64, taskKey string) TaskRewardResult {
	inviter := s.InviterOf(inviteeUID)
	if inviter <= 0 {
		return TaskRewardResult{TaskKey: taskKey, Reason: "bad_task"} // 非邀请来源：无任务奖励
	}
	return s.GrantTaskReward(inviter, taskKey, "invitee:"+strconv.FormatInt(inviteeUID, 10))
}

// InviterOf 返回某用户的邀请人 uid（0=无邀请来源）。
func (s *Store) InviterOf(uid int64) int64 {
	if uid <= 0 {
		return 0
	}
	var v int64
	_ = db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(referred_by,0) FROM users WHERE id=?", uid).Scan(&v)
	return v
}

// TaskRewardStatOf 用户在某任务上的当前周期进度（任务中心展示）。
func (s *Store) TaskRewardStatOf(uid, taskID int64) TaskRewardStat {
	var st TaskRewardStat
	today, week := taskPeriodKeys(time.Now())
	d := db.CurrentDialect()
	_ = db.QueryRow(s.db, d, `SELECT
		COALESCE(SUM(CASE WHEN period_day=? THEN 1 ELSE 0 END),0),
		COALESCE(SUM(CASE WHEN period_week=? THEN 1 ELSE 0 END),0),
		COUNT(*),
		COALESCE(MAX(expires_at),''),
		COALESCE(MAX(created_at),'')
		FROM user_task_rewards WHERE user_id=? AND task_id=?`,
		today, week, uid, taskID).Scan(&st.TodayCount, &st.WeekCount, &st.TotalCount, &st.LastExpiry, &st.LastGranted)
	return st
}

// RevokeTaskRewardForInvitee ★#33 反作弊补丁：受邀人订单全部退款后，回收以其为事件主体发放的
// 任务奖励（当前只用于 TaskKeyInvitePaid「邀请好友且任意充值 +1000 永久积分」）。
//
//	不回收就等于「付费→退款」白嫖一笔任务积分，与 A3 裂变付费奖励回收同一口径。
//	永久积分：从邀请人所属租户余额守卫式扣回（不足扣到 0，缺口留告警人工核对）；
//	临时积分：作废该邀请人名下对应来源、尚未消耗的最后一笔任务台账（left>0 才动，
//	           已消耗部分不追回，避免把已用掉的额度扣成负数）。
//	两种情形都删除发放流水行：dedup 记录撤销后，受邀人日后真实付费时可再次达标发放。
//
// 参数：inviteeUID=被邀请（且曾充值）的用户；taskKey=事件任务标识。返回实际扣回 token。
func (s *Store) RevokeTaskRewardForInvitee(inviteeUID int64, taskKey string) int64 {
	if s == nil || s.db == nil || inviteeUID <= 0 || taskKey == "" {
		return 0
	}
	d := db.CurrentDialect()
	var rowID, tid, tokens int64
	var kind string
	err := db.QueryRow(s.db, d, `SELECT id, tenant_id, reward_tokens, grant_kind
		FROM user_task_rewards WHERE task_key=? AND dedup_key=? LIMIT 1`,
		taskKey, "invitee:"+strconv.FormatInt(inviteeUID, 10)).
		Scan(&rowID, &tid, &tokens, &kind)
	if err != nil || rowID == 0 || tid <= 0 || tokens <= 0 {
		return 0 // 未发放过（无邀请来源/未达标）：无需回收
	}
	// 先撤销流水行，再收回额度：并发双退时只有一方命中，天然幂等
	res, e := db.Exec(s.db, d, "DELETE FROM user_task_rewards WHERE id=?", rowID)
	if e != nil {
		observability.Warn(context.Background(), "task_reward_revoke_delete_failed",
			slog.Int64("reward_row", rowID), slog.String("err", e.Error()))
		return 0
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0
	}
	if kind != "permanent" {
		// 临时台账：只作废尚未消耗完的最后一笔（出厂 invite_paid 为永久，此分支为兜底）
		var gID, gLeft int64
		if e := db.QueryRow(s.db, d, `SELECT id, "left" FROM quota_grants
			WHERE tenant_id=? AND kind=? AND source=? AND "left">0 ORDER BY id DESC LIMIT 1`,
			tid, TaskGrantKind, TaskGrantSource+taskKey).Scan(&gID, &gLeft); e == nil && gID > 0 {
			if _, e := db.Exec(s.db, d, "DELETE FROM quota_grants WHERE id=? AND \"left\"=?", gID, gLeft); e == nil {
				tokens = gLeft
				notifyTenantBalanceChanged(tid)
			}
		}
	}
	_ = s.EnsureBalance(tid)
	var bal int64
	_ = db.QueryRow(s.db, d, "SELECT COALESCE(balance,0) FROM balance_accounts WHERE tenant_id=?", tid).Scan(&bal)
	revoked := tokens
	if bal < revoked {
		revoked = bal
	}
	if revoked > 0 {
		if _, e := db.Exec(s.db, d,
			"UPDATE balance_accounts SET balance=balance-?, updated_at=? WHERE tenant_id=? AND balance>=?",
			revoked, time.Now().UTC().Format(time.RFC3339), tid, revoked); e != nil {
			observability.Warn(context.Background(), "task_reward_revoke_failed",
				slog.Int64("tenant_id", tid), slog.String("task", taskKey), slog.String("err", e.Error()))
			return 0
		}
	}
	if gap := tokens - revoked; gap > 0 {
		_ = s.CreateAlert(tid, "warning", "task_reward_revoke",
			fmt.Sprintf("受邀人订单全部退款，任务奖励「%s」回收缺口 %d token（余额不足），请人工核对", taskKey, gap))
	} else {
		_ = s.CreateAlert(tid, "info", "task_reward_revoke",
			fmt.Sprintf("受邀人订单全部退款，已回收任务奖励「%s」%d token", taskKey, revoked))
	}
	notifyTenantBalanceChanged(tid)
	return revoked
}

// ResetTaskGrantConsumption ★#33 特殊任务：超管手动重置「已订阅全部用户」的任务积分消耗量。
//
//	对 quota_grants 中 kind='task' 且尚未过期的台账行把 left 拉回 total——
//	消耗完（left=0）的行同样重置；有效期 expires_at 一律不改写（用户原文口径「有效期不变」）。
//	subscribedOnly=true 时仅重置存在未过期订阅台账（kind='plan' 且未过期）的租户，
//	即「已订阅全部用户」；plan/order_carry 等付费台账不在本操作影响范围内。
//
// 返回 tenants=受影响租户数，rows=重置台账行数。
func (s *Store) ResetTaskGrantConsumption(subscribedOnly bool) (tenants, rows int64, err error) {
	d := db.CurrentDialect()
	now := time.Now().UTC().Format(time.RFC3339)
	where := `kind=? AND expires_at>? AND "left"<total`
	args := []interface{}{TaskGrantKind, now}
	if subscribedOnly {
		where += ` AND tenant_id IN (SELECT DISTINCT tenant_id FROM quota_grants WHERE kind='plan' AND expires_at>?)`
		args = append(args, now)
	}
	// 先取受影响租户清单（人工触发、低频，读写间竞态可接受），再单条批量 UPDATE 拉回 left
	rowsTIDs := make([]int64, 0, 16)
	rs, err := db.Query(s.db, d, `SELECT DISTINCT tenant_id FROM quota_grants WHERE `+where, args...)
	if err != nil {
		return 0, 0, err
	}
	for rs.Next() {
		var v int64
		if err := rs.Scan(&v); err == nil {
			rowsTIDs = append(rowsTIDs, v)
		}
	}
	rs.Close()
	if len(rowsTIDs) == 0 {
		return 0, 0, nil
	}
	res, err := db.Exec(s.db, d, `UPDATE quota_grants SET "left"=total WHERE `+where, args...)
	if err != nil {
		return 0, 0, err
	}
	affected, _ := res.RowsAffected()
	// 台账 left 拉回 = 可用量增加，逐租户通知多实例影子失效（同 ResetPackageMonthly 口径）
	for _, tid := range rowsTIDs {
		notifyTenantBalanceChanged(tid)
	}
	return int64(len(rowsTIDs)), affected, nil
}

// bumpTaskCounter 周期条件计数自增：未触顶返回 true（并 +1），触顶返回 false（守卫不成立，0 行更新）。
// 与 referral_daily（C25）同款写法，SQLite / PostgreSQL 双方言均可用。
func bumpTaskCounter(tx db.Execer, uid, taskID int64, periodKey string, cap int64) (bool, error) {
	res, err := db.Exec(tx, db.CurrentDialect(), `INSERT INTO user_task_period_cnt (user_id, task_id, period_key, cnt, updated_at) VALUES (?,?,?,1,?)
		ON CONFLICT(user_id, task_id, period_key) DO UPDATE SET cnt=user_task_period_cnt.cnt+1, updated_at=excluded.updated_at
		WHERE user_task_period_cnt.cnt<?`,
		uid, taskID, periodKey, time.Now().UTC().Format(time.RFC3339), cap)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// taskPeriodKeys 返回 (日粒度键, ISO 周键)。周键形如 2026-W39（ISO 8601，跨年稳定）。
func taskPeriodKeys(t time.Time) (day, week string) {
	y, w := t.ISOWeek()
	return t.Format("2006-01-02"), fmt.Sprintf("%04d-W%02d", y, w)
}

// taskDedupKey 计算发放去重键：
//   - daily / weekly → 今日（天然「每天一次」；周上限由条件计数器把关）
//   - once           → 固定 'once'（终身一次）
//   - event          → 调用方后缀（如 invitee:<id>，每个好友一次、可叠加）
func taskDedupKey(t *UserTask, today, dedup string) string {
	switch t.Period {
	case "once":
		return "once"
	case "event":
		if dedup == "" {
			return ""
		}
		return dedup
	default: // daily / weekly
		return today
	}
}

// taskGrantKindOf 发放形态标签（展示用）：valid_days>0=temporary（临时积分），0=permanent（永久积分）。
func taskGrantKindOf(validDays int) string {
	if validDays > 0 {
		return "temporary"
	}
	return "permanent"
}

// platformTaskEnabled 任务奖励发放总开关（ops_policy.task.enabled，未配置时默认开放）。
// 与 api 层 effectivePolicyCached().Task.Enabled 同口径；本函数覆盖拿不到请求上下文的
// 存储层内部钩子（如订单确认后的「邀请好友充值」任务发放），避免关闸后仍从后门发钱。
func (s *Store) platformTaskEnabled() bool {
	raw, err := s.GetConfig("ops_policy")
	if err != nil || strings.TrimSpace(raw) == "" {
		return true
	}
	p := ops.ParseOps(raw)
	return p.Task.Enabled == nil || *p.Task.Enabled
}
