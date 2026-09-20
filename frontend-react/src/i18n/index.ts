// ============ i18n/index.ts · 职责说明 ============
// 前端国际化核心模块（React 版，无框架耦合）
// 字典与 Vue 版完全同源：base 字典（dicts.zh/en.ts）+ panels/*.ts 合并。
// 语言切换通过极简外部 store + useSyncExternalStore 驱动重渲染。
// 2026-09-17/18：新增 panels/auth（登录/注册/AI 接管引导）面板并纳入合并；
// 同期全站词条去除 emoji 前缀（改由 LangCross <Icon/> 渲染），本合并逻辑不变。
// ★ #23（2026-09-19）：Lang 从 zh|en 扩到 12 语种（zh/en/ru/fr/ar/es/pt/de/ja/ko/th/zh_hant）。
// ★ #32（2026-09-20 全站十语种）：locales/*.ts 十份词典升级为 ALL_KEYS 全量口径（2532 键逐键覆盖），
//   历史 CORE_KEYS 核心集口径已并入全量（CORE_KEYS 仅作键集前缀工具保留）。
//   取词回退链 lang→en→zh 仍在，但正常情况下不再触发——仅词典缺键或全新面板键未同步时兜底。
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
import * as pPersonas from './panels/personas'
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
// ★ #23：新语种部分词典（locales/<code>.ts）——只覆盖核心集键，其余走 lang→en→zh 回退链
import { dict as locRu } from './locales/ru'
import { dict as locFr } from './locales/fr'
import { dict as locAr } from './locales/ar'
import { dict as locEs } from './locales/es'
import { dict as locPt } from './locales/pt'
import { dict as locDe } from './locales/de'
import { dict as locJa } from './locales/ja'
import { dict as locKo } from './locales/ko'
import { dict as locTh } from './locales/th'
import { dict as locZhHant } from './locales/zh-hant'

// 语言类型：★ #23 扩为 12 语种（界面语言代码与翻译目标语代码刻意分开——
// zh_hant 只是 UI 代码，翻译目标语走 lib/langNames 的 40+ 语代码表）
export type Lang =
  | 'zh' | 'en'
  | 'ru' | 'fr' | 'ar' | 'es' | 'pt' | 'de' | 'ja' | 'ko' | 'th' | 'zh_hant'

// 语言下拉菜单的唯一数据源：native 用该语言自称（不随界面语言翻译，避免
// 「俄语界面里看不到俄语选项」的经典坑）；en 名做 aria 兜底
export const LANG_OPTIONS: ReadonlyArray<{ code: Lang; native: string; en: string }> = [
  { code: 'zh', native: '简体中文', en: 'Chinese (Simplified)' },
  { code: 'zh_hant', native: '繁體中文', en: 'Chinese (Traditional)' },
  { code: 'en', native: 'English', en: 'English' },
  { code: 'ru', native: 'Русский', en: 'Russian' },
  { code: 'fr', native: 'Français', en: 'French' },
  { code: 'ar', native: 'العربية', en: 'Arabic' },
  { code: 'es', native: 'Español', en: 'Spanish' },
  { code: 'pt', native: 'Português', en: 'Portuguese' },
  { code: 'de', native: 'Deutsch', en: 'German' },
  { code: 'ja', native: '日本語', en: 'Japanese' },
  { code: 'ko', native: '한국어', en: 'Korean' },
  { code: 'th', native: 'ไทย', en: 'Thai' },
]

// 核心集前缀：新语种部分词典必须 100% 覆盖这些前缀下的全部键（locales 测试逐语种断言）。
// 选段口径=外国用户真实要操作的链路：应用外壳(app/common/menu) + 登录注册(login/auth)
// + 聊天工作台(chat/msg/pwd)；管理后台等长尾走英文回退。
export const CORE_PREFIXES: ReadonlyArray<string> = ['app.', 'common.', 'menu.', 'login.', 'auth.', 'chat.', 'msg.', 'pwd.']
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
  ...pReferral.zh, ...pTasks.zh, ...pSdk.zh, ...pHub.zh, ...pOps.zh, ...pIndustries.zh, ...pPersonas.zh, ...pBrandterms.zh, ...pChatwin.zh, ...pDs.zh, ...pMybill.zh, ...pReconcile.zh, ...pLanding.zh, ...pAuth.zh,
}

