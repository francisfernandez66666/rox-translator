// ============================================================================
// hooks/useChat.pkgRefresh.dom.test.tsx — ★ F-11 余额刷新链回归（批G 2026-09-25）
//                                ＋ ★ F-48 广播枢纽与流终态补锁（〇-U 批 I-5 2026-09-26）
// 缺陷（F-11，已修）：聊天 SSE done 帧（points_used 落进气泡）后，顶栏「当前套餐/积分行」仍停在旧值——
//   myPackage() 只在 FrontShell 挂载时打一次，扣点不感知、套餐到期/变更也不感知（F-10 相关）。
// 修复口径：App 根持有刷新枢纽并经 PkgRefreshCtx 下发；各余额位各自订阅，
//   useChat 在流终态收尾后 debounce 2s 广播。
//
// ★ F-48（本批新增三条锁，对应三处改法）：
//   ④ 多订阅者共存：顶栏积分行 + 工作台余额条同时在场，一次广播必须**两个都收到**。
//      旧形态是单槽位可写句柄（handle.refresh = fn），第二处注册会把第一处**静默挤掉**——
//      本锁即该事故的负向复现：若有人把 hub 改回单槽，此红。
//   ⑤ 流终态含 error：扣费按实际用量实时结算（后端 ChargeUsageRealtime），中途失败/中断
//      也已经扣过一部分，旧实现只在 done 分支刷新 → 余额条停在扣费前的值。
//   ⑥ 无人订阅时不起定时器：广播前 count()===0 即返回（等价旧版「句柄未注册就不打接口」），
//      防止把 myPackage 打在没人看的界面上。
//
// 原有 debounce 语义锁（①②③）全部保留，只把句柄形态换成枢纽形态。
// 运行：npx vitest run src/hooks/useChat.pkgRefresh.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import type { ReactNode } from 'react'
import { renderHook, act, cleanup } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { setLang } from '@/i18n'
import { ChatProvider, PkgRefreshCtx, createPkgRefreshHub, useChat, type PkgRefreshHub } from '@/hooks/useChat'

// chatStream/healthCheck 换成可控桩（与 useChat.dom.test.tsx 同手法）：
// chatStream 被调用即返回外部可控的 Promise，测试手动 resolve 出「带 points_used 的 done 帧」
// 或 reject 出「流中途失败」——两条都是流终态，都必须触发余额刷新（F-48⑤）。
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
  const ctl = {
    resolve: null as unknown as (r: unknown) => void,
    reject: null as unknown as (e: unknown) => void,
  }
  h.chatStream.mockImplementation(() => new Promise((res, rej) => { ctl.resolve = res; ctl.reject = rej }))
  return ctl
}

// 单独抽出的取 hook 函数：renderHook 要求稳定的 selector 引用
function useChatForTest() { return useChat() }

