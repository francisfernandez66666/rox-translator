// ============================================================================
// TenantsP.create.dom.test.tsx — 租户管理「开通组织」（★ 2026-10-16 用户当场报障：
//   「我作为超管不能后台手动创建一个租户」）
// 这条用例回答的唯一问题：**填好编码点确定之后，POST 到底发不发**。
//   现网实证（建库至今全部日志）：`/api/tenant/create` 命中数恒为 0，且面板挂载才会发的
//   单独 `/api/tenant/list` 一次都没出现过（顶栏租户切换器那条总与 /api/admin/orgs 同时到）
//   ⇒ 这条链路在生产上**一次都没走到发请求**。先钉死「代码路径本身通不通」：
//   它绿了才说明问题在触发面（入口可见性/构造形态），不在请求体。
// 断言取向：点一次「开通组织」⇒ 弹窗开 ⇒ 填编码 ⇒ 点确定 ⇒ tenantCreate 被调用且字段原样下发。
// 反证（变异体）：把 save() 里的 tenantCreate 调用换成 no-op ⇒ 本条必须红。
// 运行：npx vitest run src/components/admin/TenantsP.create.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import { TenantsP } from './TenantsP'

const mocks = vi.hoisted(() => ({
  tenantList: vi.fn(),
  tenantCreate: vi.fn(),
  tenantUpdate: vi.fn(),
  tenantSetStatus: vi.fn(),
  tenantDelete: vi.fn(),
  tenantGrantTrial: vi.fn(),
  tenantErase: vi.fn(),
  adminOrderCreate: vi.fn(),
  adminOrderPay: vi.fn(),
  industries: vi.fn(),
  confirmDialog: vi.fn(),
  promptText: vi.fn(),
}))

// ★ 只替换本面板用到的域接口，其余保持真实导出（`@/api` 还是 stores/admin 的来源，
//   整模块 mock 会把 store 初始化一起打空——ApiKeysP 用例首跑真踩过这一档）。
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    tenantList: mocks.tenantList,
    tenantCreate: mocks.tenantCreate,
    tenantUpdate: mocks.tenantUpdate,
    tenantSetStatus: mocks.tenantSetStatus,
    tenantDelete: mocks.tenantDelete,
    tenantGrantTrial: mocks.tenantGrantTrial,
    tenantErase: mocks.tenantErase,
    adminOrderCreate: mocks.adminOrderCreate,
    adminOrderPay: mocks.adminOrderPay,
  }
})

// confirmDialog 走命令式出口（需要 DialogHost 上下文），单测里直接桩掉：删除用例要数「确认了几次」
vi.mock('@/components/uiDialogs', () => ({
  confirmDialog: mocks.confirmDialog,
  promptText: mocks.promptText,
}))
vi.mock('@/api/industry', () => ({ industries: mocks.industries }))
// toast 出口桩：本批的核心断言就是「失败必须有话」——三档 toast 的调用情况即是判据
vi.mock('@/lib/toastBus', () => ({ toastSuccess: vi.fn(), toastError: vi.fn(), toastWarn: vi.fn() }))

import { toastError } from '@/lib/toastBus'

const dialogOpen = () => !!document.querySelector('.lc-dialog')
const dialogConfirm = () =>
  document.querySelector('.lc-dialog .lc-btn--primary, .lc-dialog .lc-btn--danger') as HTMLElement

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  mocks.tenantList.mockResolvedValue({ success: true, tenants: [] })
  mocks.industries.mockResolvedValue({ success: true, industries: [] })
  mocks.tenantCreate.mockResolvedValue({ success: true })
  mocks.adminOrderCreate.mockResolvedValue({ success: true })
})

describe('租户管理「开通组织」：填好编码点确定必须真发请求', () => {
  it('点开通组织 ⇒ 弹窗开 ⇒ 填编码 ⇒ 确定 ⇒ tenantCreate 收到该编码', async () => {
    render(<TenantsP />)
    // 面板标题与「开通组织」按钮都在（入口本身在位）
    const open = await screen.findByText('开通组织')
    open.click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })

    // 编码是创建态唯一必填项：标签与输入框都必须渲染出来（空则 save 只弹「组织编码必填」不发请求）
    const label = screen.getByText('编码 (如 bmw)')
    const input = label.parentElement?.querySelector('input') as HTMLInputElement
    expect(input).toBeTruthy()
    // React 受控输入：走原生 setter + input 事件，直接改 value 不会触发 onChange
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(input, 'probe1016')
    input.dispatchEvent(new Event('input', { bubbles: true }))

    dialogConfirm().click()
    await vi.waitFor(() => { expect(mocks.tenantCreate).toHaveBeenCalledTimes(1) })
    expect(mocks.tenantCreate.mock.calls[0][0]).toMatchObject({ code: 'probe1016' })
  })

  it('编码留空点确定：不发请求（前端先拦一刀），弹窗不关，且**就地**给出必填提示', async () => {
    render(<TenantsP />)
    ;(await screen.findByText('开通组织')).click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    dialogConfirm().click()
    // 给异步一拍机会
    await new Promise((r) => setTimeout(r, 20))
    expect(mocks.tenantCreate).not.toHaveBeenCalled()
    expect(dialogOpen()).toBe(true)
    // ★ 2026-10-16：必填提示不再只靠一条会消失的 toast —— 它必须钉在输入框下方
    //   （旧形态是「toastWarn 一闪 + return」，用户没看见 toast 时观感就是「点确定完全没反应」）
    expect(screen.getByTestId('tenant-code-err')).toBeTruthy()
  })

  it('填了编码之后必填提示立刻消失（不清掉就成了永久红字）', async () => {
    render(<TenantsP />)
    ;(await screen.findByText('开通组织')).click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    dialogConfirm().click()
    await vi.waitFor(() => { expect(screen.getByTestId('tenant-code-err')).toBeTruthy() })
    const label = screen.getByText('编码 (如 bmw)')
    const input = label.parentElement?.querySelector('input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(input, 'bmw')
    input.dispatchEvent(new Event('input', { bubbles: true }))
    await vi.waitFor(() => { expect(document.querySelector('[data-testid="tenant-code-err"]')).toBeNull() })
  })
})

