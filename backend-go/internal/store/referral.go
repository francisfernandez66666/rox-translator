// ============ referral.go · 职责说明 ============
// store 包邀请裂变实现。
// 个人邀请码(ref_code)、首绑绑定(referred_by 首写闸门)、奖励发放与去重。
//
// 奖励唯一性（2026-08-26 修正定稿）——账户层与奖励层分离：
//   - 账户层：users.id 主键不可变；email 为同一时刻全局唯一的绑定属性，双验证后可换绑；
//   - 奖励层：invitee_uid 与 invitee_email **双唯一**——同类型奖励对
//     「(邀请人,被邀uid) 对」「被邀邮箱快照」任一维度历史碰撞即永久拒绝发放。
//     治理场景：注销重注换新 uid 复用旧邮箱 / 换绑邮箱后旧账号再邀 —— 均撞库拦截。
//
// =============================================
package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/ops"
)

// ReferralMigrate 邀请裂变库表迁移（幂等，随 Store.New 调用）：
//   - users 表补 ref_code（个人邀请码，非空唯一）/ referred_by（邀请人 UID，0=未绑定）两列
//   - referral_rewards 奖励流水表：type=trial_stack(体验叠加)/paid_perm(付费永久奖励)
//   - ★ 双唯一索引：idx_rr_invitee(invitee_uid,type) + idx_rr_oneid(invitee_email,type)——
//     邮箱快照回填历史行；索引若因存量重复创建失败仅告警不阻断（不破坏审计历史）
func (s *Store) ReferralMigrate() {
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE users ADD COLUMN ref_code TEXT DEFAULT ''")
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE users ADD COLUMN referred_by INTEGER DEFAULT 0")
	db.Exec(s.db, db.CurrentDialect(), "DROP INDEX IF EXISTS idx_users_ref_code")
	// ★ 个人邀请码升级为「全局唯一」：邀请裂变需跨租户解析——被邀人自建新租户时，
	//   邀请人位于不同租户，原「(tenant_id, ref_code)」租户级唯一会导致跨租户首绑失效
	//   （BindReferral 按 ref_code 跨租户查找邀请人）。升级前先清空跨租户重复的 ref_code
	//   （每组保留最早一条 MIN(id)），避免全局唯一索引在既有重复数据上创建失败。
	db.Exec(s.db, db.CurrentDialect(), `UPDATE users SET ref_code='' WHERE ref_code<>'' AND ref_code IN (SELECT ref_code FROM users WHERE ref_code<>'' GROUP BY ref_code HAVING COUNT(*)>1) AND id NOT IN (SELECT MIN(id) FROM users WHERE ref_code<>'' GROUP BY ref_code)`)
	db.Exec(s.db, db.CurrentDialect(), "CREATE UNIQUE INDEX IF NOT EXISTS idx_users_ref_code ON users(ref_code) WHERE ref_code<>''")
	db.Exec(s.db, db.CurrentDialect(), `CREATE TABLE IF NOT EXISTS referral_rewards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		inviter_uid INTEGER, inviter_tid INTEGER,
		invitee_uid INTEGER,
		type TEXT, tokens INTEGER DEFAULT 0, days INTEGER DEFAULT 0,
		created_at TEXT)`)
	db.Exec(s.db, db.CurrentDialect(), "CREATE INDEX IF NOT EXISTS idx_rr_pair ON referral_rewards(inviter_uid, invitee_uid, type)")
	// ★ 邮箱快照列 + 历史回填（奖励层的邮箱外键锚）
	db.Exec(s.db, db.CurrentDialect(), "ALTER TABLE referral_rewards ADD COLUMN invitee_email TEXT DEFAULT ''")
	db.Exec(s.db, db.CurrentDialect(), `UPDATE referral_rewards SET invitee_email=(
		SELECT lower(COALESCE(u.email,'')) FROM users u WHERE u.id=referral_rewards.invitee_uid)
		WHERE COALESCE(invitee_email,'')=''`)
	// ★ C25（2026-09-12）：日发放计数器——发放事务内条件 UPDATE（cnt<max 守卫），
	db.Exec(s.db, db.CurrentDialect(), `CREATE TABLE IF NOT EXISTS referral_daily (
		inviter_uid INTEGER NOT NULL, day TEXT NOT NULL, cnt INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (inviter_uid, day))`)
	if _, err := db.Exec(s.db, db.CurrentDialect(), "CREATE UNIQUE INDEX IF NOT EXISTS idx_rr_invitee ON referral_rewards(invitee_uid, type)"); err != nil {
		log.Printf("[migrate] referral_rewards (invitee_uid,type) 唯一索引创建失败（存在历史重复，请人工核对）: %v", err)
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(), "CREATE UNIQUE INDEX IF NOT EXISTS idx_rr_oneid ON referral_rewards(invitee_email, type) WHERE invitee_email<>''"); err != nil {
		log.Printf("[migrate] referral_rewards (invitee_email,type) 唯一索引创建失败（存在历史重复，请人工核对）: %v", err)
	}
}

