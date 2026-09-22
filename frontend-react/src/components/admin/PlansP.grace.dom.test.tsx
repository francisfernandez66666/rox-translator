// ============================================================================
// PlansP.grace.dom.test.tsx — 订阅续费宽限期提示条测试（★ #74 前端切片，2026-09-23）
// 钉死两条口径（数据源 /api/me/package 的 in_grace/grace_expires）：
//   ① in_grace=true 时渲染「已到期 · 宽限期至 {本地日期}」提示条，且日期按本地时区
//     格式化为 YYYY-MM-DD（与组件内 toLocalDate 同法，UTC 切片会在时区边界错一天）；
//   ② in_grace=false / 字段缺省时整条提示不渲染（未到期用户不该被吓）。
// 语言：vitest.setup.ts 已预置 app_lang=zh，中文断言直接成立，本用例不改语言。
// 运行：npx vitest run src/components/admin/PlansP.grace.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { PlansP } from './PlansP'

// myPackage 返回值按用例切换（hoisted：vi.mock 工厂只能引用提升变量）
const pkgMock = vi.hoisted(() => ({ value: {} as Record<string, unknown> }))

// '@/api' 全量 mock：只提供租户视角「当前套餐」区会用到的接口（同 PlansP.coupon.dom.test 口径）
vi.mock('@/api', () => ({
  billingQuota: vi.fn(async () => ({ success: true, qps: 10, concurrent: 3, max_daily_chars: 0 })),
  billingQuotaSave: vi.fn(async () => ({ success: true })),
  billingOrders: vi.fn(async () => ({ success: true, orders: [] })),
  billingInvoices: vi.fn(async () => ({ success: true, invoices: [] })),
  billingInvoiceCreate: vi.fn(async () => ({ success: true })),
  billingInvoiceVoid: vi.fn(async () => ({ success: true })),
  adminOrderRefund: vi.fn(async () => ({ success: true })),
  payCreate: vi.fn(async () => ({ success: true })),
  payStatus: vi.fn(async () => ({ success: true })),
  paySimulate: vi.fn(async () => ({ success: true })),
  payManualConfirm: vi.fn(async () => ({ success: true })),
  manualConfirmOrders: vi.fn(async () => ({ success: true, orders: [] })),
  plans: vi.fn(async () => ({ success: true, plans: [] })),
  myPackage: vi.fn(async () => ({ success: true, ...pkgMock.value })),
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
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  API_BASE: '',
}))

// 租户视角（isSuper=false）才渲染「当前套餐」概览区——宽限期提示条就挂在那里
vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: false }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

// localDate RFC3339 → 本地 YYYY-MM-DD：与 PlansP 的 toLocalDate 同一算法，
// 断言侧独立复算一遍（不 import 组件私有函数），钉住「按本地时区取日」而非 UTC 切片。
function localDate(rfc: string): string {
  const d = new Date(rfc)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
})

describe('PlansP · 订阅续费宽限期提示（#74）', () => {
  it('in_grace=true：渲染「已到期 · 宽限期至 {本地日期}」与说明文案', async () => {
    // 正午 UTC 取值：全球主要时区（±12h 以内）本地日期都落在 2026-10-05，断言不受跑测时区影响
    const graceExpires = '2026-10-05T12:00:00Z'
    pkgMock.value = {
      points_balance: 500, points_grants_left: 100, points_permanent_balance: 400, points_used_month: 60,
      package_code: 'pro', package_expires: '2026-10-01T00:00:00Z', auto_renew: true,
      balance_sentences_approx: 100, in_grace: true, grace_expires: graceExpires,
    }
    render(<PlansP />)
    await vi.waitFor(() => {
      const text = document.body.textContent || ''
      expect(text).toContain('已到期 · 宽限期至 ' + localDate(graceExpires))
      expect(text).toContain('期间订阅身份与剩余额度正常使用，续费到账即无缝续期')
      expect(text).toContain('逾期仍未到账将自动移除订阅身份')
    })
  })

  it('in_grace=false 或字段缺省：不渲染宽限期提示', async () => {
    pkgMock.value = {
      points_balance: 500, points_grants_left: 100, points_permanent_balance: 400, points_used_month: 60,
      package_code: 'pro', package_expires: '2026-12-01T00:00:00Z', auto_renew: true,
      balance_sentences_approx: 100, in_grace: false, grace_expires: '',
    }
    const { container } = render(<PlansP />)
    await vi.waitFor(() => { expect(container.textContent).toContain('自动续费') })
    expect(container.textContent).not.toContain('宽限期')

    cleanup()
    pkgMock.value = { points_balance: 500, package_code: 'pro', package_expires: '2026-12-01T00:00:00Z' }
    const { container: c2 } = render(<PlansP />)
    await vi.waitFor(() => { expect(c2.textContent).toContain('自动续费') })
    expect(c2.textContent).not.toContain('宽限期')
  })
})
