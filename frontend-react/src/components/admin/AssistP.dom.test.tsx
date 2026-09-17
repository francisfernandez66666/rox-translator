// ============================================================================
// AssistP.dom.test.tsx — AI 助手管理面板组件测试（★ 改造 1A，2026-09-17）
// 覆盖：
//   ① Token 自动注入——主后台下发 → 写入同源 localStorage('assist_tok') → iframe 渲染；
//   ② 未配置 Token（has_token=false）→ 提示兜底路径且**不覆盖**用户已手工填的值；
//   ③ 接口异常 → 显示失败态而非白屏（iframe 仍渲染，用户可手工粘贴排障）。
// 背景：此前 AssistP 要求用户手工粘贴 ASSIST_ADMIN_TOKEN，与主后台 SSO 体验断层。
// 运行：npx vitest run（文件级 @vitest-environment jsdom）
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, cleanup } from '@testing-library/react'
import AssistP from './AssistP'

const { tokenMock, rotateMock } = vi.hoisted(() => ({
  tokenMock: vi.fn(),
  rotateMock: vi.fn(),
}))

vi.mock('@/api', () => ({
  adminAssistToken: tokenMock,
  adminAssistTokenRotate: rotateMock,
}))

vi.mock('@/api/assist', () => ({ ASSIST_API: '/assist-api' }))

// 每个用例前清理 DOM、清空 mock 与 localStorage，保证 Token 注入/兜底/异常各分支断言相互独立
beforeEach(() => {
  cleanup()
  vi.clearAllMocks()
  localStorage.clear()
})

describe('AI 助手管理台 Token 自动注入（改造 1A）', () => {
  it('主后台下发 Token 时：注入同源 localStorage 并渲染 iframe', async () => {
    tokenMock.mockResolvedValue({ success: true, token: 'tok-from-db', source: 'db', has_token: true })
    const { container } = render(<AssistP />)
    await vi.waitFor(() => {
      expect(screen.getByText(/管理 Token 已自动注入/)).toBeTruthy()
    })
    // 注入契约：键名须与 assist web/admin.html 读取的 assist_tok 一致
    expect(localStorage.getItem('assist_tok')).toBe('tok-from-db')
    expect(screen.getByText(/主后台库内配置/)).toBeTruthy()
    const iframe = container.querySelector('iframe')
    expect(iframe?.getAttribute('src')).toBe('/assist-api/assist/admin')
  })

  it('env 来源时展示对应来源文案', async () => {
    tokenMock.mockResolvedValue({ success: true, token: 'tok-from-env', source: 'env', has_token: true })
    render(<AssistP />)
    await vi.waitFor(() => {
      expect(screen.getByText(/环境变量 ASSIST_ADMIN_TOKEN/)).toBeTruthy()
    })
    expect(localStorage.getItem('assist_tok')).toBe('tok-from-env')
  })

  it('未配置 Token 时：提示兜底路径，且不覆盖用户已手工填写的值', async () => {
    localStorage.setItem('assist_tok', 'manually-pasted')
    tokenMock.mockResolvedValue({ success: true, token: '', source: 'none', has_token: false })
    const { container } = render(<AssistP />)
    await vi.waitFor(() => {
      expect(screen.getByText(/尚未配置管理 Token/)).toBeTruthy()
    })
    // 关键：空 Token 不得清掉手工值，否则用户每次进面板都要重填
    expect(localStorage.getItem('assist_tok')).toBe('manually-pasted')
    expect(container.querySelector('iframe')).toBeTruthy()
  })

  it('接口异常时：显示失败态而不是白屏（iframe 仍可用）', async () => {
    tokenMock.mockRejectedValue(new Error('HTTP 500'))
    const { container } = render(<AssistP />)
    await vi.waitFor(() => {
      expect(screen.getByText(/获取管理 Token 失败/)).toBeTruthy()
    })
    expect(container.querySelector('iframe')).toBeTruthy()
  })
})
