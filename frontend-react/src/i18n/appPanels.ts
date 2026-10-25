// ============ i18n/appPanels.ts · 职责说明 ============
// 后台／长尾面板词条的**懒加载聚合**（★ 2026-10-17 首屏轻量化批 A）。
//
// 为什么要有这个文件：这 30 份面板词典（源 ≈197 KB）此前全部静态 import 进 i18n/index.ts，
// 于是**任何**访客（含只看官网首页的未登录访客）都得把它们的字节背进入口包——
// 而公开面真正用到的面板只有 landing／auth／cost／chat 四份（键 → 面板的映射实测见
// src/i18n/appPanelSplit.test.ts 的 P2 腿）。把这些面板挪到这里、由 App 入口按需 import，
// 入口包就能小一大截（跨境 6~20 KB/s 的链路上每 10 KB 都是实打实的一秒）。
//
// 三条口径（改动前必读）：
//  ① **这一份只放"非门面"面板**：门面用的 landing／auth／cost／chat 留在 index.ts（静态、立即可用）。
//     判据＝appPanelSplit.test.ts P2「公开面文件用到的每个键，在 ensureAppPanels() 之前就能取到」，
//     把某个公开面要用的面板挪进来会当场红（不是靠人记住）。
//  ② **合并次序对"跨面板重复键"不敏感，对 base 敏感**：实测全仓跨面板重复键只有 17 对，
//     全部是 base ↔ 某个面板（base 里预置了 org./fb./usage. 的同名键，面板那份为准）；
//     面板之间**零重复**。所以这里只需保证「后并的面板覆盖 base」，无须按 index.ts 的老顺序排。
//     ⚠️ 将来若真出现面板↔面板重复键，就地合并的先后会决定取值，届时必须回到 P1 腿把它显式排开。
//  ③ **加载失败不写状态**（与 ensureLocale 同口径）：catch 后不置 panelsReady，
//     下一次调用重拨；门面文案（eager 集）照常，最坏表现是后台页面短暂露几个键名。
//
// 新增面板时：本文件补一行 import ＋ 一处 spread，并在 parity.test.ts 的 PANELS 表登记
// （那张表管 zh/en 对等，与本文件的懒加载是两件事，缺一不可）。
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
import * as pReconcile from './panels/reconcile'
import * as pCoupons from './panels/coupons'
import * as pAssist from './panels/assist'
import * as pInviteLink from './panels/invitelink'

/** 面板词条的统一形状：扁平 key → 译文（懒加载词典与入口词典共用同一形状） */
type Dict = Record<string, string>

/** 懒加载面板的中文词条（顺序见文件头口径②：面板之间零重复，base 由调用方先铺） */
export const zh: Dict = {
  ...pOverview.zh, ...pTenants.zh, ...pOrg.zh, ...pUsers.zh, ...pKb.zh, ...pModels.zh,
  ...pWorkflow.zh, ...pApiKeys.zh, ...pWebhooks.zh, ...pSdk.zh, ...pHub.zh, ...pTickets.zh,
  ...pBilling.zh, ...pUsage.zh, ...pAlerts.zh, ...pInvites.zh, ...pPackages.zh, ...pFeedback.zh,
  ...pReferral.zh, ...pTasks.zh, ...pOps.zh, ...pIndustries.zh, ...pPersonas.zh, ...pBrandterms.zh,
  ...pChatwin.zh, ...pDs.zh, ...pMybill.zh, ...pReconcile.zh, ...pCoupons.zh, ...pAssist.zh, ...pInviteLink.zh,
}

/** 懒加载面板的英文词条（与 zh 同键，对等性由 parity.test.ts 管） */
export const en: Dict = {
  ...pOverview.en, ...pTenants.en, ...pOrg.en, ...pUsers.en, ...pKb.en, ...pModels.en,
  ...pWorkflow.en, ...pApiKeys.en, ...pWebhooks.en, ...pSdk.en, ...pHub.en, ...pTickets.en,
  ...pBilling.en, ...pUsage.en, ...pAlerts.en, ...pInvites.en, ...pPackages.en, ...pFeedback.en,
  ...pReferral.en, ...pTasks.en, ...pOps.en, ...pIndustries.en, ...pPersonas.en, ...pBrandterms.en,
  ...pChatwin.en, ...pDs.en, ...pMybill.en, ...pReconcile.en, ...pCoupons.en, ...pAssist.en, ...pInviteLink.en,
}
