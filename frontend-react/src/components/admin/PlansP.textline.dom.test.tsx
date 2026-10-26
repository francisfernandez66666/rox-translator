// ============================================================================
// components/admin/PlansP.textline.dom.test.tsx — 逐行翻译两条档位的运营表单（★ 2026-10-26 裁决九-4）
//
// 为什么有这张锁：这两档（text_line_retry_max / text_line_upstream_dead_streak）改的是
// **所有租户每一单**的补译枪数与上游断早停灵敏度。写死在代码里＝调档必须发版，
// 而上游抖动率只有运营看得见；放开配又必须防两类事故：
//  ① 半套参数（只改一条）⇒ 一条链新档、另一条链旧档；
//  ② 0 档语义并不存在（0＝"一枪都不打"／"一行失败就打死整语种"），必须当场拦住而不是落库；
//  ③ 界面回显的必须是**读侧钳位后的生效值**——把库里那个脏字面量塞进输入框，
//     管理员原样点保存就等于把"系统从没在用过的数"钉成正式配置（本仓反复踩的读写不同源）。
//
// 另外一条跨侧等值（区间 1~5 / 2~10 必须与后端 textLineCfgFields 的 lo/hi 逐字相等）**不在本文件**：
// 它读的是后端 Go 源文件，归 `src/lib/textLineTiers.crossSide.lock.test.ts`（node 环境）。
// 两处各写一份解析正则就是本仓最忌的"两份尺子"，所以这里只留界面行为的判据。
//
// 运行：npx vitest run src/components/admin/PlansP.textline.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'
import { DialogHost } from '@/components/uiDialogs'
import { ApiError } from '@/api'
// 期望文案一律现读词典（改文案即改期望，不写死字面量）
import { zh as billingZh } from '@/i18n/panels/billing'
import { baseZh } from '@/i18n/dicts.zh'
// 区间与字段名的前端出处（界面预拦用的就是这一份，本文件不另抄数字）
import { TEXT_LINE_TIER_RANGES, TEXT_LINE_TIER_FIELDS } from '@/lib/textLineTiers'

// S = 可控回显：tiers 注入 GET 桩的返回体；saves 采集保存调用（判"弹窗未决时零请求"）
const S = vi.hoisted(() => ({
  saves: [] as Array<Record<string, unknown>>,
  getOk: true,
  getFail: '' as '' | '403',
  cfg: {} as Record<string, unknown>,
}))

function tierCfg() {
  return {
    success: true,
    tiers: { retry_max: 3, upstream_dead_streak: 3 },
    stored: { text_line_retry_max: '', text_line_upstream_dead_streak: '' },
    defaults: { retry_max: 3, upstream_dead_streak: 3 },
    ranges: { ...TEXT_LINE_TIER_RANGES },
  }
}