/** 挂一颗 ChatProvider（外层套被测的 PkgRefreshCtx），hub 模拟 App 根持有的广播枢纽 */
function mountChat(hub: PkgRefreshHub) {
  function Wrapper({ children }: { children: ReactNode }) {
    return (
      <MemoryRouter>
        <PkgRefreshCtx.Provider value={hub}>
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

describe('useChat · F-11 done 帧后余额刷新（debounce 2s）', () => {
  it('done 帧（含 points_used）后：窗口内 0 次、满 2s 恰 1 次（等值计数锁）', async () => {
    const hub = createPkgRefreshHub()
    const refresh = vi.fn()
    hub.subscribe(refresh)
    const res = mountChat(hub)
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('hello') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: '你好', data: {}, points_used: 3 }) })
    await settle()
    expect(refresh, '未到 debounce 窗口不得抢跑余额刷新').toHaveBeenCalledTimes(0)
    act(() => { vi.advanceTimersByTime(1999) })
    expect(refresh, '1999ms 仍是 0 次（窗口边界等值锁）').toHaveBeenCalledTimes(0)
    act(() => { vi.advanceTimersByTime(1) })
    expect(refresh, '满 2s 必须且只准打一次 myPackage 刷新（F-11 等值计数锁）').toHaveBeenCalledTimes(1)
  })

  it('2s 窗口内第二条 done 与第一条合并成恰 1 次刷新（重置式 debounce，非每 done 各刷）', async () => {
    const hub = createPkgRefreshHub()
    const refresh = vi.fn()
    hub.subscribe(refresh)
    const res = mountChat(hub)
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
    expect(refresh, '两条快速 done 合并为恰 1 次余额刷新（debounce 合并语义等值锁）').toHaveBeenCalledTimes(1)
  })

  it('Provider 卸载即在途刷新作废（不得对已卸载外壳做幽灵刷新）', async () => {
    const hub = createPkgRefreshHub()
    const refresh = vi.fn()
    hub.subscribe(refresh)
    const res = mountChat(hub)
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('x') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: 'ok', data: {}, points_used: 2 }) })
    await settle()
    res.unmount() // 卸载聊天层（如切去 /admin）：cleanup 里 cancelPkgRefresh 必须清掉 2s 定时器
    act(() => { vi.advanceTimersByTime(2000) })
    expect(refresh, '卸载后在途 debounce 定时器必须作废').toHaveBeenCalledTimes(0)
  })

  it('未挂 PkgRefreshCtx（枢纽缺省）时 done 收尾不炸且不调任何刷新', async () => {
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

describe('useChat · F-48 广播枢纽与流终态（批 I-5）', () => {
  // 锁 ④：F-48③ 的核心复现。旧形态 PkgRefreshHandle 只有一个 .refresh 槽，
  // 工作台余额条后挂载就会把顶栏那份覆写成自己的，两处余额必有一处永远不刷且无报错。
  // 现在两个订阅者各收恰 1 次；若枢纽被改回单槽，第二个 subscribe 会挤掉第一个 → 此红。
  it('两个余额位同时在场：一次广播两个都收到恰 1 次（单槽位「后来者赢」负向锁）', async () => {
    const hub = createPkgRefreshHub()
    const headerSpy = vi.fn()
    const balanceBarSpy = vi.fn()
    hub.subscribe(headerSpy)   // 顶栏积分行（App.tsx FrontShell 的注册位）
    hub.subscribe(balanceBarSpy) // 工作台余额条（ChatWindow 的订阅位）
    const res = mountChat(hub)
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('多面板共存') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: 'ok', data: {}, points_used: 2 }) })
    await settle()
    act(() => { vi.advanceTimersByTime(2000) })
    expect(headerSpy, '顶栏被后挂载者挤掉＝F-48③ 复发').toHaveBeenCalledTimes(1)
    expect(balanceBarSpy, '工作台余额条没收到广播').toHaveBeenCalledTimes(1)
  })

  // 锁 ⑤：流终态不止 done——失败/中断同样已按实际用量扣过费（后端实时计费），
  // 旧实现 catch 分支不刷新，余额条停在扣费前的值。
  it('流以 error 收尾也必须刷新余额（扣费按实际用量，中断≠没花钱）', async () => {
    const hub = createPkgRefreshHub()
    const refresh = vi.fn()
    hub.subscribe(refresh)
    const res = mountChat(hub)
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('会失败的一条') })
    await act(async () => { ctl.reject(new Error('stream broke')) })
    await settle()
    expect(refresh, 'error 收尾（流终态）不得跳过余额刷新').toHaveBeenCalledTimes(0) // 窗口内仍为 0
    act(() => { vi.advanceTimersByTime(2000) })
    expect(refresh, 'error 收尾后满 2s 必须刷一次（F-48① 流终态口径）').toHaveBeenCalledTimes(1)
  })

  // 锁 ⑥：无人订阅 ⇒ 不起定时器也不打请求（旧版靠「句柄未注册」判空，枢纽必须等价保留这一性质）。
  // 反证：若把 count() 判空删掉，这里 hub.emit() 虽然没人接，但定时器已起——本锁的判据是
  // 「订阅者后来才出现时也不被那次广播叫醒」，即定时器根本没起。
  it('订阅者数为 0 时不排刷新：先 done 后订阅，新订阅者不得被那次广播叫醒', async () => {
    const hub = createPkgRefreshHub()
    const res = mountChat(hub) // 进场即挂载，但没有任何余额位订阅（如后台路由下的聊天实例）
    const ctl = armStream()
    await act(async () => { void res.result.current.sendMessage('x') })
    await act(async () => { ctl.resolve({ skill: 'translation', reply: 'ok', data: {}, points_used: 1 }) })
    await settle()
    const late = vi.fn()
    hub.subscribe(late) // 定时器若已排，这里补订阅就会被 2s 后的那次广播误叫醒
    act(() => { vi.advanceTimersByTime(2000) })
    expect(late, '无人订阅时不该排刷新（等价旧版「句柄未注册不打接口」）').toHaveBeenCalledTimes(0)
  })

  // 锁：退订即出集合（count 归零）——订阅方卸载后不得再被叫，也不得让 count 判空失真
  it('退订后不再收广播，且 count() 回到 0（订阅/退订等值闭环）', async () => {
    const hub = createPkgRefreshHub()
    const spy = vi.fn()
    const unsub = hub.subscribe(spy)
    expect(hub.count()).toBe(1)
    unsub()
    expect(hub.count(), '退订后计数必须归零（否则 schedulePkgRefresh 判空失真）').toBe(0)
    hub.emit()
    expect(spy, '已退订的订阅者不得再被叫醒').toHaveBeenCalledTimes(0)
  })

  // 锁：单个订阅者抛错不得中断其余（否则「挤掉」以另一种形态复发）
  it('第一个订阅者抛错，后续订阅者照样收到广播', () => {
    const hub = createPkgRefreshHub()
    const boom = vi.fn(() => { throw new Error('顶栏刷新炸了') })
    const survivor = vi.fn()
    hub.subscribe(boom)
    hub.subscribe(survivor)
    expect(() => hub.emit()).not.toThrow()
    expect(boom).toHaveBeenCalledTimes(1)
    expect(survivor, '一个订阅者失败不得连带饿死其余余额位').toHaveBeenCalledTimes(1)
  })
})
