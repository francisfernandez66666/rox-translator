// ============ wallet_backfill.go · 职责说明 ============
// 存量台账的**一次性**归档回填（★ 2026-10-30 三事合一批 组2b，W7／W8／W9／W10 的执行体）。
//
// 守卫判据只问一件事：**还有没有未归档的行**（tier 列为空＝数据层面的事实）。
// ⚠️ 禁止写成"tier 列存不存在"——补列语句在同一次启动里就会把它自己满足，
// 于是有历史的库上回填**永不执行**（AGENTS §三 那条现网事故：幂等迁移的守卫被同批建表语句先满足，
// 结果是 sessions／messages 两张孤儿表躺在那里没人认领）。
// 同理，第二次启动必须**零改动**：跑完之后再跑一次，pending 读数必须是 0。
//
// 五步的顺序是判据的一部分（不许重排）：
//
//	第 0 步  packages.tier 生成（只在列为空时按天数区间折一次）。
//	         ⚠️ 必须最先：后面 plan 台账归档要读这一列。列还没生成就去归档＝每一环都判"取不到"＝全落 legacy。
//	第 1 步  plan 行里的**永久哨兵**（expires_at 以 9999- 开头）归 plan_permanent。
//	         这是一条确定事实（C3：planGrantExpiry 对 duration_days<=0 就写 9999-12-31，
//	         字典序比较天然恒真），与"猜天数"无关，所以它能单独成一步，不必等包码链路。
//	第 2 步  该租户**恰有一条**待归档 plan 行 ⇒ 按 tenants.permissions.package_code 现读包档位。
//	         为什么限定"恰有一条"：套餐码是**当前**订阅身份，续费/升级会覆盖它，
//	         所以它对"最近那一次发放"是准的，对更早的历史行是错的。多于一条时宁可全部留给第 4 步，
//	         也不给一条旧包安上一个新包的档位（那会让同一份库在两个时刻给出两种扣法）。
//	第 3 步  trial/task 逐行归档 ＋ 个人归属（见下面"归属三条"）。
//	第 4 步  剩下的一律 legacy ＋计数（兜底桶：仍会被扣，只是排在最后，不丢钱也不抢先后）。
//
// 归属三条（W6／W9 的边界，一条都不许放宽）：
//
//	register ← kind='trial' ∧ source='register'。⚠️ **ref_id 的含义按发放路径分两种**（本批实读纠正）：
//	           ① 账号级发放（GrantUserTrialOnce，2026-10-21 M3 起）写 ref_id=**uid**；
//	           ② 更早的注册发放与超管重发（api/register.go、api/watchdog.go）写 ref_id=0。
//	           所以归属判据只有两条诚实的来路：ref_id>0 且**该 id 真在这个租户里存在** ⇒ 就是它；
//	           否则该租户是个人租户（is_personal=1）且**恰有一个家行用户** ⇒ 归他。
//	           两条都不成立就留 0（＝租户共享，与改动前的实际行为一致）并计数，**不许挑一个成员挂上去**。
//	           施工稿里那句「ref_id 存的正是 uid」只对①成立，拿它给②的库存硬归＝把赠送挂到错人名下。
//	task     ← kind='task'。按 (tenant_id, ref_id=任务定义号, expires_at) 去 user_task_rewards 现读，
//	           **只在唯一命中时才归**；叠加发放的两条台账撞在同一到期时刻时留 0＋计数
//	           （W9 判红的正是"拿 ref_id 硬猜"）。
//	plan_*   ← 归租户共享池（user_id=0）：用户口径是"套餐里该用户可用的那部分"，
//	           那是一道**可用池**判据（§4.5 的 orgs.token_limit），不是行归属；
//	           把共享包写成个人 uid＝别人的额度他核销不了，属反向事故。
//
// packages 的列生成之后**列就是唯一事实源**：若运营把某一行改成与天数区间不一致的档位，
// 读侧以列为准，本函数只数一行读数（W10）——拿近似值去覆盖显式配置＝把运营那一次保存抹掉。
//
// fail-open：任何一步读不到/写失败都只让那一档留空并计数，**绝不让启动失败**；
// 但每一步都出声（observability），因为"回填跑了、报 0 行"与"回填根本没跑"必须分得开。
// =============================================
package store

