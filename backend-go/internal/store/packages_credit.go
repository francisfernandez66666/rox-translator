// ============ packages_credit.go · 职责说明 ============
// 商业包发放的 token 入账助手（从原 packages_free.go 抽出，避免免费包手动领取路径回潮）。
// 调用方只负责给出目录承诺的 token 数（PackageTokenAmount），不得再乘折算率或 markup。
// =============================================
package store

import (
	"database/sql"
	"time"

	"translator/internal/db"
)

// creditTokensTx 事务内给租户余额账户入账内部 token（<=0 视为无操作）。
// 事务内先确保余额账户行存在，再原子累加，避免账户行缺失时 UPDATE 影响 0 行导致 token 静默丢失。
func (s *Store) creditTokensTx(tx *sql.Tx, tid, tokens int64) error {
	if tokens <= 0 {
		return nil
	}
	now := time.Now().Format(time.RFC3339)
	// ON CONFLICT 双方言同形；旧的 INSERT OR IGNORE 是 SQLite 专属写法。
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"INSERT INTO balance_accounts (tenant_id, balance, currency, updated_at) VALUES (?,0,'tokens',?) ON CONFLICT (tenant_id) DO NOTHING",
		tid, now); err != nil {
		return err
	}
	if _, err := db.Exec(tx, db.CurrentDialect(),
		"UPDATE balance_accounts SET balance=balance+?, updated_at=? WHERE tenant_id=?",
		tokens, now, tid); err != nil {
		return err
	}
	// ★ P1 多实例闭环：包折算 token 入账通知影子失效
	notifyTenantBalanceChanged(tid)
	return nil
}
