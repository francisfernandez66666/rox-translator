// ============================================================================
// PlansP.coupon.dom.test.tsx — 收银台优惠券交互测试（★ #41 商业洞三，2026-09-21）
// 钉死三条「前端一旦自作主张就会白拿折扣」的口径：
//   ① 未填券码时试算按钮禁用，试算永远由服务端按下单同一算法回价（前端不自算）；
//   ② 券码提交前转大写，与后端 NormalizeCouponCode 同口径；
//   ③ 建单成功后券码与试算一并清空——同一张限量券不能被一次双击核销两遍。
// 运行：npx vitest run src/components/admin/PlansP.coupon.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'

// 三个被 mock 的接口函数（券预览/下单/订阅），hoisted 供 vi.mock 工厂闭包引用
const mocks = vi.hoisted(() => ({
  couponPreview: vi.fn(),
  payCreate: vi.fn(),
  packageSubscribe: vi.fn(),
}))

// '@/api' 全量 mock：只提供租户视角收银台会用到的接口
vi.mock('@/api', () => ({
  billingQuota: vi.fn(async () => ({ success: true, qps: 10, concurrent: 3, max_daily_chars: 0 })),
  billingQuotaSave: vi.fn(async () => ({ success: true })),
  billingOrders: vi.fn(async () => ({ success: true, orders: [] })),
  billingInvoices: vi.fn(async () => ({ success: true, invoices: [] })),
  billingInvoiceCreate: vi.fn(async () => ({ success: true })),
  billingInvoiceVoid: vi.fn(async () => ({ success: true })),
  adminOrderRefund: vi.fn(async () => ({ success: true })),
  payCreate: mocks.payCreate,
  payStatus: vi.fn(async () => ({ success: true, order: { order_no: 'T1-CPO', status: 'pending' } })),
  paySimulate: vi.fn(async () => ({ success: true })),
  payManualConfirm: vi.fn(async () => ({ success: true })),
  manualConfirmOrders: vi.fn(async () => ({ success: true, orders: [] })),
  plans: vi.fn(async () => ({ success: true, plans: [] })),
  myPackage: vi.fn(async () => ({
    success: true, points_balance: 1000, points_grants_left: 0, points_permanent_balance: 0,
    points_used_month: 0, package_code: 'pro', auto_renew: 0, balance_sentences_approx: 100,
  })),
  packageSubscribe: mocks.packageSubscribe,
  packageUpgrade: vi.fn(async () => ({ success: true })),
  autoRenewSet: vi.fn(async () => ({ success: true })),
  couponPreview: mocks.couponPreview,
  adminPackages: vi.fn(async () => ({ success: true, packages: [] })),
  adminPackageCreate: vi.fn(async () => ({ success: true })),
  adminPackageUpdate: vi.fn(async () => ({ success: true })),
  adminPackageDelete: vi.fn(async () => ({ success: true })),
  adminPackageSettings: vi.fn(async () => ({ success: true, free_trial_points: 1000, usdt_enabled: '0' })),
  adminPackageSettingsSave: vi.fn(async () => ({ success: true })),
  adminQRUpload: vi.fn(async () => ({ success: true })),
  // ★ 2026-09-22：PlansP 顶部具名导入需存在（本用例是租户视角，不会真的调用）
  adminPayChannels: vi.fn(async () => ({ success: true, fields: {}, env_overridden: {} })),
  adminPayChannelsSave: vi.fn(async () => ({ success: true })),
  PAY_CH_FIELDS: ['paych_notify_base', 'paych_wechat_enabled', 'paych_wechat_app_id', 'paych_alipay_enabled'],
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  API_BASE: '',
}))

// 租户视角（isSuper=false）才渲染「套餐订阅 / 充值」两块
vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: false }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mocks.couponPreview.mockResolvedValue({ success: true, kind: 'recharge', origin_money: 100, discount_money: 10, pay_money: 90 })
  mocks.payCreate.mockResolvedValue({ success: true, order: { id: 5, order_no: 'T5-CPO', status: 'pending', channel: 'mock', amount_points: 1000, amount_money: 90 } })
  mocks.packageSubscribe.mockResolvedValue({ success: true, order: { id: 6, order_no: 'T6-CPO', status: 'pending', channel: 'mock' } })
})

describe('收银台 · 优惠券试算与下单（#41）', () => {
  it('未填券码不能试算；填了小写券码后按大写 + 当前积数请服务端回价', async () => {
    render(<PlansP />)
    await vi.waitFor(() => { expect(screen.getByText('试算优惠')).toBeTruthy() })
    const btn = screen.getByText('试算优惠')
    expect((btn as HTMLButtonElement).disabled).toBe(true)

    const codeInput = screen.getByLabelText('优惠券码') as HTMLInputElement
    fireEvent.change(codeInput, { target: { value: 'newyear2026' } })
    expect(codeInput.value).toBe('NEWYEAR2026')
    expect((screen.getByText('试算优惠') as HTMLButtonElement).disabled).toBe(false)

    fireEvent.click(screen.getByText('试算优惠'))
    await vi.waitFor(() => { expect(mocks.couponPreview).toHaveBeenCalledWith({ code: 'NEWYEAR2026', points: 3000 }) })
    // 折让与实付都取服务端数字，前端只做展示（插值会把文本切成多节点，故整体比对 textContent）
    await vi.waitFor(() => {
      expect(document.body.textContent).toContain('本单可优惠 10.00 元，实付 90.00 元')
    })
  })

  it('带券下单：coupon 随 payCreate 提交，建单成功后券码与试算一并清空（防重复核销）', async () => {
    render(<PlansP />)
    await vi.waitFor(() => { expect(screen.getByText('试算优惠')).toBeTruthy() })
    fireEvent.change(screen.getByLabelText('优惠券码'), { target: { value: 'NEWYEAR2026' } })
    fireEvent.click(screen.getByText('去支付'))
    await vi.waitFor(() => { expect(mocks.payCreate).toHaveBeenCalledWith({ points: 3000, channel: '', coupon: 'NEWYEAR2026' }) })
    // 收款台已弹出，券码输入框回到空、试算行消失
    await vi.waitFor(() => {
      expect((screen.getByLabelText('优惠券码') as HTMLInputElement).value).toBe('')
      expect(screen.queryByText(/本单可优惠/)).toBeNull()
    })
  })

  it('试算失败（券不适用）只提示、不留半成品报价', async () => {
    mocks.couponPreview.mockResolvedValue({ success: false, message: '该券仅适用于订阅订单' })
    render(<PlansP />)
    await vi.waitFor(() => { expect(screen.getByText('试算优惠')).toBeTruthy() })
    fireEvent.change(screen.getByLabelText('优惠券码'), { target: { value: 'ONLYSUB' } })
    fireEvent.click(screen.getByText('试算优惠'))
    await vi.waitFor(() => { expect(mocks.couponPreview).toHaveBeenCalled() })
    expect(screen.queryByText(/本单可优惠/)).toBeNull()
  })
})
