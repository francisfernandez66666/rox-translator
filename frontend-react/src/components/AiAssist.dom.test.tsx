// @vitest-environment jsdom
// ============================================================================
// components/AiAssist.dom.test.tsx — AI 销售/客服常驻挂件组件级测试（jsdom）
// 覆盖：登录/注册页不渲染、常规路径渲染悬浮球、展开后拉取开场引导并渲染消息、
//       ★ 会话缓存（2026-09-22「刷新一次页面就没了」）：本地缓存即时回显、
//         服务端历史权威对账、令牌失效/网络失败时不清缓存、发送后落盘
// ============================================================================

import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AiAssist from './AiAssist'
import { saveAssistMsgs, type AssistCacheMsg } from '@/api/assist'

/** 挂件对话缓存键（与 api/assist.ts 的 MSG_KEY 同值；此处不复用常量以免污染生产代码导出面） */
const MSG_KEY = 'ny_assist_msgs'

// fetch 桩：/greeting 返回固定引导，其余接口空响应
function stubFetch(greeting: object) {
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    const u = String(url)
    if (u.includes('/api/assist/greeting')) {
      return {
        ok: true,
        json: async () => greeting,
      } as Response
    }
    return { ok: true, json: async () => ({ messages: [] }) } as Response
  }))
}

/**
 * fetch 桩（可编程版）：分别控制 greeting / history / chat 三类响应，并记录被调用的 URL。
 * history 传 '401' 表示令牌失效，传数组表示服务端台账内容，不传表示空台账。
 */
function stubAssist(opts: {
  greeting?: object
  history?: unknown[] | '401' | 'fail'
  chat?: { reply: string; actions?: unknown[] }
} = {}) {
  const calls: string[] = []
  const fn = vi.fn(async (url: string) => {
    const u = String(url)
    calls.push(u)
    if (u.includes('/api/assist/greeting')) {
      return { ok: true, json: async () => opts.greeting ?? { session: 's-new', tok: 't-new', greeting: '新欢迎词', chips: [] } } as Response
    }
    if (u.includes('/api/assist/history')) {
      if (opts.history === '401') return { ok: false, status: 401, json: async () => ({ error: 'invalid session' }) } as Response
      if (opts.history === 'fail') return { ok: false, status: 500, json: async () => ({ error: 'boom' }) } as Response
      return { ok: true, json: async () => ({ messages: opts.history ?? [] }) } as Response
    }
    if (u.includes('/api/assist/chat')) {
      return { ok: true, json: async () => opts.chat ?? { reply: '收到', source: 'rule' } } as Response
    }
    return { ok: true, json: async () => ({}) } as Response
  })
  vi.stubGlobal('fetch', fn)
  return { calls }
}

/** 预置一次「已聊过两句」的本地会话：sid+tok 成对 + 消息缓存 */
function seedCache(msgs: AssistCacheMsg[], sid = 's-old', tok = 't-old') {
  window.localStorage.setItem('ny_assist_sid', sid)
  window.localStorage.setItem('ny_assist_tok', tok)
  saveAssistMsgs(sid, msgs)
}

/** 读取缓存里落存的消息条数（用于断言「发送后确实落盘」） */
function cachedMsgs(): AssistCacheMsg[] {
  try {
    const p = JSON.parse(window.localStorage.getItem(MSG_KEY) || '{"msgs":[]}')
    return p.msgs || []
  } catch { return [] }
}

beforeEach(() => {
  // 挂件用 localStorage 存会话 ID 与消息缓存，测试间清理
  window.localStorage.clear()
})
afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

// renderAt 挂件渲染辅助：在指定路由路径下渲染 AiAssist，返回容器查询句柄
function renderAt(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <AiAssist />
    </MemoryRouter>,
  )
}

