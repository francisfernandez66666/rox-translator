// ============ i18n/index.ts · 职责说明 ============
// 前端国际化核心模块（React 版，无框架耦合）
// 字典与 Vue 版完全同源：base 字典（dicts.zh/en.ts）+ panels/*.ts 合并。
// 语言切换通过极简外部 store + useSyncExternalStore 驱动重渲染。
// 2026-09-17/18：新增 panels/auth（登录/注册/AI 接管引导）面板并纳入合并；
// 同期全站词条去除 emoji 前缀（改由 LangCross <Icon/> 渲染），本合并逻辑不变。
// ★ #23（2026-09-19）：Lang 从 zh|en 扩到 12 语种（zh/en/ru/fr/ar/es/pt/de/ja/ko/th/zh_hant）。
// ★ #32（2026-09-20 全站十语种）：locales/*.ts 十份词典升级为 ALL_KEYS 全量口径（09-20 建库 2532 键；
//   此后随批次增长，★ 2026-10-06 实测 3033 键——**键数没有固定基准**，逐键覆盖由 locales.core.test.ts
//   以 ALL_KEYS 动态长度为判据，红/绿只看键集差集，不许把任何数字写进判据）。
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
// ★ 〇-X #55（2026-09-28）：官网公开「比价与算价」页 /compare 词条（公式公示 + 人工对照价出处）
import * as pCost from './panels/cost'
// 2026-09-17 新增：认证域（登录/注册/找回密码/AI 接管注册引导）文案，
// 从 dicts 基础字典与组件硬编码里独立出来，供 Login / AiRegisterFlow 取词。
import * as pAuth from './panels/auth'
// ★ 2026-09-21 #41：优惠券（超管券管理 + 收银台试算）
import * as pCoupons from './panels/coupons'
// ★ 2026-09-21 #34：AI 助手管理面板（原生 React 重做，取代 iframe 内嵌管理台）
import * as pAssist from './panels/assist'
import { baseZh } from './dicts.zh'
import { baseEn } from './dicts.en'
// ★ #23：新语种部分词典（locales/<code>.ts）——★ 2026-10-11 性能批改为**按语种懒加载**，
// 见下方 localeLoaders；这里不再有静态 import（静态引入＝十份全量词典全打进进入口包）。
// ★ 〇-Q（2026-09-23）：语种 → 文字系统 / BCP47 / 动效档位的单点真值（applyDir 与样式档位共用）
import { isRTL, BCP47_BY_LANG } from './script'

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
  ...pReferral.zh, ...pTasks.zh, ...pSdk.zh, ...pHub.zh, ...pOps.zh, ...pIndustries.zh, ...pPersonas.zh, ...pBrandterms.zh, ...pChatwin.zh, ...pDs.zh, ...pMybill.zh, ...pReconcile.zh, ...pLanding.zh, ...pAuth.zh, ...pCoupons.zh, ...pAssist.zh, ...pCost.zh,
}

// en 英文词典：base 基础字典 + 各面板模块英文文案合并
// 顺序与上方 zh 严格一致：任何一侧漏登记，parity.test.ts 的逐面板对等性即失效。
const en: Dict = {
  ...baseEn,
  ...pOverview.en, ...pTenants.en, ...pOrg.en, ...pUsers.en, ...pKb.en,
  ...pModels.en, ...pWorkflow.en, ...pApiKeys.en, ...pWebhooks.en,
  ...pTickets.en, ...pBilling.en, ...pUsage.en, ...pAlerts.en,
  ...pInvites.en, ...pChat.en, ...pPackages.en, ...pFeedback.en,
  ...pReferral.en, ...pTasks.en, ...pSdk.en, ...pHub.en, ...pOps.en, ...pIndustries.en, ...pPersonas.en, ...pBrandterms.en, ...pChatwin.en, ...pDs.en, ...pMybill.en, ...pReconcile.en, ...pLanding.en, ...pAuth.en, ...pCoupons.en, ...pAssist.en, ...pCost.en,
}

