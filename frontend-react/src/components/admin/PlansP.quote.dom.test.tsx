// ============================================================================
// PlansP.quote.dom.test.tsx — 多币种报价配置与展示测试（★ #75 前端切片，2026-09-23）
// 钉死四条口径（数据源 /api/admin/config/quote-currency 与 /api/plans、/api/me/package 报价出参）：
//   ① 超管「运营配置」渲染报价区块：币种下拉选中生效币种、倍率输入表只列白名单非 CNY 币种
//     （CNY 为结算基准恒 1，不出现输入框）；
//   ② GET 回显失败（success:false）时保存按钮禁用——空白表单直提等于整批清空线上汇率；
//   ③ env_overridden 接管时币种/倍率栏位置灰并标出变量名（防「保存成功却不生效」）；
//   ④ 保存提交的是「币种 + 全量倍率表（数字型，不含 CNY）」；选了非 CNY 却没填该币种
//     倍率时前端直接拦截不发请求（与服务端同一道门，双保险）；
//   ⑤ ★ 关闭封存态（2026-09-22 用户决策）：后端白名单只回 ['CNY'] 时报价区块整体不渲染
//     ——前端以「白名单含外币币种」为渲染条件，运营侧没有可误点的入口。
// 附带租户视角：商店卡渲染本币主价 + 「≈ ¥」人民币辅助行（¥ 老口径在 CNY 报价时逐字不变）。
// 语言：vitest.setup.ts 已预置 app_lang=zh，中文断言直接成立。
// 运行：npx vitest run src/components/admin/PlansP.quote.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'

// SUPPORTED 与 quoteMock 同处 hoisted 工厂内：vi.hoisted 回调里才能相互引用
// （hoisted 在 import 提升前执行，外部 const 此时还没初始化，直接引用会踩 TDZ）。
const H = vi.hoisted(() => {
  const SUPPORTED = ['CNY', 'USD', 'EUR', 'JPY', 'GBP', 'HKD', 'KRW', 'SGD', 'AUD', 'CAD', 'CHF', 'THB']
  const quoteMock = {
    cfg: {
      success: true, currency: 'USD', rates: { CNY: 1, USD: 7.2 } as Record<string, number>,
      supported_currencies: SUPPORTED, env_overridden: {} as Record<string, string>,
    },
    saves: [] as Array<{ currency: string; rates: Record<string, number> }>,
  }
  return { SUPPORTED, quoteMock }
})
// 取回 hoisted 固件：SUPPORTED 为「开放态」白名单基准；②改 cfg.success=false 模拟回显失败、
// ⑥改 supported_currencies=['CNY'] 模拟关闭封存，均就地改 quoteMock.cfg
const { SUPPORTED, quoteMock } = H

// planMock：租户视角商店卡消费的 /api/plans 行（含 #75 报价三字段）
const planMock = vi.hoisted(() => ({
  plans: [] as Array<Record<string, unknown>>,
  myPkg: {} as Record<string, unknown>,
}))

vi.mock('@/api', () => ({
  billingQuota: vi.fn(async () => ({ success: true, qps: 10, concurrent: 3, max_daily_chars: 0 })),
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
  plans: vi.fn(async () => ({ success: true, plans: planMock.plans })),
  myPackage: vi.fn(async () => ({ success: true, ...planMock.myPkg })),
  packageSubscribe: vi.fn(async () => ({ success: true })),
  packageUpgrade: vi.fn(async () => ({ success: true })),
  autoRenewSet: vi.fn(async () => ({ success: true })),
  couponPreview: vi.fn(async () => ({ success: true })),
  adminPackages: vi.fn(async () => ({ success: true, packages: [] })),
  adminPackageCreate: vi.fn(async () => ({ success: true })),
  adminPackageUpdate: vi.fn(async () => ({ success: true })),
  adminPackageDelete: vi.fn(async () => ({ success: true })),
  adminPackageSettings: vi.fn(async () => ({ success: true, free_trial_points: 1000, usdt_enabled: '0' })),
  adminPackageSettingsSave: vi.fn(async () => ({ success: true })),
  adminQRUpload: vi.fn(async () => ({ success: true })),
  adminPayChannels: vi.fn(async () => ({ success: true, fields: {}, env_overridden: {} })),
  adminPayChannelsSave: vi.fn(async () => ({ success: true })),
  PAY_CH_FIELDS: ['paych_notify_base', 'paych_wechat_enabled', 'paych_alipay_enabled'],
  adminQuoteCurrency: vi.fn(async () => quoteMock.cfg),
  adminQuoteCurrencySave: vi.fn(async (data: { currency: string; rates: Record<string, number> }) => {
    quoteMock.saves.push(data)
    return { success: true, currency: data.currency, rates: { CNY: 1, ...data.rates } }
  }),
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  API_BASE: '',
}))

