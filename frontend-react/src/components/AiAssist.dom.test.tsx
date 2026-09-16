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
    stubFetch({ session: 's1', greeting: '你好，我是能言助手', chips: ['怎么收费'] })
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
  })
})