import (
	"context"
	"encoding/json"
	"strings"
	"translator/internal/db"
	"translator/internal/observability"
)

// planPermanentSentinelPrefix 永久包的到期哨兵前缀（9999-12-31 那一档）。
// 用前缀而不是等值：不同批次写过的 9999 变体都归同一档。
const planPermanentSentinelPrefix = "9999-"

// pendingTierWhere 「尚未归档」这一条件的唯一写法。
// ⚠️ 两种形态都要认（空串与 NULL）：PG 的 ADD COLUMN ... NOT NULL DEFAULT 落给存量行的是空串，
// 而经旁路 SQL 插入的行可能是 NULL——只认一种就等于把一半的库存判成"已归档"。
const pendingTierWhere = `(tier='' OR tier IS NULL)`

// walletBackfillReadings 一次回填的读数（进启动日志，也是 W8/W9/W10 三条断言的判据对象）。
type walletBackfillReadings struct {
	Pending      int64 // 跑之前未归档的行数（守卫判据本身）
	Done         int64 // 本次归档成功的行数
	Unresolved   int64 // 归了档但**拿不到个人归属**、按租户共享出货的行数（register/task 两族）
	Fallback     int64 // 落 legacy 的行数（未知形态兜底）
	PackagesTier int64 // packages 现有档位的行数
	Disagree     int64 // packages 列与天数区间不一致的行数（以列为准，只报不改）
}

