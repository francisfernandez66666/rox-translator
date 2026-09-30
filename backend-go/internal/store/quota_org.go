// ============ quota_org.go · 职责说明 ============
// store 包部门预算（四期增强）数据访问与双预算墙判定。
//   - 租管为每个部门分配月度 token 预算（orgs.token_limit），∑部门预算=租户总预算
//   - 部门墙：某部门（含子树）本月消耗 ≥ 该部门预算 → 拦截并首次提醒部门管理员
//   - 组织墙：全租户本月消耗 ∑部门预算 且 >0 → 拦截并首次提醒租户管理员
//   - 月度口径：自然月（每月 1 日重置）；消耗取 usage_ledger.quantity（已含均摊系数）
//
// 两堵墙独立于强制计费开关——即使仅计量不扣减，预算墙照常生效（体验包用户也能感知约束）。
// =============================================
package store

import (
	"fmt"
	"time"
	"translator/internal/db"
)

// SetOrgTokenLimit 设置部门月度 token 预算（0=未启用该部门的部门墙）。
func (s *Store) SetOrgTokenLimit(id int64, limit int64) error {
	_, err := db.Exec(s.db, db.CurrentDialect(), "UPDATE orgs SET token_limit=?, updated_at=? WHERE id=?",
		limit, time.Now().Format(time.RFC3339), id)
	return err
}

// monthStart 当前自然月起点（★ 2026-10-01 〇-AF 修：日历语义不变，**渲染口径**必须与写入口径同区）。
//
// 为什么这是一条真缺陷而不是"测试 unlucky"：
//
//	usage_ledger.created_at 这一列在本仓**一律写 UTC**（billing.go:396/462/531…
//	`time.Now().UTC().Format(time.RFC3339)`，列型是 TEXT），而下面的谓词是
//
//	created_at>=? 的**字符串比较**——TEXT 列在 SQLite/PG 两侧都按字典序比，不比时刻。
//	旧实现用 `time.Now()` 的**本地时区**渲染边界，+08 主机上得到 "2026-10-01T00:00:00+08:00"，
//	于是每月 1 号本地 00:00–08:00 这**八个小时**里，前一日 "…T16:xx:xxZ" 起的所有行
//	按字典序都排在边界"后面"（其实全在前面）⇒ 本月已用恒读 0。
//	读 0 不是显示问题：CheckBudgetWalls 拿这个数判部门墙/组织墙，命中才拦翻译请求，
//	⇒ 每月开头八小时**预算墙形同虚设**，客户可以在墙上继续烧（与 〇-AD 那条
//	"防超支不能换防误扣"是同一条底线，只是这次的洞是日历给的）。
//
// 修法刻意只动渲染、不动日历：起点仍然取**本地历法的月初零点**（业务口径「自然月」不变、
// 客户看到的月度数不迁移），再把这个零点表示成 UTC——两侧同为 "…Z"，字典序＝时刻序，
// 于是任何时区偏移下都自洽。0 点整这一秒内的行会因 RFC3339 的小数秒尾巴（'.'<'Z'）
// 被字典序判在边界之前，属于 TEXT 时间戳的既有粒度缝，不在本条射程内。
func monthStart() string {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.Local).UTC().Format(time.RFC3339)
}

// MonthStartBound 月首边界的**唯一对外出口**（★ 2026-10-01 〇-AF 补丁三）。
//
// 存在的理由只有一条：这条边界在 2026-09 之前被抄成了**两份**——store 里的 monthStart
// 与 `internal/api/plans_api.go` 里内联的 `time.Date(...time.Local).Format(RFC3339)`，
// 两处都带同一个「本地时区渲染去比 UTC 写入的 TEXT 列」的洞，但只有内联那份在给客户
// 出数（收银台/订阅页的「本月已用」），于是每月开头八小时客户看到的是 0，
// 而 UAT A7s 的「今日已耗」同一条腿直接判红。抄一份＝修一处漏一处，与 §一·11
// 「谓词只许引用 store 里那一份常量」是同一条纪律，只是这次的对象是日历边界而不是谓词。
//
// ⚠️ 用法约束：需要「本月起点」的读腿一律调这里，**禁止**在 api/engine 层再
// `time.Date(...)` 现算（闸门见 internal/api/month_boundary_gate_test.go）。
func MonthStartBound() string { return monthStart() }

