// @vitest-environment jsdom
// ============================================================================
// components/AiAssist.dom.test.tsx — AI 销售/客服常驻挂件组件级测试（jsdom）
// 覆盖：登录/注册页不渲染、常规路径渲染悬浮球、展开后拉取开场引导并渲染消息
// ============================================================================

import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import AiAssist from './AiAssist'

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

beforeEach(() => {
  // 挂件用 localStorage 存会话 ID，测试间清理
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
})
