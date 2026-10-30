// ============ wallet_user_pool.go · 职责说明 ============
// 租户共享档（plan_*）的「该用户本月还能不能用」判据（★ 2026-10-30 三事合一批 组3 #9，D-9）。
//
// 要解决的问题不是"余额不够"，而是**归属与预算的错配**：
//
//	租户买的包（月/年/长期）是**租户共享**的（quota_grants.user_id=0），任何成员都能核销。
//	但部门预算是按**部门**给的（orgs.token_limit，月度流量）。旧形态下这两件事只在
//	`CheckBudgetWalls` 里碰面——而它是一道**整请求闸**：部门预算一满，这个人连自己的
//	注册赠送／任务奖励（那是他的个人资产）也一起用不了。个人资产被租户配额连坐，是这一档的真缺陷。
//
// 本文件的口径（三条，缺一条就会长出反向缺陷）：
//
//	① **只管租户共享的 plan_* 档**：个人档（register/task）与每日免费**不受**这个判据
//	   （它们是个人资产，不是租户配额）；历史永久余额（TierLegacy）也不管——那是租户自己充进来的钱，
//	   不是"包"的分配语义（D-9 的原话就是「包中该用户可用部分」）。
//	② **上限来源＝现成的部门月度预算**，不新造 per-user 分配表（D-9）。判据只引用既有谓词
//	   `PlatformTaskTypeExclPred`（额度侧那把尺子）＋ `MonthStartBound()`（唯一月界），
//	   **禁止**在这里自己算日历边界（每月 1 号 8 小时窗口那个老坑，见 quota_org.go）。
//	③ **fail-open**：没部门、部门没配预算、读库失败 ⇒ 一律"不限"。
//	   这是一条成本分配口径，不是防越权；把它做成 fail-closed 的代价是"库抖一下客户就翻不了"，
//	   而真正的超支防线仍然是 CheckBudgetWalls（它照旧拦整请求）。
//
// ⚠️ 与 CheckBudgetWalls 的分工（别把两处合成一处）：
//
//	CheckBudgetWalls = 整请求闸（撞墙即拦，含个人档）；
//	本文件           = 拆扣时的档位过滤（部门满额时**跳过共享 plan 档**，继续吃个人档）。
//	合起来的行为才是对客户合理的：预算用完 ⇒ 还能用自己的积分，但不再消耗公司共享的包。
//
// =============================================
package store

import (
	"context"

	"translator/internal/observability"
)

// UserSharedPlanBlocked 报告该用户本月是否**已不能用租户共享的 plan_* 档**（D-9）。
//
// 参数 tid: 租户；uid: 本次消费的用户（0＝不知道是谁/平台任务 ⇒ 不拦，返回 false）。
// 返回 true 时调用方在拆扣里跳过 `user_id=0` 的 plan 档，其余档照扣。
//
// 判定沿用户所属组织向上找**最近一个启用了预算（token_limit>0）的祖先**，与 CheckBudgetWalls
// 的部门墙完全同一条走法（"预算互不嵌套扣减"：最近那层没超就不再看更远的祖先）。
func (s *Store) UserSharedPlanBlocked(tid, uid int64) bool {
	if s == nil || s.db == nil || tid <= 0 || uid <= 0 {
		return false
	}
	u, err := s.GetUser(uid, tid)
	if err != nil || u == nil || u.OrgID <= 0 {
		return false // 无部门／读不到 ⇒ 不限（③）
	}
	cur, err := s.GetOrgByID(u.OrgID)
	if err != nil || cur == nil || cur.TenantID != tid {
		return false
	}
	for depth := 0; depth < 32 && cur != nil && cur.ID > 0; depth++ {
		if cur.TokenLimit > 0 {
			used, uerr := s.OrgTokensUsedThisMonth(tid, cur.ID)
			if uerr != nil {
				observability.Warn(context.Background(), "用户可用池判定读库失败（按不限放行）",
					"tenant_id", tid, "user_id", uid, "org_id", cur.ID, "error", uerr.Error())
				return false // ③ fail-open
			}
			return used >= cur.TokenLimit
		}
		if cur.ParentID <= 0 {
			break
		}
		parent, perr := s.GetOrgByID(cur.ParentID)
		if perr != nil {
			return false
		}
		cur = parent
	}
	return false // 沿链没有任何部门配了预算 ⇒ 不限
}

// sharedPlanTierOf 判一条台账行是不是「租户共享的收费档」（本文件唯一射程）。
// 参数 tier: 台账行档位；ownerUserID: 该行归属（0＝租户共享）。
// 返回 true 时，若 UserSharedPlanBlocked 为真则本次拆扣跳过这一行。
func sharedPlanTierOf(tier string, ownerUserID int64) bool {
	if ownerUserID != 0 {
		return false // 个人档（理论上涨不到，plan 行恒为 0；留着防御未来 per-user 分配）
	}
	switch tier {
	case TierPlanMonthly, TierPlanYearly, TierPlanPermanent:
		return true
	}
	return false
}