// DayStartBound 今日（UTC 日历日）零点边界，RFC3339 口径（★ 2026-10-01 〇-AF 补丁三）。
//
// 为什么是 UTC 日而不是本地日：本仓的**日计数器与日额度墙**就是这个口径
// （`billing.go` 的 DailyUsage / incrementDailyUsage* 一律 `time.Now().UTC().Format("2006-01-02")`
// 作 usage_daily 的 day 主键）。显示侧若换成本地日，等于给同一个「今日已用」长出第二把尺子
// ——客户看到的今日数与真拿去拦请求的那个数不一致，正是 §一·11 与 F-12 反复钉的形态。
// 所以本函数刻意与写侧同区：宁可与客户的"自然日"相差偏移量，也不许两把尺子各数各的账。
// 等值锁见 DayStartBoundAgreesWithDailyUsageKey（把"显示尺子＝拦截尺子"钉成一条断言）。
//
// ⚠️ 不用 `time.Now().UTC().Truncate(24*time.Hour)`：Truncate 按"自零时刻的绝对时长"取整，
// 而 Go 的内部时间轴含闰秒累计（1972 年以来 37 秒），24h 的整数倍会偏离真 UTC 零点；
// 显式走历法构造（年月日 + 零时分秒 + UTC）没有这一层缝，也和 monthStart 同形好读。
func DayStartBound() string {
	n := time.Now().UTC()
	return time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
}

// OrgTokensUsedThisMonth 统计某组织（含全部子树）本月 token 消耗。
// 实现：usage_ledger JOIN users 取成员消费，组织范围用既有子树展开。
func (s *Store) OrgTokensUsedThisMonth(tid, orgID int64) (int64, error) {
	ids, err := s.OrgDescendantIDs(tid, orgID)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	placeholders := ""
	args := []interface{}{tid, monthStart()}
	for _, id := range ids {
		placeholders += "?,"
		args = append(args, id)
	}
	placeholders = placeholders[:len(placeholders)-1]
	q := `SELECT COALESCE(SUM(l.quantity),0) FROM usage_ledger l
	      JOIN users u ON u.id=l.user_id
	      WHERE l.tenant_id=? AND l.created_at>=? AND l.` + PlatformTaskTypeExclPred + ` AND u.org_id IN (` + placeholders + `)`
	var total int64
	err = db.QueryRow(s.db, db.CurrentDialect(), q, args...).Scan(&total)
	return total, err
}

