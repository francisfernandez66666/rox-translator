// ============ 本文件职责中文说明 ============
// scope.go · 用户组织上下文注入与知识库可见范围装配（2026-08-26《KB组织继承链与部门隔离改造方案》）。
//   - WithUserOrg / UserOrgFromContext：把发起翻译的用户 org_id 放进请求 ctx
//     （HTTP 入口取 authUser.OrgID；工单 worker 取创建人 org；OpenAPI 任务取 Key 归属用户 org）
//   - WithUserJobRole / UserJobRoleFromContext：同上口径注入用户职业角色（persona 角色包装配依据）
//   - userScope：组装 kb.PackScope = BuildUserPackScope(tid, OrgAncestorIDs(tid, userOrg), 租户开关, job_role)
//   - getCJKCacheScoped：CJK 标点无关精确缓存，键升级为「租户|组织链指纹|跨部门开关|职业角色」
//
// =============================================
package engine

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"strconv"

	"translator/internal/kb"
)

// userOrgKey context 键：当前请求用户的组织 ID（0=未挂组织/匿名）。
type userOrgKey struct{}

// WithUserOrg 向 ctx 注入用户组织 ID（KB 部门包祖先链继承的依据）。
func WithUserOrg(ctx context.Context, orgID int64) context.Context {
	return context.WithValue(ctx, userOrgKey{}, orgID)
}

// UserOrgFromContext 读取用户组织 ID；未注入时返回 0（安全默认：不见任何部门包）。
func UserOrgFromContext(ctx context.Context) int64 {
	if v, ok := ctx.Value(userOrgKey{}).(int64); ok {
		return v
	}
	return 0
}

// userJobRoleKey context 键：当前请求用户的职业角色编码（persona 包装配依据；2026-09-19）。
type userJobRoleKey struct{}

// WithUserJobRole 向 ctx 注入用户 job_role（KB 角色包可见性的依据；空串=未选角色）。
func WithUserJobRole(ctx context.Context, jobRole string) context.Context {
	return context.WithValue(ctx, userJobRoleKey{}, jobRole)
}

// UserJobRoleFromContext 读取用户 job_role；未注入时返回空串（安全默认：不见任何角色包）。
func UserJobRoleFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userJobRoleKey{}).(string); ok {
		return v
	}
	return ""
}

// userScope 组装当前请求的知识库可见范围。
// 步骤：①取用户组织+职业角色 → ②沿 parent_id 上溯祖先链 → ③读租户策略跨部门开关 →
// ④BuildUserPackScope 装配五层集合（链内部门/企业/行业/角色/语言文化）。任一步失败
// 降级为「仅企业/共享层」的空链范围，绝不退化为全租户可见（隔离语义优先于可用性）。
// 参数：ctx=请求上下文（含租户、用户组织与 job_role）；tid=生效租户 ID。
func (e *Engine) userScope(ctx context.Context, tid int64) *kb.PackScope {
	orgID := UserOrgFromContext(ctx)
	jobRole := UserJobRoleFromContext(ctx)
	chain := []int64{}
	if e.St != nil && orgID > 0 {
		if c, err := e.St.OrgAncestorIDs(tid, orgID); err == nil {
			chain = c
		}
	}
	allowCross := crossDeptFallbackEnabled(ctx, e, tid)
	if e.St == nil {
		return &kb.PackScope{
			TenantID:         tid,
			ChainPacks:       map[int64]int{},
			TenantPackIDs:    map[int64]bool{},
			SharedPackIDs:    map[int64]bool{},
			PersonaPackIDs:   map[int64]bool{},
			JobRole:          jobRole,
			UniversalPackIDs: map[int64]bool{},
			CrossDeptPacks:   map[int64]string{},
			AllowCrossDept:   allowCross,
			Chain:            chain,
		}
	}
	scope, err := e.St.BuildUserPackScope(tid, chain, allowCross, jobRole)
	if err != nil || scope == nil {
		// 查询异常：返回空可见域（仅历史无主行按企业层兜底），不放大权限
		return &kb.PackScope{
			TenantID:         tid,
			ChainPacks:       map[int64]int{},
			TenantPackIDs:    map[int64]bool{},
			SharedPackIDs:    map[int64]bool{},
			PersonaPackIDs:   map[int64]bool{},
			JobRole:          jobRole,
			UniversalPackIDs: map[int64]bool{},
			CrossDeptPacks:   map[int64]string{},
			AllowCrossDept:   false,
			Chain:            chain,
		}
	}
	scope.Chain = chain
	return scope
}

// crossDeptFallbackEnabled 读租户策略开关 kb_cross_dept_fallback（默认开）。
// PolicyConfig.CrossDeptFallback 为指针：nil=旧配置未显式设置 → 默认开启；
// 0=显式关闭；1=显式开启。Ten 未初始化时同样默认开启（单机/测试环境语义一致）。
func crossDeptFallbackEnabled(_ context.Context, e *Engine, tid int64) bool {
	if e.Ten == nil {
		return true
	}
	pc, err := e.Ten.GetPolicyConfig(tid)
	if err != nil || pc.CrossDeptFallback == nil {
		return true // 默认开
	}
	return *pc.CrossDeptFallback == 1
}

// cjkCacheScopeKey 生成 CJK 缓存的作用域键：「租户|组织链指纹|跨部门开关|职业角色」。
// 组织树移动/开关切换/job_role 变更都会改变键 → 旧缓存自然失效（新键重建），无需主动清理。
// ★ job_role 必须在键内：角色包（persona）按用户装配，两个不同角色的用户可见域不同，
//
//	不隔离会互相串缓存（2026-09-19 角色功能引入时的正确性约束）。
func cjkCacheScopeKey(scope *kb.PackScope) string {
	sum := sha1.Sum([]byte(kb.ChainKey(scope.Chain)))
	return strconv.FormatInt(scope.TenantID, 10) + "|" + hex.EncodeToString(sum[:8]) + "|" +
		strconv.FormatBool(scope.AllowCrossDept) + "|" + scope.JobRole
}

// InvalidateKBCaches 清空全部 CJK 精确缓存（知识库内容变更后调用：
// SaveBack/TM 审核通过/条目增删导/向量重建/包启停/共享开关切换/组织移动删除）。
func (e *Engine) InvalidateKBCaches() {
	e.cjkMu.Lock()
	e.cjkCacheByTenant = map[string]map[string]int64{}
	e.cjkMu.Unlock()
}