// EnsureRefCode 确保用户拥有个人邀请码（懒生成：首次调用时分配）。
// 生成规则：4 字节随机数→8 位 hex；唯一索引冲突则重新生成直至可用。
// 参数：uid=用户 ID；返回：该用户的个人邀请码（空值不可能出现，除非用户不存在）。
func (s *Store) EnsureRefCode(uid int64) string {
	// ★ C24（2026-09-12）：旧实现 SELECT 查重→循环换码→UPDATE 错误未检查，
	//   并发下两个用户同刻生成同码会有一方「查重通过但 UPDATE 撞唯一索引失败」，
	//   返回了未持久化的码（邀请链接失效）。改为以「条件 UPDATE + 唯一索引」
	//   作为唯一仲裁：重试环带上限，最终以回读持久值兜底。
	var code string
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(ref_code,'') FROM users WHERE id=?", uid).Scan(&code)
	if code != "" {
		return code
	}
	d := db.CurrentDialect()
	for attempt := 0; attempt < 8; attempt++ {
		b := make([]byte, 4)
		rand.Read(b)
		cand := hex.EncodeToString(b)
		res, err := db.Exec(s.db, d, "UPDATE users SET ref_code=? WHERE id=? AND COALESCE(ref_code,'')=''", cand, uid)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 1 {
				return cand // UPDATE 即持久化成功（唯一索引保证不冲突）
			}
		}
		// err≠nil：撞唯一索引（并发同码）→ 换码重试；n=0 且无错：并发方已写入
		var now string
		if e := db.QueryRow(s.db, d, "SELECT COALESCE(ref_code,'') FROM users WHERE id=?", uid).Scan(&now); e == nil && now != "" {
			return now
		}
	}
	// 极端兜底：读当前持久值返回（可能仍为空——用户不存在，与旧行为一致）
	var now string
	db.QueryRow(s.db, d, "SELECT COALESCE(ref_code,'') FROM users WHERE id=?", uid).Scan(&now)
	return now
}

// BindReferral 注册时首绑：仅当被邀人 referred_by 为空才写入（首绑唯一，重复无效）。
func (s *Store) BindReferral(inviteeUID, tenantID int64, refCode string) (inviterUID, inviterTID int64, ok bool) {
	if refCode == "" || inviteeUID <= 0 || tenantID <= 0 {
		return 0, 0, false
	}
	// ★ 跨租户解析：按 ref_code 全局查找邀请人（被邀人可能位于不同租户）
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT id, tenant_id FROM users WHERE ref_code=? AND role IN ('admin','tenant_admin','dept_admin','user')",
		refCode).Scan(&inviterUID, &inviterTID)
	if err != nil || inviterUID == inviteeUID {
		return 0, 0, false
	}
	res, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE users SET referred_by=? WHERE id=? AND COALESCE(referred_by,0)=0", inviterUID, inviteeUID)
	if err != nil {
		return 0, 0, false
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return 0, 0, false // 已有绑定：首次有效，后续无效
	}
	return inviterUID, inviterTID, true
}

