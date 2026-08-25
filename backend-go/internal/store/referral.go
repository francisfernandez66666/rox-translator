// ============ referral.go · 职责说明 ============
// 邀请裂变：个人邀请码(ref_code)、首绑绑定(referred_by 首写闸门)、奖励发放与去重。
// 规则（白皮书 §五）：同对(邀请人,被邀人)每种奖励仅一次；不同被邀人之间叠加。
// =============================================
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"time"
)

// ReferralMigrate 列迁移（幂等）。
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
}

// EnsureRefCode 确保用户拥有个人邀请码。
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

// PairRewardExists 同对同类奖励去重。
func (s *Store) PairRewardExists(inviterUID, inviteeUID int64, typ string) bool {
	var one int
	return s.db.QueryRow("SELECT 1 FROM referral_rewards WHERE inviter_uid=? AND invitee_uid=? AND type=? LIMIT 1",
		inviterUID, inviteeUID, typ).Scan(&one) == nil
}

// GrantTrialStack 邀请者体验叠加：+tokens、到期=max(当前最晚体验到期,now)+days；并落奖励记录。
func (s *Store) GrantTrialStack(inviterUID, inviterTID, inviteeUID, tokens int64, days int) error {
	if s.PairRewardExists(inviterUID, inviteeUID, "trial_stack") {
		return nil
	}
	var latest string
	err := s.db.QueryRow("SELECT MAX(expires_at) FROM quota_grants WHERE tenant_id=? AND kind IN ('trial','plan') AND expires_at>?",
		inviterTID, time.Now().UTC().Format(time.RFC3339)).Scan(&latest)
	base := time.Now().UTC()
	if err == nil && latest != "" {
		if t, e := time.Parse(time.RFC3339, latest); e == nil && t.After(base) {
			base = t
		}
	}
	expT := base.Add(time.Duration(days) * 24 * time.Hour)
	if err := s.CreateQuotaGrant(inviterTID, "trial", tokens, expT, "invite", inviteeUID); err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO referral_rewards (inviter_uid,inviter_tid,invitee_uid,type,tokens,days,created_at) VALUES (?,?,?,?,?,?,?)",
		inviterUID, inviterTID, inviteeUID, "trial_stack", tokens, days, time.Now().Format(time.RFC3339))
	return err
}

// RewardPaidPermanent 受邀者首笔付费套餐→邀请者永久 token（全局每被邀人一次）。
func (s *Store) RewardPaidPermanent(inviteeUID, tokens int64, s2 *Store) error {
	var inviterUID, inviterTID int64
	err := s.db.QueryRow("SELECT referred_by, tenant_id FROM users WHERE id=?", inviteeUID).Scan(&inviterUID, &inviterTID)
	if err != nil || inviterUID <= 0 {
		return nil // 非邀请来源或历史数据：无奖励
	}
	if s.PairRewardExists(inviterUID, inviteeUID, "paid_perm") {
		return nil // 仅首笔付费触发
	}
	if _, err := s.db.Exec("UPDATE balance_accounts SET balance=balance+? WHERE tenant_id=?", tokens, inviterTID); err != nil {
		return err
	}
	_, err = s.db.Exec("INSERT INTO referral_rewards (inviter_uid,inviter_tid,invitee_uid,type,tokens,created_at) VALUES (?,?,?,?,?,?)",
		inviterUID, inviterTID, inviteeUID, "paid_perm", tokens, time.Now().Format(time.RFC3339))
	return err
}

// ListReferrals 我的邀请记录（含状态推导）。
type ReferralRecord struct {
	InviteeUID   int64  `json:"invitee_uid"`
	InviteeName  string `json:"invitee_name"`
	Type         string `json:"type"`
	Tokens       int64  `json:"tokens"`
	Days         int64  `json:"days"`
	Paid         bool   `json:"paid"`
	CreatedAt    string `json:"created_at"`
}

func (s *Store) ListReferrals(inviterUID int64) []*ReferralRecord {
	rows, err := s.db.Query(`SELECT r.invitee_uid, COALESCE(u.display_name,u.username,'#'), r.type, r.tokens, r.days, r.created_at
		FROM referral_rewards r LEFT JOIN users u ON u.id=r.invitee_uid
		WHERE r.inviter_uid=? ORDER BY r.id DESC LIMIT 100`, inviterUID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []*ReferralRecord{}
	for rows.Next() {
		var it ReferralRecord
		if rows.Scan(&it.InviteeUID, &it.InviteeName, &it.Type, &it.Tokens, &it.Days, &it.CreatedAt) == nil {
			out = append(out, &it)
		}
	}
	return out
}

var _ = sql.ErrNoRows
var _ = time.Now
