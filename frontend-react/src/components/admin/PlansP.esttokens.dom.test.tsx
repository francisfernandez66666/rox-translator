// ============================================================================
// components/admin/PlansP.esttokens.dom.test.tsx — 计费预估系数表单（★ 〇-X 第 5 项 / F-72 补口）
//
// 为什么有这张锁：这四个数（est_tokens_per_char_pro|fast 与 est_tokens_fixed_pro|fast）
// 直接决定「客户点建单时余额够不够」——调小＝放行更多单（真烧穿），调大＝更多单被误拦。
// 在它进管理台之前只能 psql 改 system_config，手滑一次没有第二个人知道。
// 表单必须做到三件事，本文件逐件钉死：
//  ① 回显的是**生效值**而不是库里的原样字符串（脏值直塞输入框＝把系统从没在用过的数正式写成配置）；
//  ② 客户端先把与服务端同口径的门挡住（K 必须 >0、四项必须齐、不许负数），
//     脏输入**不得发出请求**——「弹窗照弹/提示照出、请求照发」的假拦截判为红；
//  ③ 恢复缺省是批量清空动作，必须二次确认，取消即零调用。
//
// 运行：npx vitest run src/components/admin/PlansP.esttokens.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'
import { DialogHost } from '@/components/uiDialogs'
// 期望文案一律现读词典（改文案即改期望，不写死字面量）
import { zh as billingZh } from '@/i18n/panels/billing'
import { baseZh } from '@/i18n/dicts.zh'

// S = 可控回显：estCfg 由用例注入 GET 桩的返回体（生效值/库内原值/缺省三态）；
// saves 采集保存调用，用来判「未决弹窗时零请求」这类假拦截。
const S = vi.hoisted(() => ({
  saves: [] as Array<Record<string, unknown>>,
  getOk: true,
  cfg: {} as Record<string, unknown>,
}))

function defaultCfg() {
  return {
    success: true,
    coefficients: { k_pro: 160, k_fast: 60, fixed_pro: 3000, fixed_fast: 1200 },
    stored: { k_pro: '', k_fast: '', fixed_pro: '', fixed_fast: '' },
    defaults: { k_pro: 160, k_fast: 60, fixed_pro: 3000, fixed_fast: 1200 },
    formula: 'est_tokens = F(mode) + chars × langs × K(mode)',
    points_tokens_rate: 400,
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
  // ---- 预估系数三件套（本文件主测对象）----
  EST_TOKEN_FIELDS: ['k_pro', 'k_fast', 'fixed_pro', 'fixed_fast'],
  adminEstTokens: vi.fn(async () => (S.getOk ? S.cfg : { success: false, message: '读取失败' })),
  adminEstTokensSave: vi.fn(async (data: Record<string, unknown>) => { S.saves.push(data); return { success: true, ...S.cfg } }),
  adminGrowthFunnel: vi.fn(async () => ({ success: true, rows: [] })),
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  handleUnauthorized: vi.fn(),
  ApiError: class extends Error {},
  API_BASE: '',
}))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true, activeTenantId: 3 }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

// estBox 取预估区块根节点：标题 span → 所在按钮行 → 区块容器。
function estBox(): HTMLElement {
  const title = (Array.from(document.querySelectorAll('span')) as HTMLElement[])
    .find((s) => (s.textContent ?? '').trim() === billingZh['billing.estSection'])
  if (!title) throw new Error('未渲染预估系数区块（PlansP 结构变了？锁需随迁）')
  return title.parentElement!.parentElement!
}
// 四个输入框按 EST_TOKEN_FIELDS 声明序渲染，故下标即键序
function estInput(i: number): HTMLInputElement {
  const ins = Array.from(estBox().querySelectorAll('input')) as HTMLInputElement[]
  if (ins.length !== 4) throw new Error(`预估区块应有 4 个输入框，实得 ${ins.length}`)
  return ins[i]
}
function estButton(label: string): HTMLButtonElement {
  const btn = (Array.from(estBox().querySelectorAll('button')) as HTMLButtonElement[])
    .find((b) => (b.textContent ?? '').trim() === label)
  if (!btn) throw new Error(`预估区块未找到按钮「${label}」`)
  return btn
}
function dialogButton(key: 'common.ok' | 'common.cancel'): HTMLButtonElement | undefined {
  return ((Array.from(document.querySelectorAll('button')) as HTMLButtonElement[])
    .filter((b) => (b.textContent ?? '').trim() === baseZh[key])).pop()
}

