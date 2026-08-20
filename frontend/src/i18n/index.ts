// ============================================================================
// i18n/index.ts — 前端国际化（中英）
// 职责：集中维护界面文案字典，提供 t(key) 取词与当前语言切换。
// 结构：base 字典（通用+登录+导航）+ 各面板字典（panels/*.ts）合并。
// 用法：import { t, setLang, toggleLang, lang } from '@/i18n'
//       模板中 {{ t('login.title') }}，切换语言后响应式更新。
// ============================================================================

import { ref } from 'vue'

// 面板字典合并（各面板独立文件，避免大文件冲突）
import * as pOverview from './panels/overview'
import * as pTenants from './panels/tenants'
import * as pOrg from './panels/org'
import * as pUsers from './panels/users'
import * as pKb from './panels/kb'
import * as pModels from './panels/models'
import * as pWorkflow from './panels/workflow'
import * as pApiKeys from './panels/apikeys'
import * as pWebhooks from './panels/webhooks'
import * as pTickets from './panels/tickets'
import * as pBilling from './panels/billing'
import * as pUsage from './panels/usage'
import * as pAlerts from './panels/alerts'
import * as pInvites from './panels/invites'
import * as pChat from './panels/chat'

// Lang 支持的语言类型：zh=中文（默认）、en=英文
export type Lang = 'zh' | 'en'

// 当前语言（持久化到 localStorage，默认中文）
export const lang = ref<Lang>((localStorage.getItem('app_lang') as Lang) || 'zh')

// 文案字典类型
type Dict = Record<string, string>

// ---- base 字典（通用 / 登录页 / 后台导航） ----
const baseZh: Dict = {
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
  'common.active': '启用',
  'common.disabled': '停用',
  'common.expired': '已过期',

  // ---- 应用工作台 ----
  'app.title': '翻译助手',
  'app.starting': '翻译引擎启动中…',
  'app.modelLabel': '翻译模型',
  'app.modelRec33': '(推荐，33语专用)',
  'app.modelBackup': '(备用)',
  'app.modelFast': '(快+强)',
  'app.modelFallback': '(降级用)',
  'app.langs': '语言',

  // ---- 语言名 ----
  'lang.zh': '中文',
  'lang.en': '英语',
  'lang.zhHant': '繁体中文',
  'lang.ru': '俄语',
  'lang.ar': '阿拉伯语',
  'lang.es': '西班牙语',
  'lang.pt': '葡萄牙语',
  'lang.fr': '法语',
  'lang.kk': '哈萨克语',
  'lang.de': '德语',
  'lang.ja': '日语',
  'lang.ko': '韩语',
  'lang.th': '泰语',
  'lang.vi': '越南语',
  'lang.mn': '蒙语',
  'lang.ms': '马来语',
  'lang.id': '印尼语',
  'lang.it': '意大利语',
  'lang.pl': '波兰语',
  'lang.nl': '荷兰语',
  'lang.sv': '瑞典语',
  'lang.uk': '乌克兰语',
  'lang.tr': '土耳其语',
  'lang.hi': '印地语',
  'lang.fa': '波斯语',
  'lang.he': '希伯来语',
  'lang.el': '希腊语',
  'lang.my': '缅甸语',
  'lang.km': '柬埔寨语',
  'lang.lo': '老挝语',
  'lang.tl': '菲律宾语',
  'lang.gu': '古吉拉特语',
  'lang.ur': '乌尔都语',
  'lang.te': '泰卢固语',
  'lang.mr': '马拉地语',
  'lang.bn': '孟加拉语',
  'lang.ta': '泰米尔语',
  'lang.bo': '藏语',
  'lang.ug': '维吾尔语',
  'lang.yue': '粤语',

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

const baseEn: Dict = {
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
  'common.active': 'Active',
  'common.disabled': 'Disabled',
  'common.expired': 'Expired',

  // ---- Workspace ----
  'app.title': 'Translation Assistant',
  'app.starting': 'Starting translation engine…',
  'app.modelLabel': 'Translation model',
  'app.modelRec33': '(Recommended, 33 langs)',
  'app.modelBackup': '(Fallback)',
  'app.modelFast': '(Fast & strong)',
  'app.modelFallback': '(Fallback)',
  'app.langs': 'Languages',

  // ---- Language names ----
  'lang.zh': 'Chinese',
  'lang.en': 'English',
  'lang.zhHant': 'Traditional Chinese',
  'lang.ru': 'Russian',
  'lang.ar': 'Arabic',
  'lang.es': 'Spanish',
  'lang.pt': 'Portuguese',
  'lang.fr': 'French',
  'lang.kk': 'Kazakh',
  'lang.de': 'German',
  'lang.ja': 'Japanese',
  'lang.ko': 'Korean',
  'lang.th': 'Thai',
  'lang.vi': 'Vietnamese',
  'lang.mn': 'Mongolian',
  'lang.ms': 'Malay',
  'lang.id': 'Indonesian',
  'lang.it': 'Italian',
  'lang.pl': 'Polish',
  'lang.nl': 'Dutch',
  'lang.sv': 'Swedish',
  'lang.uk': 'Ukrainian',
  'lang.tr': 'Turkish',
  'lang.hi': 'Hindi',
  'lang.fa': 'Persian',
  'lang.he': 'Hebrew',
  'lang.el': 'Greek',
  'lang.my': 'Burmese',
  'lang.km': 'Khmer',
  'lang.lo': 'Lao',
  'lang.tl': 'Filipino',
  'lang.gu': 'Gujarati',
  'lang.ur': 'Urdu',
  'lang.te': 'Telugu',
  'lang.mr': 'Marathi',
  'lang.bn': 'Bengali',
  'lang.ta': 'Tamil',
  'lang.bo': 'Tibetan',
  'lang.ug': 'Uyghur',
  'lang.yue': 'Cantonese',

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

// 合并全部面板字典
const zh: Dict = {
  ...baseZh,
  ...pOverview.zh,
  ...pTenants.zh,
  ...pOrg.zh,
  ...pUsers.zh,
  ...pKb.zh,
  ...pModels.zh,
  ...pWorkflow.zh,
  ...pApiKeys.zh,
  ...pWebhooks.zh,
  ...pTickets.zh,
  ...pBilling.zh,
  ...pUsage.zh,
  ...pAlerts.zh,
  ...pInvites.zh,
  ...pChat.zh,
}

const en: Dict = {
  ...baseEn,
  ...pOverview.en,
  ...pTenants.en,
  ...pOrg.en,
  ...pUsers.en,
  ...pKb.en,
  ...pModels.en,
  ...pWorkflow.en,
  ...pApiKeys.en,
  ...pWebhooks.en,
  ...pTickets.en,
  ...pBilling.en,
  ...pUsage.en,
  ...pAlerts.en,
  ...pInvites.en,
  ...pChat.en,
}

const dicts: Record<Lang, Dict> = { zh, en }

// 取词：按当前语言返回文案；缺失时回退中文再回退键名
export function t(key: string): string {
  return dicts[lang.value][key] || dicts.zh[key] || key
}

// 带参数取词：{name} 占位符替换（用于含动态数值/用户输入的文案）
export function tpl(key: string, vars: Record<string, string | number> = {}): string {
  let s = t(key)
  for (const k in vars) s = s.split(`{${k}}`).join(String(vars[k]))
  return s
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