// ============ i18n/index.ts · 职责说明 ============
// 前端国际化核心模块（React 版，无框架耦合）
// 字典与 Vue 版完全同源：base 字典（dicts.zh/en.ts）+ panels/*.ts 合并。
// 语言切换通过极简外部 store + useSyncExternalStore 驱动重渲染。
// 2026-09-17/18：新增 panels/auth（登录/注册/AI 接管引导）面板并纳入合并；
// 同期全站词条去除 emoji 前缀（改由 LangCross <Icon/> 渲染），本合并逻辑不变。
// =============================================

import { useSyncExternalStore } from 'react'
// ---- 面板词典模块 ----
// 每个后台/前台面板自带 { zh, en } 两份同键词典，此处逐个 import 后在下方 spread 合并；
// 新增面板必须同步登记到 i18n/parity.test.ts 的 PANELS 表，否则中英对等性不受守护。
import * as pOverview from './panels/overview'
import * as pTenants from './panels/tenants'
import * as pOrg from './panels/org'
import * as pUsers from './panels/users'
import * as pKb from './panels/kb'
import * as pModels from './panels/models'
import * as pWorkflow from './panels/workflow'
import * as pApiKeys from './panels/apikeys'
import * as pWebhooks from './panels/webhooks'
import * as pSdk from './panels/sdk'
import * as pHub from './panels/hub'
import * as pTickets from './panels/tickets'
import * as pBilling from './panels/billing'
import * as pUsage from './panels/usage'
import * as pAlerts from './panels/alerts'
import * as pInvites from './panels/invites'
import * as pChat from './panels/chat'
import * as pPackages from './panels/packages'
import * as pFeedback from './panels/feedback'
import * as pReferral from './panels/referral'
import * as pTasks from './panels/tasks'
import * as pOps from './panels/ops'
import * as pIndustries from './panels/industries'
import * as pBrandterms from './panels/brandterms'
import * as pChatwin from './panels/chatwin'
import * as pDs from './panels/datasources'
import * as pMybill from './panels/mybilling'
import * as pLanding from './panels/landing'
import * as pReconcile from './panels/reconcile'
// 2026-09-17 新增：认证域（登录/注册/找回密码/AI 接管注册引导）文案，
// 从 dicts 基础字典与组件硬编码里独立出来，供 Login / AiRegisterFlow 取词。
import * as pAuth from './panels/auth'
import { baseZh } from './dicts.zh'
import { baseEn } from './dicts.en'

// 语言类型：'zh' 中文 / 'en' 英文（i18n 全部取词与切换基于此类型）
export type Lang = 'zh' | 'en'
// Dict 字典类型：key 为文案标识，value 为对应语言的展示文本
type Dict = Record<string, string>

// zh 中文词典：base 基础字典 + 各面板模块中文文案合并
// 合并语义：后面的 spread 覆盖前面的同名键，故新增面板一律追加在末尾（base 最低优先级）。
const zh: Dict = {
  ...baseZh,
  ...pOverview.zh, ...pTenants.zh, ...pOrg.zh, ...pUsers.zh, ...pKb.zh,
  ...pModels.zh, ...pWorkflow.zh, ...pApiKeys.zh, ...pWebhooks.zh,
  ...pTickets.zh, ...pBilling.zh, ...pUsage.zh, ...pAlerts.zh,
  ...pInvites.zh, ...pChat.zh, ...pPackages.zh, ...pFeedback.zh,
  ...pReferral.zh, ...pTasks.zh, ...pSdk.zh, ...pHub.zh, ...pOps.zh, ...pIndustries.zh, ...pBrandterms.zh, ...pChatwin.zh, ...pDs.zh, ...pMybill.zh, ...pReconcile.zh, ...pLanding.zh, ...pAuth.zh,
}

