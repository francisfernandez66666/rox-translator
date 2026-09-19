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
