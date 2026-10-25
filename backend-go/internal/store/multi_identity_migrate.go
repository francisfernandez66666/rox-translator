// ============ multi_identity_migrate.go · 多身份账号体系迁移（★ 2026-10-24 方案 B1）============
// 本文件只做 users 表的「身份模型换轨」，不承接业务读写：
//   ① 补 account_id（「人」的抽象键，同一人所有行共享）与 is_home（家行标记）两列——列形状
//      由 store.go 的建表语句 + columnAdditions 清单供给（新库/存量库同一份），本文件负责**回填**；
//   ② 回填判据问**数据层面的事实**（account_id 是否为空、账户有没有家行），
//      不问「表/列在不在」——AGENTS §三「幂等迁移的守卫判据不许被同批建表语句先满足」；
//   ③ 邮箱唯一索引从**全局**改为**按租户**（D3）：同一人可以在多个租户各持一行同邮箱，
//      但同一租户内一邮箱仍只能有一行。全局那条索引由本文件摘掉，
//      并**由本文件独占创建**按租户的新索引——OneidMigrate 不再建邮箱唯一索引
//      （否则每次启动把全局索引写回来，等于把这一族能力重新锁死，是同族「多把尺子」缺陷的温床）。
//
// 失败口径：只留日志不阻断启动（同仓既有迁移模式）。
// ================================================================

package store

import (
	"context"
	"fmt"

	"translator/internal/db"
	"translator/internal/observability"
)

// EmailUniqueIndexLegacy 历史**全局**邮箱部分唯一索引名（OneidMigrate 旧形态创建的那一条）。
// 常量导出给闸门与测试用：判「旧全局索引不许复活」必须按名字问，不许在两处各拼一遍字面。
const EmailUniqueIndexLegacy = "idx_users_email_lower_active"

// EmailUniqueIndexTenant 现行**按租户**邮箱部分唯一索引名（多身份方案 D3）。
const EmailUniqueIndexTenant = "idx_users_email_lower_active_tenant"

// MultiIdentityMigrate 幂等执行多身份迁移（随 Store.New 调用，排在 OneidMigrate 之后）。
// 参数：无（作用在 s.db 的 users 表上）。所有步骤都可重复执行：跑第二次零改动。
func (s *Store) MultiIdentityMigrate() {
	ctx := context.Background()
	d := db.CurrentDialect()

	// ① account_id 回填：cast(id as text) —— SQLite 与 PostgreSQL 都认 CAST(id AS TEXT)，
	//    且「一人一行」的现状下这个值天然自洽（每行就是一个人）。
	//    ⚠️ 不能用邮箱当人键：handleUpdateEmail 会改邮箱，拿邮箱当人键＝一次换绑把一个人劈成两个。
	if _, err := db.Exec(s.db, d, `UPDATE users SET account_id=CAST(id AS TEXT) WHERE account_id=''`); err != nil {
		observability.Warn(ctx, "多身份迁移：account_id 回填失败（读路径按空账户号降级，不影响启动）", "err", err.Error())
		return
	}

	// ② is_home 回填「每人一行⇒全置 1」：现状每个账户只有一行，那一行就是它的家。
	//    纯企业用户（管理台建的企业行）由此自然成为家行——方案 D2 明确它没有个人行时企业行就是家。
	if _, err := db.Exec(s.db, d, `UPDATE users SET is_home=1 WHERE is_home<>1 AND account_id<>''`); err != nil {
		observability.Warn(ctx, "多身份迁移：is_home 回填失败", "err", err.Error())
	}

	// ③ 不变式收敛：一个账户**至多**一个家行、且只要有可用行就必须有一个家行。
	//    回填后正常情况下每账户恰好一行，这一步是为「人工并过账户／将来批量建号」兜底，
	//    也让这段迁移在已经有多行的库上仍然跑得出合法状态。
	if err := s.reconcileHomeRows(); err != nil {
		observability.Warn(ctx, "多身份迁移：家行不变式收敛失败", "err", err.Error())
	}

	// ④ 索引换轨：先摘全局唯一索引，再按租户建。
	//    顺序不能反——按租户索引是全局索引的**严格放宽**，全局那条还在时新索引不会被违反，
	//    但反过来先建新再摘旧不会失败；这里按「摘旧建新」写是为了让任何一次启动后
	//    库里都只剩一把邮箱尺子（留两条索引＝留两条互相矛盾的判据）。
	if _, err := db.Exec(s.db, d, "DROP INDEX IF EXISTS "+EmailUniqueIndexLegacy); err != nil {
		observability.Warn(ctx, "多身份迁移：旧全局邮箱唯一索引摘除失败（同邮箱跨租户加入会继续被拒）",
			"index", EmailUniqueIndexLegacy, "err", err.Error())
	}
	// 表达式必须与读路径同源（lower(trim(email))），否则「库里有空格」与「登录时 trim」两把尺子。
	if err := db.ExecDDL(s.db, d, fmt.Sprintf(
		`CREATE UNIQUE INDEX IF NOT EXISTS %s ON users(tenant_id, lower(trim(email))) WHERE trim(email)<>'' AND status IN ('active','deactivating')`,
		EmailUniqueIndexTenant)); err != nil {
		observability.Warn(ctx, "多身份迁移：按租户邮箱唯一索引创建失败（同一租户内存量重复邮箱需人工清理后重启）",
			"index", EmailUniqueIndexTenant, "err", err.Error())
	}
}

