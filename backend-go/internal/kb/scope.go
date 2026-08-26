// ============ 本文件职责中文说明 ============
// scope.go · 知识库可见范围（PackScope）——2026-08-26《KB组织继承链与部门隔离改造方案》核心类型。
//
// 语义总图：
//   链内部门包(本部门→祖先链，就近覆盖) > 企业包 > 历史无主行(pack_id=0) > 行业包/文化包
//   跨部门回退层（仅精确匹配、且租户开关+包包两级开关联动开启）：直接采用并打标；
//   模糊/语义命中跨部门仅作例句参考，绝不直接采用。
// 类型归属本包（kb）的原因：检索层 FindExact/FuzzyHits/ScopedSearch 直接消费，
// 由 store.BuildPackScope 组装、engine 注入 ctx 构造——依赖方向 store→kb 单向，无环。
// =============================================
package kb

import "sort"

// PackScope 一次翻译请求的知识库可见范围（按用户组织祖先链计算）。
type PackScope struct {
	TenantID       int64             // 生效租户
	Chain          []int64           // 组织祖先链原始切片（[自身..根]；缓存指纹用）
	ChainPacks     map[int64]int     // 链内部门包：包ID → 祖先距离（0=本部门,1=父,...）
	TenantPackIDs  map[int64]bool    // 企业包集合（pack_id=0 历史行语义上归此层）
	SharedPackIDs  map[int64]bool    // 共享包集合（注册行业匹配的行业包 + 全部语言文化包；含宿主租户1的行）
	AllowCrossDept bool              // 租户策略开关 kb_cross_dept_fallback（默认开）
	CrossDeptPacks map[int64]string  // 跨部门候选部门包：包ID → 包名（打标用；开关关或无可共享包时为空）
}

// Distance 返回包在祖先链上的距离（0=本部门）；非链内包返回 false。
func (s *PackScope) Distance(packID int64) (int, bool) {
	if s == nil || s.ChainPacks == nil {
		return 0, false
	}
	d, ok := s.ChainPacks[packID]
	return d, ok
}

// Rank 计算行的全局排序秩（越小越优先）——同名术语冲突裁决的唯一依据：
//   链内部门包 = 祖先距离(0..89)；企业包/pack_id=0 历史行 = 100；
//   行业包 = 200；文化包 = 300；跨部门回退 = 400（两段式下不会与上文同场竞争）。
// 参数 packID=行归属包 ID（0=历史无主行），rowTenant=行宿主租户。
func (s *PackScope) Rank(packID int64, rowTenant int64) int {
	if s == nil {
		return 500
	}
	if d, ok := s.Distance(packID); ok {
		return d // 0..89：链内就近优先
	}
	if packID == 0 {
		return 100 // 历史无主行按企业层级
	}
	if s.TenantPackIDs[packID] {
		return 100
	}
	if s.SharedPackIDs[packID] {
		// 共享层内部再分行业(200)/文化(300)：由组装方在 SharedRank 里记录，
		// 此处简化为统一 250（行业与文化极少同名冲突，且原 priority 已作次序）
		return 250
	}
	if _, ok := s.CrossDeptPacks[packID]; ok {
		return 400
	}
	return 500 // 不可见兜底（正常查询已被 WHERE 排除）
}

// InChain 判断包是否属于用户祖先链（例句分层：链内候选优先填充）。
func (s *PackScope) InChain(packID int64) bool {
	if s == nil {
		return true // 无 scope 时保持旧行为：全部视为链内
	}
	if _, ok := s.ChainPacks[packID]; ok {
		return true
	}
	// 企业包与历史行也视作"链内"层级（直接采用域），供模糊采用判定复用
	return packID == 0 || s.TenantPackIDs[packID]
}

// CrossName 返回跨部门包的展示名（打标「🌐跨部门命中（来自X）」用）。
func (s *PackScope) CrossName(packID int64) string {
	if s == nil || s.CrossDeptPacks == nil {
		return ""
	}
	return s.CrossDeptPacks[packID]
}

// ChainKey 生成组织链指纹（缓存键组件）：排序后 IDs 的确定性拼接。
func ChainKey(chain []int64) string {
	cp := append([]int64{}, chain...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	key := make([]byte, 0, len(cp)*8)
	for _, id := range cp {
		key = append(key, byte(id), byte(id>>8), byte(id>>16), byte(id>>24),
			byte(id>>32), byte(id>>40), byte(id>>48), byte(id>>56))
	}
	return string(key)
}
