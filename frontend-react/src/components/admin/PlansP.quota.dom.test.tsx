// ============================================================================
// PlansP.quota.dom.test.tsx — 租户配额表单读写同源测试（★ 2026-09-26 批 I-3·F-55）
// ----------------------------------------------------------------------------
// 钉住的是什么（本轮云端实跑抓到的运维高危，界面一次点击就能拆掉租户的日用量墙）：
//   ① 读侧回带的四个值必须原样进提交体（屏上=库里，逐键等值）；
//   ② 后端**没回带** max_daily_points 时，提交体**不得含该键**——
//      旧写法 Math.max(0, Number(undefined) || 0) 把「缺失」静默变成「填了 0」，
//      而后端 0 的运行时语义是「不限」（billing/quota.go: maxDaily<=0 即放行），
//      一次什么都没动的点击实际把积分墙清掉了；
//   ③ 超管停在平台上下文（tenant_selected=false，读写的是不存在的租户 0）→ 保存钮禁用；
//   ④ GET 失败（ready=false）→ 保存钮禁用（旧行为：空表单也能保存＝拿默认值覆盖真值）；
//   ⑤ 后端回带 unlimited 语义位时，界面显式标注「不限」，0 不再长得像空值。
// 语言：vitest.setup.ts 预置 app_lang=zh，中文断言直接成立。
// 运行：npx vitest run src/components/admin/PlansP.quota.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { PlansP } from './PlansP'

// quotaMock.get = /api/admin/quota 的 GET 回包（各用例就地改），saves = 提交体采集
const Q = vi.hoisted(() => ({
  get: {
    success: true, qps: 10, concurrent: 3, max_daily_chars: 100000, max_daily_points: 20000,
    tenant_selected: true, unlimited: { max_daily_chars: false, max_daily_points: false },
  } as Record<string, unknown>,
  saves: [] as Array<Record<string, unknown>>,
}))

vi.mock('@/api', () => ({
  billingQuota: vi.fn(async () => Q.get),
  billingQuotaSave: vi.fn(async (data: Record<string, unknown>) => { Q.saves.push(data); return { success: true } }),
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
  adminPackageSettings: vi.fn(async () => ({ success: true, free_trial_points: 1000, usdt_enabled: '0' })),
  adminPackageSettingsSave: vi.fn(async () => ({ success: true })),
  adminQRUpload: vi.fn(async () => ({ success: true })),
  adminPayChannels: vi.fn(async () => ({ success: true, fields: {}, env_overridden: {} })),
  adminPayChannelsSave: vi.fn(async () => ({ success: true })),
  PAY_CH_FIELDS: ['paych_notify_base', 'paych_wechat_enabled', 'paych_alipay_enabled'],
  adminQuoteCurrency: vi.fn(async () => ({
    success: true, currency: 'CNY', rates: { CNY: 1 }, supported_currencies: ['CNY'], env_overridden: {},
  })),
  adminQuoteCurrencySave: vi.fn(async () => ({ success: true })),
  request: vi.fn(async () => ({ success: true })),
  authHeaders: vi.fn(() => ({})),
  API_BASE: '',
}))

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: true, activeTenantId: 3 }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

// saveBtn 取「保存配额」钮（本页唯一），并用 waitFor 等 GET 回包落定（异步回填 meta 后才决定禁用态）
async function readySaveBtn(): Promise<HTMLButtonElement> {
  let btn: HTMLButtonElement | undefined
  await vi.waitFor(() => {
    btn = Array.from(document.querySelectorAll('button')).find((b) => b.textContent === '保存配额')
    expect(btn).toBeTruthy()
  })
  return btn!
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  Q.get = {
    success: true, qps: 10, concurrent: 3, max_daily_chars: 100000, max_daily_points: 20000,
    tenant_selected: true, unlimited: { max_daily_chars: false, max_daily_points: false },
  }
  Q.saves.length = 0
})

describe('PlansP · 租户配额表单读写同源（F-55）', () => {
  it('① 回带四值齐全：提交体逐键等于屏上读到的值', async () => {
    render(<PlansP />)
    const btn = await readySaveBtn()
    await vi.waitFor(() => { expect(btn.disabled).toBe(false) })
    fireEvent.click(btn)
    await vi.waitFor(() => { expect(Q.saves.length).toBe(1) })
    expect(Q.saves[0]).toEqual({ qps: 10, concurrent: 3, max_daily_chars: 100000, max_daily_points: 20000 })
  })

  it('② 后端未回带 max_daily_points：提交体不得含该键（旧写法会静默送 0＝拆积分墙）', async () => {
    delete Q.get.max_daily_points
    render(<PlansP />)
    const btn = await readySaveBtn()
    await vi.waitFor(() => { expect(btn.disabled).toBe(false) })
    fireEvent.click(btn)
    await vi.waitFor(() => { expect(Q.saves.length).toBe(1) })
    expect('max_daily_points' in Q.saves[0]).toBe(false)
    // 反向负向锁：同一次提交里也不许出现"积分墙被写成 0"的形态
    expect(Q.saves[0]).not.toMatchObject({ max_daily_points: 0 })
    // 字符墙仍按屏上值提交（缺失只影响积分那一键，不能顺手清 0）
    expect(Q.saves[0]).toMatchObject({ max_daily_chars: 100000 })
  })

  it('③ 超管未选租户（tenant_selected=false）：保存钮禁用且点击不发请求', async () => {
    Q.get.tenant_selected = false
    render(<PlansP />)
    const btn = await readySaveBtn()
    await vi.waitFor(() => { expect(btn.disabled).toBe(true) })
    expect(document.body.textContent).toContain('请先在右上角租户切换器选择具体租户')
    fireEvent.click(btn)
    await new Promise((r) => setTimeout(r, 30))
    expect(Q.saves.length).toBe(0)
  })

  it('④ GET 失败：保存钮禁用（旧行为＝空表单也能保存，拿默认值覆盖线上配额）', async () => {
    Q.get = { success: false, message: 'boom' }
    render(<PlansP />)
    const btn = await readySaveBtn()
    await vi.waitFor(() => { expect(btn.disabled).toBe(true) })
    expect(document.body.textContent).toContain('配额尚未读取成功')
    fireEvent.click(btn)
    await new Promise((r) => setTimeout(r, 30))
    expect(Q.saves.length).toBe(0)
  })

  it('⑤ unlimited 语义位回带时显式标注「不限」（0 不再长得像空值）', async () => {
    // 字符墙有值、积分墙为 0（＝不限）→ 积分那一格旁有标注、字符那一格旁没有。
    // 按「输入框的父容器」定位，不用全文计数：本页其它区块（套餐表等）本来就有「不限」字样，
    // 全文计数会把它们算进来（首版就因此测到 4 枚），锁的是配额格却不是配额。
    Q.get.unlimited = { max_daily_chars: false, max_daily_points: true }
    Q.get.max_daily_points = 0
    render(<PlansP />)
    const byPlaceholder = (ph: string): HTMLInputElement | undefined =>
      Array.from(document.querySelectorAll('input')).find((i) => (i as HTMLInputElement).placeholder === ph) as HTMLInputElement | undefined
    await vi.waitFor(() => {
      expect(byPlaceholder('每日积分上限')).toBeTruthy()
      expect(document.body.textContent).toContain('0＝不限：保存后该租户此项日用量墙将被移除')
    })
    expect(byPlaceholder('每日积分上限')!.parentElement!.textContent).toContain('不限')
    expect(byPlaceholder('每日字符上限')!.parentElement!.textContent).not.toContain('不限')
  })
})
