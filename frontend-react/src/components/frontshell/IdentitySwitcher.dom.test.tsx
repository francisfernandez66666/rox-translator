// ============================================================================
// components/frontshell/IdentitySwitcher.dom.test.tsx — 身份切换器机械锁（★ 多身份方案 B4）
// 锁五件事（方案 §十一「前端 DOM：切换器渲染/切换调用/单身份不渲染」）：
//   ① **单身份账户整颗不渲染**（方案 §十 R8 拍板形态；这条是"不许给没有得选的人摆一颗按钮"）；
//   ② 多身份时触发钮在位，点开列出全部身份，**当前身份打勾**；
//   ③ 选中另一身份 ⇒ 恰好调一次 switchIdentity(tid)，成功后换 token、换 user、面板收起；
//   ④ tenant_status 非 active 的身份**置灰且点击不发请求**（后端照旧必判，前端只是少一次注定失败的往返）；
//   ⑤ 面板 portal 到 document.body ＋ position:fixed（AGENTS §一·5 浮层那条：
//      左栏仅 237/295px 且 overflow-y:auto，栏内绝对定位会把左栏撑出横向滚动条）。
// 反证 R1/R2 各摘一条判据（把单身份兜底摘掉／把 fixed 改回栏内 absolute），必须只有对应那条红。
// 注意：本仓 vitest 未开 globals，RTL 不自动 cleanup，必须显式 afterEach(cleanup)。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { ToastProvider } from '@/ui/langcross/src'

// 身份接口全部替身：本测试锁的是"前端怎么装配与分派这一份清单"，不是网络形态。
// 部分 mock（保留其余真实导出）：stores/auth 初始化还要 getAuthToken/setAuthToken 真件，
// 只有把这两个也换成假值，才测得到"切换成功后 token 真的换了"这一维。
const apiMock = {
  identities: vi.fn(),
  switchIdentity: vi.fn(),
}
vi.mock('@/api', async (io) => ({
  ...(await io<typeof import('@/api')>()),
  identities: (...a: unknown[]) => apiMock.identities(...a),
  switchIdentity: (...a: unknown[]) => apiMock.switchIdentity(...a),
}))

import IdentitySwitcher from './IdentitySwitcher'
import { useAuthStore } from '@/stores/auth'
import { setAuthToken, getAuthToken } from '@/api'
import type { Identity } from '@/api'

const PERSONAL: Identity = {
  uid: 11, tid: 1, tenant_name: '个人版租户', is_personal: true,
  role: 'user', org_name: '', is_home: true, tenant_status: 'active',
}
const ACME: Identity = {
  uid: 22, tid: 7, tenant_name: '安客企业', is_personal: false,
  role: 'dept_admin', org_name: '研发部', is_home: false, tenant_status: 'active',
}
// 已停用的企业身份：置灰、不可点（后端对这种行必判 403）
const EXPIRED: Identity = {
  uid: 33, tid: 9, tenant_name: '过期公司', is_personal: false,
  role: 'user', org_name: '', is_home: false, tenant_status: 'expired',
}

/** 落一个"当前在企业身份"的会话（tid=7 与 ACME 对齐） */
function seed(opts: { identities: Identity[]; token?: string } = { identities: [] }) {
  setAuthToken(opts.token || 'tok-old')
  useAuthStore.setState({
    user: { id: 22, username: 'u22', display_name: '', role: 'dept_admin', tenant_id: 7 },
    restoring: false,
    identities: opts.identities,
  })
}

/** 渲染（ToastProvider 必挂：切换成功/失败都要出提示，件内 useToast 无 Provider 会抛） */
function mount() {
  render(<ToastProvider><IdentitySwitcher /></ToastProvider>)
}

/** 点触发钮展开面板；返回面板节点（portal 在 body 上，不在渲染容器里） */
function openPanel() {
  const trigger = document.querySelector('[data-testid="identity-switcher"]') as Element
  fireEvent.click(trigger)
  return document.querySelector('.idsw-panel') as HTMLElement
}

beforeEach(() => {
  apiMock.identities.mockReset()
  apiMock.identities.mockResolvedValue({ success: true, identities: [] })
  apiMock.switchIdentity.mockReset()
})

afterEach(() => {
  cleanup()
  useAuthStore.setState({ user: null, identities: [] })
  setAuthToken('')
})

describe('IdentitySwitcher · 渲染门', () => {
  it('单身份账户：整颗不渲染（方案 §十「单身份不渲染 icon」）', () => {
    seed({ identities: [PERSONAL] })
    mount()
    expect(document.querySelector('[data-testid="identity-switcher"]')).toBeNull()
    expect(document.querySelector('.idsw-panel')).toBeNull()
  })

  it('未登录：同样不渲染（没有身份可言，切换器不许闪出空面板）', () => {
    useAuthStore.setState({ user: null, identities: [PERSONAL, ACME] })
    mount()
    expect(document.querySelector('[data-testid="identity-switcher"]')).toBeNull()
  })

  it('身份清单为空但已登录：挂载时补拉一次 /api/auth/identities', () => {
    seed({ identities: [] })
    mount()
    expect(apiMock.identities).toHaveBeenCalledTimes(1)
  })
})

