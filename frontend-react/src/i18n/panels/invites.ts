// ============ 本文件职责中文说明 ============
// 邀请码/邀请管理面板 i18n 键
// =============================================
// panels/invites.ts — 邀请码管理面板 i18n 键（中英）
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
export const zh: Record<string, string> = {
  'invites.title': '邀请码管理',
  'invites.hint': '绑定组织的邀请码：受邀用户加入该组织（普通用户）；未绑定组织的邀请码：受邀用户自助新建组织。',
  'invites.codePlaceholder': '邀请码',
  'invites.newOrg': '新建组织',
  'invites.create': '生成邀请码',
  'invites.colCode': '邀请码',
  'invites.colTenant': '绑定组织',
  'invites.colStatus': '状态',
  'invites.colUsedBy': '使用者',
  'invites.colCreatedAt': '创建时间',
  'invites.colUsedAt': '使用时间',
  'invites.used': '已使用',
  'invites.unused': '未使用',
  'invites.empty': '暂无邀请码',
  'invites.codeRequired': '邀请码必填',
}

// 英文文案词典：键与上方 zh 一一对应。
export const en: Record<string, string> = {
  'invites.title': 'Invite Codes',
  'invites.hint': 'Codes bound to a tenant let invited users join that org (as regular users); unbound codes let invited users self-register a new org.',
  'invites.codePlaceholder': 'Invite code',
  'invites.newOrg': 'New org',
  'invites.create': 'Generate Code',
  'invites.colCode': 'Code',
  'invites.colTenant': 'Bound Org',
  'invites.colStatus': 'Status',
  'invites.colUsedBy': 'Used By',
  'invites.colCreatedAt': 'Created At',
  'invites.colUsedAt': 'Used At',
  'invites.used': 'Used',
  'invites.unused': 'Unused',
  'invites.empty': 'No invite codes',
  'invites.codeRequired': 'Invite code is required',
}