describe('AiAssist 常驻挂件', () => {
  it('登录页不渲染（悬浮球与面板均无）', () => {
    const { container } = renderAt('/login')
    expect(container.querySelector('.na-fab')).toBeNull()
    expect(container.querySelector('.na-panel')).toBeNull()
  })

  it('注册页不渲染', () => {
    const { container } = renderAt('/register')
    expect(container.querySelector('.na-fab')).toBeNull()
  })

  it('落地页渲染悬浮球，点击展开拉取开场引导', async () => {
    stubFetch({ session: 's1', tok: 't1', greeting: '你好，我是能言助手', chips: ['怎么收费'] })
    const { container } = renderAt('/')
    const fab = container.querySelector('.na-fab')
    expect(fab).not.toBeNull()
    // 展开面板
    fireEvent.click(fab!)
    await waitFor(() => expect(container.querySelector('.na-panel')).not.toBeNull())
    // 欢迎词渲染
    await waitFor(() => expect(screen.getByText('你好，我是能言助手')).toBeTruthy())
    // 快捷提问 chips 渲染
    await waitFor(() => expect(screen.getByText('怎么收费')).toBeTruthy())
    // ★ P0-1（2026-09-18）：greet 下发的会话能力令牌必须与 sid 成对落存
    expect(window.localStorage.getItem('ny_assist_sid')).toBe('s1')
    expect(window.localStorage.getItem('ny_assist_tok')).toBe('t1')
  })

  it('★ P0-1：发消息随带能力令牌；401 时重新 greet 自愈并重发一次', async () => {
    const fetchStub = vi.fn(async (url: string, init?: { body?: string }) => {
      const u = String(url)
      if (u.includes('/api/assist/chat')) {
        const body = JSON.parse(init?.body || '{}')
        if (body.tok !== 'good') {
          return { ok: false, status: 401, json: async () => ({ error: 'invalid session' }) } as Response
        }
        return { ok: true, json: async () => ({ reply: '收到', source: 'rule' }) } as Response
      }
      // greeting：首答令牌过期场景返回新 sid+tok
      return { ok: true, json: async () => ({ session: 's2', tok: 'good', greeting: '你好', chips: [] }) } as Response
    })
    vi.stubGlobal('fetch', fetchStub)
    // 预置一个令牌已失效的老会话
    window.localStorage.setItem('ny_assist_sid', 's1')
    window.localStorage.setItem('ny_assist_tok', 'expired')
    const { container } = renderAt('/')
    fireEvent.click(container.querySelector('.na-fab')!)
    await waitFor(() => expect(container.querySelector('.na-panel')).not.toBeNull())
    // 历史恢复走 401 空响应桩：返回空后组件会 greet 新会话；再发送一条消息触发自愈链路
    const input = container.querySelector('input, textarea') as HTMLInputElement
    if (input) {
      fireEvent.change(input, { target: { value: 'hi' } })
      fireEvent.keyDown(input, { key: 'Enter' })
      await waitFor(() => expect(screen.getByText('收到')).toBeTruthy(), { timeout: 3000 })
      // 本地令牌已刷新为新值
      expect(window.localStorage.getItem('ny_assist_tok')).toBe('good')
    }
  })

  // ---------------------------------------------------------------------------
  // ★ 会话缓存（2026-09-22 用户反馈「ai 助手要带缓存，不然刷新一次页面就没了很尴尬」）
  // 恢复优先序：本地缓存（立刻可见）→ 服务端 history（权威对账）→ 新会话 greet。
  // 关键负向锁：缓存非空时**绝不** greet 覆盖，否则令牌一失效用户就看到「对话全没了」。
  // ---------------------------------------------------------------------------
  it('★ 缓存：刷新后本地缓存立即回显，且不发 greet 覆盖成欢迎词', async () => {
    seedCache([
      { role: 'user', content: '缓存里的问题' },
      { role: 'assistant', content: '缓存里的回答' },
    ])
    const { calls } = stubAssist({ history: [] }) // 服务端台账为空（例如 tok 已换过会话）
    const { container } = renderAt('/')
    fireEvent.click(container.querySelector('.na-fab')!)
    await waitFor(() => expect(screen.getByText('缓存里的回答')).toBeTruthy())
    expect(screen.getByText('缓存里的问题')).toBeTruthy()
    // 负向：没有新会话欢迎词，且根本没打 greeting 接口
    expect(screen.queryByText('新欢迎词')).toBeNull()
    expect(calls.some((u) => u.includes('/api/assist/greeting'))).toBe(false)
  })

  it('★ 缓存：服务端 history 有内容时以服务端为准', async () => {
    seedCache([{ role: 'user', content: '本地旧内容' }])
    stubAssist({ history: [
      { role: 'user', content: '服务端的问题' },
      { role: 'assistant', content: '服务端的回答' },
    ] })
    const { container } = renderAt('/')
    fireEvent.click(container.querySelector('.na-fab')!)
    await waitFor(() => expect(screen.getByText('服务端的回答')).toBeTruthy())
    // 服务端权威：旧的本地内容不再显示，并被服务端列表覆写回缓存
    expect(screen.queryByText('本地旧内容')).toBeNull()
    await waitFor(() => expect(cachedMsgs().map((m) => m.content)).toEqual(['服务端的问题', '服务端的回答']))
  })

  it('★ 缓存：history 401（令牌失效）保留气泡、清掉失效会话、不 greet', async () => {
    seedCache([
      { role: 'user', content: '还在的提问' },
      { role: 'assistant', content: '还在的回答' },
    ])
    const { calls } = stubAssist({ history: '401' })
    const { container } = renderAt('/')
    fireEvent.click(container.querySelector('.na-fab')!)
    await waitFor(() => expect(window.localStorage.getItem('ny_assist_sid')).toBeNull())
    expect(screen.getByText('还在的回答')).toBeTruthy()
    expect(screen.queryByText('新欢迎词')).toBeNull()
    expect(calls.some((u) => u.includes('/api/assist/greeting'))).toBe(false)
    // 缓存本身也不该被清掉（下次打开还能看见）
    expect(cachedMsgs()).toHaveLength(2)
  })

  it('★ 缓存：history 请求失败（5xx）只标离线，不把气泡清空', async () => {
    seedCache([{ role: 'assistant', content: '失败时也要留着的话' }])
    stubAssist({ history: 'fail' })
    const { container } = renderAt('/')
    fireEvent.click(container.querySelector('.na-fab')!)
    await waitFor(() => expect(container.querySelector('.na-offline')).not.toBeNull())
    expect(screen.getByText('失败时也要留着的话')).toBeTruthy()
    // 无缓存时的降级提示不应出现在有缓存的场景里
    expect(container.querySelectorAll('.na-row').length).toBe(1)
  })

  it('★ 缓存：发送成功后用户消息与回复一并落盘', async () => {
    seedCache([{ role: 'assistant', content: '上一轮的回答' }])
    stubAssist({ chat: { reply: '新的回答' } })
    const { container } = renderAt('/')
    fireEvent.click(container.querySelector('.na-fab')!)
    await waitFor(() => expect(screen.getByText('上一轮的回答')).toBeTruthy())
    const input = container.querySelector('input') as HTMLInputElement
    fireEvent.change(input, { target: { value: '再问一句' } })
    fireEvent.keyDown(input, { key: 'Enter' })
    await waitFor(() => expect(screen.getByText('新的回答')).toBeTruthy())
    await waitFor(() => expect(cachedMsgs().map((m) => m.content))
      .toEqual(['上一轮的回答', '再问一句', '新的回答']))
  })
})
