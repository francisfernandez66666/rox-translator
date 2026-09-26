// ============================================================================
// components/admin/PlansP.paymode.dom.test.tsx — mock 支付模式保存必须二次确认
// （★ O-1 三层处置的前端锁，2026-09-27 〇-U 收尾批）
//
// 因果链：超管「支付模式」单选一直含 mock（研发自助验收必需入口，批 I-10 决定不删）。
// 风险形态是**发布后误切**：下拉一抖、保存一点，全站收银台立刻出现「模拟支付」，
// 任何人零成本开通套餐。旧写法 savePayMode 直接落库、无任何拦截。
// 现三层处置：① 后端审计 ⚠ 硬账（Go 锁＝o1_mock_audit_test.go）；
// ② 本文件——前端 mock 档保存前 confirmDialog 显式二次确认，取消即不落库；
// ③ 发布红线两站 pay_mode 非 mock（部署指南 §十，人工）。
//
// 锁法（三断言两对照）：
//  ① 选 mock → 点保存：确认弹窗文案在场（等值取词典 packages.confirmMockPayMode 现读值），
//     且**弹窗未决时保存接口零调用**——防止「弹窗照弹、保存照发」的假拦截；
//  ② 点确认后才发生恰好 1 次保存调用，载荷 pay_mode=mock；
//  ③ 反向对照 A：选 mock 后点「取消」→ 保存接口零调用（误点可撤）；
//  ④ 反向对照 B：选 static_qr（非 mock）保存 → **不得**弹确认（防把守卫扩成全员骚扰）。
//
// 运行：npx vitest run src/components/admin/PlansP.paymode.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'
// confirmDialog 需要 <DialogHost/> 宿主（与 main.tsx / BrandTermsP.dom.test 同挂载方式）
import { DialogHost } from '@/components/uiDialogs'
// 期望文案现读词典：mock 选项名、确认文案、通用钮——改文案即改期望，不写死字面量
import { zh as pkgZh } from '@/i18n/panels/packages'
import { baseZh } from '@/i18n/dicts.zh'

// S = 可控状态：保存调用采集；初始 pay_mode 由用例经 Q.initPayMode 注入桩回包。
const S = vi.hoisted(() => ({
  saves: [] as Array<Record<string, unknown>>,
  initPayMode: 'static_qr',
}))

// @/api 整模块桩（沿用 PlansP.invoice.dom.test.tsx 已验证可行的桩集合）
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
  adminPackageSettings: vi.fn(async () => ({
    success: true, free_trial_points: 1000, usdt_enabled: '0', pay_mode: S.initPayMode,
  })),
  adminPackageSettingsSave: vi.fn(async (data: Record<string, unknown>) => { S.saves.push(data); return { success: true } }),
  adminQRUpload: vi.fn(async () => ({ success: true })),
  adminPayChannels: vi.fn(async () => ({ success: true, fields: {}, env_overridden: {} })),
  adminPayChannelsSave: vi.fn(async () => ({ success: true })),
  PAY_CH_FIELDS: [],
  adminQuoteCurrency: vi.fn(async () => ({ success: true, currency: 'CNY', rates: { CNY: 1 }, supported_currencies: ['CNY'], env_overridden: {} })),
  adminQuoteCurrencySave: vi.fn(async () => ({ success: true })),
  adminGrowthFunnel: vi.fn(async () => ({ success: true, rows: [] })),
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  handleUnauthorized: vi.fn(),
  API_BASE: '',
}))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true, activeTenantId: 3 }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

const $ = (sel: string) => Array.from(document.querySelectorAll(sel)) as HTMLElement[]

// payModeRow 取「支付模式」下拉所在行（select 与其保存钮同容器，避免误点其他区块的保存钮）。
function payModeSelect(): HTMLSelectElement {
  const sel = $('select').find((s) => Array.from((s as HTMLSelectElement).options).some((o) => o.value === 'mock'))
  if (!sel) throw new Error('未找到含 mock 选项的支付模式下拉（PlansP 结构变了？锁需随迁）')
  return sel as HTMLSelectElement
}
function payModeSaveButton(): HTMLButtonElement {
  const row = payModeSelect().parentElement!
  const btn = Array.from(row.querySelectorAll('button')).find((b) => (b.textContent ?? '').trim() === baseZh['common.save'])
  if (!btn) throw new Error('支付模式行内未找到保存钮')
  return btn
}
function dialogButton(key: 'common.ok' | 'common.cancel'): HTMLButtonElement | undefined {
  // 弹窗 portal 挂 body 末尾：取**最后一个**同名钮，防背景面板同名钮干扰
  return ($('button') as HTMLButtonElement[]).filter((b) => (b.textContent ?? '').trim() === baseZh[key]).pop()
}

async function mountAndSettle() {
  render(<><PlansP /><DialogHost /></>)
  await vi.waitFor(() => expect(payModeSelect()).toBeTruthy())
}

beforeEach(() => {
  cleanup()
  S.saves.length = 0
  S.initPayMode = 'static_qr'
})

describe('PlansP · O-1 mock 支付模式保存二次确认', () => {
  it('① 选 mock 点保存：先弹词典文案确认框，弹窗未决时保存接口零调用', async () => {
    await mountAndSettle()
    fireEvent.change(payModeSelect(), { target: { value: 'mock' } })
    fireEvent.click(payModeSaveButton())
    await vi.waitFor(() => expect(document.body.textContent).toContain(pkgZh['packages.confirmMockPayMode']))
    // ★ 假拦截判据：弹了框但背后已经把 pay_mode=mock 发出去
    expect(S.saves.length, '确认框未决时不得调用保存接口').toBe(0)
  })

  it('② 点确认：恰好 1 次保存且载荷 pay_mode=mock', async () => {
    await mountAndSettle()
    fireEvent.change(payModeSelect(), { target: { value: 'mock' } })
    fireEvent.click(payModeSaveButton())
    await vi.waitFor(() => expect(document.body.textContent).toContain(pkgZh['packages.confirmMockPayMode']))
    fireEvent.click(dialogButton('common.ok')!)
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0]).toEqual({ pay_mode: 'mock' })
  })

  it('③ 反向对照 A：点取消 → 保存接口零调用（误点可撤）', async () => {
    await mountAndSettle()
    fireEvent.change(payModeSelect(), { target: { value: 'mock' } })
    fireEvent.click(payModeSaveButton())
    await vi.waitFor(() => expect(document.body.textContent).toContain(pkgZh['packages.confirmMockPayMode']))
    fireEvent.click(dialogButton('common.cancel')!)
    await new Promise((r) => setTimeout(r, 50))
    expect(S.saves.length, '取消后不得落库').toBe(0)
  })

  it('④ 反向对照 B：非 mock（static_qr）保存不弹确认，直接落库', async () => {
    S.initPayMode = 'sdk' // 初始 sdk → 目标 static_qr：变更但非 mock
    await mountAndSettle()
    fireEvent.change(payModeSelect(), { target: { value: 'static_qr' } })
    fireEvent.click(payModeSaveButton())
    await vi.waitFor(() => expect(S.saves.length).toBe(1))
    expect(S.saves[0]).toEqual({ pay_mode: 'static_qr' })
    // 给足渲染余量后确认框文案仍不得出现（防把守卫扩成全员骚扰）
    await new Promise((r) => setTimeout(r, 50))
    expect(document.body.textContent).not.toContain(pkgZh['packages.confirmMockPayMode'])
  })
})