// PairRewardExists 同对(邀请人,被邀uid)同类奖励去重。
func (s *Store) PairRewardExists(inviterUID, inviteeUID int64, typ string) bool {
	var one int
	return db.QueryRow(s.db, db.CurrentDialect(), "SELECT 1 FROM referral_rewards WHERE inviter_uid=? AND invitee_uid=? AND type=? LIMIT 1",
		inviterUID, inviteeUID, typ).Scan(&one) == nil
}

// EmailRewardExists 邮箱维度撞库检查（★ 双唯一之邮箱侧）：
// 该邮箱历史上（任意账号名下、任意邀请人）已领取过该类型奖励即返回 true——
// 注销重注/换绑流转均无法绕过，因为流水行保留邮箱快照且全局比对。
func (s *Store) EmailRewardExists(inviteeEmail, typ string) bool {
	if strings.TrimSpace(inviteeEmail) == "" {
		return false // 未绑定邮箱的历史被邀人不参与邮箱维度判定
	}
	var one int
	return db.QueryRow(s.db, db.CurrentDialect(), "SELECT 1 FROM referral_rewards WHERE invitee_email=? AND type=? LIMIT 1",
		strings.ToLower(strings.TrimSpace(inviteeEmail)), typ).Scan(&one) == nil
}

// inviteeEmailOf 读取被邀人当前绑定邮箱（小写规整；未绑定返回空串）。
func (s *Store) inviteeEmailOf(uid int64) string {
	var e string
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(email,'') FROM users WHERE id=?", uid).Scan(&e)
	return strings.ToLower(strings.TrimSpace(e))
}

// ErrReferralCap ★ C25：日发放上限触顶（事务内计数器守卫拒绝）。
var ErrReferralCap = errors.New("referral daily cap reached")

// GrantTrialStack 邀请者体验叠加：+tokens、到期=max(当前最晚体验到期,now)+days。
// ★ C23（2026-09-12）：「占用流水 + 台账发放 + 日计数」收敛为同一事务。
//
//	旧实现占用 INSERT 成功后发放失败且错误被吞——referral_rewards 唯一索引
//	反向把补发路径焊死（钱没到、也永远无法重试）。现在任何一步失败整体回滚，
//	占用不留痕，下一触发点可完整重试；不再有「吞错保占用」的半状态。
//
// ★ C25：maxDaily>0 时在事务内做条件计数器 UPDATE（cnt<max 守卫），
//
//	消除旧「COUNT 判断→发放」check-then-act 竞态下并发注册小幅击穿日上限的问题；
//	触顶返回 ErrReferralCap（整体回滚，不占额度）。maxDaily=0 表示不限（兼容调用）。
func (s *Store) GrantTrialStack(inviterUID, inviterTID, inviteeUID, tokens int64, days int, maxDaily int64) error {
	const typ = "trial_stack"
	if s.PairRewardExists(inviterUID, inviteeUID, typ) {
		return nil
	}
	email := s.inviteeEmailOf(inviteeUID)
	if s.EmailRewardExists(email, typ) {
		return nil // 撞库：该邮箱曾领过体验叠加，永久拒绝
	}
	d := db.CurrentDialect()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if maxDaily > 0 {
		day := time.Now().Format("2006-01-02")
		res, e := db.Exec(tx, d, `INSERT INTO referral_daily (inviter_uid, day, cnt) VALUES (?,?,1)
			ON CONFLICT(inviter_uid, day) DO UPDATE SET cnt=referral_daily.cnt+1 WHERE referral_daily.cnt<?`, inviterUID, day, maxDaily)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrReferralCap // 守卫不成立=触顶：回滚（不消耗、不发放）
		}
	}
	if _, e := db.Exec(tx, d, "INSERT INTO referral_rewards (inviter_uid,inviter_tid,invitee_uid,invitee_email,type,tokens,days,created_at) VALUES (?,?,?,?,?,?,?,?)",
		inviterUID, inviterTID, inviteeUID, email, typ, tokens, days, time.Now().UTC().Format(time.RFC3339)); e != nil {
		log.Printf("[referral] trial_stack 占用失败（唯一索引拦截=重复发放，跳过）invitee=%d err=%v", inviteeUID, e)
		return nil
	}
	// 到期叠加：取邀请人未过期体验/订阅额度最晚到期日（事务内读，保证与发放一致）
	var latest string
	db.QueryRow(tx, d, "SELECT MAX(expires_at) FROM quota_grants WHERE tenant_id=? AND kind IN ('trial','plan') AND expires_at>?",
		inviterTID, time.Now().UTC().Format(time.RFC3339)).Scan(&latest)
	base := time.Now().UTC()
	if latest != "" {
		if t, e := time.Parse(time.RFC3339, latest); e == nil && t.After(base) {
			base = t
		}
	}
	expT := base.Add(time.Duration(days) * 24 * time.Hour)
	if e := createQuotaGrantTx(tx, inviterTID, "trial", tokens, expT, "invite", inviteeUID); e != nil {
		return fmt.Errorf("trial_stack 台账发放失败（占用已一并回滚，可重试）: %w", e)
	}
	if e := tx.Commit(); e != nil {
		return e
	}
	s.resetTrialNotified(inviterTID) // 与 CreateQuotaGrant("trial") 副作用对齐（best-effort）
	return nil
}

