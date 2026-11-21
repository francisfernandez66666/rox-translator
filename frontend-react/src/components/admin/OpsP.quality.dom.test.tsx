// ============================================================================
// OpsP.quality.dom.test.tsx — 运营策略面板里的「复核链路（质量）」卡片
// ★ 2026-11-21 用户令：即时翻译 / 翻译工单**各自**可配是否走复核链路。
//
// 锁的是界面这一侧的四条（后端与引擎侧见 api/review_toggle_api_gate_test.go、
// engine/review_scope_test.go、orchestrator/review_toggle_workflow_test.go）：
//   ① 缺省档必须是「两档都开」——后端 effective 段没这个键时写成 `!!x` 会显示为关，
//      运营顺手一保存就把全站质量降一档（这是本卡片最危险的一态）；
//   ② effective 段显式 false 时必须如实显示为关（读写同源）；
//   ③ 拨动一颗开关只改它自己那一档，保存时整包送出且另一档保持现值；
//   ④ 非超管一律禁用（本卡片是平台级配置，租户管理员只能看）。
//
// 运行：npx vitest run src/components/admin/OpsP.quality.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { OpsP } from './panels_e'

// 只替换本卡片真正会调的接口，其余保持真实导出（整模块 mock 会把同文件其它导出一起打空）。
const mocks = vi.hoisted(() => ({
  opsPolicy: vi.fn(),
  opsSlo: vi.fn(),
  opsRoutes: vi.fn(),
  opsPolicySave: vi.fn(),
  opsWindowSave: vi.fn(),
  opsPackageReset: vi.fn(),
  cfCacheState: vi.fn(),
  cfCacheSave: vi.fn(),
  cfCachePurge: vi.fn(),
  isSuper: true,
}))

vi.mock('@/api/ops', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api/ops')>()
  return {
    ...actual,
    opsPolicy: mocks.opsPolicy,
    opsSlo: mocks.opsSlo,
    opsRoutes: mocks.opsRoutes,
    opsPolicySave: mocks.opsPolicySave,
    opsWindowSave: mocks.opsWindowSave,
    opsPackageReset: mocks.opsPackageReset,
    cfCacheState: mocks.cfCacheState,
    cfCacheSave: mocks.cfCacheSave,
    cfCachePurge: mocks.cfCachePurge,
  }
})

vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ isSuper: mocks.isSuper, activeTenantId: 0, myLevel: mocks.isSuper ? 4 : 2, tenants: [], switchTenant: () => {} }),
  useAdminStore: { getState: () => ({ gotoPanel: () => {} }), setState: () => {} },
}))

vi.mock('@/lib/toastBus', () => ({
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  toastWarn: vi.fn(),
}))

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mocks.isSuper = true
  mocks.opsPolicy.mockResolvedValue({ success: true, effective: {} })
  mocks.opsSlo.mockResolvedValue({ success: true, slos: [] })
  mocks.opsRoutes.mockResolvedValue({ success: true })
  mocks.cfCacheState.mockResolvedValue({ success: true, set: false, masked: '', file_present: false })
  mocks.opsPolicySave.mockResolvedValue({ success: true })
})

/** 取卡片里那两颗开关（顺序＝即时档、工单档，与 JSX 一致）。 */
function qualitySwitches(): HTMLInputElement[] {
  const card = screen.getByTestId('ops-quality')
  return Array.from(card.querySelectorAll('input[role="switch"]')) as HTMLInputElement[]
}

/** 等策略读回、卡片画出来。 */
async function waitCard() {
  await vi.waitFor(() => {
    expect(screen.getByTestId('ops-quality')).toBeTruthy()
    expect(qualitySwitches().length).toBe(2)
    expect(mocks.opsPolicy).toHaveBeenCalled()
  })
}

/**
 * 等 load 的 setState 真正落地。
 * 光等"卡片画出来"是不够的：初始草稿 quality={} 会让两颗开关先显示为开，
 * 而 effective 段里的 false 要等 GET 回来才写进草稿——不等这一拍，
 * 「显式 false 显示为关」那条判据会在初始态上**空转通过**。
 */
async function waitLoaded(check: () => void) {
  await vi.waitFor(check)
}

describe('OpsP · 复核链路（质量）卡片', () => {
  it('缺省档：effective 没有 quality 时两颗开关都必须显示为开（默认走复核）', async () => {
    render(<OpsP />)
    await waitCard()
    const [instant, ticket] = qualitySwitches()
    // ① 这一条是整张卡最危险的一态：写成 `!!x` 会让存量部署一开面板就看到"全关"
    expect(instant.checked).toBe(true)
    expect(ticket.checked).toBe(true)
    expect(instant.disabled).toBe(false)
  })

  it('effective 显式 false 时如实显示为关（读写同源）', async () => {
    mocks.opsPolicy.mockResolvedValue({
      success: true,
      effective: { quality: { instant_review_enabled: false, ticket_review_enabled: true } },
    })
    render(<OpsP />)
    await waitCard()
    // 必须等 load 落地：初始草稿 quality={} 会让两颗都先显示为开
    await waitLoaded(() => { expect(qualitySwitches()[0].checked).toBe(false) })
    const [instant, ticket] = qualitySwitches()
    expect(instant.checked).toBe(false)
    expect(ticket.checked).toBe(true)
  })

  it('只拨即时档：保存时整包送出，工单档保持现值', async () => {
    mocks.opsPolicy.mockResolvedValue({
      success: true,
      effective: { quality: { instant_review_enabled: false, ticket_review_enabled: true } },
    })
    render(<OpsP />)
    await waitCard()
    await waitLoaded(() => { expect(qualitySwitches()[0].checked).toBe(false) })
    const [instant] = qualitySwitches()
    // 受控 checkbox：React 的 onChange 走 click 通道（dispatch 'change' 不会触发）。
    fireEvent.click(instant)

    // 主保存按钮与 CF 卡片那颗同名（「保存策略」），这里取不带 ops-cf-save 的那一颗
    const saves = screen.getAllByText('保存策略') as HTMLElement[]
    const mainSave = saves.find((el) => el.getAttribute('data-testid') !== 'ops-cf-save')!
    mainSave.click()
    await vi.waitFor(() => { expect(mocks.opsPolicySave).toHaveBeenCalledTimes(1) })
    const [scope, pol] = mocks.opsPolicySave.mock.calls[0]
    expect(scope).toBe('platform')
    // ③ 两档各自独立：即时开、工单仍开（另一档现值不许被这次拨动洗掉）
    expect(pol.quality).toEqual({ instant_review_enabled: true, ticket_review_enabled: true })
  })

  it('非超管：两颗开关禁用（平台级配置，租户管理员只能看）', async () => {
    mocks.isSuper = false
    render(<OpsP />)
    await waitCard()
    const [instant, ticket] = qualitySwitches()
    expect(instant.disabled).toBe(true)
    expect(ticket.disabled).toBe(true)
  })
})