// en 英文词典：base 基础字典 + 各面板模块英文文案合并
// 顺序与上方 zh 严格一致：任何一侧漏登记，parity.test.ts 的逐面板对等性即失效。
const en: Dict = {
  ...baseEn,
  ...pOverview.en, ...pTenants.en, ...pOrg.en, ...pUsers.en, ...pKb.en,
  ...pModels.en, ...pWorkflow.en, ...pApiKeys.en, ...pWebhooks.en,
  ...pTickets.en, ...pBilling.en, ...pUsage.en, ...pAlerts.en,
  ...pInvites.en, ...pChat.en, ...pPackages.en, ...pFeedback.en,
  ...pReferral.en, ...pTasks.en, ...pSdk.en, ...pHub.en, ...pOps.en, ...pIndustries.en, ...pPersonas.en, ...pBrandterms.en, ...pChatwin.en, ...pDs.en, ...pMybill.en, ...pReconcile.en, ...pLanding.en, ...pAuth.en,
}

// 核心集键清单（按 CORE_PREFIXES 从英文全量词典筛出、排序冻结）：locales 覆盖测试与
// 翻译生产都以这一份为准。★ 2026-09-20 全站十语种后升级为 ALL_KEYS 全量口径，
// 本清单保留给历史批次对照（多语言批次记忆/文档引用的是 453 这个数）。
export const CORE_KEYS: ReadonlyArray<string> = Object.keys(en).filter((k) => CORE_PREFIXES.some((p) => k.startsWith(p))).sort()
// 全量键清单（★ 全站十语种）：十份 locales/*.ts 必须逐键覆盖这份表，
// locales.core.test.ts 的全量闸门以此为基准——新增面板键而某语种没跟上即红灯
export const ALL_KEYS: ReadonlyArray<string> = Object.keys(en).sort()

// 新语种部分词典：键空间是核心集（CORE_PREFIXES 命中键），locales 测试逐语种守护
const locales: Partial<Record<Lang, Dict>> = {
  ru: locRu, fr: locFr, ar: locAr, es: locEs, pt: locPt,
  de: locDe, ja: locJa, ko: locKo, th: locTh, zh_hant: locZhHant,
}

// dicts 按语言索引的词典集合，取词时按当前语言定位；
// ★ 回退链在 t() 里做（lang→en→zh），此处只登记各语种已有的部分词典
// （zh/en 全量词典写在展开位之前会触发 TS 重复键告警，统一用一次断言收口）
const dicts = { zh, en, ...locales } as Record<Lang, Dict>

// ---- 极简外部语言 store ----
// ★ 2026-09-20 外国人可读（用户反馈④）：首次访问（localStorage 无 app_lang）不再
// 一律落中文，而是按浏览器语言自动选 UI 语种——中文系按区域分简/繁，命中语种表
// 直接用该语种，其余非中文一律回落英文（land.* 等长尾键本就 lang→en→zh 回退，
// 英文落地页对非中文访客完整可读）。用户手动切换后写 app_lang，永久覆盖检测值。
function detectBrowserLang(): Lang {
  try {
    const cands = navigator.languages?.length ? Array.from(navigator.languages) : [navigator.language || '']
    for (const raw of cands) {
      const tag = (raw || '').toLowerCase()
      if (!tag) continue
      if (tag.startsWith('zh')) return /(^|[-_])(tw|hk|mo|hant)/.test(tag) ? 'zh_hant' : 'zh'
      const two = tag.slice(0, 2) as Lang
      if (LANG_OPTIONS.some((o) => o.code === two)) return two
    }
  } catch { /* 非浏览器环境（SSR/测试）忽略检测 */ }
  return 'en'
}
// currentLang 当前语言（localStorage 有效值 > 浏览器检测；★ #23 存的野值一律重检测，
// 防止旧版本脏数据让全站取词踩空）
const stored = localStorage.getItem('app_lang') as Lang | null
let currentLang: Lang = stored && LANG_OPTIONS.some((o) => o.code === stored) ? stored : detectBrowserLang()

// ★ F3：RTL 方向接线——document.dir 随语言切换（ar/fa/he/ur/ps/ku/dv 为从右到左）。
//   ★ #23：阿拉伯语已进 UI 语种表，本钩子正式生效；
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

// getLang 读取当前语言（非 Hook，供命令式场景与测试断言使用）
export function getLang(): Lang {
  return currentLang
}

// 在当前界面语言之间切换的职责移交 LangSelect 下拉菜单（★ #23：12 语种下
// 二元 toggle 已无意义），这里只保留 setLang 单入口

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
/** 文案取词：key → 当前语言文本；★ #23 回退链 lang→en→zh，未翻键不露裸 key */
export function t(key: string): string {
  return dicts[currentLang][key] || en[key] || zh[key] || key
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