// resetTrialNotified 复位「到期前 3 天提醒」去重标记（新体验重新进入提醒窗口）。
func (s *Store) resetTrialNotified(tid int64) {
	d := db.CurrentDialect()
	if _, e := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONSetFalse(d, "permissions", "notified_exp3")+", updated_at=? WHERE id=?",
		time.Now().Format(time.RFC3339), tid); e != nil {
		log.Printf("[quota] trial 提醒标记复位失败 tid=%d: %v", tid, e)
	}
}

// RewardPaidPermanent 受邀者首笔付费套餐→邀请者 token 奖励（双唯一：同对仅一次 + 邮箱快照全局仅一次）。
// 参数：inviteeUID=受邀人用户 ID；tokens=奖励 token 数；days=有效期（天），0 天=永久（默认），>0 为限时台账。
// 限时奖励写入带到期日的 quota_grants（按最早到期优先扣减）；发放失败兜底永久余额，保证奖励不丢。
func (s *Store) RewardPaidPermanent(inviteeUID, tokens, days int64) error {
	const typ = "paid_perm"
	var inviterUID int64
	err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT referred_by FROM users WHERE id=?", inviteeUID).Scan(&inviterUID)
	if err != nil || inviterUID <= 0 {
		return nil // 非邀请来源或历史数据：无奖励
	}
	// ★ 邀请人的租户 ID 必须取自邀请人本人记录（此前误用被邀人租户，已修正）
	var inviterTID int64
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT tenant_id FROM users WHERE id=?", inviterUID).Scan(&inviterTID); err != nil || inviterTID <= 0 {
		return nil
	}
	// ★ 企业邀请人（非个人租户）不参与「多邀得多」付费奖励（需求 5）
	var inviterPersonal int
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT is_personal FROM tenants WHERE id=?", inviterTID).Scan(&inviterPersonal); err != nil || inviterPersonal != 1 {
		return nil // 企业租户：跳过付费奖励
	}
	email := s.inviteeEmailOf(inviteeUID)
	if s.PairRewardExists(inviterUID, inviteeUID, typ) || s.EmailRewardExists(email, typ) {
		return nil // 仅首笔付费触发；邮箱撞库同样永久拒绝
	}
	// ★ C23（2026-09-12）：占用与到账同事务。旧实现占用成功→加余额失败被吞，
	//   唯一索引把补发路径永久焊死。失败整体回滚，调用方日志后可重新触发。
	d := db.CurrentDialect()
	if err := s.EnsureBalance(inviterTID); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, e := db.Exec(tx, d, "INSERT INTO referral_rewards (inviter_uid,inviter_tid,invitee_uid,invitee_email,type,tokens,days,created_at) VALUES (?,?,?,?,?,?,?,?)",
		inviterUID, inviterTID, inviteeUID, email, typ, tokens, days, time.Now().UTC().Format(time.RFC3339)); e != nil {
		log.Printf("[referral] paid_perm 占用失败（视为重复发放跳过）invitee=%d err=%v", inviteeUID, e)
		return nil
	}
	var e error
	if days > 0 {
		// 限时奖励：带到期日台账（按最早到期优先扣减）
		expT := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		e = createQuotaGrantTx(tx, inviterTID, "referral_paid", tokens, expT, "invite", inviteeUID)
	} else {
		// 永久奖励（默认）：累加永久余额
		_, e = db.Exec(tx, d, "UPDATE balance_accounts SET balance=balance+? WHERE tenant_id=?", tokens, inviterTID)
	}
	if e != nil {
		return fmt.Errorf("paid_perm 到账失败（占用已一并回滚，可重试）: %w", e)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// ★ P1 多实例闭环：邀请人一级付费奖励到账，通知影子失效
	//   （限时台账分支已由 createQuotaGrantTx 钩子覆盖，此处补永久余额分支的确定性通知）
	notifyTenantBalanceChanged(inviterTID)
	// ★ H9 二级返佣（限 2 级防合规风险）：一级发放成功后按分成比例自动结算推荐人；
	//   幂等沿用 referral_rewards (invitee_uid,type) 唯一占用模式。
	s.rewardSecondLevel(inviteeUID, inviterUID, email, tokens, days)
	return nil
}