vi.mock('@/api', () => ({
  billingQuota: vi.fn(async () => ({ success: true, qps: 10, concurrent: 3, max_daily_chars: 0, max_daily_points: 0, tenant_selected: true, unlimited: {} })),
  billingQuotaSave: vi.fn(async () => ({ success: true })),
  billingOrders: vi.fn(async () => ({ success: true, orders: [] })),
  billingInvoices: vi.fn(async () => ({ success: true, invoices: [] })),
  billingInvoiceCreate: vi.fn(async () => ({ success: true })),
  billingInvoiceVoid: vi.fn(async () => ({ success: true })),
  adminOrderRefund: vi.fn(async () => ({ success: true })),
  adminOrderPay: vi.fn(async () => ({ success: true })),
  payCreate: vi.fn(async () => ({ success: true })),
  payStatus: vi.fn(async () => ({ success: true })),
  paySimulate: vi.fn(async () => ({ success: true })),
  payManualConfirm: vi.fn(async () => ({ success: true })),
  manualConfirmOrders: vi.fn(async () => ({ success: true, orders: [] })),
  plans: vi.fn(async () => ({ success: true, plans: [] })),
  myPackage: vi.fn(async () => ({ success: true })),
  packageSubscribe: vi.fn(async () => ({ success: true })),
  packageUpgrade: vi.fn(async () => ({ success: true })),
  autoRenewSet: vi.fn(async () => ({ success: true })),
  couponPreview: vi.fn(async () => ({ success: true })),
  adminPackages: vi.fn(async () => ({ success: true, packages: [] })),
  adminPackageCreate: vi.fn(async () => ({ success: true })),
  adminPackageUpdate: vi.fn(async () => ({ success: true })),
  adminPackageDelete: vi.fn(async () => ({ success: true })),
  adminPackageSettings: vi.fn(async () => ({ success: true, free_trial_points: 1000, usdt_enabled: '0', pay_mode: 'static_qr' })),
  adminPackageSettingsSave: vi.fn(async () => ({ success: true })),
  adminQRUpload: vi.fn(async () => ({ success: true })),
  adminPayChannels: vi.fn(async () => ({ success: true, fields: {}, env_overridden: {} })),
  adminPayChannelsSave: vi.fn(async () => ({ success: true })),
  PAY_CH_FIELDS: [],
  adminQuoteCurrency: vi.fn(async () => ({ success: true, currency: 'CNY', rates: { CNY: 1 }, supported_currencies: ['CNY'], env_overridden: {} })),
  adminQuoteCurrencySave: vi.fn(async () => ({ success: true })),
  EST_TOKEN_FIELDS: ['k_pro', 'k_fast', 'fixed_pro', 'fixed_fast'],
  adminEstTokens: vi.fn(async () => ({ success: true, coefficients: {}, stored: {}, defaults: {} })),
  adminEstTokensSave: vi.fn(async () => ({ success: true })),
  // ---- 本文件主测对象：两条档位的读与写 ----
  adminTextLineTiers: vi.fn(async () => {
    if (S.getFail === '403') {
      // 后端读面对非平台超管回 403（bizResp 会把 403 照抛以触发重登录），这里造同一形态
      const e = new ApiError('无权限访问该配置', 403)
      throw e
    }
    return S.getOk ? S.cfg : { success: false, message: '读取失败' }
  }),
  adminTextLineTiersSave: vi.fn(async (data: Record<string, unknown>) => {
    S.saves.push(data)
    return { success: true, ...(S.cfg as Record<string, unknown>) }
  }),
  adminGrowthFunnel: vi.fn(async () => ({ success: true, rows: [] })),
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  handleUnauthorized: vi.fn(),
  // 与真实 core.ts 同构造签名（message, status）——guardRead 按 status 分派 401/其余，
  // 桩少了 status 这一维就等于把「403 走 toast」那条腿的靶子抹平了
  ApiError: class extends Error {
    status?: number
    constructor(message: string, status?: number) {
      super(message)
      this.name = 'ApiError'
      this.status = status
    }
  },
  API_BASE: '',
}))

// 提示总线收成可断言的清单：「拦下了」必须既能证明没发请求（负向），
// 也能证明给出了该给的那句话（正向对照）——只数请求次数的话，一句都不弹也算"拦下"。
const T = vi.hoisted(() => ({ warn: [] as string[], error: [] as string[], ok: [] as string[] }))
vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn((m: string) => { T.ok.push(m) }),
  toastWarn: vi.fn((m: string) => { T.warn.push(m) }),
  toastError: vi.fn((m: string) => { T.error.push(m) }),
}))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true, activeTenantId: 3 }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

