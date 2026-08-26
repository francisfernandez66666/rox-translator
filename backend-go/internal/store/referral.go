// ============ referral.go · 职责说明 ============
// 邀请裂变：个人邀请码(ref_code)、首绑绑定(referred_by 首写闸门)、奖励发放与去重。
//
// ★ 奖励唯一性（2026-08-26 修正定稿）——账户层与奖励层分离：
//   - 账户层：users.id 主键不可变；email 为同一时刻全局唯一的绑定属性，双验证后可换绑；
//   - 奖励层：invitee_uid 与 invitee_email **双唯一**——同类型奖励对
//     「(邀请人,被邀uid) 对」「被邀邮箱快照」任一维度历史碰撞即永久拒绝发放。
//     治理场景：注销重注换新 uid 复用旧邮箱 / 换绑邮箱后旧账号再邀 —— 均撞库拦截。
//
// =============================================
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// ReferralMigrate 邀请裂变库表迁移（幂等，随 Store.New 调用）：
//   - users 表补 ref_code（个人邀请码，非空唯一）/ referred_by（邀请人 UID，0=未绑定）两列
//   - referral_rewards 奖励流水表：type=trial_stack(体验叠加)/paid_perm(付费永久奖励)
//   - ★ 双唯一索引：idx_rr_invitee(invitee_uid,type) + idx_rr_oneid(invitee_email,type)——
//     邮箱快照回填历史行；索引若因存量重复创建失败仅告警不阻断（不破坏审计历史）
func (s *Store) ReferralMigrate() {
	s.db.Exec("ALTER TABLE users ADD COLUMN ref_code TEXT DEFAULT ''")
	s.db.Exec("ALTER TABLE users ADD COLUMN referred_by INTEGER DEFAULT 0")
	s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_ref_code ON users(ref_code) WHERE ref_code<>''")
	s.db.Exec(`CREATE TABLE IF NOT EXISTS referral_rewards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		inviter_uid INTEGER, inviter_tid INTEGER,
		invitee_uid INTEGER,
		type TEXT, tokens INTEGER DEFAULT 0, days INTEGER DEFAULT 0,
		created_at TEXT)`)
	s.db.Exec("CREATE INDEX IF NOT EXISTS idx_rr_pair ON referral_rewards(inviter_uid, invitee_uid, type)")
	// ★ 邮箱快照列 + 历史回填（奖励层的邮箱外键锚）
	s.db.Exec("ALTER TABLE referral_rewards ADD COLUMN invitee_email TEXT DEFAULT ''")
	s.db.Exec(`UPDATE referral_rewards SET invitee_email=(
		SELECT lower(COALESCE(u.email,'')) FROM users u WHERE u.id=referral_rewards.invitee_uid)
		WHERE COALESCE(invitee_email,'')=''`)
	if _, err := s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_rr_invitee ON referral_rewards(invitee_uid, type)"); err != nil {
		log.Printf("[migrate] referral_rewards (invitee_uid,type) 唯一索引创建失败（存在历史重复，请人工核对）: %v", err)
	}
	if _, err := s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_rr_oneid ON referral_rewards(invitee_email, type) WHERE invitee_email<>''"); err != nil {
		log.Printf("[migrate] referral_rewards (invitee_email,type) 唯一索引创建失败（存在历史重复，请人工核对）: %v", err)
	}
}

// EnsureRefCode 确保用户拥有个人邀请码（懒生成：首次调用时分配）。
// 生成规则：4 字节随机数→8 位 hex；唯一索引冲突则重新生成直至可用。
// 参数：uid=用户 ID；返回：该用户的个人邀请码（空值不可能出现，除非用户不存在）。
func (s *Store) EnsureRefCode(uid int64) string {
	var code string
	s.db.QueryRow("SELECT COALESCE(ref_code,'') FROM users WHERE id=?", uid).Scan(&code)
	if code != "" {
		return code
	}
	b := make([]byte, 4)
	rand.Read(b)
	code = hex.EncodeToString(b)
	for {
		var exists string
		err := s.db.QueryRow("SELECT ref_code FROM users WHERE ref_code=?", code).Scan(&exists)
		if err == sql.ErrNoRows {
			break
		}
		b = make([]byte, 4)
		rand.Read(b)
		code = hex.EncodeToString(b)
	}
	s.db.Exec("UPDATE users SET ref_code=? WHERE id=? AND COALESCE(ref_code,'')=''", code, uid)
	return code
}