// ReferralL2Pct 二级分成百分比（system_config referral_l2_pct；默认 30，0=关闭）。
func (s *Store) ReferralL2Pct() int64 {
	if v, _ := s.GetConfig("referral_l2_pct"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x >= 0 && x <= 100 {
			return x
		}
	}
	return 30
}

// rewardSecondLevel ★ H9：受邀人首付费 → 推荐人的推荐人（第 2 级）按分成结算。
// 约束：仅限个人租户上级；环检测（上级不得回到受邀人/一级推荐人）；
// type='paid_perm_l2' 唯一占用防重复；失败仅记日志（不影响一级已发奖励）。
func (s *Store) rewardSecondLevel(inviteeUID, l1UID int64, email string, tokens, days int64) {
	pct := s.ReferralL2Pct()
	if pct <= 0 || !s.ReferralEnabled() {
		return
	}
	l2Tokens := tokens * pct / 100
	if l2Tokens <= 0 {
		return
	}
	var l2UID int64
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT referred_by FROM users WHERE id=?", l1UID).Scan(&l2UID); err != nil || l2UID <= 0 {
		return // 一级推荐人无上级：无二级
	}
	if l2UID == inviteeUID || l2UID == l1UID {
		return // 环检测
	}
	const typ = "paid_perm_l2"
	var l2TID int64
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT tenant_id FROM users WHERE id=?", l2UID).Scan(&l2TID); err != nil || l2TID <= 0 {
		return
	}
	var l2Personal int
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT is_personal FROM tenants WHERE id=?", l2TID).Scan(&l2Personal); err != nil || l2Personal != 1 {
		return // 企业上级不参与
	}
	if s.PairRewardExists(l2UID, inviteeUID, typ) || (email != "" && s.EmailRewardExists(email, typ)) {
		return
	}
	d := db.CurrentDialect()
	if err := s.EnsureBalance(l2TID); err != nil {
		log.Printf("[referral-l2] EnsureBalance tid=%d: %v", l2TID, err)
		return
	}
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	if _, e := db.Exec(tx, d, "INSERT INTO referral_rewards (inviter_uid,inviter_tid,invitee_uid,invitee_email,type,tokens,days,created_at) VALUES (?,?,?,?,?,?,?,?)",
		l2UID, l2TID, inviteeUID, email, typ, l2Tokens, days, time.Now().UTC().Format(time.RFC3339)); e != nil {
		return // 唯一占用冲突：已结算过
	}
	var e error
	if days > 0 {
		expT := time.Now().UTC().Add(time.Duration(days) * 24 * time.Hour)
		e = createQuotaGrantTx(tx, l2TID, "referral_paid_l2", l2Tokens, expT, "invite", inviteeUID)
	} else {
		_, e = db.Exec(tx, d, "UPDATE balance_accounts SET balance=balance+? WHERE tenant_id=?", l2Tokens, l2TID)
	}
	if e != nil {
		log.Printf("[referral-l2] 二级到账失败（回滚占用）invitee=%d: %v", inviteeUID, e)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("[referral-l2] commit: %v", err)
	} else {
		// ★ P1 多实例闭环：二级返佣到账通知影子失效（限时分支由 createQuotaGrantTx 钩子覆盖）
		notifyTenantBalanceChanged(l2TID)
	}
}