// box 取档位区块根节点：标题 span → 按钮行 → 区块容器（与 esttokens 同一取法）
function box(): HTMLElement {
  const title = (Array.from(document.querySelectorAll('span')) as HTMLElement[])
    .find((s) => (s.textContent ?? '').trim() === billingZh['billing.tlSection'])
  if (!title) throw new Error('未渲染逐行翻译档位区块（PlansP 结构变了？锁需随迁）')
  return title.parentElement!.parentElement!
}
// 两个输入框按 TEXT_LINE_TIER_FIELDS 声明序渲染，故下标即键序
function input(i: number): HTMLInputElement {
  const ins = Array.from(box().querySelectorAll('input')) as HTMLInputElement[]
  if (ins.length !== TEXT_LINE_TIER_FIELDS.length) {
    throw new Error(`档位区块应有 ${TEXT_LINE_TIER_FIELDS.length} 个输入框，实得 ${ins.length}`)
  }
  return ins[i]
}
function btn(label: string): HTMLButtonElement {
  const b = (Array.from(box().querySelectorAll('button')) as HTMLButtonElement[])
    .find((x) => (x.textContent ?? '').trim() === label)
  if (!b) throw new Error(`档位区块未找到按钮「${label}」`)
  return b
}
function dialogBtn(key: 'common.ok' | 'common.cancel'): HTMLButtonElement | undefined {
  return ((Array.from(document.querySelectorAll('button')) as HTMLButtonElement[])
    .filter((b) => (b.textContent ?? '').trim() === baseZh[key])).pop()
}

async function mount() {
  render(<><PlansP /><DialogHost /></>)
  await vi.waitFor(() => expect(input(0).value).toBe('3'))
}

beforeEach(() => {
  cleanup()
  S.saves.length = 0
  S.getOk = true
  S.getFail = ''
  T.warn.length = 0
  T.error.length = 0
  T.ok.length = 0
  S.cfg = tierCfg()
})

