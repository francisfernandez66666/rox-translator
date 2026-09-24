// ============ lib/personas.ts · 职责说明 ============
// 职业角色本地兜底词库（2026-09-19 需求：注册/个人中心角色下拉）。
// 唯一数据源是后端租户0 的 persona 包（register-config / /api/register/personas 下发），
// 此处仅在接口失败或返回空时兜底，code 必须与 store/persona_dict.go builtinPersonas 对齐。
// =============================================
export const PERSONA_FALLBACK: Array<{ code: string; name: string }> = [
  { code: 'fullstack', name: '全栈工程师' },
  { code: 'frontend', name: '前端开发' },
  { code: 'backend', name: '后端开发' },
  { code: 'pm', name: '产品经理' },
  { code: 'pj', name: '项目经理' },
  { code: 'uiux', name: 'UI/UX 设计' },
  { code: 'ops', name: '运营' },
  { code: 'sales', name: '销售' },
]

// ★ 2026-09-24 后台去写死中文批：内置角色 code → 双语显示名。
// 后端 persona 包下发的是中文名（管理员可改），英文界面下 built-in code 走这张表取英文名，
// 代价是「超管改了内置角色名」时非中文界面仍显示标准英文名（自定义 code 一律原样显示）。
const BUILTIN_PERSONA_EN: Record<string, string> = {
  fullstack: 'Full-stack Engineer',
  frontend: 'Frontend Developer',
  backend: 'Backend Developer',
  pm: 'Product Manager',
  pj: 'Project Manager',
  uiux: 'UI/UX Designer',
  ops: 'Operations',
  sales: 'Sales',
}

/** personaName 角色显示名本地化：非中文界面 + 内置 code → 英文名；其余原样返回后端/兜底名 */
export function personaName(code: string, name: string, lang: string): string {
  if (!lang.startsWith('zh')) {
    const en = BUILTIN_PERSONA_EN[code]
    if (en) return en
  }
  return name
}