async function mount() {
  render(<><PlansP /><DialogHost /></>)
  await vi.waitFor(() => expect(estInput(0).value).toBe('160'))
}

beforeEach(() => {
  cleanup()
  S.saves.length = 0
  S.getOk = true
  S.cfg = defaultCfg()
})

describe('PlansP · 计费预估系数表单（〇-X #51 / F-72 补口）', () => {
  it('① 回显生效值 + 库里未配置时标注「走代码缺省」', async () => {
    await mount()
    expect([estInput(0).value, estInput(1).value, estInput(2).value, estInput(3).value]).toEqual(['160', '60', '3000', '1200'])
    const txt = estBox().textContent ?? ''
    expect(txt).toContain(billingZh['billing.estDefault'].replace('{v}', '160'))
    expect(txt).toContain('est_tokens = F(mode)') // 公式随接口回显放出，界面上不另写一份
  })

  it('② K 填 0：客户端拦住且保存接口零调用（假拦截判红）', async () => {
    await mount()
    fireEvent.change(estInput(0), { target: { value: '0' } })
    fireEvent.click(estButton(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 50))
    expect(S.saves.length, 'K=0 不得发出保存请求').toBe(0)
  })

  it('③ 四项合法改动：恰好 1 次保存且四项一次交齐', async () => {
    await mount()
    fireEvent.change(estInput(0), { target: { value: '150' } })
    fireEvent.change(estInput(2), { target: { value: '0' } }) // 固定项 0 合法（退回纯线性）
    fireEvent.click(estButton(baseZh['common.save']))
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0]).toEqual({ k_pro: 150, k_fast: 60, fixed_pro: 0, fixed_fast: 1200 })
  })

  it('④ 库里是脏值：输入框填生效值、给脏值提示，保存发出的是生效值而非脏串', async () => {
    S.cfg = {
      ...defaultCfg(),
      stored: { k_pro: 'abc', k_fast: '', fixed_pro: '3000', fixed_fast: '1200' },
    }
    await mount()
    expect(estInput(0).value, '脏值不得直塞输入框').toBe('160')
    expect(estBox().textContent ?? '').toContain(
      billingZh['billing.estDirty'].replace('{v}', 'abc').replace('{d}', '160'))
    fireEvent.click(estButton(baseZh['common.save']))
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0]).toEqual({ k_pro: 160, k_fast: 60, fixed_pro: 3000, fixed_fast: 1200 })
  })

  it('⑤ 恢复缺省：先二次确认，取消零调用、确认只发 {reset:true}', async () => {
    await mount()
    fireEvent.click(estButton(billingZh['billing.estReset']))
    await vi.waitFor(() => expect(document.body.textContent).toContain(billingZh['billing.estConfirmReset']))
    fireEvent.click(dialogButton('common.cancel')!)
    await new Promise((r) => setTimeout(r, 50))
    expect(S.saves.length, '取消后不得清空线上配置').toBe(0)
    fireEvent.click(estButton(billingZh['billing.estReset']))
    await vi.waitFor(() => expect(document.body.textContent).toContain(billingZh['billing.estConfirmReset']))
    fireEvent.click(dialogButton('common.ok')!)
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0]).toEqual({ reset: true })
  })

  it('⑥ GET 失败：输入框禁用且点保存零调用（防拿空表单覆盖线上四键）', async () => {
    S.getOk = false
    render(<><PlansP /><DialogHost /></>)
    await vi.waitFor(() => {
      const t = (Array.from(document.querySelectorAll('span')) as HTMLElement[])
        .find((s) => (s.textContent ?? '').trim() === billingZh['billing.estSection'])
      expect(t).toBeTruthy()
    })
    const ins = Array.from(estBox().querySelectorAll('input')) as HTMLInputElement[]
    expect(ins.every((i) => i.disabled), '回显失败时四个输入框必须禁用').toBe(true)
    fireEvent.click(estButton(baseZh['common.save']))
    await new Promise((r) => setTimeout(r, 50))
    expect(S.saves.length).toBe(0)
  })
})
