// @vitest-environment jsdom
// ============================================================================
// ProductPublicPage.notFound.dom.test.tsx — R7（施工图 §5 / §4.2.3）：
// 产品不存在（publicProduct 失败 / not_found）时页面仍可用（不白屏）、挂件不崩。
//
// 反证由变异体 M0282 提供：让 publicProduct 失败时那条兜底被改坏（不再 setNotFound），
// 失败态就退化成一片空白（等价 throw 后无人接管）⇒ 本锁的「public-not-found 可见」判红。
//
// 「挂件不崩」口径：ProductPublicPage 与 <AiAssist /> 同处 PublicRoot 的 MarketingShell（R1），
// 本组件在 useEffect 里抛错/整棵崩掉会带塌同壳的挂件。故这里以「整棵子树仍渲染、
// 留资出口仍在、无同步 throw」作代理判据，而不是去 import 挂件本身（避免把后台依赖拉进公开面）。
//
// 依赖全部打桩：@/api/product 控制成败、@/ui/langcross/src 给最小控件、@/i18n 给最小 useT/tplOr，
// 让本锁只测 ProductPublicPage 自己的失败语义，不牵动懒加载面板那一大包。
// ============================================================================
import { cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ publicProduct: vi.fn() }))
vi.mock('@/api/product', () => ({ publicProduct: mocks.publicProduct }))
// 最小控件：真实 UI 库会拉一批样式/依赖，这里只关心 form/not-found/标题这些原生节点是否在。
vi.mock('@/ui/langcross/src', () => ({
  Input: () => null,
  Button: () => null,
}))
vi.mock('@/i18n', () => ({
  useT: () => ['zh', (k: string) => k, (k: string) => k],
  tplOr: (k: string) => k,
  getLang: () => 'zh',
  setLang: () => {},
  useLang: () => 'zh',
}))

import ProductPublicPage from './ProductPublicPage'

beforeEach(() => {
  cleanup()
  mocks.publicProduct.mockReset()
})
afterEach(() => cleanup())

/** 页面「仍可用」的三件：主容器在、标题在、留资出口在（不白屏的最小充分证据） */
function expectPageUsable() {
  expect(document.querySelector('main.lc-pp-main'), '主容器 <main> 不在 ⇒ 页面白屏').toBeTruthy()
  expect(document.querySelector('main.lc-pp-main h1'), '标题不在 ⇒ 页面白屏').toBeTruthy()
  expect(document.querySelector('[data-testid="lead-form"]'), '留资出口不在 ⇒ 页面不可用').toBeTruthy()
}

describe('R7 产品不存在时页面仍可用、挂件不崩', () => {
  it('R7 publicProduct 失败（reject）时兜底：整棵子树在、not-found 可见', async () => {
    mocks.publicProduct.mockRejectedValue(new Error('upstream down'))
    expect(() => render(<ProductPublicPage tenantCode="acme" pageCode="9f3a" />), '渲染就抛错 ⇒ 挂件所在外壳会被带塌').not.toThrow()
    expectPageUsable()
    await waitFor(() =>
      expect(screen.queryByTestId('public-not-found'), 'R7 产品不存在时页面白屏：public-not-found 没渲染（失败兜底被改坏）').toBeTruthy(),
    )
  })

  it('R7 正向对照：publicProduct 回 not_found 时同样出 not-found（不是只对 reject 生效）', async () => {
    mocks.publicProduct.mockResolvedValue({ success: false, not_found: true })
    render(<ProductPublicPage tenantCode="acme" pageCode="9f3a" />)
    expectPageUsable()
    await waitFor(() => expect(screen.queryByTestId('public-not-found'), 'R7 not_found 态 public-not-found 没渲染（兜底被改坏）').toBeTruthy())
  })

  it('R7 射程自证：成功态真渲染出事实、且不出 not-found（证明上两条不是对着恒空页恒真）', async () => {
    mocks.publicProduct.mockResolvedValue({
      success: true,
      product: { name: 'Acme Widget', tagline: 'tag' },
      facts: [{ id: 1, key: '交期', text: '9 天', visibility: 'public' }],
      gated_count: 0,
    })
    render(<ProductPublicPage tenantCode="acme" pageCode="9f3a" />)
    await waitFor(() => expect(document.querySelector('main.lc-pp-main')?.textContent).toContain('Acme Widget'))
    expect(screen.queryByTestId('public-not-found'), '成功态不该出现 not-found').toBeNull()
  })
})
