// @vitest-environment jsdom
// ============================================================================
// ui/langcross/src/useReveal.dom.test.tsx — 滚动揭示 hook 的回归测试
//
// 重点回归「观察区死区」这个真实缺陷（2026-09-18 首页实测踩到）：
//   根观察区下缘内收 12% 后，文档最末尾的矮元素（如收尾 CTA 的按钮）
//   即使滚到底也够不到那条边 —— 实测 16 个揭示元素里有 3 个永远停在 opacity:0。
//   本文件把 IntersectionObserver 桩成「永不投递条目」，只验证滚到底的兜底路径。
// ============================================================================

import { act, cleanup, render } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useReveal } from './useReveal'

// 桩：IntersectionObserver 永不投递条目 —— 精确复现死区（元素永远进不了观察区）
class SilentIO {
  observe() { /* 永不投递 */ }
  unobserve() { /* noop */ }
  disconnect() { /* noop */ }
  takeRecords() { return [] }
}

function Demo() {
  const ref = useReveal<HTMLDivElement>()
  return (
    <div ref={ref}>
      <div className="lc-reveal"data-testid="a"/>
      <div className="lc-reveal"data-testid="b"/>
    </div>
  )
}

/** jsdom 里 innerHeight / scrollY / scrollHeight 恒为 0，必须伪造才能构造「滚到底」 */
function setScroll(y: number, innerH: number, scrollH: number) {
  Object.defineProperty(window, 'innerHeight', { value: innerH, configurable: true })
  Object.defineProperty(window, 'scrollY', { value: y, configurable: true })
  Object.defineProperty(document.documentElement, 'scrollHeight', { value: scrollH, configurable: true })
}

/** 推进一帧，让 hook 内的 requestAnimationFrame 回调落地 */
async function nextFrame() {
  await act(async () => {
    await new Promise<void>((r) => requestAnimationFrame(() => r()))
  })
}

describe('useReveal · 观察区死区兜底', () => {
  beforeEach(() => { vi.stubGlobal('IntersectionObserver', SilentIO) })
  afterEach(() => { vi.unstubAllGlobals(); cleanup() })

  it('未滚到底时不提前点亮（保持原有的观察节拍）', async () => {
    setScroll(0, 800, 2000)
    const { getByTestId } = render(<Demo />)
    await nextFrame()
    expect(getByTestId('a').classList.contains('is-in')).toBe(false)
    expect(getByTestId('b').classList.contains('is-in')).toBe(false)
  })

  it('滚到文档末尾时，观察器没点亮的元素必须被兜底点亮', async () => {
    setScroll(0, 800, 2000)
    const { getByTestId } = render(<Demo />)
    // 800(视口) + 1200(已滚) >= 2000(文档高) → 判定为已到底
    Object.defineProperty(window, 'scrollY', { value: 1200, configurable: true })
    await act(async () => { window.dispatchEvent(new Event('scroll')) })
    await nextFrame()
    expect(getByTestId('a').classList.contains('is-in')).toBe(true)
    expect(getByTestId('b').classList.contains('is-in')).toBe(true)
  })

  it('容器内没有 .lc-reveal 时不注册监听、不抛错', async () => {
    setScroll(0, 800, 2000)
    function Empty() {
      const ref = useReveal<HTMLDivElement>()
      return <div ref={ref}><span data-testid="plain"/></div>
    }
    const { getByTestId } = render(<Empty />)
    await nextFrame()
    expect(getByTestId('plain')).toBeTruthy()
  })
})