// ============================================================================
// 失败面必须出话（★ 2026-10-16 用户报障「点确定完全没反应」的修复面）
// 现网实证：全量日志里 /api/tenant/create 命中 0 ⇒ 请求根本没发出过；而这条链上**每一处
// 失败**在旧写法里都是静默的：接口层裸 request（4xx 直接抛）、调用点 `if (!r.success)`
// 走不到抛错分支 ⇒ 未处理 rejection ⇒ 弹窗不关、列表不动、零提示。
// 本组三条分别钉：业务 4xx 出后端原文 / 网络异常出话 / 两者都不抛未处理 rejection。
// 反证（变异体）：把 tenant.ts 的 bizResp 摘掉 ⇒ 第一条红（ApiError 直接抛穿，toastError 不被调用）；
//   把 save() 的 runGuarded 摘掉 ⇒ 第二条红。
// ============================================================================
describe('租户创建失败面：业务 4xx 与网络异常都必须在界面上出声', () => {
  const fillCode = async () => {
    const label = screen.getByText('编码 (如 bmw)')
    const input = label.parentElement?.querySelector('input') as HTMLInputElement
    const setter = Object.getOwnPropertyDescriptor(window.HTMLInputElement.prototype, 'value')!.set!
    setter.call(input, 'dup1016')
    input.dispatchEvent(new Event('input', { bubbles: true }))
  }

  it('后端 4xx（bizResp 还原成 success:false）⇒ 弹出后端原文，弹窗保持打开', async () => {
    // ★ 这里喂的就是 bizResp 的产物形态：request 抛 400，tenant.ts 已把结构化失败体还原成信封
    mocks.tenantCreate.mockResolvedValue({ success: false, message: '组织编码已存在', status: 409 })
    render(<TenantsP />)
    ;(await screen.findByText('开通组织')).click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    await fillCode()
    dialogConfirm().click()
    await vi.waitFor(() => { expect(toastError).toHaveBeenCalledWith('组织编码已存在') })
    expect(dialogOpen()).toBe(true)
  })

  it('网络异常（接口 reject）⇒ runGuarded 兜底出声，弹窗保持打开', async () => {
    mocks.tenantCreate.mockRejectedValue(new Error('无法连接后端服务'))
    render(<TenantsP />)
    ;(await screen.findByText('开通组织')).click()
    await vi.waitFor(() => { expect(dialogOpen()).toBe(true) })
    await fillCode()
    dialogConfirm().click()
    await vi.waitFor(() => { expect(toastError).toHaveBeenCalledWith('无法连接后端服务') })
    expect(dialogOpen()).toBe(true)
  })
})

// ============================================================================
// 删除租户只许确认一次（★ 2026-10-16 C 族双确认批，与 ApiKeysP 删除同形）
// 旧形态：行内 Link 先 confirmDialog 一次，进去 removeTenant() 又 confirmDialog 一次
// ⇒ 点第一次「确定」时请求还没发、列表不动（用户观感即「没反应」）。
// 反证（变异体）：把行内那次 confirm 装回去 ⇒ 本用例的 toHaveBeenCalledTimes(1) 判红。
// ============================================================================
describe('删除租户：确认恰好一次', () => {
  it('点删除 ⇒ 一次确认 ⇒ tenantDelete 收到该 id 与确认串', async () => {
    mocks.tenantList.mockResolvedValue({ success: true, tenants: [{ id: 9, code: 't9', name: 'T9', status: 'active', expires_at: '', permissions: '{}', created_at: '', updated_at: '' }] })
    mocks.confirmDialog.mockResolvedValue(true)
    mocks.tenantDelete.mockResolvedValue({ success: true })
    render(<TenantsP />)
    const del = await screen.findByText('删除')
    del.click()
    // ★ 2026-10-23：第二个参数是确认串（该行的 code）——服务端逐字比对，
    // 用来把「要删的那一行」绑进请求；少传会让新接口回 400 而不是照 id 删
    await vi.waitFor(() => { expect(mocks.tenantDelete).toHaveBeenCalledWith(9, 't9') })
    expect(mocks.confirmDialog).toHaveBeenCalledTimes(1)
  })
})