// ReferralEnabled 邀请裂变总开关（system_config referral_enabled；仅显式 "0" 关闭，
// 缺省/读取异常均视为开启，保证存量部署行为不变）。关闭后：注册绑定与两类奖励全部停发。
// ★ 运营策略引擎（2026-09 合并）：平台 ops_policy.invite.enabled=false 同样视为关闭——
// 邀请奖励发放开关收敛到「运营策略」中台，租户/业务页不再持有独立发放开关。
func (s *Store) ReferralEnabled() bool {
	if v, _ := s.GetConfig("referral_enabled"); v == "0" {
		return false
	}
	if inv, ok := s.platformInvitePolicy(); ok && inv.Enabled != nil && !*inv.Enabled {
		return false
	}
	return true
}

// platformInvitePolicy 读取平台级邀请奖励因子（system_config.ops_policy.invite）。
// 返回: 邀请因子补丁 + 是否存在显式配置。付费奖励（MarkOrderPaid 触发）无租户上下文，
// 以平台策略为唯一权威；注册绑定奖励在 API 层另有租户级 effectivePolicy 兜底。
func (s *Store) platformInvitePolicy() (ops.InvitePatch, bool) {
	raw, err := s.GetConfig("ops_policy")
	if err != nil || strings.TrimSpace(raw) == "" {
		return ops.InvitePatch{}, false
	}
	p := ops.ParseOps(raw)
	return p.Invite, true
}

// ReferralPaidReward 付费奖励入口（MarkOrderPaid 成功确认 paid 套餐后调用）：
// 奖励金额取值优先级：运营策略 invite.paid_reward_tokens/days → system_config
// inviter_paid_reward_tokens/days（后台可调）→ env INVITER_PAID_REWARD_TOKENS → 默认 50 万。
// 内部按对去重，重复调用幂等；非邀请来源静默跳过。参数 inviteeUID=下单用户 ID。
func (s *Store) ReferralPaidReward(inviteeUID int64) error {
	if inviteeUID <= 0 {
		return nil
	}
	// ★ 总开关门禁（2026-08-26 U3 + 2026-09 运营策略）：后台/策略关闭裂变后不再发放任何奖励
	if !s.ReferralEnabled() {
		return nil
	}
	tokens := int64(500000)
	days := int64(0)
	// 奖励金额取值优先级：默认 50 万 → env → 存量散键 → 运营策略（平台级，最高优先）。
	// ★ 2026-09 邀请奖励合并进策略引擎：运营策略为权威来源，必须最后应用才不会被旧散键覆盖。
	if v := os.Getenv("INVITER_PAID_REWARD_TOKENS"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			tokens = x
		}
	}
	if v, _ := s.GetConfig("inviter_paid_reward_tokens"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x > 0 {
			tokens = x
		}
	}
	// 付费邀请奖励有效期（天）：0=永久（默认），>0=限时台账
	if v, _ := s.GetConfig("inviter_paid_reward_days"); v != "" {
		if x, e := strconv.ParseInt(v, 10, 64); e == nil && x >= 0 {
			days = x
		}
	}
	// 运营策略最后应用（赢）：invite.paid_reward_tokens/days 覆盖以上全部来源
	if inv, ok := s.platformInvitePolicy(); ok {
		if inv.PaidRewardTokens != nil && *inv.PaidRewardTokens > 0 {
			tokens = *inv.PaidRewardTokens
		}
		if inv.PaidRewardDays != nil { // ★ C32：指针化后显式 0（永久）也能覆盖散键
			days = int64(*inv.PaidRewardDays)
		}
	}
	return s.RewardPaidPermanent(inviteeUID, tokens, days)
}