// TenantTokensUsedThisMonth 统计全租户本月 token 消耗（含未分配部门的直属用户）。
//
// ★ 2026-09-29 〇-AD 补丁二（与上面部门腿同口径，一起排掉平台承担的用途标签）：
// 这两条 SUM 不是「看看而已」——CheckBudgetWalls 拿它们判**部门墙/组织墙**，
// 命中即 gateUsage 直接拦下客户的翻译请求（「部门 token 已耗尽」）。
// 平台承担的行（知识库 Embedding、行业包采集、Judge 抽样、后台任务）以前照单计入，
// 于是「平台自己垫的成本会把客户的月度预算吃穿」：客户一分积分没掉，
// 却被告知预算用尽——本批第一只口子（不扣积分）修完，这只是必须同批修的第二只。
// ⚠️ 这里用的**不是** RealDebitPred：额度侧问的是「这量是不是客户自己下单产生的」，
//
//	未开强制计费的租户全是 charge_kind='log' 的行，套实扣谓词会把他们的预算墙判成
//	「一点没用」、防超支失效（名单与语义见 store.PlatformTaskTypeExclPred）。
func (s *Store) TenantTokensUsedThisMonth(tid int64) (int64, error) {
	var total int64
	err := db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND created_at>=? AND `+PlatformTaskTypeExclPred,
		tid, monthStart()).Scan(&total)
	return total, err
}

// OrgBudgetSummary 组织预算总览：∑部门预算 / 全租户本月已用。
// 总预算即各部门预算之和（分配时由面板保证语义：调整任一部门预算即调整总额的构成）。
type OrgBudgetSummary struct {
	TotalLimit    int64           `json:"total_limit"`     // 租户总预算 = ∑部门预算
	UsedThisMonth int64           `json:"used_this_month"` // 全租户本月已消耗 token
	Depts         []OrgBudgetItem `json:"depts"`
}

// OrgBudgetItem 单个部门预算项。
type OrgBudgetItem struct {
	OrgID         int64  `json:"org_id"`
	Name          string `json:"name"`
	TokenLimit    int64  `json:"token_limit"`     // 部门预算（0=未启用）
	UsedThisMonth int64  `json:"used_this_month"` // 部门（含子树）本月消耗
}

// GetOrgBudgetSummary 汇总租户预算面板数据（含每个启用了预算的部门及其月度消耗）。
func (s *Store) GetOrgBudgetSummary(tid int64) (*OrgBudgetSummary, error) {
	orgs, err := s.ListOrgs(tid)
	if err != nil {
		return nil, err
	}
	sum := &OrgBudgetSummary{Depts: []OrgBudgetItem{}}
	for _, o := range orgs {
		if o.Type == "root" {
			continue // 根组织代表租户本身，不参与部门预算分配
		}
		if o.TokenLimit <= 0 {
			continue // 未启用部门墙的部门不占预算
		}
		used, uerr := s.OrgTokensUsedThisMonth(tid, o.ID)
		if uerr != nil {
			continue
		}
		sum.TotalLimit += o.TokenLimit
		sum.Depts = append(sum.Depts, OrgBudgetItem{
			OrgID: o.ID, Name: o.Name, TokenLimit: o.TokenLimit, UsedThisMonth: used,
		})
	}
	if sum.TotalLimit > 0 {
		sum.UsedThisMonth, _ = s.TenantTokensUsedThisMonth(tid)
	}
	return sum, nil
}

// QuotaWallHit 双预算墙判定结果。
type QuotaWallHit struct {
	Wall  string // dept | tenant
	Msg   string // 前台展示文案
	Limit int64  // 命中的上限值
	Used  int64  // 命中时的已用量
	OrgID int64  // 部门墙时为组织 ID；组织墙为 0
}

// CheckBudgetWalls 双预算墙判定（gateUsage 调用；独立于强制计费开关）：
//  1. 用户归属部门启用预算且（含子树）本月消耗 ≥ 部门预算 → 部门墙
//  2. 启用了任意部门预算且全租户本月消耗 ≥ 总预算（∑部门预算）→ 组织墙
//
// 返回: nil=未撞墙。命中时同时负责「首次跨越提醒」（按 open 告警去重）。
func (s *Store) CheckBudgetWalls(tid int64, userID int64) *QuotaWallHit {
	if tid <= 0 || s == nil {
		return nil
	}
	u, err := s.GetUser(userID, tid)
	if err != nil {
		return nil
	}
	// 部门墙：仅当用户归属组织时沿父链找最近启用预算的组织
	if u.OrgID > 0 {
		org, oerr := s.GetOrgByID(u.OrgID)
		if oerr == nil && org != nil && org.TenantID == tid {
			cur := org
			for cur != nil && cur.ID > 0 {
				if cur.TokenLimit > 0 {
					used, uerr := s.OrgTokensUsedThisMonth(tid, cur.ID)
					if uerr == nil && used >= cur.TokenLimit {
						s.notifyDeptQuota(tid, cur, used)
						return &QuotaWallHit{Wall: "dept", Limit: cur.TokenLimit, Used: used, OrgID: cur.ID,
							Msg: fmt.Sprintf("部门「%s」token 已耗尽，请联系管理员及时充值", cur.Name)}
					}
					break // 最近启用预算的祖先未超，则更远祖先亦无需检查（预算互不嵌套扣减）
				}
				if cur.ParentID <= 0 {
					break
				}
				parent, perr := s.GetOrgByID(cur.ParentID)
				if perr != nil {
					break
				}
				cur = parent
			}
		}
	}
	// 组织墙：对全部用户生效（含未分配部门的直属用户）
	// 组织墙：总预算>0（存在部门预算）且全租户本月消耗≥总预算
	sum, err := s.GetOrgBudgetSummary(tid)
	if err == nil && sum.TotalLimit > 0 && sum.UsedThisMonth >= sum.TotalLimit {
		s.notifyTenantQuota(tid, sum)
		return &QuotaWallHit{Wall: "tenant", Limit: sum.TotalLimit, Used: sum.UsedThisMonth,
			Msg: fmt.Sprintf("组织 token 已耗尽（本月预算 %d 已用完），请联系管理员及时充值", sum.TotalLimit)}
	}
	return nil
}

// notifyDeptQuota 部门墙首次跨越提醒：通知该组织的部门管理员（无则兜底租户管理员），
// 以 open 告警做去重闸（同一部门/月份只提醒一轮）。
func (s *Store) notifyDeptQuota(tid int64, org *Org, used int64) {
	marker := fmt.Sprintf("dept_quota#%d#%04d-%02d", org.ID, time.Now().Year(), int(time.Now().Month()))
	if s.openAlertExists(marker) {
		return
	}
	msg := fmt.Sprintf("部门「%s」本月 token 预算已用尽（已消耗 %d），相关翻译请求已被拦截。%s",
		org.Name, used, marker)
	_ = s.CreateAlert(tid, "warning", "quota", msg)
	targets := s.usersByRoleInOrg(tid, org.ID, "dept_admin")
	if len(targets) == 0 {
		targets = s.usersByRoleInOrg(tid, org.ID, "tenant_admin")
	}
	for _, uid := range targets {
		_ = s.CreateNotification(uid, "部门预算已耗尽", msg, "quota", org.ID)
	}
}

// notifyTenantQuota 组织墙首次跨越提醒：通知全部租户管理员（open 告警去重）。
func (s *Store) notifyTenantQuota(tid int64, sum *OrgBudgetSummary) {
	marker := fmt.Sprintf("tenant_quota#%d#%04d-%02d", tid, time.Now().Year(), int(time.Now().Month()))
	if s.openAlertExists(marker) {
		return
	}
	msg := fmt.Sprintf("组织本月 token 预算（%d）已用尽，当前消耗 %d，相关翻译请求已被拦截。%s",
		sum.TotalLimit, sum.UsedThisMonth, marker)
	_ = s.CreateAlert(tid, "critical", "quota", msg)
	for _, uid := range s.usersByRoleInOrg(tid, 0, "tenant_admin") {
		_ = s.CreateNotification(uid, "组织预算已耗尽", msg, "quota", tid)
	}
}

// openAlertExists 是否已存在同标记的 open 告警（去重闸）。
func (s *Store) openAlertExists(marker string) bool {
	var n int
	_ = db.QueryRow(s.db, db.CurrentDialect(),
		`SELECT COUNT(*) FROM alerts WHERE status='open' AND kind='quota' AND message LIKE ?`,
		"%"+marker+"%").Scan(&n)
	return n > 0
}

// usersByRoleInOrg 列出某组织（或全租户 orgID=0）下指定角色的用户 ID。
func (s *Store) usersByRoleInOrg(tid int64, orgID int64, role string) []int64 {
	q := "SELECT id FROM users WHERE tenant_id=? AND role=? AND status='active'"
	args := []interface{}{tid, role}
	if orgID > 0 {
		q += " AND org_id=?"
		args = append(args, orgID)
	}
	rows, err := db.Query(s.db, db.CurrentDialect(), q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			out = append(out, id)
		}
	}
	return out
}
