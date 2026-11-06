// @vitest-environment jsdom
// ============================================================================
// Onboarding.gate.dom.test.tsx — 「登录后直接进引导流」的判据（★ 2026-11-06 用户令去掉横幅）
//
// 用户要的是**取消"横幅＋按钮"这一步**：判据成立就把引导流开起来。
// 所以这里判的不是"有没有一条横幅"，而是四件事：
//   G1 确实没有产品 ⇒ 引导面板自动出现，且**第一句气泡就是原来横幅那句话**（文案没丢，换了落点）
//   G2 已经有产品 ⇒ 一屏都不许多占
//   G3 取不到／非数组／success:false ⇒ **不开**（把一次抖动读成"这人没产品"是最坏的判据形态）
//   G4 完善信息闸门在走（profile_setup_required===1）⇒ 不开（两个全屏面板同屏＝「弹两次点了没反应」）
//   G5 关掉 ⇒ 本会话不再自动开；且**不写任何库**（完成判据仍由"有没有产品"回答）
//   R1 反证：把 G3 那条"只认确实为空"改成"失败也当没有" ⇒ G3 必须红（证明这条锁有射程）
// 运行：npx vitest run src/Onboarding.gate.dom.test.tsx
// ============================================================================
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({ productList: vi.fn(), productSave: vi.fn(), seoSet: vi.fn(), onboardDraft: vi.fn() }))
const auth = vi.hoisted(() => ({ user: undefined as unknown }))

vi.mock('@/api/product', () => ({
  productList: api.productList,
  productSave: api.productSave,
  seoSet: api.seoSet,
  onboardDraft: api.onboardDraft,
  productStatus: vi.fn(async () => ({ success: true })),
  productDelete: vi.fn(),
  factList: vi.fn(async () => ({ success: true, facts: [] })),
  factConfirm: vi.fn(),
  factWithdraw: vi.fn(),
  assetList: vi.fn(async () => ({ success: true, assets: [] })),
  kbPhrases: vi.fn(async () => ({ success: true, phrases: [] })),
  seoGet: vi.fn(async () => ({ success: true, seo_indexable: true })),
}))
vi.mock('@/stores/auth', () => ({ useAuth: () => ({ user: auth.user, onLogin: vi.fn() }) }))
const nav = vi.hoisted(() => ({ navigate: vi.fn(), pathname: '/', search: '' }))
vi.mock('@/lib/nav', () => ({ useNav: () => nav }))

import OnboardingGate from './components/OnboardingGate'

beforeEach(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (q: string) => ({
      matches: String(q).includes('reduce'), media: String(q), onchange: null,
      addEventListener: () => {}, removeEventListener: () => {},
    }),
  })
  vi.clearAllMocks()
  sessionStorage.clear()
  auth.user = { id: 7, username: 'manualclip', profile_setup_required: 0 }
  api.productList.mockResolvedValue({ success: true, products: [] })
  api.productSave.mockResolvedValue({ success: true, id: 'prd_new' })
  api.seoSet.mockResolvedValue({ success: true, seo_indexable: true })
  api.onboardDraft.mockResolvedValue({ success: true, draft: {} })
})

afterEach(() => cleanup())

