// ============================================================================
// i18n.ts — 前端国际化（中英）
// 职责：集中维护界面文案字典，提供 t(key) 取词与当前语言切换。
// 覆盖范围：本轮覆盖登录页与管理后台关键导航；翻译工作台文案次轮接入。
// 用法：import { t, setLang, lang } from '@/i18n'
//       模板中 {{ t('login.title') }}，切换语言后响应式更新。
// ============================================================================

import { ref } from 'vue'

// Lang 支持的语言类型：zh=中文（默认）、en=英文
export type Lang = 'zh' | 'en'

// 当前语言（持久化到 localStorage，默认中文）
export const lang = ref<Lang>((localStorage.getItem('app_lang') as Lang) || 'zh')

// 中英文案字典
type Dict = Record<string, string>
const zh: Dict = {
  // ---- 通用 ----
  'common.online': '在线',
  'common.offline': '离线',
  'common.settings': '设置',
  'common.logout': '退出',
  'common.close': '关闭',
  'common.save': '保存',
  'common.cancel': '取消',
  'common.confirm': '确认',
  'common.operations': '操作',

  // ---- 登录页 ----
  'login.platformAdmin': '🏢 翻译平台管理后台',
  'login.platform': '🌐 翻译平台',
  'login.adminOnly': '仅限管理员账号登录',
  'login.enterWorkspace': '登录后进入翻译工作台',
  'login.username': '用户名',
  'login.password': '密码',
  'login.signingIn': '登录中…',
  'login.signIn': '登 录',
  'login.forgot': '忘记密码？',
  'login.backToLogin': '返回登录',
  'login.selfRegister': '没有账号？自助注册试用',
  'login.selfRegisterTitle': '🌐 自助注册试用',
  'login.selfRegisterSub': '填写邀请码可加入已有组织；留空则创建新组织并获得试用额度',
  'login.emailPlaceholder': '联系邮箱（找回密码用，可选）',
  'login.orgCode': '组织编码（新建组织时必填）',
  'login.orgName': '组织名称（新建组织时必填）',
  'login.invite': '邀请码（可选）',
  'login.registering': '注册中…',
  'login.registerAndLogin': '注册并登录',
  'login.forgotTitle': '🔑 找回密码',
  'login.forgotSub': '输入用户名或绑定邮箱，验证码将发送到邮箱',
  'login.boundEmail': '绑定邮箱',
  'login.sendCode': '发送验证码',
  'login.verificationCode': '6 位验证码',
  'login.newPassword': '新密码（至少 6 位）',
  'login.resetting': '重置中…',
  'login.resetPassword': '重置密码',
  'login.errorRequired': '请输入用户名和密码',
  'login.errorNoPerm': '该账号无管理权限',
  'login.errorLogin': '登录失败',
  'login.successReset': '密码已重置，请使用新密码登录',
  'login.forgotSent': '验证码已发送到绑定邮箱（未配置邮件时请在服务端日志查看）',
  'login.regSuccess': '注册成功，正在登录…',

  // ---- 后台导航 ----
  'admin.console': '管理后台',
  'admin.ops': '经营',
  'admin.org': '组织',
  'admin.content': '内容',
  'admin.currentOrg': '当前组织',
  'admin.backWorkspace': '翻译工作台',
  'admin.forbid': '当前账号无管理权限，请使用管理员账号登录。',
  'admin.overview': '📊 系统看板',
  'admin.dashboard': '📈 数据看板',
  'admin.tenant': '👥 组织',
  'admin.tenants': '🏢 租户管理',
  'admin.orgs': '🗂️ 组织架构',
  'admin.users': '👤 账户',
  'admin.kb': '📚 行业管理',
  'admin.engine': '引擎',
  'admin.models': '🧠 模型/路由/策略',
  'admin.workflow': '⚙️ 流程/evals',
  'admin.open': '开放',
  'admin.apikeys': '🔑 API Key',
  'admin.webhooks': '🔔 Webhook',
  'admin.tickets': '📝 工单/审批',
  'admin.billing': '💰 计费',
  'admin.usage': '📊 用量报表',
  'admin.billingPanel': '💳 计费/充值',
  'admin.system': '系统',
  'admin.alerts': '🚨 系统告警',
  'admin.audit': '📋 审计日志',
  'admin.health': '🩺 系统健康',
  'admin.invites': '✉️ 邀请码',
  'admin.self': '☁️ 开放 API 文档',
  'admin.gdpr': '🛡️ 数据合规',
}

