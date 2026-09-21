// ============ i18n/parity.test.ts · 职责说明 ============
// 国际化字典静态逻辑测试：
//   - 每个面板/基础字典的中文键与英文键必须一一对应（缺译/多译即失败）；
//   - 合并后的全局词典取词行为：命中返回原文、缺失回退中文、再缺失回退键名。
// 覆盖 2026-09 新增强译键（ops.task*/promo*/billing.iPaidFailed 等）的成对性。
// 2026-09-17/18 追加守护：panels/auth（登录/注册/AI 接管引导全套键）纳入逐面板对等性；
// 同期 landing/chat/tickets 因换肤重排了键序、剥掉了词条里的 emoji 前缀——
// 键序不参与断言（只比集合），但「文本非空」断言会拦住把键留着、文案清空成占位的回退。
// =============================================
import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
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
import * as pIndustries from './panels/industries'
import * as pPersonas from './panels/personas'
import * as pBrandterms from './panels/brandterms'
import * as pChatwin from './panels/chatwin'
import * as pDatasources from './panels/datasources'
import * as pMybill from './panels/mybilling'
import * as pLanding from './panels/landing'
// 2026-09-17 新增面板：认证域（登录/注册/找回密码/AI 接管引导）
import * as pAuth from './panels/auth'
// ★ 2026-09-21 #41：优惠券面板词典
import * as pCoupons from './panels/coupons'
// ★ 2026-09-21 #34：AI 助手管理面板词典（原生面板，取代 iframe）
import * as pAssist from './panels/assist'
// ★ 2026-09-18 缺口修复：index.ts 已合并 sdk / hub / reconcile 三面板，但本守护的
//   PANELS 表漏收 → 这三份词典的 zh/en 键不对等、空值漏译永远不会被闸门发现。
import * as pSdk from './panels/sdk'
import * as pHub from './panels/hub'
import * as pReconcile from './panels/reconcile'

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
  { name: 'industries', mod: pIndustries }, { name: 'personas', mod: pPersonas }, { name: 'brandterms', mod: pBrandterms },
  { name: 'chatwin', mod: pChatwin }, { name: 'datasources', mod: pDatasources },
  { name: 'mybilling', mod: pMybill }, { name: 'landing', mod: pLanding },
  // 与 i18n/index.ts 的合并顺序对齐：新增面板同样追加在末尾
  { name: 'auth', mod: pAuth },
  // ★ 2026-09-21 #41：优惠券面板（券管理 + 收银台试算）
  { name: 'coupons', mod: pCoupons },
  // ★ 2026-09-21 #34：AI 助手管理面板
  { name: 'assist', mod: pAssist },
  { name: 'sdk', mod: pSdk }, { name: 'hub', mod: pHub }, { name: 'reconcile', mod: pReconcile },
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

// ★ F2：组件中文字面量扫描——迁移后这 9 个文件的代码（去注释）不得含 CJK 字符串字面量/JSX 文本
describe('F2 组件 i18n 覆盖扫描', () => {
  const files = [
    'src/components/EditorPage.tsx', 'src/components/TicketsPage.tsx', 'src/components/ChatWindow.tsx',
    'src/components/admin/DataSourcesP.tsx', 'src/components/admin/IndustriesP.tsx', 'src/components/admin/PersonasP.tsx', 'src/components/admin/BrandTermsP.tsx',
    'src/App.tsx', 'src/hooks/useChat.tsx', 'src/components/MyBilling.tsx',
  ]
  it('8 个组件文件无残留中文字面量（注释除外）', () => {
    const root = fileURLToPath(new URL('../..', import.meta.url))
    const lit = /"[^"\n]*[\u4e00-\u9fa5][^"\n]*"|'[^'\n]*[\u4e00-\u9fa5][^'\n]*'|`[^`\n]*[\u4e00-\u9fa5][^`\n]*`|>[^<]*[\u4e00-\u9fa5][^<]*</ // 含 JSX 文本+表达式混排（★ F2 补丁）
    const bad: string[] = []
    for (const f of files) {
      // ★ 全文去注释（含 /* */ 与行注释），再按整文扫描——避免跨行 JSX 文本被行级扫描漏网
      const src = readFileSync(root + '/' + f, 'utf-8')
        .replace(/\/\*[\s\S]*?\*\//g, ' ')
        .split('\n').map((l) => l.replace(/(^|[^:])\/\/[^\n]*/g, '$1')).join('\n')
      src.split('\n').forEach((l, i) => { if (lit.test(l)) bad.push(`${f}:${i + 1} ${l.trim().slice(0, 60)}`) })
      const jsx = src.match(/>[^<]*[\u4e00-\u9fa5][^<]*</g) || []
      jsx.forEach((m) => {
        const ln = src.slice(0, src.indexOf(m)).split('\n').length
        if (!bad.some((b) => b.startsWith(`${f}:${ln} `))) bad.push(`${f}:${ln} [jsx-cross] ${m.slice(0, 50)}`)
      })
    }
    expect(bad).toEqual([])
  })
})
