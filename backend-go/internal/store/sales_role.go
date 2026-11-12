// ============ sales_role.go · 职责说明 ============
// 三期 W3 销售域角色权限的**唯一真值层**（★ 不新增第五种角色常量，四档仍在 iam/models.go）。
//
// 三条硬口径（施工图 §3 W3 + A09b）：
//  1. **PermForEndpoint 单一真值**：所有销售域端点的「路径 → 权限项」只有这一张表；
//     api 侧与测试一律从 store.PermForEndpoint() 现推，禁止散落第二份 if / 第二份清单。
//  2. **行式三列**：权限落库为 (tenant_id, role_level, perm_key, allowed) 行式三列
//     （新表由 sales_role_migrate.go 的 SalesRoleMigrate() 幂等建立），唯一键天然防重复、
//     逐项可审计、无整行 JSON 并发覆盖。未落库 / 新表未挂进域链时回退出厂缺省，不放行。
//  3. **SCRM 双读单写迁移期映射**：admin→tenant_admin(3)、readonly 不许降成 user(1)，
//     具名常量是 A09 两枚变异体（M0220/M0221）唯一可锚定的靶，ValidateSCRMRoleMap 守不变式。
//
// 错误用 apierrors.New 在 api 层包装；本层 fmt.Errorf 只作内部错误，中文原文已登记（见报告）。
// ================================================
package store

import (
	"fmt"
	"strings"
	"time"

	"translator/internal/db"
	"translator/internal/iam"
)

// 销售域权限项枚举（10 项，施工图 §3 W3 全量；A09b 逐项验映射，删一项即红）。
const (
	PermBotToggle      = "bot_toggle"
	PermKBEdit         = "kb_edit"
	PermScriptEdit     = "script_edit"
	PermLeadView       = "lead_view"
	PermLeadRoute      = "lead_route"
	PermQuoteIssue     = "quote_issue"
	PermQuoteApprove   = "quote_approve"
	PermOutboundConfig = "outbound_config"
	PermConsentManage  = "consent_manage"
	PermOpsView        = "ops_view"
)

// allSalesPermKeys 权限项全集（单一真值；导出函数返回副本，防调用方就地改真值）。
var allSalesPermKeys = []string{
	PermBotToggle, PermKBEdit, PermScriptEdit, PermLeadView, PermLeadRoute,
	PermQuoteIssue, PermQuoteApprove, PermOutboundConfig, PermConsentManage, PermOpsView,
}

// AllSalesPermKeys 返回销售域 10 个权限项的副本（A09b 与管理面 GET 共用同一份清单）。
// 参数：无；返回：权限项键名切片副本。
func AllSalesPermKeys() []string {
	out := make([]string, len(allSalesPermKeys))
	copy(out, allSalesPermKeys)
	return out
}

// SalesPermExists 判断 perm_key 是否在 10 项全集内（未登记项一律不放行，fail-closed）。
// 参数：permKey=待判权限项；返回：是否登记。
func SalesPermExists(permKey string) bool {
	for _, k := range allSalesPermKeys {
		if k == permKey {
			return true
		}
	}
	return false
}

// salesEndpointPerms 端点 → 权限项的**唯一映射表**（PermForEndpoint 唯一真值）。
// 收口四端点 + 配置面 + 报价面 + 归因面 + 本批管理面，确保 10 个权限项逐项都有落点。
var salesEndpointPerms = map[string]string{
	"/api/sales/capability":                PermOpsView,
	"/api/sales/drafts":                    PermLeadView,
	"/api/sales/draft":                     PermLeadView,
	"/api/sales/draft/approve":             PermQuoteApprove,
	"/api/admin/sales/config":              PermOpsView,
	"/api/admin/sales/config/save":         PermBotToggle,
	"/api/admin/sales/lexicon/save":        PermKBEdit,
	"/api/admin/sales/lexicon/delete":      PermScriptEdit,
	"/api/admin/sales/lexicon/seed":        PermScriptEdit,
	"/api/admin/sales/quote":               PermQuoteIssue,
	"/api/admin/sales/quote/create":        PermQuoteIssue,
	"/api/admin/sales/quote/accept":        PermQuoteApprove,
	"/api/admin/sales/attribution/replies": PermLeadRoute,
	"/api/admin/sales/roles":               PermConsentManage,
	"/api/admin/sales/roles/save":          PermConsentManage,
	"/api/admin/sales/ops":                 PermOutboundConfig,
	"/api/admin/sales/ops/save":            PermOutboundConfig,
}