describe('PlansP · 逐行翻译两条档位（裁决九-4）', () => {
  it('① 回显生效值；库里没表态时标注「走代码缺省」', async () => {
    await mount()
    expect([input(0).value, input(1).value]).toEqual(['3', '3'])
    const txt = box().textContent ?? ''
    expect(txt).toContain(billingZh['billing.estDefault'].replace('{v}', '3'))
    expect(txt).toContain(billingZh['billing.tlHint'])
  })

  it('② 越界与 0 一律当场拦下且零请求（假拦截判红）', async () => {
    await mount()
    // retry_max：0（低于下界且带"一枪都不打"的误读风险）／6（高于上界）
    // upstream_dead_streak：1（低于下界＝一行失败就打死整语种）／11（高于上界）
    const bads: Array<[number, string]> = [[0, '0'], [0, '6'], [1, '1'], [1, '11']]
    for (const [i, v] of bads) {
      fireEvent.change(input(i), { target: { value: v } })
      fireEvent.click(btn(baseZh['common.save']))
      await new Promise((r) => setTimeout(r, 30))
      expect(S.saves.length, `填 ${v} 到第 ${i} 档却发出了请求`).toBe(0)
    }
    // 正向对照：拦下必须**出声**（只数请求次数的话，"什么都不做"也算拦下，
    // 而运营看到的是按钮没反应 —— 那是本仓 #42 批定性过的静默失败形态）
    expect(T.warn, '越界输入被拦下却没给任何提示').toContain(billingZh['billing.tlNeedAll'])
  })

  it('③ 非整数（3.5）也拦下——这两档是"次数"，小数没有意义', async () => {
    await mount()
    fireEvent.change(input(0), { target: { value: '3.5' } })
    fireEvent.click(btn(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 30))
    expect(S.saves.length).toBe(0)
  })

  it('④ 区间两端各能存进去，且载荷两键一次交齐（端点现算，本文件不抄第二份区间数字）', async () => {
    const [loR, hiR] = TEXT_LINE_TIER_RANGES.retry_max
    const [loS, hiS] = TEXT_LINE_TIER_RANGES.upstream_dead_streak
    for (const [a, b] of [[loR, loS], [hiR, hiS]] as Array<[number, number]>) {
      S.saves.length = 0
      await mount()
      fireEvent.change(input(0), { target: { value: String(a) } })
      fireEvent.change(input(1), { target: { value: String(b) } })
      fireEvent.click(btn(baseZh['common.save']))
      await vi.waitFor(() => expect(S.saves.length).toBe(1))
      expect(S.saves[0]).toEqual({ retry_max: a, upstream_dead_streak: b })
      cleanup()
    }
  })

  it('⑤ 库里是脏值：输入框填生效值、给脏值提示，保存发出的是生效值而非脏串', async () => {
    S.cfg = {
      success: true,
      tiers: { retry_max: 5, upstream_dead_streak: 3 },
      stored: { text_line_retry_max: '99', text_line_upstream_dead_streak: '' },
      defaults: { retry_max: 3, upstream_dead_streak: 3 },
      ranges: { ...TEXT_LINE_TIER_RANGES },
    }
    render(<><PlansP /><DialogHost /></>)
    await vi.waitFor(() => expect(input(0).value).toBe('5'))
    expect(box().textContent ?? '').toContain('99') // 脏值提示把库内原值点名出来
    fireEvent.click(btn(baseZh['common.save']))
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0]).toEqual({ retry_max: 5, upstream_dead_streak: 3 })
  })

  it('⑥ GET 失败：输入框禁用且点保存零调用（防拿空表单覆盖线上两档）', async () => {
    S.getOk = false
    render(<><PlansP /><DialogHost /></>)
    await new Promise((r) => setTimeout(r, 80))
    expect(input(0).disabled).toBe(true)
    expect(input(1).disabled).toBe(true)
    fireEvent.click(btn(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 30))
    expect(S.saves.length).toBe(0)
  })

  it('⑦ 恢复缺省：先二次确认；取消零调用，确认只发 {reset:true}', async () => {
    await mount()
    fireEvent.click(btn(billingZh['billing.estReset']))
    await vi.waitFor(() => expect(dialogBtn('common.ok')).toBeTruthy())
    fireEvent.click(dialogBtn('common.cancel')!)
    await new Promise((r) => setTimeout(r, 30))
    expect(S.saves.length, '取消确认却发出了请求').toBe(0)
    fireEvent.click(btn(billingZh['billing.estReset']))
    await vi.waitFor(() => expect(dialogBtn('common.ok')).toBeTruthy())
    fireEvent.click(dialogBtn('common.ok')!)
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0]).toEqual({ reset: true })
  })

  it('⑧ 403（非平台超管读到越权）：走 toast、界面不白屏、两档禁用且零写入', async () => {
    // 后端读面对租户管理员也回 403，bizResp 按 F-64 口径把 403 **照抛**（触发重登录），
    // 所以这一跳落在 guardRead 的 catch 上：必须转成"回显失败"那一态，而不是把异常抛穿
    // 让整块面板（含同一页的订单表、收款台配置）跟着白屏。
    S.getFail = '403'
    render(<><PlansP /><DialogHost /></>)
    await new Promise((r) => setTimeout(r, 80))
    // 界面仍在（box() 取不到节点会直接抛 ⇒ 这一句本身就是"没白屏"的判据）
    expect(box()).toBeTruthy()
    expect(input(0).disabled).toBe(true)
    expect(input(1).disabled).toBe(true)
    // 越权那句要出声（401 才刻意不弹，见 guardRead 注释；403 不弹＝运营以为按钮坏了）
    expect(T.error.length, '403 却没弹任何提示').toBeGreaterThan(0)
    expect(T.error.join('|')).toContain('无权限访问该配置')
    fireEvent.click(btn(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 30))
    expect(S.saves.length, '回显都失败还发出了写请求').toBe(0)
  })

  it('⑨ 承诺禁词：档位面不许出现"人工/复核/审核/稍后重试"字样（本仓无人工环节）', () => {
    const banned = ['人工', '复核', '审核', '稍后重试', '我们会跟进']
    for (const k of ['billing.tlSection', 'billing.tlHint', 'billing.tlRange', 'billing.tlNeedAll', 'billing.tlConfirmReset']) {
      const v = billingZh[k]
      for (const w of banned) {
        expect(v.includes(w), `${k} 含禁词「${w}」（本仓无人工环节，不许承诺）`).toBe(false)
      }
    }
  })
})
