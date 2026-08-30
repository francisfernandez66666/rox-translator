// ============ i18n/index.ts · 职责说明 ============
// 前端国际化核心模块（React 版，无框架耦合）
// 字典与 Vue 版完全同源：base 字典（dicts.zh/en.ts）+ panels/*.ts 合并。
// 语言切换通过极简外部 store + useSyncExternalStore 驱动重渲染。
// =============================================

import { useSyncExternalStore } from 'react'
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
import * as pPackages from './panels/packages'
import * as pFeedback from './panels/feedback'
import * as pReferral from './panels/referral'
import { baseZh } from './dicts.zh'
import { baseEn } from './dicts.en'

// 语言类型：'zh' 中文 / 'en' 英文（i18n 全部取词与切换基于此类型）
export type Lang = 'zh' | 'en'
type Dict = Record<string, string>

const zh: Dict = {
  ...baseZh,
  ...pOverview.zh, ...pTenants.zh, ...pOrg.zh, ...pUsers.zh, ...pKb.zh,
  ...pModels.zh, ...pWorkflow.zh, ...pApiKeys.zh, ...pWebhooks.zh,
  ...pTickets.zh, ...pBilling.zh, ...pUsage.zh, ...pAlerts.zh,
  ...pInvites.zh, ...pChat.zh, ...pPackages.zh, ...pFeedback.zh,
  ...pReferral.zh,
}

const en: Dict = {
  ...baseEn,
  ...pOverview.en, ...pTenants.en, ...pOrg.en, ...pUsers.en, ...pKb.en,
  ...pModels.en, ...pWorkflow.en, ...pApiKeys.en, ...pWebhooks.en,
  ...pTickets.en, ...pBilling.en, ...pUsage.en, ...pAlerts.en,
  ...pInvites.en, ...pChat.en, ...pPackages.en, ...pFeedback.en,
  ...pReferral.en,
}

const dicts: Record<Lang, Dict> = { zh, en }

// ---- 极简外部语言 store ----
let currentLang: Lang = (localStorage.getItem('app_lang') as Lang) || 'zh'
const listeners = new Set<() => void>()

function emit() { listeners.forEach((l) => l()) }

// 设置当前语言并持久化到 localStorage，触发订阅者重渲染
export function setLang(l: Lang) {
  currentLang = l
  try { localStorage.setItem('app_lang', l) } catch { /* ignore */ }
  emit()
}

// 在当前中文/英文之间切换语言
export function toggleLang() {
  setLang(currentLang === 'zh' ? 'en' : 'zh')
}

function subscribe(cb: () => void) {
  listeners.add(cb)
  return () => { listeners.delete(cb) }
}

function getSnapshot(): Lang { return currentLang }

/** useLang 订阅当前语言；语言切换时所有调用组件重渲染 */
export function useLang(): Lang {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot)
}

/** t 纯函数取词（非响应式；组件内请配合 useLang 使用以获得切换刷新） */
export function t(key: string): string {
  return dicts[currentLang][key] || dicts.zh[key] || key
}

/** tpl 带参数取词：{name} 占位符替换 */
export function tpl(key: string, vars: Record<string, string | number> = {}): string {
  let s = t(key)
  for (const k in vars) s = s.split(`{${k}}`).join(String(vars[k]))
  return s
}

/** useT 组合钩子：返回 [lang, t, tpl]，语言切换自动重渲染 */
export function useT(): [Lang, typeof t, typeof tpl] {
  useLang()
  return [currentLang, t, tpl]
}