const en: Dict = {
  // ---- Common ----
  'common.online': 'Online',
  'common.offline': 'Offline',
  'common.settings': 'Settings',
  'common.logout': 'Logout',
  'common.close': 'Close',
  'common.save': 'Save',
  'common.cancel': 'Cancel',
  'common.confirm': 'Confirm',
  'common.operations': 'Actions',

  // ---- Login ----
  'login.platformAdmin': '🏢 Translation Admin',
  'login.platform': '🌐 Translation Platform',
  'login.adminOnly': 'Admin accounts only',
  'login.enterWorkspace': 'Sign in to start translating',
  'login.username': 'Username',
  'login.password': 'Password',
  'login.signingIn': 'Signing in…',
  'login.signIn': 'Sign In',
  'login.forgot': 'Forgot password?',
  'login.backToLogin': 'Back to login',
  'login.selfRegister': 'No account? Register for trial',
  'login.selfRegisterTitle': '🌐 Self Registration',
  'login.selfRegisterSub': 'Enter an invite code to join an org, or leave blank to create a new one with trial credits',
  'login.emailPlaceholder': 'Contact email (for recovery, optional)',
  'login.orgCode': 'Org code (required when creating)',
  'login.orgName': 'Org name (required when creating)',
  'login.invite': 'Invite code (optional)',
  'login.registering': 'Registering…',
  'login.registerAndLogin': 'Register & Sign In',
  'login.forgotTitle': '🔑 Reset Password',
  'login.forgotSub': 'Enter username or bound email; a code will be sent',
  'login.boundEmail': 'Bound email',
  'login.sendCode': 'Send Code',
  'login.verificationCode': '6-digit code',
  'login.newPassword': 'New password (min 6 chars)',
  'login.resetting': 'Resetting…',
  'login.resetPassword': 'Reset Password',
  'login.errorRequired': 'Enter username and password',
  'login.errorNoPerm': 'No admin permission',
  'login.errorLogin': 'Login failed',
  'login.successReset': 'Password reset. Sign in with the new password.',
  'login.forgotSent': 'Code sent to bound email (check server log if mail is not configured)',
  'login.regSuccess': 'Registered. Signing in…',

  // ---- Admin nav ----
  'admin.console': 'Admin Console',
  'admin.ops': 'Operations',
  'admin.org': 'Org',
  'admin.content': 'Content',
  'admin.currentOrg': 'Current Org',
  'admin.backWorkspace': 'Translation Workspace',
  'admin.forbid': 'No admin permission. Please sign in with an admin account.',
  'admin.overview': '📊 Dashboard',
  'admin.dashboard': '📈 Dashboard',
  'admin.tenant': '👥 Org',
  'admin.tenants': '🏢 Tenants',
  'admin.orgs': '🗂️ Org Structure',
  'admin.users': '👤 Users',
  'admin.kb': '📚 Knowledge Base',
  'admin.engine': 'Engine',
  'admin.models': '🧠 Models/Routing/Policy',
  'admin.workflow': '⚙️ Workflow/Evals',
  'admin.open': 'Open',
  'admin.apikeys': '🔑 API Keys',
  'admin.webhooks': '🔔 Webhooks',
  'admin.tickets': '📝 Tickets/Approvals',
  'admin.billing': '💰 Billing',
  'admin.usage': '📊 Usage Report',
  'admin.billingPanel': '💳 Billing/Top-up',
  'admin.system': 'System',
  'admin.alerts': '🚨 Alerts',
  'admin.audit': '📋 Audit Log',
  'admin.health': '🩺 System Health',
  'admin.invites': '✉️ Invite Codes',
  'admin.self': '☁️ Open API Docs',
  'admin.gdpr': '🛡️ Data Compliance',
}

const dicts: Record<Lang, Dict> = { zh, en }

// 取词：按当前语言返回文案；缺失时回退中文再回退键名
export function t(key: string): string {
  return dicts[lang.value][key] || dicts.zh[key] || key
}

// 切换语言并持久化
export function setLang(l: Lang) {
  lang.value = l
  try { localStorage.setItem('app_lang', l) } catch {}
}

// 切换语言（快捷入口：点击按钮 zh/en 互切）
export function toggleLang() {
  setLang(lang.value === 'zh' ? 'en' : 'zh')
}