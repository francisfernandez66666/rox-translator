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
	attachProductPackIDs(scope) // nil map 先建，下面写入才不会 panic
	// ★ 一期《确定的多语言能力》§4.1：产品包插在 Rank 的 150 档，必须在这里装配集合。
	//   BuildPackScope 在冻结文件 kbpackages.go 里（只减不增），不许改它；
	//   产品包 pack_type 复用 'tenant' ⇒ 它同时已在 TenantPackIDs 内，allow 集合天然包含，
	//   这里只补「哪几个包是产品包」这一维，供 Rank 判 150 与 packAllowSet 并集。
	//   ★ 查询失败不放大也不阻断：仅不叠加产品层，老租户行为与此前逐字一致（E1）。
	if ids, perr := s.ListProductPackIDs(tid); perr == nil {
		for id := range ids {
			scope.ProductPackIDs[id] = true
		}
	}
	return s.attachPersonaPacks(scope, jobRole), nil
}

// attachProductPackIDs 初始化产品包集合（nil map 写入会 panic，统一在这里建）。
// 参数：scope=可见范围；返回同一对象。
func attachProductPackIDs(scope *kb.PackScope) *kb.PackScope {
	if scope.ProductPackIDs == nil {
		scope.ProductPackIDs = map[int64]bool{}
	}
	return scope
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
