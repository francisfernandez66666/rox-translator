// ============ WordSwap.dom.test.tsx · 职责说明 ============
// ★ D2 #24 加载动效组件断言：验证「错词打字→红线划掉→正词辉光定版」循环演出，
// prefers-reduced-motion 命中时直接落终态（错词红线 + 正词全显、不逐帧演），
// 以及 ★ 用户追加需求「多语言循环播放」：缺省词对覆盖 12 界面语种、运行时确实
// 轮到第二语种（RU）拍且语种标签前置刷新。
// 节拍为真实定时器（单拍约 3s），waitFor 超时相应放宽。
// ★ 2026-09-22 追加需求「加载动效要久一点、至少读完三个语言再载入」的三条锁：
//  ⑥ WordSwap 每演完一拍（正词定版停留结束）回传累计拍数；
//  ⑦ useWordSwapGate 在 loading 撤销后仍按住占位，直到补足 minBeats 拍；
//  ⑧ 未发生加载 / 命中减弱动效 两种情形下闸门不补拍（不许凭空卡首屏）。
// =============================================
// @vitest-environment jsdom
import { act, cleanup, render, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { renderHook } from '@testing-library/react'
import WordSwap, { DEFAULT_PAIRS, MIN_READ_BEATS, useWordSwapGate } from './WordSwap'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  // 移除本文件注入的 matchMedia 桩（jsdom 原生没有该属性，delete 即回到干净态，
  // 防止「减弱动效」用例的 matches:true 泄漏给后续演出路径用例）
  delete (window as unknown as { matchMedia?: unknown }).matchMedia
})

// 单拍词对基线：多数演出用例只需一组 bad→good，断言集中在这对上
const PAIRS = [{ tag: 'EN', bad: 'compare', good: 'benchmark' }]
// 两拍词对：给「每拍回传累计拍数」用，避免动效自己演完不计数
const PAIRS2 = [PAIRS[0], { tag: 'RU', bad: 'сравнение', good: 'бенчмарк' }]

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
    // 错词只等前 4 个字符（不是整个词）：RU 词打得慢，等满词会把断言推到下一拍的边界上；
    // 「前缀已出现」足以证明换语种后真的在重新打字，而不是停留在 EN 的残影。
    await waitFor(() => expect(wtEl.textContent).toContain('сравнение'.slice(0, 4)), { timeout: 5000 })
  }, 20000)
})

