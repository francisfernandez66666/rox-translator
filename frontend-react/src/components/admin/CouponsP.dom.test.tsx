// ============================================================================
// CouponsP.dom.test.tsx — 优惠券管理面板组件测试（★ #41 商业洞三，2026-09-21）
// 锁定四条改坏就直接影响资金/对账口径的行为：
//   ① 列表按「只减钱不减货」口径回显力度、配额与累计优惠，且界面无 token 裸值；
//   ② 券码输入即转大写（与后端 NormalizeCouponCode 同口径）、力度为 0 时拦下不发请求；
//   ③ 有效期提交必须落到 RFC3339 UTC——datetime-local 的本地值原样发给后端会差一个时区；
//   ④ 停用走同一次 save（enabled=0）不删历史；删除需二次确认；核销流水按券 ID 过滤拉取。
// 运行：npx vitest run src/components/admin/CouponsP.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import CouponsP from './CouponsP'
import { DialogHost } from '@/components/uiDialogs'
import { toastWarn } from '@/lib/toastBus'

const mocks = vi.hoisted(() => ({
  adminCoupons: vi.fn(),
  adminCouponSave: vi.fn(),
  adminCouponDelete: vi.fn(),
  adminCouponRedemptions: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminCoupons: mocks.adminCoupons,
  adminCouponSave: mocks.adminCouponSave,
  adminCouponDelete: mocks.adminCouponDelete,
  adminCouponRedemptions: mocks.adminCouponRedemptions,
}))

// toast 需要 <ToastBridge/> 才落 DOM，单测里直接断言调用即可
vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}))

/** 一条限量 100 张、已核销 3 张、累计优惠 30 元的按比例券（有折让上限与门槛） */
const percentCoupon = {
  id: 1, code: 'NEWYEAR2026', name: '新年九折', kind: 'recharge', discount_type: 'percent',
  discount_value: 10, max_discount: 50, min_amount: 100, max_uses: 100, used_count: 3,
  per_tenant_limit: 1, valid_from: '', valid_until: '', enabled: 1, note: '', remaining: 97,
  total_discount: 30,
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mocks.adminCoupons.mockResolvedValue({ success: true, coupons: [percentCoupon] })
  mocks.adminCouponSave.mockResolvedValue({ success: true })
  mocks.adminCouponDelete.mockResolvedValue({ success: true })
  mocks.adminCouponRedemptions.mockResolvedValue({ success: true, redemptions: [{
    id: 9, code: 'NEWYEAR2026', tenant_id: 12, order_id: 70, order_no: 'T70-CPX',
    origin_money: 100, discount_money: 10, paid_money: 90, created_at: '2026-09-21T00:00:00Z',
  }] })
})

describe('优惠券管理 · 列表口径（#41）', () => {
  it('回显力度/配额/累计优惠，不出现 token 裸值', async () => {
    const { container } = render(<CouponsP />)
    await vi.waitFor(() => { expect(screen.getByText('NEWYEAR2026')).toBeTruthy() })
    expect(screen.getByText('新年九折')).toBeTruthy()
    expect(screen.getByText('仅充值单')).toBeTruthy()
    expect(screen.getByText('10% ≤ 50.00 元')).toBeTruthy()
    expect(screen.getByText('100.00 元')).toBeTruthy()
    expect(screen.getByText('3 / 100（剩余 97）')).toBeTruthy()
    expect(screen.getByText('30.00')).toBeTruthy()
    expect(container.textContent).not.toMatch(/token/i)
  })
})

/** 弹窗由 Dialog 挂到 document.body（portal），查询输入框必须走整份文档而不是 container */
const input = (sel: string) => document.querySelector(sel) as HTMLInputElement

/** 弹窗主按钮（确认位） */
const dialogConfirm = () => document.querySelector('.lc-dialog .lc-btn--primary') as HTMLElement

