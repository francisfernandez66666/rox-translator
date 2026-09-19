// ============ WordSwap.dom.test.tsx · 职责说明 ============
// ★ D2 #24 加载动效组件断言：验证「错词打字→红线划掉→正词辉光定版」循环演出，
// prefers-reduced-motion 命中时直接落终态（错词红线 + 正词全显、不逐帧演），
// 以及 ★ 用户追加需求「多语言循环播放」：缺省词对覆盖 12 界面语种、运行时确实
// 轮到第二语种（RU）拍且语种标签前置刷新。
// 节拍为真实定时器（单拍约 3s），waitFor 超时相应放宽。
// =============================================
// @vitest-environment jsdom
import { cleanup, render, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import WordSwap, { DEFAULT_PAIRS } from './WordSwap'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  // 移除本文件注入的 matchMedia 桩（jsdom 原生没有该属性，delete 即回到干净态，
  // 防止「减弱动效」用例的 matches:true 泄漏给后续演出路径用例）
  delete (window as unknown as { matchMedia?: unknown }).matchMedia
})

const PAIRS = [{ tag: 'EN', bad: 'compare', good: 'benchmark' }]

describe('WordSwap 换词加载动效', () => {
  it('正常路径：错词逐字打出 → 挂 struck 红线 → 正词打出并定版', async () => {
    const { container } = render(<WordSwap pairs={PAIRS} />)
    const tagEl = container.querySelector<HTMLElement>('[data-ws="tag"]')!
    const wEl = container.querySelector<HTMLElement>('[data-ws="w"]')!
    const wtEl = container.querySelector<HTMLElement>('[data-ws="wt"]')!
    const rEl = container.querySelector<HTMLElement>('[data-ws="r"]')!

    // 语种标签先落位（★ 多语言循环的前置位）
    expect(tagEl.textContent).toBe('EN')
    // 第一拍：错词逐字打满
    await waitFor(() => expect(wtEl.textContent).toBe('compare'), { timeout: 5000 })
    // 第二拍：struck 挂到 w 位（红线是 wt ::before，类只挂外层）
    await waitFor(() => expect(wEl.classList.contains('struck')).toBe(true), { timeout: 3000 })
    // 第三拍：正词打满并 lock 定版（glow 在打字前挂上；lock 在打完的下一个 await 挂上，
    // 与 textContent 打满之间有毫秒级间隙，必须 waitFor 而不是同步 expect）
    await waitFor(() => expect(rEl.textContent).toBe('benchmark'), { timeout: 5000 })
    await waitFor(() => expect(rEl.classList.contains('lock')).toBe(true), { timeout: 1000 })
  })

  it('减弱动效：不演出，直接落「错词带红线 + 正词全显」终态', () => {
    // jsdom 无 window.matchMedia（组件用 ?. 守卫走演出路径），此处按 AiRegisterFlow 测试同法
    // 用 defineProperty 注入命中 reduce 的桩，而不是 spyOn（spyOn 对 undefined 直接报错）
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      value: () => ({ matches: true }),
    })
    const { container } = render(<WordSwap pairs={PAIRS} />)
    const tagEl = container.querySelector<HTMLElement>('[data-ws="tag"]')!
    const wEl = container.querySelector<HTMLElement>('[data-ws="w"]')!
    const wtEl = container.querySelector<HTMLElement>('[data-ws="wt"]')!
    const rEl = container.querySelector<HTMLElement>('[data-ws="r"]')!
    expect(tagEl.textContent).toBe('EN')
    expect(wtEl.textContent).toBe('compare')
    expect(rEl.textContent).toBe('benchmark')
    expect(wEl.classList.contains('struck')).toBe(true)
  })

  it('装饰口径：role=img + aria-label 可覆盖默认文案', () => {
    const { container } = render(<WordSwap pairs={PAIRS} ariaLabel="翻译中" />)
    const root = container.querySelector('.ws')!
    expect(root.getAttribute('role')).toBe('img')
    expect(root.getAttribute('aria-label')).toBe('翻译中')
  })
})

describe('WordSwap 多语言循环（★ 加载动效多语言播放）', () => {
  it('④ 缺省词对数据：12 拍恰好覆盖 12 界面语种，唯一 RTL 拍是阿语', () => {
    expect(DEFAULT_PAIRS.map((p) => p.tag)).toEqual(['EN', 'RU', 'JA', 'KO', 'DE', 'FR', 'ES', 'PT', 'AR', 'TH', '繁中', '简中'])
    // 每拍错词≠正词（换词语义成立）；全部非空
    for (const p of DEFAULT_PAIRS) {
      expect(p.bad.length).toBeGreaterThan(0)
      expect(p.good.length).toBeGreaterThan(0)
      expect(p.bad).not.toBe(p.good)
    }
    expect(DEFAULT_PAIRS.filter((p) => p.rtl).map((p) => p.tag)).toEqual(['AR'])
  })

  it('⑤ 运行时轮播：EN 拍演完自动进 RU 拍（标签+错词都换语种）', async () => {
    const { container } = render(<WordSwap />)
    const tagEl = container.querySelector<HTMLElement>('[data-ws="tag"]')!
    const wtEl = container.querySelector<HTMLElement>('[data-ws="wt"]')!
    await waitFor(() => expect(wtEl.textContent).toBe('compare'), { timeout: 5000 })
    // EN 拍约 3.1s 演完（打字+停留+划掉+定版+擦除），随后 RU 拍标签先换、错词开始打字
    await waitFor(() => expect(tagEl.textContent).toBe('RU'), { timeout: 8000 })
    await waitFor(() => expect(wtEl.textContent).toContain('сравнение'.slice(0, 4)), { timeout: 5000 })
  }, 20000)
})