// BindReferral 注册时首绑：仅当被邀人 referred_by 为空才写入（首绑唯一，重复无效）。
func (s *Store) BindReferral(inviteeUID int64, refCode string) (inviterUID, inviterTID int64, ok bool) {
	if refCode == "" || inviteeUID <= 0 {
		return 0, 0, false
	}
	err := s.db.QueryRow("SELECT id, tenant_id FROM users WHERE ref_code=? AND role IN ('admin','tenant_admin','dept_admin','user')",
		refCode).Scan(&inviterUID, &inviterTID)
	if err != nil || inviterUID == inviteeUID {
		return 0, 0, false
	}
	res, err := s.db.Exec("UPDATE users SET referred_by=? WHERE id=? AND COALESCE(referred_by,0)=0", inviterUID, inviteeUID)
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
	return s.db.QueryRow("SELECT 1 FROM referral_rewards WHERE inviter_uid=? AND invitee_uid=? AND type=? LIMIT 1",
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
	return s.db.QueryRow("SELECT 1 FROM referral_rewards WHERE invitee_email=? AND type=? LIMIT 1",
		strings.ToLower(strings.TrimSpace(inviteeEmail)), typ).Scan(&one) == nil
}

// inviteeEmailOf 读取被邀人当前绑定邮箱（小写规整；未绑定返回空串）。
func (s *Store) inviteeEmailOf(uid int64) string {
	var e string
	s.db.QueryRow("SELECT COALESCE(email,'') FROM users WHERE id=?", uid).Scan(&e)
	return strings.ToLower(strings.TrimSpace(e))
}

// GrantTrialStack 邀请者体验叠加：+tokens、到期=max(当前最晚体验到期,now)+days。
// ★ 发放顺序（2026-08-26 修正）：先「占用」奖励流水（含邮箱快照，双唯一索引拦截并发/撞库），
//
//	占用成功才发放额度；台账失败回退永久余额通道，保证奖励不丢。
func (s *Store) GrantTrialStack(inviterUID, inviterTID, inviteeUID, tokens int64, days int) error {
	const typ = "trial_stack"
	if s.PairRewardExists(inviterUID, inviteeUID, typ) {
		return nil
	}
	email := s.inviteeEmailOf(inviteeUID)
	if s.EmailRewardExists(email, typ) {
		return nil // 撞库：该邮箱曾领过体验叠加，永久拒绝
	}
	// 占用优先：插入失败 = 并发抢到 / 唯一索引拦截 → 视为已发放，静默跳过
	res, err := s.db.Exec("INSERT INTO referral_rewards (inviter_uid,inviter_tid,invitee_uid,invitee_email,type,tokens,days,created_at) VALUES (?,?,?,?,?,?,?,?)",
		inviterUID, inviterTID, inviteeUID, email, typ, tokens, days, time.Now().Format(time.RFC3339))
	if err != nil {
		log.Printf("[referral] trial_stack 占用失败（视为重复发放跳过）invitee=%d err=%v", inviteeUID, err)
		return nil
	}
	_ = res
	// 发放额度：台账（t+max(now,最晚到期)+days），失败兜底永久余额
	var latest string
	// 取邀请人当前所有未过期体验/订阅额度的最晚到期日，实现时长叠加
	err = s.db.QueryRow("SELECT MAX(expires_at) FROM quota_grants WHERE tenant_id=? AND kind IN ('trial','plan') AND expires_at>?",
		inviterTID, time.Now().UTC().Format(time.RFC3339)).Scan(&latest)
	base := time.Now().UTC()
	if err == nil && latest != "" {
		if t, e := time.Parse(time.RFC3339, latest); e == nil && t.After(base) {
			base = t
		}
	}
	expT := base.Add(time.Duration(days) * 24 * time.Hour)
	if gerr := s.CreateQuotaGrant(inviterTID, "trial", tokens, expT, "invite", inviteeUID); gerr != nil {
		_ = s.Charge(inviterTID, tokens)
	}
	return nil
}

// RewardPaidPermanent 受邀者首笔付费套餐→邀请者永久 token（双唯一：同对仅一次 + 邮箱快照全局仅一次）。
// 参数：inviteeUID=受邀人用户 ID；tokens=奖励 token 数；返回错误（非邀请来源静默返回 nil）。
func (s *Store) RewardPaidPermanent(inviteeUID, tokens int64) error {
	const typ = "paid_perm"
	var inviterUID int64
	err := s.db.QueryRow("SELECT referred_by FROM users WHERE id=?", inviteeUID).Scan(&inviterUID)
	if err != nil || inviterUID <= 0 {
		return nil // 非邀请来源或历史数据：无奖励
	}
	// ★ 邀请人的租户 ID 必须取自邀请人本人记录（此前误用被邀人租户，已修正）
	var inviterTID int64
	if err := s.db.QueryRow("SELECT tenant_id FROM users WHERE id=?", inviterUID).Scan(&inviterTID); err != nil || inviterTID <= 0 {
		return nil
	}
	email := s.inviteeEmailOf(inviteeUID)
	if s.PairRewardExists(inviterUID, inviteeUID, typ) || s.EmailRewardExists(email, typ) {
		return nil // 仅首笔付费触发；邮箱撞库同样永久拒绝
	}
	// 占用优先：流水落库成功才加余额（并发/撞库由双唯一索引在此拦截）
	if _, err := s.db.Exec("INSERT INTO referral_rewards (inviter_uid,inviter_tid,invitee_uid,invitee_email,type,tokens,created_at) VALUES (?,?,?,?,?,?,?)",
		inviterUID, inviterTID, inviteeUID, email, typ, tokens, time.Now().Format(time.RFC3339)); err != nil {
		log.Printf("[referral] paid_perm 占用失败（视为重复发放跳过）invitee=%d err=%v", inviteeUID, err)
		return nil
	}
	_ = s.EnsureBalance(inviterTID)
	_, err = s.db.Exec("UPDATE balance_accounts SET balance=balance+? WHERE tenant_id=?", tokens, inviterTID)
	return err
}

// ReferralEnabled 邀请裂变总开关（system_config referral_enabled；仅显式 "0" 关闭，
// 缺省/读取异常均视为开启，保证存量部署行为不变）。关闭后：注册绑定与两类奖励全部停发。
func (s *Store) ReferralEnabled() bool {
	if v, _ := s.GetConfig("referral_enabled"); v == "0" {
		return false
	}
	return true
}

// ReferralPaidReward 付费奖励入口（MarkOrderPaid 成功确认 paid 套餐后调用）：
// 奖励金额取值优先级：system_config inviter_paid_reward_tokens（后台可调）→ env INVITER_PAID_REWARD_TOKENS → 默认 50 万。
// 内部按对去重，重复调用幂等；非邀请来源静默跳过。参数 inviteeUID=下单用户 ID。
func (s *Store) ReferralPaidReward(inviteeUID int64) {
	if inviteeUID <= 0 {
		return
	}
	// ★ 总开关门禁（2026-08-26 U3）：后台关闭裂变后不再发放任何奖励
	if !s.ReferralEnabled() {
		return
	}
	tokens := int64(500000)
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
	_ = s.RewardPaidPermanent(inviteeUID, tokens)
}

// OneidMigrate 账户体系邮箱唯一化（幂等，随 Store.New 调用）：
//   - 存量重复治理：同邮箱保留最小 id 一行持有，其余置空解绑（不删号，可重新绑定其他邮箱）；
//   - 部分唯一索引 idx_users_email：仅约束非空邮箱——「同一时刻一个邮箱至多归属一个账号」，
//     本人换绑不受影响（新旧双验证流程不变）。这是账户层外键唯一的最终兜底。
func (s *Store) OneidMigrate() {
	res, err := s.db.Exec(`UPDATE users SET email='' WHERE COALESCE(email,'')<>'' AND id IN (
		SELECT id FROM (
			SELECT u.id, ROW_NUMBER() OVER (PARTITION BY lower(u.email) ORDER BY u.id) rn
			FROM users u WHERE COALESCE(u.email,'')<>''
		) t WHERE t.rn > 1)`)
	if err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			log.Printf("[migrate] oneid: 已解绑 %d 个重复绑定邮箱的账号（保留最早账号持有）", n)
		}
	}
	if _, err := s.db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email<>''"); err != nil {
		log.Printf("[migrate] users.email 唯一索引创建失败（存在大小写/空白变体重复，请人工清理后重启）: %v", err)
	}
}

// CountInviterRewardsToday 统计邀请人今日已发放的奖励笔数（白皮书 §5.4 防刷：
// 同邀请人单日新增 ≥5 名验证用户触发人工复核告警——不拦截，事后核查）。
func (s *Store) CountInviterRewardsToday(inviterUID int64) int64 {
	day := time.Now().Format("2006-01-02")
	var n int64
	s.db.QueryRow("SELECT COUNT(*) FROM referral_rewards WHERE inviter_uid=? AND created_at>=?",
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
	rows, err := s.db.Query(`SELECT r.invitee_uid, COALESCE(u.display_name,u.username,'#'),
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
	return s.db.QueryRow("SELECT 1 FROM referral_rewards WHERE invitee_email=? LIMIT 1", e).Scan(&one) == nil
}
