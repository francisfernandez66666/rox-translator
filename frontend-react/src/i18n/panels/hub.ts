// ============ panels/hub.ts · 职责说明 ============
// 后台一级菜单合并 Hub（2026-09-15 Tab 精简）所需标签：
// 新一级菜单名 + Hub 内子 Tab 平铺文案（避免沿用带 emoji 的旧菜单标签）。
// =============================================
export const zh: Record<string, string> = {
'hub.menuBilling':'计费与套餐',
  'hub.tabPlans': '套餐与订单',
  'hub.tabInvites': '邀请码',
  'hub.tabUsers': '成员账户',
  'hub.tabTenants': '租户管理',
  'hub.tabOps': '运营策略',
  'hub.tabModels': '模型与供应商',
  'hub.tabReconcile': '成本对账',
  'hub.tabBrand': '品牌与页脚',
}

// 英文文案字典（与 zh 同 key 对齐，parity.test.ts 守护双语一致性）
export const en: Record<string, string> = {
'hub.menuBilling':'Billing & Plans',
  'hub.tabPlans': 'Plans & Orders',
  'hub.tabInvites': 'Invite Codes',
  'hub.tabUsers': 'Members',
  'hub.tabTenants': 'Tenants',
  'hub.tabOps': 'Ops Strategy',
  'hub.tabModels': 'Models & Providers',
  'hub.tabReconcile': 'Reconciliation',
  'hub.tabBrand': 'Brand & Footer',
}