// WalletBackfill 跑一次存量归档。随 WalletMigrate 调用，幂等。
func (s *Store) WalletBackfill() walletBackfillReadings {
	var r walletBackfillReadings
	d := db.CurrentDialect()
	ctx := context.Background()

	// —— 第 0 步：packages 档位列（只在空列时生成一次）——
	bands := []struct {
		tier  string
		where string
	}{
		{TierPlanMonthly, "duration_days>0 AND duration_days<=31"},
		{TierPlanYearly, "duration_days>31 AND duration_days<=366"},
		{TierPlanPermanent, "duration_days>366 OR duration_days<=0"},
	}
	for _, b := range bands {
		if _, err := db.Exec(s.db, d,
			"UPDATE packages SET tier=? WHERE (tier='' OR tier IS NULL) AND ("+b.where+")", b.tier); err != nil {
			observability.Warn(ctx, "钱包回填：packages 档位生成失败（该档跳过；plan 台账那一步会落 legacy）",
				"tier", b.tier, "error", err.Error())
		}
	}
	_ = db.QueryRow(s.db, d, `SELECT COUNT(*) FROM packages WHERE tier<>''`).Scan(&r.PackagesTier)
	_ = db.QueryRow(s.db, d,
		`SELECT COUNT(*) FROM packages WHERE tier<>'' AND (CASE `+
			"WHEN duration_days>0 AND duration_days<=31 THEN '"+TierPlanMonthly+"' "+
			"WHEN duration_days>31 AND duration_days<=366 THEN '"+TierPlanYearly+"' "+
			"ELSE '"+TierPlanPermanent+"' END) <> tier").Scan(&r.Disagree)

	// —— 守卫判据：数据层面"还有没归档的行"（W8 的正靶）——
	if err := db.QueryRow(s.db, d, "SELECT COUNT(*) FROM quota_grants WHERE "+pendingTierWhere).Scan(&r.Pending); err != nil {
		observability.Warn(ctx, "钱包回填：读待归档行数失败（本轮不回填）", "error", err.Error())
		return r
	}
	if r.Pending == 0 {
		return r // 第二次启动走这一支：零 DDL、零改动（W7 的幂等腿）
	}

	// —— 第 1 步：永久哨兵 ——
	if res, err := db.Exec(s.db, d,
		"UPDATE quota_grants SET tier=? WHERE "+pendingTierWhere+" AND kind='plan' AND expires_at LIKE ?",
		TierPlanPermanent, planPermanentSentinelPrefix+"%"); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			r.Done += n
		}
	} else {
		observability.Warn(ctx, "钱包回填：永久哨兵归档失败", "error", err.Error())
	}

	// —— 第 2 步：该租户恰有一条待归档 plan 行 ⇒ 按当前套餐码归档 ——
	type planRow struct {
		tid int64
		id  int64
	}
	var live []planRow
	if rows, err := db.Query(s.db, d,
		"SELECT tenant_id, MIN(id) FROM quota_grants WHERE "+pendingTierWhere+
			" AND kind='plan' GROUP BY tenant_id HAVING COUNT(*)=1"); err == nil {
		for rows.Next() {
			var p planRow
			if err := rows.Scan(&p.tid, &p.id); err == nil {
				live = append(live, p)
			}
		}
		rows.Close()
	} else {
		observability.Warn(ctx, "钱包回填：读待归档 plan 行失败（本轮跳过第 2 步）", "error", err.Error())
	}
	for _, p := range live {
		tier, ok := s.tenantPackageTier(p.tid)
		if !ok {
			continue // 留给第 4 步落 legacy——这一条的档位此刻没有任何可靠依据
		}
		if _, err := db.Exec(s.db, d,
			"UPDATE quota_grants SET tier=?, user_id=0 WHERE id=? AND "+pendingTierWhere, tier, p.id); err != nil {
			observability.Warn(ctx, "钱包回填：plan 行归档失败（留空等下一轮）", "grant_id", p.id, "error", err.Error())
			continue
		}
		r.Done++
	}

	// —— 第 3 步：trial / task 逐行归档 ＋ 个人归属 ——
	if rows, err := db.Query(s.db, d,
		"SELECT id, tenant_id, kind, COALESCE(source,''), COALESCE(ref_id,0), COALESCE(expires_at,'')"+
			" FROM quota_grants WHERE "+pendingTierWhere+" AND kind IN ('trial','task')"); err != nil {
		observability.Warn(ctx, "钱包回填：读 trial/task 行失败（本轮跳过第 3 步）", "error", err.Error())
	} else {
		defer rows.Close()
		type pending struct {
			id, tid, refID int64
			kind, source   string
			expiresAt      string
		}
		var todo []pending
		for rows.Next() {
			var p pending
			if err := rows.Scan(&p.id, &p.tid, &p.kind, &p.source, &p.refID, &p.expiresAt); err == nil {
				todo = append(todo, p)
			}
		}
		for _, p := range todo {
			tier, uid := TierLegacy, int64(0)
			switch {
			case p.kind == "task":
				tier = TierTask
				if got, ok := s.taskGrantOwner(p.tid, p.refID, p.expiresAt); ok {
					uid = got
				} else {
					r.Unresolved++
				}
			case p.kind == "trial" && p.source == "register":
				tier = TierRegister
				if got, ok := s.registerGrantOwner(p.tid, p.refID); ok {
					uid = got
				} else {
					r.Unresolved++
				}
			default:
				// trial 的其它来源（rescue_pack／活动补发／超管自定义 source）不冒充注册赠送。
				r.Fallback++
			}
			if _, e := db.Exec(s.db, d,
				"UPDATE quota_grants SET tier=?, user_id=? WHERE id=? AND "+pendingTierWhere, tier, uid, p.id); e != nil {
				observability.Warn(ctx, "钱包回填：单行归档失败（留空等下一轮）", "grant_id", p.id, "error", e.Error())
				continue
			}
			r.Done++
		}
	}

	// —— 第 4 步：剩下的一律 legacy（兜底桶）——
	if res, err := db.Exec(s.db, d,
		"UPDATE quota_grants SET tier=? WHERE "+pendingTierWhere, TierLegacy); err == nil {
		if n, _ := res.RowsAffected(); n > 0 {
			r.Fallback += n
			r.Done += n
		}
	} else {
		observability.Warn(ctx, "钱包回填：兜底归档失败", "error", err.Error())
	}

	observability.Info(ctx, "钱包档位回填完成",
		"pending", r.Pending, "done", r.Done, "unresolved_owner", r.Unresolved,
		"fallback_legacy", r.Fallback, "packages_tier", r.PackagesTier, "packages_disagree", r.Disagree)
	return r
}

