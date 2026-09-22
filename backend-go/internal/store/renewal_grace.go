// ============ 本文件职责中文说明 ============
// 续费宽限期 + 自动续费重试的数据访问层（★ #74，2026-09-23）。
//
// #41 的自动续费只做到「到期前建单 + 催付」，且订阅一到期就被 watchdog 摘掉付费身份，
// 客户晚付一天就得重新选包；本文件补齐商业闭环缺的两块地基：
//
//	① 宽限期状态：tenants.permissions 两枚新键（grace_expires_at / notified_grace），
//	   分别表示「宽限期截止时刻」与「进入宽限期的通知已发」。订阅身份（package_code /
//	   package_expires_at）在宽限期内**原样保留**，故额度与权限无需任何额外放行逻辑。
//	② 重试去重：renewal_attempts 表，唯一键 (tenant_id, package_id, attempt_date) ——
//	   「同一租户同一包同一天最多一张续费单」由数据库约束兜住，而不是靠内存标记。
//
// WHY 用唯一键抢占而不是「查一下有没有单」：扫描任务本身已由 runExclusive 单跑者化，
// 但 Redis 异常时降级本地执行、多实例同时跑仍是可能态；先 INSERT OR IGNORE 抢占
// （RowsAffected=0 即今日已建过）才是与 billing.go「pending 条件抢占」同一口径的幂等写法。
//
// 调度与触达（谁在什么时候调用这些方法）在 api/subscription_grace.go / api/watchdog.go；
// 本文件只写状态，不发通知、不做业务判定。store 冻结规则（AGENTS.md §1）禁止把新方法
// 追加进 billing.go / packages.go，故独立成域文件。
// ==========================================
package store

import (
	"context"
	"time"

	"translator/internal/db"
	"translator/internal/observability"
)

// 建单尝试状态（renewal_attempts.status）：只作运维留痕与排障，不参与业务判定。
const (
	RenewalAttemptPending = "pending" // 已抢占资格、尚未回写结果（建单中途崩溃会留这一格）
	RenewalAttemptCreated = "created" // 续费单已生成
	RenewalAttemptFailed  = "failed"  // 建单失败（原因见 reason 列，下一阶梯日重试）
)

// RenewalGraceMigrate 续费宽限期/重试台账迁移入口（Store.New 迁移链调用，幂等）。
// 宽限期两键落在 tenants.permissions JSON 上（无需补列），此处只建重试台账表与唯一索引。
func (s *Store) RenewalGraceMigrate() { s.ensureRenewalAttempts() }

// ensureRenewalAttempts 惰性建「自动续费建单重试台账」表 + 同日唯一索引（幂等）。
// 建在到期日之前也可用：本表记录的是「某个阶梯日的建单尝试」，与是否处于宽限期无关。
func (s *Store) ensureRenewalAttempts() {
	d := db.CurrentDialect()
	if err := db.ExecDDL(s.db, d, `CREATE TABLE IF NOT EXISTS renewal_attempts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		tenant_id INTEGER NOT NULL DEFAULT 0,
		package_id INTEGER NOT NULL DEFAULT 0,
		attempt_date TEXT NOT NULL DEFAULT '',
		order_id INTEGER NOT NULL DEFAULT 0,
		order_no TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL DEFAULT '',
		reason TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL DEFAULT '')`); err != nil {
		observability.Error(context.Background(), "renewal_attempts 建表失败", "err", err.Error())
		return
	}
	// 同日唯一键是「不重复建单」的硬闸门；存量重复行（理论上不存在，本表新建）会让建索引失败，
	// 失败只出声不阻断启动——与 coupons/usdt 的迁移口径一致。
	if err := db.ExecDDL(s.db, d,
		`CREATE UNIQUE INDEX IF NOT EXISTS uniq_renewal_attempt_day ON renewal_attempts(tenant_id, package_id, attempt_date)`); err != nil {
		observability.Warn(context.Background(), "renewal_attempts 同日唯一索引建立失败（需人工核查存量重复行）", "err", err.Error())
	}
}