describe('IdentitySwitcher · 多身份下拉', () => {
  it('两条身份：触发钮在位、点开列出两项、当前身份打勾', () => {
    seed({ identities: [PERSONAL, ACME] })
    mount()
    expect(document.querySelector('[data-testid="identity-switcher"]')).toBeTruthy()
    const panel = openPanel()
    expect(panel).toBeTruthy()
    const items = Array.from(panel.querySelectorAll('[data-testid="identity-option"]'))
    expect(items.length).toBe(2)
    // 当前会话 tid=7 ⇒ 只有"安客企业"那一项带勾
    const current = panel.querySelectorAll('.idsw-item--current')
    expect(current.length).toBe(1)
    expect(current[0].textContent).toContain('安客企业')
    // 个人版走既有 app.personalPlan 词，不另立第二套叫法
    expect(panel.textContent).toContain('个人版')
    // 部门名随身份出栈（org_name），没有部门的身份不占一格
    expect(panel.textContent).toContain('研发部')
  })

  it('点非当前身份：恰好一次 switchIdentity(该 tid)，成功后换 token 与 user、面板收起', async () => {
    seed({ identities: [PERSONAL, ACME] })
    apiMock.switchIdentity.mockResolvedValue({
      success: true, token: 'tok-new',
      user: { id: 11, username: 'u11', display_name: '', role: 'user', tenant_id: 1 },
      identities: [PERSONAL, ACME],
    })
    mount()
    const panel = openPanel()
    const personalItem = Array.from(panel.querySelectorAll('[data-testid="identity-option"]'))
      .find((n) => (n.textContent || '').includes('个人版')) as HTMLElement
    fireEvent.click(personalItem)
    await new Promise((r) => setTimeout(r, 0))
    expect(apiMock.switchIdentity).toHaveBeenCalledTimes(1)
    expect(apiMock.switchIdentity.mock.calls[0][0]).toBe(1)
    expect(getAuthToken()).toBe('tok-new')
    expect(useAuthStore.getState().user?.tenant_id).toBe(1)
    expect(document.querySelector('.idsw-panel')).toBeNull()
  })

  it('置灰身份（tenant_status 非 active）：aria-disabled 且点击一次请求都不发', async () => {
    seed({ identities: [PERSONAL, ACME, EXPIRED] })
    mount()
    const panel = openPanel()
    const dead = Array.from(panel.querySelectorAll('[data-testid="identity-option"]'))
      .find((n) => (n.textContent || '').includes('过期公司')) as HTMLElement
    expect(dead.getAttribute('aria-disabled')).toBe('true')
    expect(dead.className).toContain('idsw-item--disabled')
    fireEvent.click(dead)
    await new Promise((r) => setTimeout(r, 0))
    expect(apiMock.switchIdentity).not.toHaveBeenCalled()
    expect(getAuthToken()).toBe('tok-old')
  })

  it('切换失败（后端 403 那句）：不换 token、面板关闭、把后端文案原样提示出来', async () => {
    seed({ identities: [PERSONAL, ACME] })
    apiMock.switchIdentity.mockRejectedValue(new Error('无权切换到该身份'))
    mount()
    const panel = openPanel()
    const personalItem = Array.from(panel.querySelectorAll('[data-testid="identity-option"]'))
      .find((n) => (n.textContent || '').includes('个人版')) as HTMLElement
    fireEvent.click(personalItem)
    await new Promise((r) => setTimeout(r, 0))
    expect(getAuthToken()).toBe('tok-old')
    expect(useAuthStore.getState().user?.tenant_id).toBe(7)
    expect(document.body.textContent).toContain('无权切换到该身份')
  })
})

describe('IdentitySwitcher · 浮层形态锁（AGENTS §一·5 portal 那条）', () => {
  it('面板挂在 document.body 直下，且样式是 position:fixed', () => {
    seed({ identities: [PERSONAL, ACME] })
    mount()
    const panel = openPanel()
    expect(panel.parentElement).toBe(document.body)
    // position 收在 .idsw-panel 规则里（不是内联），所以判据问**那条规则**：
    // 早期写法 `panel.style.position === '' || === 'fixed'` 在"整个没写 position"时也会放行——
    // 一条只认"没写"或"写死 fixed"的判据，把 fixed 摘掉照样绿，属空转锁。
    const css = Array.from(document.querySelectorAll('style')).map((s) => s.textContent || '').join('\n')
    expect(css).toMatch(/\.idsw-panel\s*{[^}]*position:\s*fixed/)
    expect(css).not.toMatch(/\.idsw-panel\s*{[^}]*position:\s*absolute/)
    // 落点四值由 place() 现算写进内联样式（不是 CSS 里写死的坐标）
    expect(panel.style.left).not.toBe('')
    expect(panel.style.maxHeight).not.toBe('')
  })

  it('点面板外任意处：关闭（portal 后浮层不在触发器父节点里，外点判定必须放行自身）', () => {
    seed({ identities: [PERSONAL, ACME] })
    mount()
    expect(openPanel()).toBeTruthy()
    fireEvent.mouseDown(document.body)
    expect(document.querySelector('.idsw-panel')).toBeNull()
  })
})