// OneidMigrate 账户体系邮箱唯一化（幂等，随 Store.New 调用）：
//   - 存量重复治理：同邮箱保留最小 id 一行持有，其余置空解绑（不删号，可重新绑定其他邮箱）；
//   - 部分唯一索引 idx_users_email：仅约束非空邮箱——「同一时刻一个邮箱至多归属一个账号」，
//     本人换绑不受影响（新旧双验证流程不变）。这是账户层外键唯一的最终兜底。
func (s *Store) OneidMigrate() {
	res, err := db.Exec(s.db, db.CurrentDialect(), `UPDATE users SET email='' WHERE COALESCE(email,'')<>'' AND id IN (
		SELECT id FROM (
			SELECT u.id, ROW_NUMBER() OVER (PARTITION BY lower(u.email) ORDER BY u.id) rn
			FROM users u WHERE COALESCE(u.email,'')<>''
		) t WHERE t.rn > 1)`)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("[migrate] oneid: 已解绑 %d 个重复绑定邮箱的账号（保留最早账号持有）", n)
		}
	}
	if _, err := db.Exec(s.db, db.CurrentDialect(), "CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email<>''"); err != nil {
		log.Printf("[migrate] users.email 唯一索引创建失败（存在大小写/空白变体重复，请人工清理后重启）: %v", err)
	}
}

// CountInviterRewardsToday 统计邀请人今日已发放的奖励笔数（白皮书 §5.4 防刷：
// 同邀请人单日新增 ≥5 名验证用户触发人工复核告警——不拦截，事后核查）。
func (s *Store) CountInviterRewardsToday(inviterUID int64) int64 {
	day := time.Now().Format("2006-01-02")
	var n int64
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT COUNT(*) FROM referral_rewards WHERE inviter_uid=? AND created_at>=?",
		inviterUID, day+"T00:00:00").Scan(&n)
	return n
}

// ReferralRecord 一条邀请奖励记录（ListReferrals 行结构）。
type ReferralRecord struct {
	InviteeUID   int64  `json:"invitee_uid"`   // 被邀人用户 ID
	InviteeName  string `json:"invitee_name"`  // 被邀人显示名（无则 #）
	InviteeEmail string `json:"invitee_email"` // 被邀人注册邮箱快照（2026-08-26 前台记录需求）
	Type         string `json:"type"`          // 奖励类型：trial_stack=体验叠加 / paid_perm=付费永久奖励
	Tokens       int64  `json:"tokens"`        // 奖励 token 数
	Days         int64  `json:"days"`          // 叠加天数（仅体验类有值）
	Paid         bool   `json:"paid"`          // 该被邀人是否已完成首笔付费套餐（paid_perm 行存在即 true）
	CreatedAt    string `json:"created_at"`    // 发放时间
}

