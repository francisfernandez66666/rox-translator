// ============================================================================
// components/Bell.dom.test.tsx — ★ F-11 顺手项：铃铛展开直拉列表回归（批G 2026-09-25）
// 缺陷：toggle 展开时调的 refresh 闭包里 open 还是翻转前的 false——那一次只刷未读数
//   不拉列表，列表全靠 open 翻转 → refresh 换身份 → useEffect 重跑兜底（异步双跳，
//   慢网下浮层先闪 EmptyState）。修复后 toggle 内直接 loadList()。
// 断言两把锁：
//   ① 展开后 1s 内 EmptyState（.lc-empty）消失——列表到手，空态让位；
//   ② stale-refresh 判别锁：notifications() 被调起的那一刻，DOM 里还不应存在 .bell-panel
//     （React effect 在 commit 之后才跑）——即「列表请求在点击处理器内同步发出」，
//     而不是等 open 落定、effect 兜底再发。旧实现该锁必红（列表请求只发生在 effect 轮）。
// 运行：npx vitest run src/components/Bell.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { render, fireEvent, waitFor, cleanup } from '@testing-library/react'
import { setLang } from '@/i18n'
import { notifications, notificationsUnread } from '@/api'
import Bell from '@/components/Bell'

// 通知接口换成可控桩：unread 恒 1，列表回一条通知（保留其余 api 导出原样，避免牵连别的 import）
const h = vi.hoisted(() => ({
  /** notifications() 被调起那一刻 .bell-panel 是否已在 DOM（effect 兜底路径下会是 true） */
  panelPresentAtCall: null as boolean | null,
  calls: 0,
}))
vi.mock('@/api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/api')>()
  return {
    ...actual,
    notifications: vi.fn(async () => {
      h.panelPresentAtCall = !!document.querySelector('.bell-panel')
      h.calls += 1
      return { success: true, notifications: [{ id: 1, title: '套餐到期提醒', body: '三日后到期', created_at: '2026-09-25T08:00:00Z' }] }
    }),
    notificationsUnread: vi.fn(async () => ({ success: true, unread: 1 })),
  }
})
// 后台跳转/超管判定取自 admin store：整 store 保留真身，只换 useAdmin 钩子为静默桩
vi.mock('@/stores/admin', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/stores/admin')>()
  return { ...actual, useAdmin: () => ({ gotoPanel: vi.fn(), openFeedback: vi.fn(), isSuper: false }) }
})

beforeEach(() => {
  cleanup()
  setLang('zh')
  h.panelPresentAtCall = null
  h.calls = 0
  vi.clearAllMocks()
})

describe('Bell · 展开直拉列表（消 stale-refresh）', () => {
  it('toggle 展开后 1s 内 EmptyState 消失，且列表请求在点击同步发出（非 effect 兜底）', async () => {
    const { container } = render(<Bell />)
    // 收起态不打列表接口（挂载只刷未读数）——先锁住这一起点，否则后面对象计数失真
    await waitFor(() => expect(notificationsUnread).toHaveBeenCalled())
    expect(notifications, '收起态不得请求通知列表（30s 轻量轮询口径不变）').toHaveBeenCalledTimes(0)

    fireEvent.click(container.querySelector('.bell-trigger') as HTMLElement)
    // 锁①：展开后 1s 内 EmptyState 消失（列表到手）
    await waitFor(() => {
      expect(container.querySelector('.lc-empty'), '列表已到手仍闪空态 = 展开没直拉列表').toBeNull()
    }, { timeout: 1000 })
    expect(container.querySelectorAll('.bell-item')).toHaveLength(1)

    // 锁②：notifications() 调起时浮层尚未 commit 进 DOM ⇒ 请求出自点击处理器本身
    expect(h.panelPresentAtCall, '列表请求发生在渲染后 = 仍靠 useEffect 兜底（stale-refresh 未消）').toBe(false)
    // 等值计数锁：本次开合列表接口恰打 1 次（toggle 直拉；refresh 已不随 open 重建，无 effect 补刀）
    expect(notifications).toHaveBeenCalledTimes(1)
  })
})

// ★ 2026-10-11 跨境链路瓶颈批：通知列表改真分页（后端默认 20 条／页）后的三把锁。
// 缺陷本体：后端一次吐 100 条、前端 `slice(0,20)` 只截渲染，**传输整包照付**——
// 在实测塌到 KB/s 的跨境链路上列表永远转不完，而几十字节的未读数先到 ⇒ 用户看到「铃铛有数字、面板没内容」。
// 三把锁各自抓一种坏法：① 请求参数没真的带上档位（改回无参＝整包又回来了）；
// ② 「更多」按钮的出现/收口条件写反（到底了还挂着、还有页却不给）；
// ③ 翻页 offset 不是「已到手条数」（写死 20 会让第二页之后的页码错位）。
describe('Bell · 通知列表分页（一页 20 条＋「更多」追加）', () => {
  // 造 n 条通知：id 从 startId 起，标题带序号，便于断言拼接顺序
  const page = (n: number, startId: number) =>
    Array.from({ length: n }, (_, i) => ({ id: startId + i, title: `T${startId + i}`, body: 'b', created_at: '2026-10-11T00:00:00Z' }))

  it('第一页按 (20,0) 请求；has_more 为真才出「更多」；点它按 (20,已到手条数) 追加并把到底收口', async () => {
    const mock = vi.mocked(notifications)
    // 页 1：取满 20 条＋has_more=true（后端「本页取满」近似判有下一页）；页 2：只回 5 条 ⇒ 到底
    mock
      .mockResolvedValueOnce({ success: true, notifications: page(20, 1), has_more: true } as never)
      .mockResolvedValueOnce({ success: true, notifications: page(5, 21), has_more: false } as never)

    const { container } = render(<Bell />)
    await waitFor(() => expect(notificationsUnread).toHaveBeenCalled())
    // 起点锁：收起态一律不打列表（分页改造不许顺手把列表塞进 30s 轮询）
    expect(notifications).toHaveBeenCalledTimes(0)

    fireEvent.click(container.querySelector('.bell-trigger') as HTMLElement)
    await waitFor(() => expect(container.querySelectorAll('.bell-item')).toHaveLength(20))
    // 锁①：请求参数是 (20, 0)——档位真的进了请求，不是"后端整包＋前端截渲染"的旧形态
    expect(mock.mock.calls[0]).toEqual([20, 0])
    // 锁②前半：还有下一页 ⇒ 「更多」在位
    const more = container.querySelector('.bell-more') as HTMLElement
    expect(more, 'has_more 为真却不给「更多」＝ 用户永远看不到第 21 条以后').toBeTruthy()

    fireEvent.click(more)
    await waitFor(() => expect(container.querySelectorAll('.bell-item')).toHaveLength(25))
    // 锁③：offset ＝ 已到手条数（20），不是写死的某个数
    expect(mock.mock.calls[1]).toEqual([20, 20])
    // 锁②后半：第二页没取满 ⇒ 到底，按钮必须自己收（挂着不消失会让人反复点空页）
    await waitFor(() => expect(container.querySelector('.bell-more')).toBeNull())
    // 拼接顺序锁：追加发生在尾部，且第一页 20 条没被覆盖（函数式更新写坏会在这里现形）
    const titles = Array.from(container.querySelectorAll('.bell-item')).map((el) => el.querySelector('div')?.textContent)
    expect(titles[0]).toBe('T1')
    expect(titles[20]).toBe('T21')
    expect(titles).toHaveLength(25)
  })
})