// PermForEndpoint 返回端点所需权限项（★ 销售域鉴权唯一真值，api 与测试都只认它）。
// 未登记路径返回空串，调用方须 fail-closed 拒绝，不许猜一把默认权限放行。
// 参数：path=请求路径（可带 query）；返回：权限项键名或空串。
func PermForEndpoint(path string) string {
	// 去 query 与尾斜杠：保证 /api/sales/draft?id=1 与 /api/sales/draft 同判。
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path = path[:i]
	}
	path = strings.TrimRight(path, "/")
	if path == "" {
		path = "/"
	}
	return salesEndpointPerms[path]
}

// SalesEndpointPaths 返回映射表内登记的全部端点（A09b 现推「逐项有映射」，不写死分母）。
// 参数：无；返回：端点路径切片。
func SalesEndpointPaths() []string {
	out := make([]string, 0, len(salesEndpointPerms))
	for p := range salesEndpointPerms {
		out = append(out, p)
	}
	return out
}

// salesPermMinLevel 每个权限项的出厂最低放行等级（行式缺省矩阵的紧凑真值）。
// 读类 ≥2、审批/管理类 ≥3；落库后以 sales_role_perms 为准，本表只在无行时兜底。
var salesPermMinLevel = map[string]int{
	PermOpsView: 2, PermLeadView: 2,
	PermLeadRoute: 3, PermBotToggle: 3, PermKBEdit: 3, PermScriptEdit: 3,
	PermQuoteIssue: 3, PermQuoteApprove: 3, PermOutboundConfig: 3, PermConsentManage: 3,
}

// DefaultSalesRoleAllowed 出厂缺省判定：等级达标且权限项已登记才放行（A10 期望值现推用）。
// 参数：level=角色等级 1..4；permKey=权限项；返回：是否放行。
func DefaultSalesRoleAllowed(level int, permKey string) bool {
	if !SalesPermExists(permKey) {
		return false
	}
	min, ok := salesPermMinLevel[permKey]
	if !ok {
		return false
	}
	return level >= min
}

// SalesRolePermRow 行式权限的一行（RoleLevel × perm → allowed，管理面回读/审计用）。
type SalesRolePermRow struct {
	RoleLevel int    `json:"role_level"`
	PermKey   string `json:"perm_key"`
	Allowed   bool   `json:"allowed"`
}

// SalesRoleAllowed 判定某租户某等级是否被授予某权限项（★ 可供 api 层调用的鉴权助手真值）。
// 落库行优先；无行 / 新表尚未挂进 New() 域链时回退出厂缺省，绝不静默放行。
// 参数：tid=租户；level=角色等级；permKey=权限项；返回：是否放行。
func (s *Store) SalesRoleAllowed(tid int64, level int, permKey string) bool {
	var allowed int
	err := db.QueryRow(s.db, db.CurrentDialect(),
		"SELECT allowed FROM sales_role_perms WHERE tenant_id=? AND role_level=? AND perm_key=?",
		tid, level, permKey).Scan(&allowed)
	if err == nil {
		return allowed == 1
	}
	return DefaultSalesRoleAllowed(level, permKey)
}