// 核心集键清单（按 CORE_PREFIXES 从英文全量词典筛出、排序冻结）：locales 覆盖测试与
// 翻译生产都以这一份为准。★ 2026-09-20 全站十语种后升级为 ALL_KEYS 全量口径，
// 本清单保留给历史批次对照（多语言批次记忆/文档引用的是 453 这个数）。
export const CORE_KEYS: ReadonlyArray<string> = Object.keys(en).filter((k) => CORE_PREFIXES.some((p) => k.startsWith(p))).sort()
// 全量键清单（★ 全站十语种）：十份 locales/*.ts 必须逐键覆盖这份表，
// locales.core.test.ts 的全量闸门以此为基准——新增面板键而某语种没跟上即红灯
export const ALL_KEYS: ReadonlyArray<string> = Object.keys(en).sort()

// ---- 新语种词典：按当前语种懒加载（★ 2026-10-11 跨境链路瓶颈批）----
// 为什么改：十份 locales/*.ts 是 **ALL_KEYS 全量词典**（源 2.1 MB、打进 dist 后 gzip 约 440 KB），
// 过去静态 import 进本文件 ⇒ 无论访客用哪种语言，入口 JS 都要把**十一份**词典全下完。
// 实测入口包 2.63 MB／gzip 442 KB，而大陆→Cloudflare 免费版→美西边缘这条链塌到 KB/s，
// 「刷新页面动效加载十几秒」的分子就是它——这是入口体积的最大单一来源。
// 现在入口只含 zh/en 全量＋面板词典；其余语种在「当前语种确定」时各取自己那一份 chunk。
//
// ★ 代价（必须知晓，别再当成缺陷回查）：目标语种 chunk 到手前的那一两帧走
//   lang→en→zh 回退链，于是非中文访客会先看到**英文**界面、chunk 落地后 emit() 触发重渲染换成母语。
//   这是懒加载的固有形态，不是回归；对中文访客完全没有这一帧（zh 就在入口包里）。
//   也因此本文件里**所有**取词点都必须经 dictFor()——旧形态 `dicts[lang][key]` 在词典未到手时
//   会对 undefined 取下标直接 TypeError，把整站白屏（这是本次改造唯一的硬风险，锁见 lazyLocale.test.ts）。
type LocaleLoader = () => Promise<Dict>
// localeLoaders 语种 → 动态加载器。每个 loader 必须写成**字面量** dynamic import：
// Vite 靠静态分析这一串字面量切 chunk，写成 `import('./locales/' + lang)` 会退化成整目录隐式引用。
const localeLoaders: Partial<Record<Lang, LocaleLoader>> = {
  ru: () => import('./locales/ru').then((m) => m.dict),
  fr: () => import('./locales/fr').then((m) => m.dict),
  ar: () => import('./locales/ar').then((m) => m.dict),
  es: () => import('./locales/es').then((m) => m.dict),
  pt: () => import('./locales/pt').then((m) => m.dict),
  de: () => import('./locales/de').then((m) => m.dict),
  ja: () => import('./locales/ja').then((m) => m.dict),
  ko: () => import('./locales/ko').then((m) => m.dict),
  th: () => import('./locales/th').then((m) => m.dict),
  zh_hant: () => import('./locales/zh-hant').then((m) => m.dict),
}
// loadedLocales 已到手的全量词典（该语种没在表里＝还没加载，取词按回退链走 en/zh）
const loadedLocales: Partial<Record<Lang, Dict>> = {}
// inflightLocales 同一语种的并发请求复用同一个 promise：LangSelect 展开、setLang、
// 模块初始化三处可能同时要 ru，不该发出三次网络请求（chunk 有 HTTP 缓存，但重复的
// fetch  promise 各自还会触发一次 emit／重渲染）。
const inflightLocales = new Map<Lang, Promise<void>>()

/**
 * ensureLocale 确保某语种词典到手（zh/en 恒立即完成——它们就在入口包里）。
 * 幂等且可并发；**失败不写状态**（既不落 loadedLocales 也不留 inflight），
 * 所以下一次调用会重新拨——把一次网络抖动钉成"这个语种永远翻不了"是不可接受的。
 */
