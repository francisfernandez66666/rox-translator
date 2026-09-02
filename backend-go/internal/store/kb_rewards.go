// ============ kb_rewards.go · 职责说明 ============
// store 包知识库上传奖励（功能⑥）数据层实现。
// 规则（2026-09-02 重构，按需求 6）：
//   - 奖励按「实际载入的新知识库源文字符长度」计量：字符数 × 单字符 token 单价
//   - 单字符单价初始 100 token；超管可在后台开关 + 调价（配置键）
//   - 奖励开关：kb_upload_reward_enabled（1=开 0=关，默认开），超管后台可设
//   - 审批触发：共享包（行业/语言文化）用户投稿经超管审批通过后发放；
//     自服务（tenant/dept/cross_dept）包仍未设审批，沿用即时发放口径
//   - 单租户单日封顶（防刷）；奖励入永久余额（balance_accounts），流水入 kb_upload_rewards 表
//
// =============================================
package store

import (
	"strconv"
	"time"

	"translator/internal/db"
)

// KBReward 一条 KB 上传奖励流水。
type KBReward struct {
	ID        int64  // 流水 ID
	TenantID  int64  // 获奖租户
	UserID    int64  // 触发导入/投稿的用户
	PackageID int64  // 导入目标包
	Chars     int64  // 本次计入奖励的源文字符数
	Tokens    int64  // 本次奖励 token 数
	CreatedAt string // 发放时间 RFC3339
}

// KBRewardMigrate 建表与索引（幂等，随 Store.New 调用）。
func (s *Store) KBRewardMigrate() {
	db.Exec(s.db, db.CurrentDialect(), `CREATE TABLE IF NOT EXISTS kb_upload_rewards (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL,
		user_id INTEGER NOT NULL DEFAULT 0,
		package_id INTEGER NOT NULL DEFAULT 0,
		added INTEGER NOT NULL DEFAULT 0,
		tokens INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL)`)
	db.Exec(s.db, db.CurrentDialect(), `CREATE INDEX IF NOT EXISTS idx_kbr_tid_day ON kb_upload_rewards(tenant_id, created_at)`)
}

// KBRewardEnabled 奖励总开关（kb_upload_reward_enabled，默认开）。
// 超管后台可关闭；关闭后不再发放新奖励（存量余额不受影响）。
func (s *Store) KBRewardEnabled() bool {
	if v, err := s.GetConfig("kb_upload_reward_enabled"); err == nil && v != "" {
		return v == "1"
	}
	return true
}

// KBRewardTokensPerChar 单字符 token 单价（kb_upload_reward_tokens_per_char，默认 100）。
func (s *Store) KBRewardTokensPerChar() int64 {
	if v, err := s.GetConfig("kb_upload_reward_tokens_per_char"); err == nil && v != "" {
		if n, perr := parseInt64Safe(v); perr == nil && n > 0 {
			return n
		}
	}
	return 100
}

// KBRewardDailyCap 单租户日封顶（kb_upload_reward_daily_cap，默认 50000）。
func (s *Store) KBRewardDailyCap() int64 {
	if v, err := s.GetConfig("kb_upload_reward_daily_cap"); err == nil && v != "" {
		if n, perr := parseInt64Safe(v); perr == nil && n > 0 {
			return n
		}
	}
	return 50000
}

// KBRewardUsedToday 单租户当日已发放奖励合计（按自然日，tenant_id 维度防刷封顶依据）。
// ★ 口径必须与流水写入一致：流水 created_at 存 UTC RFC3339，故按 UTC 自然日前缀匹配，
//
//	否则 UTC+8 等时区下本地日期≠UTC 日期会导致日封顶失效。
func (s *Store) KBRewardUsedToday(tid int64) int64 {
	var n int64
	dayStart := time.Now().UTC().Format("2006-01-02")
	// created_at 为 RFC3339（UTC）；按前缀 "2006-01-02" 前缀匹配自然日
	db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(SUM(tokens),0) FROM kb_upload_rewards WHERE tenant_id=? AND created_at LIKE ?",
		tid, dayStart+"%").Scan(&n)
	return n
}

// GrantKBRewardByChars 按源文字符数发放 KB 奖励（永久余额 + 流水，事务原子）：
//   - 开关关闭（KBRewardEnabled=false）直接跳过
//   - tokens = chars × 单字符单价
//   - 校验单租户日封顶：今日已发 + 本次 ≤ cap 才发放（超限跳过，返回 granted=false）
//   - 奖励入永久余额（balance_accounts，与充值包同桶，永不过期）
//   - 流水落 kb_upload_rewards（防刷统计与审计留痕；added 列记字符数）
//
// 参数：tid=获奖租户，uid=触发用户，pkgID=目标包，chars=计入奖励的源文字符数。
// 返回：granted=是否实际发放，tokens=本次奖励 token 数，used=发放后当日累计。
func (s *Store) GrantKBRewardByChars(tid, uid, pkgID, chars int64) (granted bool, tokens, used int64) {
	if !s.KBRewardEnabled() {
		return false, 0, s.KBRewardUsedToday(tid)
	}
	rate := s.KBRewardTokensPerChar()
	cap := s.KBRewardDailyCap()
	if rate <= 0 || chars <= 0 {
		return false, 0, s.KBRewardUsedToday(tid)
	}
	tokens = chars * rate
	used = s.KBRewardUsedToday(tid)
	if used+tokens > cap {
		return false, 0, used
	}
	tx, err := s.db.Begin() // DSN _txlock=immediate ⇒ BEGIN IMMEDIATE
	if err != nil {
		return false, 0, used
	}
	defer tx.Rollback()
	// 事务内确保余额账户行存在后原子累加（与 GrantPackageSentences 同款并发安全）
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT OR IGNORE INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?)",
		tid, time.Now().Format(time.RFC3339)); err != nil {
		return false, 0, used
	}
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		tokens, time.Now().Format(time.RFC3339), tid); err != nil {
		return false, 0, used
	}
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT INTO kb_upload_rewards (tenant_id, user_id, package_id, added, tokens, created_at) VALUES (?,?,?,?,?,?)",
		tid, uid, pkgID, chars, tokens, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return false, 0, used
	}
	if err := tx.Commit(); err != nil {
		return false, 0, used
	}
	return true, tokens, used + tokens
}

// GrantKBReward 兼容旧调用：按条数发放（内部按每条固定字符数换算，保底同一函数路径）。
// 仅供历史调用方（不在新审批流程中）使用；新增调用请使用 GrantKBRewardByChars。
func (s *Store) GrantKBReward(tid, uid, pkgID, added int64) (granted bool, tokens, used int64) {
	perEntryChars := int64(20) // 旧语义「每条约额 200 token」≈ 单字符100 × 20 字符，保持量级一致
	return s.GrantKBRewardByChars(tid, uid, pkgID, added*perEntryChars)
}

// parseInt64Safe 安全解析字符串为 int64（配置读取用，非法返回 error 由调用方兜底默认值）。
func parseInt64Safe(v string) (int64, error) {
	return strconv.ParseInt(v, 10, 64)
}
