// ============ 本文件职责中文说明 ============
// scope.go · 知识库可见范围（PackScope）——2026-08-26《KB组织继承链与部门隔离改造方案》核心类型。
//
// 语义总图（业务优先级，2026-08-29 调整）：
//   部门(链内部门包，按祖先距离就近) > 跨部门 > 企业包 > 行业包/文化包 > 无scope兜底
//   注：跨部门仅作例句参考（InChain=false，绝不直接采用），但业务展示优先级排在企业/行业之上。
// 类型归属本包（kb）的原因：检索层 FindExact/FuzzyHits/ScopedSearch 直接消费，
// 由 store.BuildPackScope 组装、engine 注入 ctx 构造——依赖方向 store→kb 单向，无环。
// =============================================
package kb

import "sort"

// PackScope 一次翻译请求的知识库可见范围（按用户组织祖先链计算）。
type PackScope struct {
	TenantID       int64             // 生效租户
	Chain          []int64           // 组织祖先链原始切片（[自身..根]；缓存指纹用）
	ChainPacks        map[int64]int     // 链内部门包：包ID → 祖先距离（0=本部门,1=父,...）
	TenantPackIDs     map[int64]bool    // 企业包集合（pack_id=0 历史行语义上归此层）
	SharedPackIDs     map[int64]bool    // 行业包集合（注册行业匹配；含宿主租户1的行业包；全行业+超管可见）
	UniversalPackIDs  map[int64]bool    // 通用语言习惯包（locale）：全用户可见、最低优先级档（无scope）
	AllowCrossDept   bool              // 租户策略开关 kb_cross_dept_fallback（默认开）
	CrossDeptPacks   map[int64]string  // 跨部门候选部门包：包ID → 包名（打标用；开关关或无可共享包时为空）
}

// Distance 返回包在祖先链上的距离（0=本部门）；非链内包返回 false。
func (s *PackScope) Distance(packID int64) (int, bool) {
	if s == nil || s.ChainPacks == nil {
		return 0, false
	}
	d, ok := s.ChainPacks[packID]
	return d, ok
}

// Rank 计算行的全局排序秩（越小越优先）——业务优先级裁决依据：
//   部门(链内部门包，按祖先距离 0..N) > 跨部门(100) > 企业包/历史无主行(200)
//   > 行业包/文化包(300) > 无scope兜底(400)。
// 注：跨部门仅作例句参考（InChain=false），但业务展示优先级排在企业/行业之上。
// 参数 packID=行归属包 ID（0=历史无主行），rowTenant=行宿主租户。
func (s *PackScope) Rank(packID int64, rowTenant int64) int {
	if s == nil {
		return 400
	}
	if d, ok := s.Distance(packID); ok {
		return d // 0..N：部门就近优先（始终小于其余档）
	}
	if _, ok := s.CrossDeptPacks[packID]; ok {
		return 100 // 跨部门（仅例句参考，但优先级在企业/行业之上）
	}
	if packID == 0 {
		return 200 // 历史无主行按企业层级
	}
	if s.TenantPackIDs[packID] {
		return 200
	}
	if s.SharedPackIDs[packID] {
		return 300 // 行业包
	}
	if s.UniversalPackIDs[packID] {
		return 400 // 通用语言习惯包（无scope，全用户可见，最低档）
	}
	return 400 // 无scope兜底（正常查询已被 WHERE 排除）
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
	return packID == 0 || s.TenantPackIDs[packID] || s.SharedPackIDs[packID] || s.UniversalPackIDs[packID]
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