describe('优惠券管理 · 新建与编辑（#41）', () => {
  it('券码输入即转大写；力度为 0 时只提示、不发请求', async () => {
    render(<><CouponsP /><DialogHost /></>)
    await vi.waitFor(() => { expect(mocks.adminCoupons).toHaveBeenCalled() })
    fireEvent.click(screen.getByText('新建优惠券'))

    const codeInput = input('input[placeholder*="NEWYEAR"]')
    fireEvent.change(codeInput, { target: { value: 'newyear2026' } })
    expect(codeInput.value).toBe('NEWYEAR2026')

    const valueInput = screen.getByDisplayValue('20') as HTMLInputElement
    fireEvent.change(valueInput, { target: { value: '' } })
    fireEvent.click(screen.getByText('保存'))
    await vi.waitFor(() => { expect(toastWarn).toHaveBeenCalledWith('请填写券码，并选定折扣方式与力度') })
    expect(mocks.adminCouponSave).not.toHaveBeenCalled()

    fireEvent.change(valueInput, { target: { value: '15' } })
    fireEvent.click(screen.getByText('保存'))
    await vi.waitFor(() => { expect(mocks.adminCouponSave).toHaveBeenCalledTimes(1) })
    const body = mocks.adminCouponSave.mock.calls[0][0]
    expect(body).toMatchObject({ id: 0, code: 'NEWYEAR2026', discount_type: 'percent', discount_value: 15, enabled: 1 })
    // 未填有效期 → 提交空串（=不限），不得把 Invalid Date 发出去
    expect(body.valid_from).toBe('')
    expect(body.valid_until).toBe('')
    // 保存成功后必须回刷列表（超管要立刻看到配额与状态）
    await vi.waitFor(() => { expect(mocks.adminCoupons.mock.calls.length).toBeGreaterThanOrEqual(2) })
  })

  it('编辑回填按本地时区展示、保存仍回 RFC3339 UTC（不因时区漂移）', async () => {
    const utc = '2026-12-31T16:00:00Z'
    mocks.adminCoupons.mockResolvedValue({ success: true, coupons: [{ ...percentCoupon, valid_until: utc }] })
    render(<><CouponsP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('NEWYEAR2026')).toBeTruthy() })

    fireEvent.click(screen.getByText('编辑'))
    // 弹窗内有两个 datetime-local（生效/失效），此处只校验失效时间的往返
    const locals = Array.from(document.querySelectorAll('input[type="datetime-local"]')) as HTMLInputElement[]
    expect(locals).toHaveLength(2)
    expect(new Date(locals[1].value).getTime()).toBe(new Date(utc).getTime())
    fireEvent.click(screen.getByText('保存'))
    await vi.waitFor(() => { expect(mocks.adminCouponSave).toHaveBeenCalledTimes(1) })
    const body = mocks.adminCouponSave.mock.calls[0][0]
    expect(body).toMatchObject({ id: 1, code: 'NEWYEAR2026' })
    // 回环后仍是同一时刻（毫秒位数差异不算漂移）
    expect(new Date(String(body.valid_until)).getTime()).toBe(new Date(utc).getTime())
  })
})

describe('优惠券管理 · 启停 / 流水 / 删除（#41）', () => {
  it('停用只改 enabled，券码与规则原样带回', async () => {
    const { container } = render(<><CouponsP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('NEWYEAR2026')).toBeTruthy() })
    fireEvent.click(container.querySelector('input[type="checkbox"]') as HTMLInputElement)
    await vi.waitFor(() => { expect(mocks.adminCouponSave).toHaveBeenCalledTimes(1) })
    expect(mocks.adminCouponSave.mock.calls[0][0]).toMatchObject({
      id: 1, code: 'NEWYEAR2026', discount_value: 10, max_uses: 100, enabled: 0,
    })
  })

  it('按券拉核销流水，展示折前/优惠/实付三栏', async () => {
    render(<><CouponsP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('NEWYEAR2026')).toBeTruthy() })
    fireEvent.click(screen.getByText('核销流水'))
    await vi.waitFor(() => { expect(mocks.adminCouponRedemptions).toHaveBeenCalledWith(1) })
    await vi.waitFor(() => {
      expect(screen.getByText('T70-CPX')).toBeTruthy()
      // 折前 100 / 优惠 10 / 实付 90 三栏齐全，正是活动复盘与对账的三条线
      expect(document.body.textContent).toMatch(/100\.00[\s\S]*10\.00[\s\S]*90\.00/)
    })
  })

  it('删除需二次确认，确认后只删模板', async () => {
    render(<><CouponsP /><DialogHost /></>)
    await vi.waitFor(() => { expect(screen.getByText('NEWYEAR2026')).toBeTruthy() })
    fireEvent.click(screen.getByText('删除'))
    await vi.waitFor(() => { expect(screen.getByText(/核销流水会保留/)).toBeTruthy() })
    expect(mocks.adminCouponDelete).not.toHaveBeenCalled()
    // 确认位按钮文案沿用「删除」（confirmText 传入），点它才真正下发删除
    fireEvent.click(dialogConfirm())
    await vi.waitFor(() => { expect(mocks.adminCouponDelete).toHaveBeenCalledWith(1) })
  })
})