// reconcileHomeRows 把每个账户的家行收敛成「有且仅有一个」：
//   - 多枚家行 ⇒ 只留 id 最小那一枚（最小 id＝最早的行，也就是历史上那个"本人账号"）；
//   - 一枚家行都没有、但还有 active/deactivating 行 ⇒ 把 id 最小的可用行提升为家。
//
// 判据现读现算在 Go 侧而不是拼一条 UPDATE…WHERE id NOT IN (SELECT … GROUP BY …)：
// SQLite 的 UPDATE 会在扫描过程中读到**自己刚写过的行**，同一账户的第二行是否还满足
// 「SUM(is_home)=0」取决于循环到第几行——那种"结果依赖扫描顺序"的迁移不敢留。
// 返回错误只可能是查询/写入失败；单账户处理失败即中止并回传（调用方留日志、下次启动重跑）。
func (s *Store) reconcileHomeRows() error {
	d := db.CurrentDialect()
	// 需要收敛的账户：家行数 ≠ 1
	rows, err := db.Query(s.db, d,
		`SELECT account_id, COUNT(*) FROM users
		 WHERE account_id<>'' GROUP BY account_id HAVING COUNT(*) > 1 OR SUM(CASE WHEN is_home=1 THEN 1 ELSE 0 END) <> 1`)
	if err != nil {
		return err
	}
	type acct struct {
		id string
		n  int
	}
	var targets []acct
	for rows.Next() {
		var a acct
		if err := rows.Scan(&a.id, &a.n); err == nil && a.id != "" {
			targets = append(targets, a)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, a := range targets {
		// 该账户期望的家行：优先 id 最小的可用行（active/deactivating），
		// 全是停用行时退到 id 最小的那一行（停用账户不要求有家，但保留一个确定值比留 0 枚好排查）。
		var want int64
		q := `SELECT id FROM users WHERE account_id=? ORDER BY CASE WHEN status IN ('active','deactivating') THEN 0 ELSE 1 END, id LIMIT 1`
		if err := db.QueryRow(s.db, d, q, a.id).Scan(&want); err != nil {
			continue // 读不到就不猜：下一次启动再收敛
		}
		if _, err := db.Exec(s.db, d, `UPDATE users SET is_home=0 WHERE account_id=? AND is_home<>0`, a.id); err != nil {
			return err
		}
		if _, err := db.Exec(s.db, d, `UPDATE users SET is_home=1 WHERE id=? AND account_id=?`, want, a.id); err != nil {
			return err
		}
	}
	return nil
}
