// ============ panels/users.ts · 职责说明 ============
// 账户/用户管理面板 i18n 键
// 导出本面板中英双语词典：zh 为对应 i18n key 的中文显示文本，en 为英文显示文本（键一一对应），最终由 i18n/index.ts 合并到全局词典。
// =============================================
export const zh: Record<string, string> = {
  'users.title': '账户管理',
  'users.usernamePlaceholder': '用户名',
  'users.passPlaceholder': '初始密码（仅平台账号需要，≥6位）',
  'users.emailOptionalPlaceholder': '邮箱（可选，预绑到这条邀请链接）',
  'users.createHintEnterprise': '企业账号不再设初始密码：提交后发出一枚一次性邀请链接，由本人自设邮箱与密码完成注册；链接谁拿到都能进这家企业，转发前请确认对象。',
  'users.createHintPlatform': '平台账号（超管／管理员）仍走初始密码，登录后即可进后台；这类账号不发放邀请链接。',
  'users.displayNamePlaceholder': '显示名称',
  'users.role.user': '普通用户',
  'users.role.dept_admin': '部门管理员',
  'users.role.tenant_admin': '组织管理员',
  'users.role.super_admin': '超级管理员',
  'users.role.admin': '超级管理员',
  'users.orgItem': '组织 {id} ({code})',
  'users.create': '创建',
  'users.colId': 'ID',
  'users.colUsername': '用户名',
  'users.colName': '名称',
  'users.colOrg': '组织',
  'users.colRole': '角色',
  'users.colStatus': '状态',
  'users.colLastLogin': '最近登录',
  'users.colActions': '操作',
  'users.enable': '启用',
  'users.disable': '停用',
  'users.resetPwd': '重置密码',
  'users.rootOrgOption': '根组织（未分配）',
  'users.rootOrg': '根组织',
  'users.orgHash': '组织 #{id}',
  'users.resetPwdPrompt': '为 {name} 设置新密码：',
  // ★ 2026-10-24 多身份 B4-d（R8+R2）：这一列原来的「删除」删的**不是账号**，
  //   而是这个人在当前企业里的那一份身份（后端停用该行＋留 tenant_leave 审计），
  //   所以按钮不许再写「删除」——写错了管理员会以为把人整个删号，当事人也会以为自己被清了。
  'users.removeMember': '移除成员',
}

// 英文文案词典：键与上方 zh 一一对应。
export const en: Record<string, string> = {
  'users.title': 'User Management',
  'users.usernamePlaceholder': 'Username',
  'users.passPlaceholder': 'Initial password (platform accounts only, ≥6 chars)',
  'users.emailOptionalPlaceholder': 'Email (optional, pre-bound to this invite link)',
  'users.createHintEnterprise': 'Enterprise accounts no longer get an initial password: submitting sends a one-time invite link and the person sets their own email and password. Anyone holding the link can join this company, so double-check who you forward it to.',
  'users.createHintPlatform': 'Platform accounts (super admin / admin) still use an initial password and can enter the console right after login; no invite link is issued for them.',
  'users.displayNamePlaceholder': 'Display name',
  'users.role.user': 'User',
  'users.role.dept_admin': 'Dept Admin',
  'users.role.tenant_admin': 'Org Admin',
  'users.role.super_admin': 'Super Admin',
  'users.role.admin': 'Super Admin',
  'users.orgItem': 'Org {id} ({code})',
  'users.create': 'Create',
  'users.colId': 'ID',
  'users.colUsername': 'Username',
  'users.colName': 'Name',
  'users.colOrg': 'Org',
  'users.colRole': 'Role',
  'users.colStatus': 'Status',
  'users.colLastLogin': 'Last login',
  'users.colActions': 'Actions',
  'users.enable': 'Enable',
  'users.disable': 'Disable',
  'users.resetPwd': 'Reset password',
  'users.rootOrgOption': 'Root org (unassigned)',
  'users.rootOrg': 'Root org',
  'users.orgHash': 'Org #{id}',
  'users.resetPwdPrompt': 'Set a new password for {name}:',
  // ★ 2026-10-24 multi-identity B4-d (R8+R2): this row action removes the person's
  //    identity **in this company** (the backend disables that row and leaves a tenant_leave
  //    audit); it does not delete the account. The old "Delete" label toldadmins the opposite.
  'users.removeMember': 'Remove member',
}
