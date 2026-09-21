// ============================================================================
// lib/timerLeak.dom.test.tsx — ★ #42 前端坏味道闸门：定时器必须随卸载/依赖变化清理
//
// 为什么钉这一条：验证码冷却倒计时此前在 Login.tsx 与 modals.tsx（改密弹窗、换邮箱弹窗两处）
// 各自手写 `window.setInterval`，句柄只活在事件回调闭包里、且只在「递减到 0」那一次自清。
// 后果是组件先卸载（弹窗关闭 / 路由切走）时无人 clearInterval —— 定时器最长再空跑 59 次、
// 每秒对已销毁组件 setState 一次；这类泄漏在 React 18 下不再告警，只能靠断言钉住。
// lib/useCountdown.ts 把这一类收口（句柄进 ref + `useEffect(() => stop, [stop])`）。
//
// 本测做两件事：
//   ① 正向锁 useCountdown 的三条口径：卸载即清、归零自停、重复启动互斥（不叠加定时器）；
//   ② 反向复现旧写法（用例「旧写法对照」）——卸载后定时器仍在跑，一旦有人把调用点
//      改回手写 setInterval 就失去这层说明。全部用 vi.useFakeTimers()，不依赖真实时间。
// 注意：本仓 vitest 未开 globals，RTL 不会自动卸载 → 本文件必须自己 afterEach(cleanup)。
// 运行：npx vitest run src/lib/timerLeak.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { act, cleanup, render } from '@testing-library/react'
import { useState } from 'react'
import { useCountdown } from './useCountdown'

beforeEach(() => {
  vi.useFakeTimers()
})

afterEach(() => {
  // 顺序有讲究：先 cleanup 卸载组件（触发 useCountdown 的卸载清理），再丢弃 fake timer 并还原真实计时器，
  // 否则上一个用例残留的 interval 会跟着下一个用例一起跳，断言就失去意义了。
  cleanup()
  vi.clearAllTimers()
  vi.useRealTimers()
})

/** 探针组件：把剩余秒数渲染成文本，便于「卸载后不再有任何更新」这一断言可观测 */
function Probe() {
  const { left, start, stop } = useCountdown(60)
  return (
    <div>
      <span data-testid="left">{left}</span>
      <button data-testid="start" onClick={start}>start</button>
      <button data-testid="restart" onClick={() => { start(); start() }}>restart</button>
      <button data-testid="stop" onClick={stop}>stop</button>
    </div>
  )
}

/** 读探针当前渲染的剩余秒数 */
const leftOf = (byTestId: (k: string) => HTMLElement) => Number(byTestId('left').textContent)

describe('useCountdown · 定时器清理口径（#42）', () => {
  it('① 卸载即清：弹窗关闭后不再有第二个 interval 在跳', () => {
    const { getByTestId, unmount } = render(<Probe />)
    act(() => { getByTestId('start').click() })
    expect(leftOf(getByTestId), '启动后应显示满值 60').toBe(60)

    act(() => { vi.advanceTimersByTime(20_000) })
    expect(leftOf(getByTestId)).toBe(40)
    expect(vi.getTimerCount()).toBe(1)

    // 冷却中途卸载（等价于用户关掉改密弹窗）
    unmount()
    expect(vi.getTimerCount(), '卸载后不得留下任何 interval（旧写法翻车处）').toBe(0)
    // 再推进一整轮：没有定时器可跑，也就没有任何 setState 打到已销毁组件
    act(() => { vi.advanceTimersByTime(120_000) })
    expect(vi.getTimerCount()).toBe(0)
  })

  it('② 归零自停：60 次 tick 后落到 0 并把定时器清掉', () => {
    const { getByTestId } = render(<Probe />)
    act(() => { getByTestId('start').click() })
    act(() => { vi.advanceTimersByTime(60_000) })
    expect(leftOf(getByTestId)).toBe(0)
    expect(vi.getTimerCount(), '归零后必须自清，不能继续每秒回调').toBe(0)
  })

  it('③ 重复启动互斥：两次 start 只有一个定时器，60s 不会被打成 30s', () => {
    const { getByTestId } = render(<Probe />)
    act(() => { getByTestId('restart').click() })
    expect(vi.getTimerCount(), 'start 内部先 stop 旧句柄，只允许存在一个 interval').toBe(1)
    act(() => { vi.advanceTimersByTime(1000) })
    expect(leftOf(getByTestId), '一秒只能减 1（两个定时器并存会减到 58）').toBe(59)
  })

  it('④ stop 立即停：手动停止后归零且无残留定时器', () => {
    const { getByTestId } = render(<Probe />)
    act(() => { getByTestId('start').click() })
    act(() => { vi.advanceTimersByTime(5000) })
    act(() => { getByTestId('stop').click() })
    expect(leftOf(getByTestId)).toBe(0)
    act(() => { vi.advanceTimersByTime(30_000) })
    expect(vi.getTimerCount()).toBe(0)
    expect(leftOf(getByTestId), '停止后不应被后续 tick 复活').toBe(0)
  })
})

// —— 反向对照：#42 改动前 modals.tsx / Login.tsx 的真实写法（勿抄）——
// 这里刻意把旧实现重写一遍，只为了让上面 ① 的「卸载即清」有对照组：
// 同样推进 60s，旧写法卸载后 interval 仍然活着（vi.getTimerCount() === 1），
// 每秒继续回调一次 setState。谁将来把调用点退回手写 setInterval，这层说明就白写了。
describe('旧写法对照（说明 useCountdown 存在的理由）', () => {
  function LegacyProbe() {
    const [cooldown, setCooldown] = useState(0)
    // 缺陷 1：句柄只在闭包里，组件卸载时无从清理；缺陷 2：clearInterval 藏在 setState 更新函数内
    const send = () => {
      setCooldown(60)
      const iv = window.setInterval(() => {
        setCooldown((c) => { if (c <= 1) { window.clearInterval(iv); return 0 } return c - 1 })
      }, 1000)
    }
    return (
      <div>
        <span data-testid="legacy-left">{cooldown}</span>
        <button data-testid="legacy-start" onClick={send}>send</button>
      </div>
    )
  }

  it('卸载后定时器继续空跑（正是 useCountdown 要消灭的行为）', () => {
    const { getByTestId, unmount } = render(<LegacyProbe />)
    act(() => { getByTestId('legacy-start').click() })
    act(() => { vi.advanceTimersByTime(20_000) })
    expect(Number(getByTestId('legacy-left').textContent)).toBe(40)

    unmount()
    expect(vi.getTimerCount(), '旧写法：卸载不清理，泄漏的 interval 还在跳').toBe(1)
    // 新写法（上面用例 ①）在同一位置断言的是 0 —— 两条一起看才是本文件的完整口径
    act(() => { vi.advanceTimersByTime(5000) })
    expect(vi.getTimerCount(), '泄漏的 interval 会一直跑到自己归零').toBe(1)
  })
})