export function ensureLocale(lang: Lang): Promise<void> {
  const loader = localeLoaders[lang]
  if (!loader || loadedLocales[lang]) return Promise.resolve()
  const flying = inflightLocales.get(lang)
  if (flying) return flying
  const p = loader()
    .then((d) => {
      loadedLocales[lang] = d
      emit() // 词典到手＝出栈形态变了，让所有订阅组件重渲染（英文一闪 → 母语）
    })
    .catch(() => { /* 静默：界面继续走 en/zh 回退链，下一次 ensureLocale 重试 */ })
    .finally(() => { inflightLocales.delete(lang) })
  inflightLocales.set(lang, p)
  return p
}

// dictFor 取某语种的词典本体；未到手的新语种回**空对象**（调用方的 || 链自然落到 en→zh）。
// 这一层是懒加载的全部风险所在：绝不允许返回 undefined 再让调用方取下标。
function dictFor(lang: Lang): Dict {
  if (lang === 'zh') return zh
  if (lang === 'en') return en
  return loadedLocales[lang] || {}
}

// 新语种部分词典：键空间是**全量 ALL_KEYS**（★ 2026-09-20 全站十语种），locales 测试逐语种守护；
// ★ 2026-10-11 起这些词典不再静态合并进 dicts，改由上方 localeLoaders 按语种懒加载（取词一律经 dictFor）。

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
// ★ 2026-10-12（现网「标题正常、整页黑」诊断）：这一行原本是一条**裸**的模块顶层 localStorage 读。
//   浏览器把站点数据/Cookie 按策略拒绝时（Edge·Chrome 的「阻止所有 Cookie」档，含隐私模式），
//   取 `localStorage` 属性当场抛 SecurityError ⇒ 入口 chunk 的模块评估中断 ⇒ React 根本没挂载，
//   屏幕上只剩黑色背景与正确标题（HTML 已到、JS 没跑完）。同文件另三处存储读写
//   （setLang 的 setItem／detectBrowserLang／applyDir）本来就各自包着 try，这一处是唯一漏网的那条。
//   取向＝**取不到就当没存过**（自然回落浏览器检测），绝不让「读不到偏好」升级成「整站打不开」。
//   机械锁见 src/bootStorageGate.test.ts（顶层裸存储访问零命中＋六枚反证）。
let storedRaw: string | null = null
try { storedRaw = localStorage.getItem('app_lang') } catch { /* 站点数据被浏览器策略拒绝⇒不认存值，也不许把全站打黑 */ }
const stored = storedRaw as Lang | null
let currentLang: Lang = stored && LANG_OPTIONS.some((o) => o.code === stored) ? stored : detectBrowserLang()

// ★ F3：RTL 方向接线——document.dir 随语言切换。
//   ★ #23：阿拉伯语已进 UI 语种表，本钩子正式生效；
//   工作台/账单核心样式已改逻辑属性（margin-inline-* 等），dir=rtl 即镜像生效。
//   ★ 〇-Q（2026-09-23）：RTL 集合与 BCP47 标签搬到 i18n/script.ts 单点维护
//   （此前这里另有一份 RTL_LANGS，与样式档位表各写一套，加语种必然漏改）。
//   尤其注意 `documentElement.lang` **必须**写 BCP47：内部语种码 `zh_hant` 含下划线，
//   不是合法语言标签 → CSS `:lang(zh-Hant)` 永远匹配不到，字体档/排版档全部静默失效。
//   这是「改了 tokens 但繁体界面没变化」这类问题的根因。
// 按语言切换页面文字方向与语言标签
function applyDir(l: Lang) {
  try {
    document.documentElement.dir = isRTL(l) ? 'rtl' : 'ltr'
    document.documentElement.lang = BCP47_BY_LANG[l]
  } catch { /* 非浏览器环境忽略 */ }
}
applyDir(currentLang)
// listeners 语言订阅者集合（语言切换时依次触发，驱动组件重渲染）
const listeners = new Set<() => void>()

// emit 通知所有语言订阅者执行回调
function emit() { listeners.forEach((l) => l()) }