// ListReferrals 我的邀请记录（最新在前，最多 100 条）。
//
// ★ 2026-08-26 前台记录需求增强：补 invitee_email 快照与 paid 标记——
//
//	每行即一条「邀请成功」记录（行存在=绑定+注册完成）；
//	paid 列由同被邀人是否存在 paid_perm 行推导（首笔付费是否成功）。
//
// 参数：inviterUID=邀请人用户 ID；返回：奖励记录列表（查询失败返回 nil，前端按空态处理）。
func (s *Store) ListReferrals(inviterUID int64) []*ReferralRecord {
	rows, err := db.Query(s.db, db.CurrentDialect(), `SELECT r.invitee_uid, COALESCE(u.display_name,u.username,'#'),
		COALESCE(r.invitee_email,''), r.type, r.tokens, r.days, r.created_at,
		EXISTS(SELECT 1 FROM referral_rewards p WHERE p.invitee_uid=r.invitee_uid AND p.type='paid_perm')
		FROM referral_rewards r LEFT JOIN users u ON u.id=r.invitee_uid
		WHERE r.inviter_uid=? ORDER BY r.id DESC LIMIT 100`, inviterUID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []*ReferralRecord{}
	for rows.Next() {
		var it ReferralRecord
		if rows.Scan(&it.InviteeUID, &it.InviteeName, &it.InviteeEmail, &it.Type, &it.Tokens, &it.Days, &it.CreatedAt, &it.Paid) == nil {
			out = append(out, &it)
		}
	}
	return out
}

// EmailInRewardLedger 换绑撞库预检（账户层，2026-08-26 需求）：
// 目标邮箱只要出现在邀请奖励流水中（任意类型/任意历史账号），即视为撞库——
// 换绑到该邮箱将被拒绝（见 api handleUpdateEmail），从入口杜绝「先换绑、后邀零奖励」的死胡同。
func (s *Store) EmailInRewardLedger(inviteeEmail string) bool {
	e := strings.ToLower(strings.TrimSpace(inviteeEmail))
	if e == "" {
		return false
	}
	var one int
	return db.QueryRow(s.db, db.CurrentDialect(), "SELECT 1 FROM referral_rewards WHERE invitee_email=? LIMIT 1", e).Scan(&one) == nil
}

// ============ ★ H9 分销归因漏斗（2 级邀请树统计） ============

// InviteFunnelStat 邀请人视角的转化漏斗（L1=直邀，L2=下级直邀）。
type InviteFunnelStat struct {
	L1Invited      int64 `json:"l1_invited"`       // 一级受邀注册数
	L1Paid         int64 `json:"l1_paid"`          // 一级完成首付费数
	L2Invited      int64 `json:"l2_invited"`       // 二级受邀注册数（下级再邀请）
	L2Paid         int64 `json:"l2_paid"`          // 二级首付费触发我的分成数
	RewardTokensL1 int64 `json:"reward_tokens_l1"` // 我获得的一级付费奖励 token 合计
	RewardTokensL2 int64 `json:"reward_tokens_l2"` // 我获得的二级分成 token 合计
	RegRewards     int64 `json:"reg_rewards"`      // 注册绑定奖励（trial_stack）次数
}

// InviteFunnel 归因看板数据（全表聚合走 referred_by/inviter_uid 索引，量级安全）。
func (s *Store) InviteFunnel(uid int64) InviteFunnelStat {
	var f InviteFunnelStat
	d := db.CurrentDialect()
	db.QueryRow(s.db, d, "SELECT COUNT(*) FROM users WHERE referred_by=?", uid).Scan(&f.L1Invited)
	db.QueryRow(s.db, d, "SELECT COUNT(*) FROM referral_rewards WHERE inviter_uid=? AND type='paid_perm'", uid).Scan(&f.L1Paid)
	db.QueryRow(s.db, d, "SELECT COUNT(*) FROM users WHERE referred_by IN (SELECT id FROM users WHERE referred_by=?)", uid).Scan(&f.L2Invited)
	db.QueryRow(s.db, d, "SELECT COUNT(*) FROM referral_rewards WHERE type='paid_perm_l2' AND inviter_uid=?", uid).Scan(&f.L2Paid)
	db.QueryRow(s.db, d, "SELECT COALESCE(SUM(tokens),0) FROM referral_rewards WHERE inviter_uid=? AND type='paid_perm'", uid).Scan(&f.RewardTokensL1)
	db.QueryRow(s.db, d, "SELECT COALESCE(SUM(tokens),0) FROM referral_rewards WHERE inviter_uid=? AND type='paid_perm_l2'", uid).Scan(&f.RewardTokensL2)
	db.QueryRow(s.db, d, "SELECT COUNT(*) FROM referral_rewards WHERE inviter_uid=? AND type='trial_stack'", uid).Scan(&f.RegRewards)
	return f
}

// SecondLevelInvitee 受邀人的一级推荐人（H9 环/树调试用，0=无上级）。
func (s *Store) SecondLevelInvitee(uid int64) int64 {
	var up int64
	db.QueryRow(s.db, db.CurrentDialect(), "SELECT referred_by FROM users WHERE id=?", uid).Scan(&up)
	return up
}