// ClaimRenewalAttempt 抢占「本租户本包今日」的建单资格。
// 参数：tid=租户 ID，packageID=商业包 ID，attemptDate=本地日期（2006-01-02）。
// 返回 true=抢到（调用方可以建单）；false=今日已抢过（含并发对手抢先），调用方应直接跳过。
//
// 语义刻意做成「抢到即消耗」：建单失败也认这一格，避免同一天反复撞同一个必败错误
// （包下架、DB 抖动）把订单表刷成一串垃圾 pending 单；重试节奏交给下一个阶梯日。
func (s *Store) ClaimRenewalAttempt(tid, packageID int64, attemptDate string) (bool, error) {
	s.ensureRenewalAttempts()
	if packageID <= 0 || attemptDate == "" {
		return false, nil
	}
	res, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT OR IGNORE INTO renewal_attempts (tenant_id, package_id, attempt_date, order_id, order_no, status, reason, created_at)
		 VALUES (?,?,?,0,'',?,'',?)`, tid, packageID, attemptDate, RenewalAttemptPending, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// FinishRenewalAttempt 回写本次建单结果（订单号/成功失败与原因），供管理台与排障查看。
// 参数：status 取 RenewalAttemptCreated / RenewalAttemptFailed；reason 为失败原因（可空）。
// 只更新今日那一格（唯一键定位），不回写历史行。
func (s *Store) FinishRenewalAttempt(tid, packageID int64, attemptDate, status string, orderID int64, orderNo, reason string) error {
	s.ensureRenewalAttempts()
	if attemptDate == "" || packageID <= 0 {
		return nil
	}
	_, err := db.Exec(s.db, db.CurrentDialect(),
		"UPDATE renewal_attempts SET status=?, order_id=?, order_no=?, reason=? "+
			"WHERE tenant_id=? AND package_id=? AND attempt_date=?",
		status, orderID, orderNo, reason, tid, packageID, attemptDate)
	return err
}

// RenewalAttemptsToday 今日建单尝试数（管理台/单测断言用：同一天多实例也只应为 0 或 1）。
func (s *Store) RenewalAttemptsToday(tid, packageID int64, attemptDate string) int {
	s.ensureRenewalAttempts()
	var n int64
	if err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COUNT(1) FROM renewal_attempts WHERE tenant_id=? AND package_id=? AND attempt_date=?",
		tid, packageID, attemptDate).Scan(&n); err != nil {
		return 0
	}
	return int(n)
}

// SetSubscriptionGrace 落「宽限期截止时刻」并复位进入通知的发送标记。
// 参数：tid=租户 ID，graceEnd=宽限期截止时刻（RFC3339 落库，与 package_expires_at 同口径）。
//
// ★ 单字段原子写（db.JSONPatchSet）：不得整体覆盖 permissions——扫描任务读到的快照与
//
//	用户并发购买/充值之间存在丢失更新窗口（B1 整改口径，见 autorenew.go 同款写法）。
func (s *Store) SetSubscriptionGrace(tid int64, graceEnd time.Time) error {
	d := db.CurrentDialect()
	patch := `{"grace_expires_at":"` + graceEnd.UTC().Format(time.RFC3339) + `","notified_grace":false}`
	_, err := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
		patch, time.Now().Format(time.RFC3339), tid)
	return err
}

// MarkGraceNotified 「已进入宽限期」站内通知已发（去重标记，单字段原子置位）。
// 先置位再发通知：两步之间崩溃只会少一条提醒，反序则每轮扫描重复轰炸。
func (s *Store) MarkGraceNotified(tid int64) error {
	d := db.CurrentDialect()
	_, err := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
		`{"notified_grace":true}`, time.Now().Format(time.RFC3339), tid)
	return err
}

// ClearSubscriptionGrace 清宽限期状态（续订成交、或到期提醒档位需要重新计时时调用）。
// 摘除订阅身份的一侧由 ExpirePackage 的补丁统一负责，不走本函数。
func (s *Store) ClearSubscriptionGrace(tid int64) error {
	d := db.CurrentDialect()
	_, err := db.Exec(s.db, d,
		"UPDATE tenants SET "+db.JSONPatchSet(d, "permissions")+", updated_at=? WHERE id=?",
		`{"grace_expires_at":"","notified_grace":false}`, time.Now().Format(time.RFC3339), tid)
	return err
}
