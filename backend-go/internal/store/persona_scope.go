// ============================================================================
// store/persona_scope.go — 角色包（persona）检索装配（2026-09-19 角色功能）
// BuildPackScope 保持原三参口径不动（冻结规则 + 90+ 调用点稳定），
// 新增薄包装 BuildUserPackScope：在原可见范围上叠加「当前用户 job_role 命中的角色包」。
// 装配规则：宿主租户0、pack_type='persona'、enabled=1、code=job_role 的包 → PersonaPackIDs
// （优先级 350 档，见 kb.PackScope.Rank）；job_role 为空或包不存在 → 不叠加，行为与旧一致。
// ============================================================================
package store

import (
	"translator/internal/db"
	"translator/internal/kb"
)

// BuildUserPackScope 按「组织链 + 用户职业角色」组装完整可见范围。
// 参数：tid=租户，chain=组织祖先链，allowCross=跨部门开关，jobRole=用户 job_role（可空）。
func (s *Store) BuildUserPackScope(tid int64, chain []int64, allowCross bool, jobRole string) (*kb.PackScope, error) {
	scope, err := s.BuildPackScope(tid, chain, allowCross)
	if err != nil || scope == nil {
		return scope, err
	}
	return s.attachPersonaPacks(scope, jobRole), nil
}

// attachPersonaPacks 把 job_role 命中的启用角色包叠加进 scope（就地修改并返回同一对象）。
// jobRole 同时写入 scope.JobRole——CJK 精确缓存键必须包含它，否则「前端开发」与「销售」
// 两个用户的翻译结果会互相串缓存（角色术语命中不同）。
func (s *Store) attachPersonaPacks(scope *kb.PackScope, jobRole string) *kb.PackScope {
	scope.JobRole = jobRole
	if scope.PersonaPackIDs == nil {
		scope.PersonaPackIDs = map[int64]bool{}
	}
	if jobRole == "" {
		return scope
	}
	rows, err := db.Query(s.db, db.CurrentDialect(),
		"SELECT id FROM kb_packages WHERE tenant_id=? AND pack_type=? AND code=? AND COALESCE(enabled,1)=1",
		SharedHostTenant, PackPersona, jobRole)
	if err != nil {
		return scope // 查询失败不放大也不阻断：仅不叠加角色层
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			scope.PersonaPackIDs[id] = true
		}
	}
	return scope
}
