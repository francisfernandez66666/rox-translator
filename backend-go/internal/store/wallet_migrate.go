// ============ wallet_migrate.go · 职责说明 ============
// 钱包数据面的幂等升级（★ 2026-10-30 三事合一批 组2b）：两条补列 ＋ 一张扣档明细表。
//
// 三件事各自为什么必须存在（不是"顺手加的列"）：
//
//	① `quota_grants.user_id`：现状是**整张表只有租户维度**，所以"注册送的那 300 积分"
//	   在库里与租户月包长得一模一样——个人消费顺序（①②③④⑤⑥）根本没有依据可判。
//	   `0` ＝租户共享档（套餐），任何成员可核销；`>0` ＝ 只该成员可核销（W6：跨用户扣他人赠送＝数据事故级）。
//	② `quota_grants.tier`：档位。空串＝**未回填**，回填判据就靠它（W8：判据必须是"还有没有空档位的行"
//	   这一数据层面事实，**禁止**写成"列存不存在"——补列语句在同批启动里就会先把它自己满足，
//	   于是有历史的库上回填永不执行；现网孤儿表 `sessions`/`messages` 正是那一族）。
//	③ `usage_debit_lines`：**一次扣款跨几档就写几行**。没有它，退款只能"整体退到某个桶"，
//	   而本批的退款口径是按原档回补（W13）；账单分档、`SUM(debit)=SUM(实扣)` 对账（W3/W23）
//	   也都没有事实源。`TierLegacy` 那一行 `grant_id=0`（它对应 balance_accounts，不是台账行）。
//
// ⚠️ 位置纪律：`packages.tier` 的补列**必须排在 `quota_grants` 回填之前**——
//
//	回填要把套餐行归成 plan_month/year/permanent，靠的就是现读 `packages.tier`；
//	列还没补上就去读它，SQL 直接报错，而 `WalletBackfill` 是 fail-open 的（读不到就留空＋WARN），
//	那会让"回填跑了"与"回填什么都没干"在日志里长得一样。
//
// 老库启动自动升级（AGENTS §一·1.4 幂等补列唯一入口 `db.EnsureColumns`；禁止一次性 ALTER 直执行）。
// =============================================
package store

import (
	"context"
	"translator/internal/db"
	"translator/internal/observability"
)

// WalletMigrate 钱包数据面的建表与补列，并跑一次回填。随 Store.New 调用（幂等）。
func (s *Store) WalletMigrate() {
	d := db.CurrentDialect()
	// ① packages 先补档位列（见文件头那条顺序纪律）
	if err := db.EnsureColumns(s.db, d, "packages", map[string]string{
		"tier": "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		s.walletMigrateFail("packages 补 tier 列", err)
		return
	}
	// ② quota_grants 补归属与档位
	if err := db.EnsureColumns(s.db, d, "quota_grants", map[string]string{
		"user_id": "INTEGER NOT NULL DEFAULT 0",
		"tier":    "TEXT NOT NULL DEFAULT ''",
	}); err != nil {
		s.walletMigrateFail("quota_grants 补 user_id/tier 列", err)
		return
	}
	// ③ 扣档明细表＋两条检索索引（退款按 ledger_id 找原拆扣、按 grant_id 找某一行台账的历史进出）
	//    `refunded_tokens` 是**按档回补的幂等标记**（W13/W14）：退款只回补 `tokens - refunded_tokens`
	//    那一段，同一笔退两次第二次是 0；没有这一列的话，守卫式 UPDATE 只能在"客户还没再花掉"时才挡住重复退。
	ddl := []string{
		`CREATE TABLE IF NOT EXISTS usage_debit_lines (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ledger_id INTEGER NOT NULL DEFAULT 0,
			tenant_id INTEGER NOT NULL DEFAULT 0,
			user_id INTEGER NOT NULL DEFAULT 0,
			tier TEXT NOT NULL DEFAULT '',
			grant_id INTEGER NOT NULL DEFAULT 0,
			tokens INTEGER NOT NULL DEFAULT 0,
			refunded_tokens INTEGER NOT NULL DEFAULT 0,
			created_at TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_debit_lines_ledger ON usage_debit_lines(ledger_id)`,
		`CREATE INDEX IF NOT EXISTS idx_usage_debit_lines_grant ON usage_debit_lines(grant_id)`,
		// 档位与归属维度都要按人/按租户聚合（钱包面板与对账腿都走这一条）
		`CREATE INDEX IF NOT EXISTS idx_usage_debit_lines_tier ON usage_debit_lines(tenant_id, user_id, tier)`,
		// 台账行按档位＋剩余量筛（扣减主查询的 WHERE 形状）
		`CREATE INDEX IF NOT EXISTS idx_qg_tier_spend ON quota_grants(tenant_id, user_id, tier, "left")`,
	}
	for _, q := range ddl {
		if err := db.ExecDDL(s.db, d, q); err != nil {
			s.walletMigrateFail("钱包建表/建索引", err)
			return
		}
	}
	// ③-b 老库补 `refunded_tokens`（本列是 2026-10-30 组3 后半新加的：先于它建出来的库
	//     走 CREATE TABLE IF NOT EXISTS 不会被补上，必须走幂等补列）。
	if err := db.EnsureColumns(s.db, d, "usage_debit_lines", map[string]string{
		"refunded_tokens": "INTEGER NOT NULL DEFAULT 0",
	}); err != nil {
		s.walletMigrateFail("usage_debit_lines 补 refunded_tokens 列", err)
		return
	}
	// ④ 回填（判据是数据层面：还有没有 tier='' 的行；见 wallet_backfill.go 文件头）
	s.WalletBackfill()
}

// walletMigrateFail 迁移失败的出声口径。
//
// ⚠️ 这里**不许** panic：钱包列缺失时旧代码路径（按到期日扣）照样能跑，
// 把整站启动挡住的代价远大于"这一档暂时不分档"；但必须 Warn 级出声——
// 静默的 schema 缺口意味着客户后来拿到的钱包面板是空的，而那条腿永远不会报错。
// ⚠️ 也不许写成 `log.Printf`：AGENTS §一·2 那条棘轮按**行**数且只减不增（internal 根基线 176），
// 新增一处存量口径的日志就是当场红；启动期没有请求上下文，用 context.Background() 走 observability。
func (s *Store) walletMigrateFail(what string, err error) {
	observability.Warn(context.Background(), "钱包数据面迁移失败（不阻断启动，档位与拆扣暂不生效）",
		"step", what, "error", err.Error())
}
