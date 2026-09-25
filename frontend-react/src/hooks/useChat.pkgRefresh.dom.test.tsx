// ============================================================================
// hooks/useChat.pkgRefresh.dom.test.tsx — ★ F-11 顶栏积分刷新链回归（批G 2026-09-25）
// 缺陷：聊天 SSE done 帧（points_used 落进气泡）后，顶栏「当前套餐/积分行」仍停在旧值——
//   myPackage() 只在 FrontShell 挂载时打一次，扣点不感知、套餐到期/变更也不感知（F-10 相关）。
// 修复口径：App 根持有可变句柄（PkgRefreshHandle）经 PkgRefreshCtx 下发；FrontShell 注册
//   refreshPkgLine，useChat 在 done 收尾后 debounce 2s 回调句柄。
// 本测以假定时器锁 debounce 语义：
//   ① done 帧后 2s 内顶栏刷新函数被调到「恰 1 次」（等值计数锁，窗口内为 0 次）；
//   ② 2s 窗口内第二条 done 合并进同一次刷新（重置式 debounce，不是每 done 各刷一次）；
//   ③ Provider 卸载即在途定时器作废（不得卸载后幽灵刷新）。
// 运行：npx vitest run src/hooks/useChat.pkgRefresh.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { ReactNode } from 'react'
import { renderHook, act, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { setLang } from '@/i18n'
import { ChatProvider, PkgRefreshCtx, useChat, type PkgRefreshHandle } from '@/hooks/useChat'

// chatStream/healthCheck 换成可控桩（与 useChat.dom.test.tsx 同手法）：
// chatStream 被调用即返回外部可控的 Promise，测试手动 resolve 出「带 points_used 的 done 帧」
const h = vi.hoisted(() => ({
  chatStream: vi.fn(),
  healthCheck: vi.fn(async () => ({ status: 'ok' })),
}))
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return { ...actual, chatStream: h.chatStream, healthCheck: h.healthCheck }
})
const confirmDialog = vi.hoisted(() => vi.fn(async () => false))
vi.mock('@/components/uiDialogs', () => ({ confirmDialog }))

import { useAuthStore } from '@/stores/auth'

/** 起一次发送并拿到 done 帧控制器（resolve 即触发 useChat 的 done 收尾 → schedulePkgRefresh） */
function armStream() {
  const ctl = { resolve: null as unknown as (r: unknown) => void }
  h.chatStream.mockImplementation(() => new Promise((res) => { ctl.resolve = res }))
  return ctl
}

// 单独抽出的取 hook 函数：renderHook 要求稳定的 selector 引用
function useChatForTest() { return useChat() }

/** 挂一颗 ChatProvider（外层套被测的 PkgRefreshCtx），handle 模拟 App 根持有的可变句柄 */
function mountChat(handle: PkgRefreshHandle) {
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <MemoryRouter>
        <PkgRefreshCtx.Provider value={handle}>
          <ChatProvider>{children}</ChatProvider>
        </PkgRefreshCtx.Provider>
      </MemoryRouter>
    )
  }
  return renderHook(() => useChatForTest(), { wrapper: Wrapper })
}

/** 把 promise 微任务队列跑干（done 收尾是 async 链，act 内需先落定再推定时器） */
async function settle() { await act(async () => { await Promise.resolve() }) }

beforeEach(() => {
  cleanup()
  setLang('zh')
  h.chatStream.mockReset()
  h.healthCheck.mockClear()
  confirmDialog.mockClear()
  useAuthStore.setState({ user: null, restoring: false })
  vi.useFakeTimers() // debounce 2s / persist 400ms 全部由测试手动推进，假定时器即等价「2s 后」
})
afterEach(() => {
  vi.useRealTimers()
  cleanup()
})

describe('useChat · F-11 done 帧后顶栏积分刷新（debounce 2s）', () => {
  it('done 帧（含 points_used）后：窗口内 0 次、满 2s 恰 1 次（等值计数锁）', async () => {
    const refresh = vi.fn()
    const handle: PkgRefreshHandle = { refresh }
    const res = mountChat(handle)
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('hello') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: '你好', data: {}, points_used: 3 }) })
    await settle()
    expect(refresh, '未到 debounce 窗口不得抢跑顶栏刷新').toHaveBeenCalledTimes(0)
    act(() => { vi.advanceTimersByTime(1999) })
    expect(refresh, '1999ms 仍是 0 次（窗口边界等值锁）').toHaveBeenCalledTimes(0)
    act(() => { vi.advanceTimersByTime(1) })
    expect(refresh, '满 2s 必须且只准打一次 myPackage 刷新（F-11 等值计数锁）').toHaveBeenCalledTimes(1)
  })

  it('2s 窗口内第二条 done 与第一条合并成恰 1 次刷新（重置式 debounce，非每 done 各刷）', async () => {
    const refresh = vi.fn()
    const handle: PkgRefreshHandle = { refresh }
    const res = mountChat(handle)
    const ctl1 = armStream()
    await act(async () => { void res.result.current.sendMessage('第一条') })
    await act(async () => { ctl1.resolve({ skill: 'translation', reply: 'a', data: {}, points_used: 1 }) })
    await settle()
    act(() => { vi.advanceTimersByTime(1000) }) // 第一条 done 后过 1s，窗口未关
    const ctl2 = armStream()
    await act(async () => { void res.result.current.sendMessage('第二条') })
    await act(async () => { ctl2.resolve({ skill: 'translation', reply: 'b', data: {}, points_used: 1 }) })
    await settle()
    act(() => { vi.advanceTimersByTime(999) }) // 距第一条已 1999ms——若无重置语义这里已刷过
    expect(refresh, '第二条 done 应重置计时，第一条的 2s 到点不得触发刷新').toHaveBeenCalledTimes(0)
    act(() => { vi.advanceTimersByTime(1001) }) // 距第二条满 2s
    expect(refresh, '两条快速 done 合并为恰 1 次顶栏刷新（debounce 合并语义等值锁）').toHaveBeenCalledTimes(1)
  })

  it('Provider 卸载即在途刷新作废（不得对已卸载外壳做幽灵刷新）', async () => {
    const refresh = vi.fn()
    const handle: PkgRefreshHandle = { refresh }
    const res = mountChat(handle)
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('x') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: 'ok', data: {}, points_used: 2 }) })
    await settle()
    res.unmount() // 卸载聊天层（如切去 /admin）：cleanup 里 cancelPkgRefresh 必须清掉 2s 定时器
    act(() => { vi.advanceTimersByTime(2000) })
    expect(refresh, '卸载后在途 debounce 定时器必须作废').toHaveBeenCalledTimes(0)
  })

  it('未挂 PkgRefreshCtx（句柄缺省）时 done 收尾不炸且不调任何刷新', async () => {
    const res = renderHook(() => useChatForTest(), {
      wrapper: ({ children }: { children: ReactNode }) =>
        <MemoryRouter><ChatProvider>{children}</ChatProvider></MemoryRouter>,
    })
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('x') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: 'ok', data: {}, points_used: 1 }) })
    await settle()
    act(() => { vi.advanceTimersByTime(2000) })
    // 后台路由等无顶栏场景：静默跳过（本测的「不炸」即走到这里 store 状态仍自洽）
    expect(res.result.current.isLoading).toBe(false)
    expect(res.result.current.messages.some((m) => m.content === 'ok')).toBe(true)
  })
})
