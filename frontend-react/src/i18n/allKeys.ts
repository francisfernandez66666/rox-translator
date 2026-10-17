// ============ i18n/allKeys.ts · 职责说明 ============
// **全量键清单**的唯一出处（ALL_KEYS／CORE_KEYS）。★ 2026-10-17 首屏轻量化批 A 从 index.ts 搬来。
//
// 为什么必须单独一份、且**只能被测试引用**：全量清单天然要包含 30 份懒加载面板的键，
// 而 index.ts 已经刻意不再静态 import 那些面板（那正是入口包瘦下来的原因）。
// 谁把这张表 import 回运行期代码（组件、api、App），30 份面板（源 ≈197 KB）就顺着它回到入口包——
// 体积回归在源码里看不出来、只在产物字节里看得见（判据＝check_frontend_tar.sh T3 的入口体积上限
// 与本次新增的 T7）。所以本文件是**测试专用件**：三个消费者都是测试
// （locales.core.test.ts／missingKeyGate.test.ts／usdtPromise.test.ts），index.ts 里**不许** re-export 它。
//
// 与运行期的关系：这里拼出来的键集必须与「门面四份 ＋ ensureAppPanels() 合并后的 zh/en」逐键相等。
// 两边都从同一批 panels 模块派生，漂移由 appPanelSplit.test.ts 的 P1 腿（现算全集比对）当场抓——
// 新增面板只补一边（比如只加进 appPanels.ts 忘了这里）会立刻红，不是靠人记住。
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
import * as pCost from './panels/cost'
import * as pAuth from './panels/auth'
import * as pCoupons from './panels/coupons'
import * as pAssist from './panels/assist'
import { baseEn } from './dicts.en'
import { CORE_PREFIXES } from './index'

/** 词条形状：扁平 key → 译文；ALL_KEYS 由这份 en 全量词典现算派生 */
type Dict = Record<string, string>

// 合并顺序：base 最低、面板之后（与 index.ts 的老口径一致）。
// 实测全仓跨面板重复键 17 对**全部**是 base ↔ 面板（面板之间零重复），故这份顺序只需保证"面板压 base"。
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
// locales.core.test.ts 的全量闸门以此为基准——新增面板键而某语种没跟上即红灯。
export const ALL_KEYS: ReadonlyArray<string> = Object.keys(en).sort()
