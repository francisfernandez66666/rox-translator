// ============================================================================
// TenantsP.charge.honest.dom.test.tsx — 超管「注入资金」三张脸的行为锁
//   钉施工方案《后台超时与慢SQL_跨租户与充值_工单双下载_施工方案_20261103.md》
//   §4.1（诚实化）／§4.5（断言与门禁）点名的这一份新锁：
//     ① 建单失败 ⇒ 红条＋不出账（第二步一次都不走）；
//     ② 建单成功、置付失败 ⇒ **不绿**，出「订单 <单号> 已生成、尚未到账」并点名单号；
//     ③ 两步都成（含到账回验）⇒ 绿条；
//     ＋ 反证：把「置付判结果」摘掉（源码级 mock 切回旧形态）⇒ 第②腿判据必须真判红；
//     ＋ 负向：② 那张脸上不许出现成功徽标。
//   旧病灶（TenantsP.tsx:137-151 的历史形态）：置付不判 success，无条件 toastSuccess「已充值」，
//   于是「权限被拒」和「停在待支付」在屏幕上长得一模一样。本文件按 runGuarded/toast 通道
//   实测三张脸，并用可切换的判据 mock 证明这条锁不是空替换（new===old 不算反证）。
// 运行：cd frontend-react && npx vitest run src/components/admin/TenantsP.charge.honest.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { tpl } from '@/i18n'
import { TenantsP } from './TenantsP'

const ORDER_NO = 'ORD-1103-77'
const TENANT_ID = 7
const POINTS = 500

const h = vi.hoisted(() => ({
  tenantList: vi.fn(),
  adminOrderCreate: vi.fn(),
  adminOrderPay: vi.fn(),
  promptText: vi.fn(),
  confirmDialog: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  // 反证开关：true ⇒ 判据函数退化成「旧形态：不判置付结果、建单成功就无条件绿」
  mutant: false,
}))

// 只替换本面板用到的域接口，其余保持真实导出（整模块 mock 会把 stores/admin 初始化一起打空）
vi.mock('@/api', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api')>()
  return {
    ...real,
    tenantList: h.tenantList,
    adminOrderCreate: h.adminOrderCreate,
    adminOrderPay: h.adminOrderPay,
  }
})
vi.mock('@/api/industry', () => ({ industries: vi.fn(async () => ({ success: true, industries: [] })) }))
vi.mock('@/components/uiDialogs', () => ({
  confirmDialog: h.confirmDialog,
  promptText: h.promptText,
}))
// toast 通道桩：三张脸的语气全靠这两只出口判，正向对照也靠它们证明「0 是真的 0」
vi.mock('@/lib/toastBus', () => ({
  toastSuccess: h.toastSuccess,
  toastError: h.toastError,
  toastWarn: vi.fn(),
  toast: vi.fn(),
  registerToastHandler: vi.fn(),
}))
// runGuarded 通道：spy 包装真实实现（保留「抛错⇒toastError⇒返回 undefined」的兜底语义，
// 只把调用变成可观测，便于断言 charge 确实走了 guarded 路径）
vi.mock('@/lib/runGuarded', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/lib/runGuarded')>()
  return { runGuarded: vi.fn((fn: any, opts?: any) => real.runGuarded(fn, opts)) }
})
// 源码级 mock 切换：正常路径调真判据；mutant 路径把「置付判结果」整段摘掉，
// 复刻旧缺陷「created && hasOrderId 就无条件绿」。这是 §4.5 反证要的变异体。
vi.mock('@/lib/tenantChargeOutcome', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/lib/tenantChargeOutcome')>()
  return {
    ...real,
    decideChargeOutcome: (f: any) => h.mutant
      ? (f.created && f.hasOrderId
          ? { kind: 'credited', msgKey: 'tenants.charged', success: true }
          : real.decideChargeOutcome(f))
      : real.decideChargeOutcome(f),
  }
})

import { runGuarded } from '@/lib/runGuarded'

const tenantRow = {
  id: TENANT_ID, code: 't7', name: '租户七', status: 'active',
  expires_at: '', permissions: '{}', created_at: '', updated_at: '',
}

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  h.mutant = false
  localStorage.setItem('app_lang', 'zh')
  h.tenantList.mockResolvedValue({ success: true, tenants: [tenantRow] })
  h.promptText.mockResolvedValue(String(POINTS))
  h.confirmDialog.mockResolvedValue(true)
  // 默认：建单成功＋置付成功且余额装得下这一笔 ⇒ credited（③ 那张脸）
  h.adminOrderCreate.mockResolvedValue({ success: true, order: { id: 77, order_no: ORDER_NO } })
  h.adminOrderPay.mockResolvedValue({ success: true, balance_points: POINTS, order_points: POINTS })
})

/** 点行内「充值」入口（Link 渲染成 button），等两步链跑到出栈 */
async function fireCharge() {
  const btn = await screen.findByRole('button', { name: '充值' })
  fireEvent.click(btn)
  await waitFor(() => { expect(h.adminOrderCreate).toHaveBeenCalledTimes(1) })
  await waitFor(() => {
    expect(h.toastSuccess.mock.calls.length + h.toastError.mock.calls.length).toBeGreaterThanOrEqual(1)
  })
}