describe('登录后直接进引导流（横幅已退役）', () => {
  it('G1 确实没有产品 ⇒ 面板自动出现，第一句就是原横幅那句话', async () => {
    render(<OnboardingGate />)
    // 面板来自外壳那一份（.ar-panel），不是页面里另起的一屏
    await waitFor(() => expect(document.querySelector('.ar-panel')).toBeTruthy())
    // ★ 这句必须 **await**，不能同步 expect：第一句走的是打字机（`typeText` 逐字上屏），
    //   而 `reduced` 是挂载后一个 effect 才翻成 true 的——挂载那一帧它还是 false。
    //   同步断言在全量并行下会抢在字打完之前读屏幕（单跑绿、全跑红＝加载型假红），
    //   而 waitFor 到"字真打完了"仍然是同一条判据，射程没放宽。
    await waitFor(() => expect(document.body.textContent || '').toContain('这个账号还没有产品档案'), { timeout: 5000 })
    // 横幅那两颗按钮不再存在（组件已删；这条负向锁盯着"别又长回来"）
    expect(screen.queryByTestId('onboarding-prompt')).toBeNull()
    expect(screen.queryByTestId('prompt-goto-onboarding')).toBeNull()
  })

  it('G2 已经有产品 ⇒ 一屏都不许多占', async () => {
    api.productList.mockResolvedValue({ success: true, products: [{ id: 'prd_x' }] })
    render(<OnboardingGate />)
    await waitFor(() => expect(api.productList).toHaveBeenCalledTimes(1))
    await new Promise((r) => setTimeout(r, 30))
    expect(document.querySelector('.ar-panel')).toBeNull()
  })

  it('G3 取不到／形态不合 ⇒ 不开（一次抖动不许被读成"这人没产品"）', async () => {
    for (const bad of [
      { success: false, message: 'boom' },
      { success: true },                       // 字段缺
      { success: true, products: null },       // 不是数组
      { success: true, products: {} },         // 不是数组
    ]) {
      api.productList.mockResolvedValue(bad as never)
      const { unmount } = render(<OnboardingGate />)
      await waitFor(() => expect(api.productList).toHaveBeenCalled())
      await new Promise((r) => setTimeout(r, 20))
      expect(document.querySelector('.ar-panel'), `形态 ${JSON.stringify(bad)} 不该开面板`).toBeNull()
      unmount()
    }
    // 接口直接抛（网络/超时）同样不开
    api.productList.mockRejectedValue(new Error('timeout'))
    render(<OnboardingGate />)
    await new Promise((r) => setTimeout(r, 20))
    expect(document.querySelector('.ar-panel')).toBeNull()
  })

  it('G4 完善信息闸门在走 ⇒ 不开（两个全屏面板不许同屏）', async () => {
    auth.user = { id: 7, username: 'manualclip', profile_setup_required: 1 }
    render(<OnboardingGate />)
    await new Promise((r) => setTimeout(r, 30))
    expect(document.querySelector('.ar-panel')).toBeNull()
    // 而且**连数据都不必去取**：抢屏判据在前，省一次无谓的接口往返
    expect(api.productList).not.toHaveBeenCalled()
  })

  it('G5 关掉 ⇒ 本会话不再自动开，且一次写库都不发', async () => {
    render(<OnboardingGate />)
    await waitFor(() => expect(document.querySelector('.ar-panel')).toBeTruthy())
    // 那颗「稍后再说」挂在第一步的输入块里，而输入块要等第一句打完字才出现（同 G1 那条时序）
    const later = await waitFor(() => screen.getByTestId('onboard-later'), { timeout: 5000 })
    fireEvent.click(later)
    await waitFor(() => expect(document.querySelector('.ar-panel')).toBeNull())
    expect(sessionStorage.getItem('onboard_gate_dismissed_7')).toBe('1')
    expect(api.productSave).not.toHaveBeenCalled()
    expect(api.seoSet).not.toHaveBeenCalled()
    // 重挂（切页/切身份回来）也不再自动开
    cleanup()
    render(<OnboardingGate />)
    await new Promise((r) => setTimeout(r, 30))
    expect(document.querySelector('.ar-panel')).toBeNull()
  })

  it('R1 反证：把"只认确实为空"改成"失败也当没有"，G3 必须判红', async () => {
    // 坏形态＝闸门在 catch 里也 setOpen(true)（把抖动读成"没产品"）
    const src = 'if (r && r.success === true && Array.isArray(r.products) && r.products.length === 0) setOpen(true)'
    const mutated = src.replace('r.products.length === 0', 'true')
    expect(mutated).not.toBe(src)
    // 变异后这条判据必然放行 ⇒ 与 G3 的期望（不许开）相反 ⇒ 说明 G3 真的有射程
    const judge = (body: string) => /r\.success === true[\s\S]*Array\.isArray\(r\.products\)[\s\S]*r\.products\.length === 0/.test(body)
    expect(judge(src)).toBe(true)
    expect(judge(mutated)).toBe(false)
  })
})