// SaveSalesRolePerm 逐项 upsert 一行（行式三列，唯一键天然防重复，不整行 JSON 覆盖）。
// 参数：tid=租户；level=角色等级；permKey=权限项；allowed=是否放行；返回：写错误。
func (s *Store) SaveSalesRolePerm(tid int64, level int, permKey string, allowed bool) error {
	if !SalesPermExists(permKey) {
		return fmt.Errorf("未登记的销售权限项: %s", permKey)
	}
	if level < 1 || level > 4 {
		return fmt.Errorf("角色等级越界: %d", level)
	}
	flag := 0
	if allowed {
		flag = 1
	}
	// 双方言同一条 DDL 语义：占位符由 db.Exec 按 CurrentDialect 改写，无 DELETE/UPDATE 尾随 LIMIT。
	_, err := db.Exec(s.db, db.CurrentDialect(),
		`INSERT INTO sales_role_perms (tenant_id, role_level, perm_key, allowed, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(tenant_id, role_level, perm_key)
		 DO UPDATE SET allowed=excluded.allowed, updated_at=excluded.updated_at`,
		tid, level, permKey, flag, time.Now().UTC().Format(time.RFC3339))
	return err
}

// ListSalesRolePerms 回读某租户的全部落库行（未落库项由调用方按 DefaultSalesRoleAllowed 补缺省）。
// 参数：tid=租户；返回：行式权限行与读错误。
func (s *Store) ListSalesRolePerms(tid int64) ([]SalesRolePermRow, error) {
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT role_level, perm_key, allowed FROM sales_role_perms WHERE tenant_id=?", tid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SalesRolePermRow, 0)
	for rows.Next() {
		var r SalesRolePermRow
		var allowed int
		if err := rows.Scan(&r.RoleLevel, &r.PermKey, &allowed); err != nil {
			return nil, err
		}
		r.Allowed = allowed == 1
		out = append(out, r)
	}
	return out, rows.Err()
}

// ============ SCRM 双读单写迁移期角色映射（A09 / M0220 / M0221） ============

// scrmAdminMappedRole：SCRM 旧 "admin" 在双读单写迁移期映射到的 IAM 角色（★ M0220 唯一靶）。
// 注意：这是 SCRM 自己的角色名，与 iam.RoleAdmin（旧管理员→超管 4）不是同一套语义，勿互换。
const scrmAdminMappedRole = iam.RoleTenantAdmin

// scrmReadonlyMappedRole：SCRM 旧 "readonly" 映射到的 IAM 角色（★ 不许降成 user(1)，M0221 唯一靶）。
const scrmReadonlyMappedRole = iam.RoleDeptAdmin

// SCRMRoleMap SCRM 双读单写迁移期旧角色名 → 新 IAM 角色的唯一映射（A09 锁的真值）。
var SCRMRoleMap = map[string]string{
	"admin":    scrmAdminMappedRole,
	"readonly": scrmReadonlyMappedRole,
}

// SCRMRoleLevel 把 SCRM 旧角色名映射到 IAM 权限等级（A09 现推用；未登记返回 0）。
// 参数：oldRole=SCRM 旧角色名；返回：权限等级 0..4。
func SCRMRoleLevel(oldRole string) int {
	mapped, ok := SCRMRoleMap[oldRole]
	if !ok {
		return 0
	}
	return iam.RoleLevel(mapped)
}

// ValidateSCRMRoleMap 校验一份映射是否守住 A09 两条不变式（生产自检 + 测试反向对照共用）。
// R1：admin 必须映射到等级 3；R2：readonly 等级不得 < 2（不许降成 user(1)）。
// 参数：m=待校验映射；返回：校验错误（守住了返回 nil）。
func ValidateSCRMRoleMap(m map[string]string) error {
	if len(m) == 0 {
		return fmt.Errorf("SCRM 角色映射为空")
	}
	if iam.RoleLevel(m["admin"]) != 3 {
		return fmt.Errorf("SCRM admin 必须映射到等级 3（tenant_admin）")
	}
	if iam.RoleLevel(m["readonly"]) < 2 {
		return fmt.Errorf("SCRM readonly 不许降成 user(1)")
	}
	return nil
}