// tenantPackageTier 现读该租户**当前订阅套餐**的档位（第 2 步用）。
// 取不到（没订阅身份／包码在 packages 里查不到／档位列为空或不合名单）一律回 false。
func (s *Store) tenantPackageTier(tid int64) (string, bool) {
	if tid <= 0 {
		return "", false
	}
	var perms string
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(permissions,'') FROM tenants WHERE id=?", tid).Scan(&perms); err != nil {
		return "", false
	}
	// 用 encoding/json 而不是 JSON1：SQLite 要编译期带 JSON1、PG 的函数名是另一套，
	// 而这里只需要一个字段（AGENTS §一·4「同一份 SQL 两方言都跑得通」）。
	var p struct {
		PackageCode string `json:"package_code"`
	}
	if err := json.Unmarshal([]byte(perms), &p); err != nil || strings.TrimSpace(p.PackageCode) == "" {
		return "", false
	}
	var tier string
	// 平台包（tenant_id=0）与租户自建包同名时**按本租户优先**：ORDER BY tenant_id DESC 那一腿
	// 就是这个意思；写成裸 LIMIT 1 在 PG 上取到谁没有保证（AGENTS §18 那条 is_home 假红同族）。
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT COALESCE(tier,'') FROM packages WHERE code=? AND (tenant_id=? OR tenant_id=0) ORDER BY tenant_id DESC LIMIT 1",
		p.PackageCode, tid).Scan(&tier)
	if err != nil {
		return "", false
	}
	switch strings.TrimSpace(tier) {
	case TierPlanMonthly, TierPlanYearly, TierPlanPermanent:
		return strings.TrimSpace(tier), true
	default:
		return "", false
	}
}

// registerGrantOwner 判一条注册赠送台账该归谁（两条诚实来路，按可靠性排序）。
//
//	① ref_id>0 且**该 id 真是这个租户的成员** ⇒ 就是它（账号级发放那条路径写下的正是 uid；
//	   带租户条件是因为 ref_id 在别的来源里是订单号/任务号，一个碰巧同值的订单号绝不能被当成人）。
//	② 个人租户且恰有一个家行用户 ⇒ 归那个家行（老路径 ref_id=0 时唯一还能站住的推断：
//	   个人租户本身就意味着"这份赠送是发给这一个人的"）。
//	两条都不成立回 false（留 0＝按租户共享出货，与改动前的行为一致，且必须计数）。
func (s *Store) registerGrantOwner(tid, refID int64) (int64, bool) {
	if tid > 0 && refID > 0 {
		var one int64
		err := db.QueryRow(s.db, db.CurrentDialect(),
			"SELECT id FROM users WHERE id=? AND tenant_id=?", refID, tid).Scan(&one)
		if err == nil && one == refID {
			return one, true
		}
	}
	return s.personalTenantHomeUser(tid)
}

// personalTenantHomeUser 取该租户**唯一**的家行用户 id。
// 返回 false 的三种情况一律"不归属"：非个人租户（公司里的赠送本就按租户共享）、
// 查不到家行、脏数据里有两个家行（那条 HAVING 让它取不到值，正是我们要的"不归"）。
func (s *Store) personalTenantHomeUser(tid int64) (int64, bool) {
	if tid <= 0 {
		return 0, false
	}
	var personal int
	if err := db.QueryRow(s.db, db.CurrentDialect(), "SELECT COALESCE(is_personal,0) FROM tenants WHERE id=?", tid).Scan(&personal); err != nil {
		return 0, false
	}
	if personal != 1 {
		return 0, false
	}
	var n int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT MIN(id) FROM users WHERE tenant_id=? AND is_home=1 GROUP BY tenant_id HAVING COUNT(*)=1", tid).Scan(&n)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// taskGrantOwner 按 (租户, 任务定义号, 到期时刻) 反查唯一领奖人。
// ⚠️ 唯一命中才归：user_task_rewards 没有 grant_id 外键（它记的是 task_id/user_id/dedup_key/expires_at），
// 叠加发放的两条台账可能落在同一个到期时刻上——那时把其中一条归给某个人就是猜（W9 判红的正是这个）。
func (s *Store) taskGrantOwner(tid, taskID int64, expires string) (int64, bool) {
	if tid <= 0 || taskID <= 0 || strings.TrimSpace(expires) == "" {
		return 0, false
	}
	var uid int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT MIN(user_id) FROM user_task_rewards WHERE tenant_id=? AND task_id=? AND expires_at=?"+
			" GROUP BY task_id, expires_at HAVING COUNT(DISTINCT user_id)=1 AND COUNT(*)=1",
		tid, taskID, expires).Scan(&uid)
	if err != nil || uid <= 0 {
		return 0, false
	}
	return uid, true
}
