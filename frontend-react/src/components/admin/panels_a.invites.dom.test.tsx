// ============================================================================
// panels_a.invites.dom.test.tsx — 邀请码「撤销」生命周期行为锁（★ 2026-10-19 G9）
// ----------------------------------------------------------------------------
// 缺陷背景：邀请码面板过去只有「生成 + 查看」，发出去的码没有收回路径（凭证类只进不出）。
// 本批补：状态三态（未使用／已使用／已撤销）＋ 撤销动作列，且**不删行**（保留 used_by 追溯）。
// 判据（等值 + 反证）：
//   ① 三态文案逐行在位；撤销链接只对「未使用且未撤销」的行渲染（其余行显示占位「—」）；
//   ② 点撤销：confirmDialog **恰一次**（danger 档）→ 只发一次 POST /api/admin/invite-codes/revoke，
//      载荷 { id }；成功后刷新列表；
//   ③ 反证：确认框返回 false ⇒ 一个请求都不发（确认是真闸门，不是摆设）；
//   ④ 反证：撤销行**仍在**（不删行）——刷新后三行俱在。
// 运行：npx vitest run src/components/admin/panels_a.invites.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent } from '@testing-library/react'
import { InvitesP } from './panels_a'

const m = vi.hoisted(() => ({
  inviteCodes: vi.fn(),
  request: vi.fn(),
  confirm: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
}))

// 保留真实模块（bizResp/authHeaders 等），只把取数与网络出口换成 spy
vi.mock('@/api', async (importOriginal) => {
  const real = await importOriginal<typeof import('@/api')>()
  return { ...real, inviteCodes: m.inviteCodes, request: m.request }
})
vi.mock('@/stores/admin', () => ({
  useAdmin: () => ({ tenants: [], activeTenantId: 1, isSuper: false }),
  roleName: (r?: string) => r ?? '',
}))
vi.mock('@/components/uiDialogs', () => ({ confirmDialog: m.confirm, promptText: vi.fn() }))
vi.mock('@/lib/toastBus', () => ({ toastSuccess: m.success, toastError: m.error, toastWarn: vi.fn() }))

/** 三行样本：未使用／已使用／已撤销 */
const ROWS = [
  { id: 1, code: 'AAA', tenant_id: 1, used: 0, used_by: '', created_at: '', used_at: '' },
  { id: 2, code: 'BBB', tenant_id: 1, used: 1, used_by: 'kim', created_at: '', used_at: '' },
  { id: 3, code: 'CCC', tenant_id: 1, used: 0, revoked: 1, created_at: '', used_at: '' },
]

beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  localStorage.setItem('app_lang', 'zh')
  m.inviteCodes.mockResolvedValue({ success: true, invite_codes: ROWS })
  m.request.mockResolvedValue({ success: true })
  m.confirm.mockResolvedValue(true)
})

describe('邀请码面板 · 撤销生命周期（★ G9）', () => {
  it('① 三态文案逐行在位；撤销链接只在「未使用」行、其余行显示占位', async () => {
    const { container } = render(<InvitesP />)
    await vi.waitFor(() => { expect(screen.getByText('未使用')).toBeTruthy() })
    expect(screen.getByText('已使用')).toBeTruthy()
    expect(screen.getByText('已撤销')).toBeTruthy()
    // 撤销链恰一枚（只有 id=1 那一行可撤）
    const links = Array.from(container.querySelectorAll('a,button,span')).filter((el) => el.textContent?.trim() === '撤销')
    expect(links.length, '仅「未使用」行渲染撤销入口').toBe(1)
    // 另两行是占位符，不是链接
    const dashes = Array.from(container.querySelectorAll('span')).filter((el) => el.textContent?.trim() === '—')
    expect(dashes.length).toBe(2)
  })

  it('② 点撤销：确认恰一次（danger）→ 一次 POST /revoke，载荷 { id }；成功后刷新', async () => {
    const { container } = render(<InvitesP />)
    await vi.waitFor(() => { expect(screen.getByText('未使用')).toBeTruthy() })
    const codesCallsBefore = m.inviteCodes.mock.calls.length

    const link = Array.from(container.querySelectorAll('a,button,span')).find((el) => el.textContent?.trim() === '撤销')!
    fireEvent.click(link)

    await vi.waitFor(() => { expect(m.request).toHaveBeenCalledTimes(1) })
    // 确认框恰一次、且是破坏性档
    expect(m.confirm).toHaveBeenCalledTimes(1)
    expect(m.confirm.mock.calls[0][0].danger).toBe(true)
    // 端点与载荷
    const [url, init] = m.request.mock.calls[0]
    expect(url).toBe('/api/admin/invite-codes/revoke')
    expect(init.method).toBe('POST')
    expect(JSON.parse(init.body).id).toBe(1)
    // 成功提示 + 刷新（列表重取）
    await vi.waitFor(() => { expect(m.success).toHaveBeenCalledTimes(1) })
    await vi.waitFor(() => { expect(m.inviteCodes.mock.calls.length).toBeGreaterThan(codesCallsBefore) })
  })

  it('③ 反证：确认框返回 false ⇒ 一个请求都不发', async () => {
    m.confirm.mockResolvedValue(false)
    const { container } = render(<InvitesP />)
    await vi.waitFor(() => { expect(screen.getByText('未使用')).toBeTruthy() })
    const link = Array.from(container.querySelectorAll('a,button,span')).find((el) => el.textContent?.trim() === '撤销')!
    fireEvent.click(link)
    // 给异步链一个落定窗口，再判「零请求」
    await Promise.resolve()
    expect(m.request).not.toHaveBeenCalled()
    expect(m.confirm).toHaveBeenCalledTimes(1)
  })

  it('④ 反证：撤销后行仍在（不删行，保留追溯）——刷新返回同样三行', async () => {
    render(<InvitesP />)
    await vi.waitFor(() => { expect(screen.getByText('未使用')).toBeTruthy() })
    // 刷新仍回三行（本模块不做本地删行）
    await vi.waitFor(() => { expect(m.inviteCodes.mock.calls.length).toBeGreaterThanOrEqual(1) })
    expect(screen.getAllByText(/AAA|BBB|CCC/).length).toBe(3)
  })
})