describe('注入资金三张脸（施工方案 §4.1/§4.5）', () => {
  it('① 建单失败 ⇒ 红条＋不出账（置付那一步一次都不走）', async () => {
    h.adminOrderCreate.mockResolvedValue({ success: false, message: '权限不足' })
    render(<TenantsP />)
    await fireCharge()

    expect(h.promptText, '链路真的走到了 charge()').toHaveBeenCalled()
    expect(runGuarded, '建单走 runGuarded 通道（测量通道活）').toHaveBeenCalled()
    expect(h.adminOrderPay, '不出账：第二步一次都不走').not.toHaveBeenCalled()
    expect(h.toastSuccess, '不绿').not.toHaveBeenCalled()
    expect(h.toastError).toHaveBeenCalledTimes(1)
    expect(h.toastError.mock.calls[0][0]).toBe(tpl('tenants.chargeCreateFailed', { no: '', points: POINTS }))
  })

  it('② 建单成功、置付失败 ⇒ 不绿，红条点名单号', async () => {
    h.adminOrderPay.mockResolvedValue({ success: false, message: '权限被拒' })
    render(<TenantsP />)
    await fireCharge()

    expect(h.adminOrderPay, '置付确实走到了（失败的是它，不是没发）').toHaveBeenCalledWith(77, TENANT_ID)
    expect(h.toastSuccess, '★ 绝不绿').not.toHaveBeenCalled()
    expect(h.toastError).toHaveBeenCalledTimes(1)
    const msg = String(h.toastError.mock.calls[0][0])
    expect(msg, '点名订单号，让人工去订单面板核对').toContain(ORDER_NO)
    expect(msg).toBe(tpl('tenants.chargeNotCredited', { no: ORDER_NO, points: POINTS }))
    expect(msg).toMatch(/尚未到账|未到账/)
  })

  it('③ 两步都成且到账回验通过 ⇒ 绿条', async () => {
    render(<TenantsP />)
    await fireCharge()

    expect(h.adminOrderPay).toHaveBeenCalledWith(77, TENANT_ID)
    expect(h.toastError, '没有红条').not.toHaveBeenCalled()
    expect(h.toastSuccess).toHaveBeenCalledTimes(1)
    const msg = String(h.toastSuccess.mock.calls[0][0])
    expect(msg).toContain('已充值')
    expect(msg).toBe(tpl('tenants.charged', { no: ORDER_NO, points: POINTS }))
  })

  it('反证：把「置付判结果」摘掉 ⇒ 第②腿判据真能判红（不许空替换）', async () => {
    // 与用例②完全同一套判据，不另写一份 —— 反证要证的就是「这套判据有判别力」
    function expectSecondFace() {
      expect(h.adminOrderCreate).toHaveBeenCalledTimes(1)
      expect(h.adminOrderPay).toHaveBeenCalledWith(77, TENANT_ID)
      expect(h.toastSuccess).not.toHaveBeenCalled() // ★ 不绿
      expect(h.toastError).toHaveBeenCalledTimes(1)
      expect(String(h.toastError.mock.calls[0][0])).toContain(ORDER_NO) // 点名单号
    }

    // 正常实现：第②腿全绿（正向对照）
    h.adminOrderPay.mockResolvedValue({ success: false, message: '权限被拒' })
    render(<TenantsP />)
    await fireCharge()
    expectSecondFace()

    // 变异体：源码级 mock 把判据切成「不判置付结果、建单成功就绿」的旧形态
    cleanup()
    vi.clearAllMocks()
    h.tenantList.mockResolvedValue({ success: true, tenants: [tenantRow] })
    h.promptText.mockResolvedValue(String(POINTS))
    h.adminOrderCreate.mockResolvedValue({ success: true, order: { id: 77, order_no: ORDER_NO } })
    h.adminOrderPay.mockResolvedValue({ success: false, message: '权限被拒' })
    h.mutant = true
    render(<TenantsP />)
    await fireCharge()
    // 先证变异体真的改了行为（不是 new===old 的空替换）：置付失败却弹了绿条
    await waitFor(() => { expect(h.toastSuccess).toHaveBeenCalledTimes(1) })
    // 再用同一套第②腿判据机械证明它「判红」
    let judgedRed = false
    try { expectSecondFace() } catch { judgedRed = true }
    expect(judgedRed, '摘掉置付判结果后，第②腿的「不绿」判据必须抛断言错').toBe(true)
    h.mutant = false
  })

  it('负向：② 那张脸上不许出现成功徽标', async () => {
    h.adminOrderPay.mockResolvedValue({ success: false, message: '权限被拒' })
    render(<TenantsP />)
    await fireCharge()

    expect(h.toastSuccess, '成功徽标通道零调用').not.toHaveBeenCalled()
    expect(document.body.textContent || '', '屏上不许有绿条文案').not.toContain('已充值')
    expect(String(h.toastError.mock.calls[0][0]), '红条本身也不许混进成功字样').not.toContain('已充值')
    // 正向对照：用例③里同一条 toastSuccess 通道真的绿过 ⇒ 这里的 0 是真的 0，不是采不到
  })
})