// en 英文词典：base 基础字典 + 各面板模块英文文案合并
// 顺序与上方 zh 严格一致：任何一侧漏登记，parity.test.ts 的逐面板对等性即失效。
const en: Dict = {
  ...baseEn,
  ...pOverview.en, ...pTenants.en, ...pOrg.en, ...pUsers.en, ...pKb.en,
  ...pModels.en, ...pWorkflow.en, ...pApiKeys.en, ...pWebhooks.en,
  ...pTickets.en, ...pBilling.en, ...pUsage.en, ...pAlerts.en,
  ...pInvites.en, ...pChat.en, ...pPackages.en, ...pFeedback.en,
  ...pReferral.en, ...pTasks.en, ...pSdk.en, ...pHub.en, ...pOps.en, ...pIndustries.en, ...pBrandterms.en, ...pChatwin.en, ...pMybill.en, ...pReconcile.en, ...pLanding.en, ...pAuth.en,
}

// dicts 按语言索引的词典集合，取词时按当前语言定位
const dicts: Record<Lang, Dict> = { zh, en }

// ---- 极简外部语言 store ----
// currentLang 当前语言（首次从 localStorage 读取，默认中文）
let currentLang: Lang = (localStorage.getItem('app_lang') as Lang) || 'zh'

// ★ F3：RTL 方向接线——document.dir 随语言切换（ar/fa/he/ur/ps/ku/dv 为从右到左）。
//   当前 UI 语言仅 zh/en（LTR），本钩子为品牌语言扩展（阿拉伯语界面等）预置；
//   工作台/账单核心样式已改逻辑属性（margin-inline-* 等），dir=rtl 即镜像生效。
const RTL_LANGS = new Set(['ar', 'fa', 'he', 'ur', 'ps', 'ku', 'dv'])
// 按语言切换页面文字方向（RTL 语言设 dir=rtl）
function applyDir(l: string) {
  try {
    document.documentElement.dir = RTL_LANGS.has(l) ? 'rtl' : 'ltr'
    document.documentElement.lang = l
  } catch { /* 非浏览器环境忽略 */ }
}
applyDir(currentLang)
// listeners 语言订阅者集合（语言切换时依次触发，驱动组件重渲染）
const listeners = new Set<() => void>()

// emit 通知所有语言订阅者执行回调
function emit() { listeners.forEach((l) => l()) }

// 设置当前语言并持久化到 localStorage，触发订阅者重渲染
export function setLang(l: Lang) {
  currentLang = l
  applyDir(l)
  try { localStorage.setItem('app_lang', l) } catch { /* ignore */ }
  emit()
}

// 在当前中文/英文之间切换语言
export function toggleLang() {
  setLang(currentLang === 'zh' ? 'en' : 'zh')
}

// subscribe 注册语言订阅回调，返回用于取消订阅的函数
function subscribe(cb: () => void) {
  listeners.add(cb)
  return () => { listeners.delete(cb) }
}

// getSnapshot 返回当前语言快照（供 useSyncExternalStore 读取）
function getSnapshot(): Lang { return currentLang }

/** useLang 订阅当前语言；语言切换时所有调用组件重渲染 */
/** Hook：当前语言（订阅切换） */
export function useLang(): Lang {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

/** t 纯函数取词（非响应式；组件内请配合 useLang 使用以获得切换刷新） */
/** 文案取词：key → 当前语言文本（缺词回退中文） */
export function t(key: string): string {
  return dicts[currentLang][key] || dicts.zh[key] || key
}

/** tpl 带参数取词：{name} 占位符替换 */
/** 带占位符文案：{name} 变量插值 */
export function tpl(key: string, vars: Record<string, string | number> = {}): string {
  let s = t(key)
  for (const k in vars) s = s.split(`{${k}}`).join(String(vars[k]))
  return s
}

/** useT 组合钩子：返回 [lang, t, tpl]，语言切换自动重渲染 */
/** Hook：一次取 [lang, t, tpl] */
export function useT(): [Lang, typeof t, typeof tpl] {
  useLang()
  return [currentLang, t, tpl]
}
