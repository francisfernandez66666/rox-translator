// ============ 本文件职责中文说明 ============
// 部门预算（四期增强）数据访问与双预算墙判定：
//   - 租管为每个部门分配月度 token 预算（orgs.token_limit），∑部门预算=租户总预算
//   - 部门墙：某部门（含子树）本月消耗 ≥ 该部门预算 → 拦截并首次提醒部门管理员
//   - 组织墙：全租户本月消耗 ∑部门预算 且 >0 → 拦截并首次提醒租户管理员
//   - 月度口径：自然月（每月 1 日重置）；消耗取 usage_ledger.quantity（已含均摊系数）
// 两堵墙独立于强制计费开关——即使仅计量不扣减，预算墙照常生效（体验包用户也能感知约束）。
// =============================================
package store

import (
	"fmt"
	"time"
)

// SetOrgTokenLimit 设置部门月度 token 预算（0=未启用该部门的部门墙）。
func (s *Store) SetOrgTokenLimit(id int64, limit int64) error {
	_, err := s.db.Exec("UPDATE orgs SET token_limit=?, updated_at=? WHERE id=?",
		limit, time.Now().Format(time.RFC3339), id)
	return err
}

// monthStart 当前自然月起点（RFC3339，与 usage_ledger.created_at 同格式比较）。
func monthStart() string {
	n := time.Now()
	return time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, n.Location()).Format(time.RFC3339)
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
	      WHERE l.tenant_id=? AND l.created_at>=? AND u.org_id IN (` + placeholders + `)`
	var total int64
	err = s.db.QueryRow(q, args...).Scan(&total)
	return total, err
}

// TenantTokensUsedThisMonth 统计全租户本月 token 消耗（含未分配部门的直属用户）。
func (s *Store) TenantTokensUsedThisMonth(tid int64) (int64, error) {
	var total int64
	err := s.db.QueryRow(
		`SELECT COALESCE(SUM(quantity),0) FROM usage_ledger WHERE tenant_id=? AND created_at>=?`,
		tid, monthStart()).Scan(&total)
	return total, err
}

// OrgBudgetSummary 组织预算总览：∑部门预算 / 全租户本月已用。
// 总预算即各部门预算之和（分配时由面板保证语义：调整任一部门预算即调整总额的构成）。
type OrgBudgetSummary struct {
	TotalLimit    int64           `json:"total_limit"`    // 租户总预算 = ∑部门预算
	UsedThisMonth int64           `json:"used_this_month"` // 全租户本月已消耗 token
	Depts         []OrgBudgetItem `json:"depts"`
}

// OrgBudgetItem 单个部门预算项。
type OrgBudgetItem struct {
	OrgID         int64 `json:"org_id"`
	Name          string `json:"name"`
	TokenLimit    int64 `json:"token_limit"`     // 部门预算（0=未启用）
	UsedThisMonth int64 `json:"used_this_month"` // 部门（含子树）本月消耗
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
	if err != nil || u.OrgID <= 0 {
		return nil
	}
	// 找到用户所属组织及其父链上最近一个启用了预算的组织（子部门共享上级预算）
	org, err := s.GetOrgByID(u.OrgID)
	if err != nil || org == nil || org.TenantID != tid {
		return nil
	}
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
	_ = s.db.QueryRow(
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
	rows, err := s.db.Query(q, args...)
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