describe('WordSwap 载入闸门（★ 2026-09-22「至少读完三个语言再载入」）', () => {
  // 本 describe 走真实定时器，⑥ 一条就要二十多秒——慢是设计使然，不要为了提速改写法：
  // 换成 vi.useFakeTimers 后拍数会在瞬间跑完，用例只剩「计数器的算术」，
  // 验不到用户端的实质语义（三拍是否读得完），#66 就会被静默掏空。
  it('⑥ 每演完一拍回传累计拍数（1 → 2，正词定版停留之后才计数）', async () => {
    // onBeat 用 vi.fn() 包一层再接：既能查调用序列，又不改变组件自身的计数实现。
    const onBeat = vi.fn()
    // ★ 时长锁（#66 的实质口径是「读得完」而非「闪过了」）：记录每拍完成时刻。
    // 任何人把 SP.hold 调短都会静默废掉「至少读完三个语言」这条需求，而计次断言仍全绿。
    // 实测单拍约 2.7~3.1s，取下限 2000ms（机器卡顿只会让它变长，不会变短）。
    const t0 = Date.now()
    const stamps: number[] = []
    render(<WordSwap pairs={PAIRS2} onBeat={(n) => { stamps.push(Date.now() - t0); onBeat(n) }} />)
    await waitFor(() => expect(onBeat).toHaveBeenCalledWith(1), { timeout: 8000 })
    await waitFor(() => expect(onBeat).toHaveBeenCalledWith(2), { timeout: 12000 })
    // 只回传「读得完」的拍：一拍的计数严格递增，不会把打字中途算成一拍
    expect(onBeat.mock.calls.map((c) => c[0])).toEqual([1, 2])
    expect(stamps[1] - stamps[0], `单拍仅 ${stamps[1] - stamps[0]}ms，正词停留被调短`).toBeGreaterThanOrEqual(2000)
  }, 25000)

  it('⑦ loading 撤销后仍按住占位，补足 minBeats 拍才放行', () => {
    // 闸门是纯状态判定（busy = loading || played < need），内部无定时器，
    // 所以这里可以 act + 同步 expect 逐步推进，不必 waitFor，也不受机器快慢影响；
    // WordSwap 的演出与拍数由组件自己驱动，本用例只喂它「演到第几拍」这个事实。
    const { result, rerender } = renderHook(
      ({ loading }: { loading: boolean }) => useWordSwapGate(loading),
      { initialProps: { loading: true } },
    )
    expect(result.current.busy).toBe(true)
    // 接口先回来：此时一拍未演完，占位不能收（这正是旧行为「闪一下就没了」的成因）
    act(() => rerender({ loading: false }))
    expect(result.current.busy).toBe(true)
    // 边界取 minBeats-1 / minBeats 两点：只测「满 3 拍放行」会漏掉 off-by-one，
    // 差一拍就收场（用户读到第 2 个语种被打断）正是这条需求最直观的失败形态。
    act(() => { result.current.onBeat(MIN_READ_BEATS - 1) })
    expect(result.current.busy).toBe(true)
    act(() => { result.current.onBeat(MIN_READ_BEATS) })
    expect(result.current.busy).toBe(false)
  })

  it('⑧ 未发生加载 / 减弱动效：闸门不补拍，不凭空卡首屏', () => {
    // 本条是 ⑦ 的**反向**锁：闸门只在「确实发生过加载」的那次挂载上补拍，不是无条件卡首屏。
    // ⑦ 全程从 loading:true 起步，证明不了 need 的抬起时机；情形一补的正是这个洞
    // （实现里 need 初值为 0、仅由 loading 抬起，写成「无条件 need=minBeats」的话 ⑦ 仍全绿，
    //   而未登录直出登录页的访客会被凭空多按 9 秒——这条先红）。
    // 情形一：restoring 恒 false（无 token 直出登录页），首屏不该被动效拖住
    const a = renderHook(({ loading }: { loading: boolean }) => useWordSwapGate(loading), { initialProps: { loading: false } })
    expect(a.result.current.busy).toBe(false)
    a.unmount()
    // 情形二：系统偏好「减弱动效」，WordSwap 直接落终态不循环，闸门只跟随 loading 本身
    //         ——此时一拍都不会回传，若 busy 还要求 played≥need，命中该偏好的人永远进不去。
    //         matchMedia 桩用 defineProperty 注入（同「减弱动效」那条，jsdom 原生没有该属性）。
    Object.defineProperty(window, 'matchMedia', { configurable: true, value: () => ({ matches: true }) })
    const b = renderHook(({ loading }: { loading: boolean }) => useWordSwapGate(loading), { initialProps: { loading: true } })
    expect(b.result.current.busy).toBe(true)
    act(() => b.rerender({ loading: false }))
    expect(b.result.current.busy).toBe(false)
  })

  it('⑨ 口径常量锁：闸门至少 3 拍，且缺省词对够排 3 个不同语种', () => {
    // 「三个语言」是用户明确需求，落在 MIN_READ_BEATS 上；词对不足 3 拍时会循环回 EN，
    // 用户看到的仍是同一批语种，等于需求被悄悄打折，故此处一并锁住。
    expect(MIN_READ_BEATS).toBe(3)
    expect(DEFAULT_PAIRS.length).toBeGreaterThanOrEqual(MIN_READ_BEATS)
    expect(new Set(DEFAULT_PAIRS.map((p) => p.tag)).size).toBeGreaterThanOrEqual(MIN_READ_BEATS)
  })
})
