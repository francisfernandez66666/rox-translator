// ============ i18n/parity.test.ts · 职责说明 ============
// 国际化字典静态逻辑测试：
//   - 每个面板/基础字典的中文键与英文键必须一一对应（缺译/多译即失败）；
//   - 合并后的全局字典取词行为：命中返回原文、缺失回退中文、再缺失回退键名。
// 覆盖 2026-09 新增强译键（ops.task*/promo*/billing.iPaidFailed 等）的成对性。
// =============================================
import { describe, expect, it } from 'vitest'
import { baseZh } from './dicts.zh'
import { baseEn } from './dicts.en'
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
import * as pTasks from './panels/tasks'
import * as pOps from './panels/ops'

// 与 i18n/index.ts 保持同序的面板模块表（保证合并口径一致）
const PANELS: { name: string; mod: { zh: Record<string, string>; en: Record<string, string> } }[] = [
  { name: 'overview', mod: pOverview }, { name: 'tenants', mod: pTenants },
  { name: 'org', mod: pOrg }, { name: 'users', mod: pUsers },
  { name: 'kb', mod: pKb }, { name: 'models', mod: pModels },
  { name: 'workflow', mod: pWorkflow }, { name: 'apikeys', mod: pApiKeys },
  { name: 'webhooks', mod: pWebhooks }, { name: 'tickets', mod: pTickets },
  { name: 'billing', mod: pBilling }, { name: 'usage', mod: pUsage },
  { name: 'alerts', mod: pAlerts }, { name: 'invites', mod: pInvites },
  { name: 'chat', mod: pChat }, { name: 'packages', mod: pPackages },
  { name: 'feedback', mod: pFeedback }, { name: 'referral', mod: pReferral },
  { name: 'tasks', mod: pTasks }, { name: 'ops', mod: pOps },
]

describe('i18n 中英词典键值对等性', () => {
  it('基础字典 baseZh/baseEn 键一一对应', () => {
    const onlyZh = Object.keys(baseZh).filter((k) => !(k in baseEn))
    const onlyEn = Object.keys(baseEn).filter((k) => !(k in baseZh))
    expect(onlyZh).toEqual([])
    expect(onlyEn).toEqual([])
  })

  it.each(PANELS)('面板 $name 的 zh/en 键一一对应', ({ mod }) => {
    const onlyZh = Object.keys(mod.zh).filter((k) => !(k in mod.en))
    const onlyEn = Object.keys(mod.en).filter((k) => !(k in mod.zh))
    expect(onlyZh).toEqual([])
    expect(onlyEn).toEqual([])
  })

  it('翻译文本不应留空（防占位键静默漏译）', () => {
    const empty = [...PANELS, { name: 'base', mod: { zh: baseZh, en: baseEn } }]
      .flatMap(({ name, mod }) =>
        Object.keys(mod.zh)
          .filter((k) => !String(mod.zh[k]).trim() || !String(mod.en[k]).trim())
          .map((k) => `${name}.${k}`))
    expect(empty).toEqual([])
  })
})

describe('全局词典取词行为', () => {
  it('t()：命中返回原文 / 缺失回退中文 / 双缺失回退键名', async () => {
    // 动态导入 i18n/index 以复用同一合并口径
    const { t } = await import('./index')
    // 命中中文原文
    expect(t('common.save')).toBe('保存')
    // 新增键可正常取词（2026-09）
    expect(t('ops.taskEnabled')).toMatch(/开关|switch/i)
    expect(t('billing.iPaidFailed')).toMatch(/失败|failed/i)
    // 双缺失回退键名
    expect(t('no.such.key.zzz')).toBe('no.such.key.zzz')
  })

  it('tpl()：占位符替换生效', async () => {
    const { tpl } = await import('./index')
    expect(tpl('billing.orderNo', { orderNo: 'ORD-1' })).toContain('ORD-1')
  })
})