// adminMock：超管位开关，⑤置 false 走「租户视角商店卡」场景（报价展示不依赖超管权限）
const adminMock = vi.hoisted(() => ({ isSuper: true }))
vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: adminMock.isSuper }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

// quoteSaveButton 找到「多币种报价」区块标题所在行的保存按钮：
// 运营配置面板里同名按钮（计费参数/支付渠道/USDT）不止一枚，只认标题同一行的那枚。
function quoteSaveButton(): HTMLButtonElement | undefined {
  return Array.from(document.querySelectorAll('button'))
    .find((b) => (b.parentElement?.textContent || '').includes('多币种报价'))
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  adminMock.isSuper = true
  quoteMock.cfg = {
    success: true, currency: 'USD', rates: { CNY: 1, USD: 7.2 },
    supported_currencies: SUPPORTED, env_overridden: {},
  }
  quoteMock.saves.length = 0
  planMock.plans = []
  planMock.myPkg = {}
})

describe('PlansP · 多币种报价配置（#75）', () => {
  it('① 回显：币种下拉选中 USD，倍率输入 11 项（白名单去 CNY），USD 格已填 7.2、EUR 格空', async () => {
    const { container } = render(<PlansP />)
    await vi.waitFor(() => {
      expect(container.textContent).toContain('多币种报价（仅展示口径）')
    })
    // 币种下拉取区块内第一个 select 不可靠（面板上方还有其它 select）——按 option 集合反查；
    // waitFor 到 CHF 选项出现为止：标题是静态渲染，配置回显是异步，只等标题会抢到空表单。
    let quoteSel: HTMLSelectElement | undefined
    await vi.waitFor(() => {
      quoteSel = Array.from(container.querySelectorAll('select'))
        .find((s) => Array.from(s.options).some((o) => o.value === 'CHF'))
      expect(quoteSel).toBeTruthy()
    })
    expect(quoteSel!.value).toBe('USD')
    const rateInputs = Array.from(container.querySelectorAll('input[step="0.0001"]')) as HTMLInputElement[]
    expect(rateInputs.length).toBe(11) // 12 白名单币种 - CNY（基准恒 1 不可配）
    expect(rateInputs[0].value).toBe('7.2') // 第一个非 CNY 币种 = USD
    expect(rateInputs[1].value).toBe('')    // EUR 未配置
  })

  it('② GET 回显失败：区块整体不渲染（拿不到白名单就没有可编辑表单，更不存在空白表单直提清空线上汇率）', async () => {
    quoteMock.cfg = { success: false, message: '仅平台超管可配置报价币种与汇率' } as never
    const { container } = render(<PlansP />)
    // 锚点用同面板静态渲染的 USDT 区块：面板已出、报价区块仍无 ⇒ 确系回显失败被隐藏，
    // 而不是挂载竞态（等标题本身会永远等不到，等异步数据块又会误判）。
    await vi.waitFor(() => {
      expect(container.textContent).toContain('USDT')
    })
    expect(container.textContent).not.toContain('多币种报价')
  })

  it('③ env 接管：币种/倍率置灰并标出环境变量名', async () => {
    quoteMock.cfg.env_overridden = { quote_currency: 'QUOTE_CURRENCY', fx_rates: 'FX_RATES' }
    const { container } = render(<PlansP />)
    await vi.waitFor(() => {
      expect(container.textContent).toContain('QUOTE_CURRENCY')
    })
    expect(container.textContent).toContain('FX_RATES')
    const rateInputs = Array.from(container.querySelectorAll('input[step="0.0001"]')) as HTMLInputElement[]
    expect(rateInputs.every((i) => i.disabled)).toBe(true)
    const quoteSel = Array.from(container.querySelectorAll('select'))
      .find((s) => Array.from(s.options).some((o) => o.value === 'CHF'))
    expect(quoteSel!.disabled).toBe(true)
  })

  it('④ 保存载荷：换币种 EUR + 补倍率 → 提交 {currency:EUR, rates:{USD,EUR} 数字型、无 CNY}；缺倍率时前端拦截', async () => {
    quoteMock.cfg = { ...quoteMock.cfg, rates: { CNY: 1, USD: 7.2 } }
    const { container } = render(<PlansP />)
    let quoteSel: HTMLSelectElement | undefined
    await vi.waitFor(() => {
      quoteSel = Array.from(container.querySelectorAll('select'))
        .find((s) => Array.from(s.options).some((o) => o.value === 'CHF'))
      expect(quoteSel).toBeTruthy()
    })
    const rateInputs = Array.from(container.querySelectorAll('input[step="0.0001"]')) as HTMLInputElement[]
    // 先把币种切到 EUR 但 EUR 倍率留空 → 点保存必须被前端拦下（不发请求）
    fireEvent.change(quoteSel!, { target: { value: 'EUR' } })
    fireEvent.click(quoteSaveButton()!)
    expect(quoteMock.saves.length).toBe(0)
    // 补上 EUR 倍率再存：整表覆盖提交，字符串→数字，CNY 不进载荷
    fireEvent.change(rateInputs[1], { target: { value: '7.8' } })
    fireEvent.click(quoteSaveButton()!)
    expect(quoteMock.saves.length).toBe(1)
    expect(quoteMock.saves[0]).toEqual({ currency: 'EUR', rates: { USD: 7.2, EUR: 7.8 } })
  })

  it('⑤ 租户视角商店卡：USD 报价渲染 $3.00 主价 + ≈ ¥21.6 辅助行；CNY 报价保持 ¥ 老口径', async () => {
    adminMock.isSuper = false
    planMock.plans = [
      { id: 1, code: 'pro', name: '专业包', ptype: 'paid', points: 50000, sentences: 0, price_money: 21.6, duration_days: 30, price_display: 3, quote_currency: 'USD' },
    ]
    planMock.myPkg = { points_balance: 100, package_code: '' }
    const { container } = render(<PlansP />)
    await vi.waitFor(() => {
      expect(container.textContent).toContain('$3.00')
    })
    expect(container.textContent).toContain('≈ ¥21.6')

    cleanup()
    planMock.plans = [
      { id: 1, code: 'pro', name: '专业包', ptype: 'paid', points: 50000, sentences: 0, price_money: 21.6, duration_days: 30, price_display: 21.6, quote_currency: 'CNY' },
    ]
    const { container: c2 } = render(<PlansP />)
    await vi.waitFor(() => {
      expect(c2.textContent).toContain('¥21.6')
    })
    expect(c2.textContent).not.toContain('≈ ¥')
  })

  it('⑥ 关闭封存态：白名单只回 CNY 时报价区块整体不渲染（★ 2026-09-22 决策的界面级锁）', async () => {
    // 生产关闭态下 GET 恒回 {currency:'CNY', supported_currencies:['CNY'], feature_open:false}
    quoteMock.cfg = {
      success: true, currency: 'CNY', rates: { CNY: 1 },
      supported_currencies: ['CNY'], env_overridden: {},
    }
    const { container } = render(<PlansP />)
    await vi.waitFor(() => {
      expect(container.textContent).toContain('USDT')
    })
    expect(container.textContent).not.toContain('多币种报价')
    expect(container.querySelectorAll('input[step="0.0001"]').length).toBe(0)
  })
})