// ★ 2026-10-11 懒加载的启动腿：模块一被评估就去取**当前语种**那一份词典（不必等用户操作），
// 否则首屏要一直停在英文回退态、直到有人调用才动。写在 listeners/emit 之后，
// 是为了让 .then 里的 emit() 一定看得到已初始化的订阅集合（同一轮微任务里模块评估已跑完）。
void ensureLocale(currentLang)

// 设置当前语言并持久化到 localStorage，触发订阅者重渲染
export function setLang(l: Lang) {
  currentLang = l
  applyDir(l)
  try { localStorage.setItem('app_lang', l) } catch { /* ignore */ }
  emit() // 先按当前已到手词典重渲染（未到手时这一帧走 en/zh 回退，见 dictFor 上方代价说明）
  void ensureLocale(l) // 再确保该语种词典；到手后 ensureLocale 内部会再 emit() 一次换成母语
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
  // ★ 2026-10-11 懒加载：取词典一律经 dictFor（旧写法 dicts[currentLang][key] 在词典未到手时会崩）
  return dictFor(currentLang)[key] || en[key] || zh[key] || key
}

/**
 * 取词但**绝不露裸键名**：三档词典（当前语种→en→zh）都查不到时回落到调用方给的 fallback。
 * ★ F-53（2026-09-26 〇-U 批 I-8）：错误分支的文案必须走这一版——t() 的「未翻键回退到 key」
 * 是常规兜底（正常批次键数闸会拦住缺键），但错误气泡恰好是最容易被用户截图投诉的地方，
 * 把 `chat.sensitiveBlocked` 这样的键名显示出来，与本轮缺陷「界面露 sensitive_blocked 裸码」同形态。
 */
export function textOr(key: string, fallback = ''): string {
  return dictFor(currentLang)[key] || en[key] || zh[key] || fallback
}

/**
 * textOr 的插值版：先按「三档词典→fallback」取到句子，再替换 {name} 占位符。
 * fallback 里同样可以带占位符（调用方给一句中文原句，与 api/core.ts 的 apiMsg 口径一致：
 * 词典缺键时界面仍是人话，而不是 `chat.textTooLong` 这种键名）。
 */
export function tplOr(key: string, vars: Record<string, string | number> = {}, fallback = ''): string {
  let s = textOr(key, fallback)
  for (const k in vars) s = s.split(`{${k}}`).join(String(vars[k]))
  return s
}

/** 在指定语种取词（不走当前 UI 语种）；回退链 lang→en→zh。
 *  ★ 〇-Q 修正：落地页演示卡英语 UI 下源=英、定稿=中（EN→ZH），与其余语种「母语→EN」镜像；
 *  演示源句/术语按 srcLang 取词，必须绕过当前 UI 语种。此前 22:45 误折回 'zh'（变成 ZH→EN）已翻正。 */
export function translateIn(lang: Lang, key: string): string {
  // ★ 2026-10-11 懒加载：同 t()，词典未到手时回空对象 → 自然落到 en/zh 那一档。
  //   落地页演示卡因此在 chunk 到达前显示英文对照，到手后随 emit() 换成母语（调用方无需改）。
  return dictFor(lang)[key] || en[key] || zh[key] || key
}

/**
 * 落地页演示卡的**源语种**决策（★ 〇-Q 修正）：
 * - 英语 UI：源=英、定稿=中，展示真实 EN→ZH 翻译（与其余语种「母语→EN」镜像）；
 * - 其余语种：源语种 = 界面语种自身（俄语界面展示 RU→EN，阿拉伯界面展示 AR→EN…）。
 * 此前 22:45 误将英语 UI 折回 'zh'（变成 ZH→EN），与用户口径相反，已翻正。
 * 抽到 i18n 核心层，一是让 Landing.tsx 与测试共用同一份真值，二是避免测试被迫 import
 * 拉动 React 组件树（branding/api 在模块加载即触浏览器 API，node 环境会炸）。
 */
export function demoSrcLang(lang: Lang): Lang {
  return lang
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
