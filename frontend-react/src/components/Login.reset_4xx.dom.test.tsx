// @vitest-environment jsdom
// ============================================================================
// components/Login.reset_4xx.dom.test.tsx — 观察2② 前端侧回归锁（2026-09-25 批G）
// 背景：后端 handleResetPassword 四类失败族从「HTTP 200 + success:false」收口为
//   「HTTP 400 + 统一错误码 VALIDATION_ERROR」（writeError）。本锁验两件事：
//   ① request() 封装对 4xx 的归一链路真的通——错误体里的 message 会被解析成
//     ApiError.message 抛出（而不是掉进「请求失败 (400): <原文>」兜底串）；
//   ② Login 找回密码第二步「重置」失败时，后端文案原样上屏到提示槽（用户看得见
//     「验证码错误或已过期」，不是静默）。
// 手法：不 mock @/api，只在 fetch 层桩响应——让真实 request()（api/core.ts）走完
//   解析、抛错、上屏全链路，这才是「request 封装 4xx 归一顺验」的本意。
// ============================================================================
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import Login from './Login'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang, t } from '@/i18n'

// 品牌桩：去掉专属域/AI 接管等分支干扰（与既有 Login dom 测试同口径）
vi.mock('@/branding', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/branding')>()
  return { ...actual, useBranding: () => ({ brandName: '', dedicatedRegister: false, tenantName: '', tenantId: 0, brandLogo: '', domain: '' }) }
})

// fetch 层路由桩：只桩「响应字节」，不碰 request() 本体
type FakeRes = { ok: boolean; status: number; json: () => Promise<unknown>; text: () => Promise<string> }
const jsonRes = (status: number, body: Record<string, unknown>): FakeRes => ({
  ok: status >= 200 && status < 300, status,
  json: async () => body, text: async () => JSON.stringify(body),
})
// 记录出网请求（断 reset-password 这一枪真的开了、载荷对）
const outbox: Array<{ url: string; body: string }> = []

function routeFetch(input: unknown): FakeRes {
  const url = String(input)
  if (url.includes('/api/auth/forgot-password')) return jsonRes(200, { success: true, message: 'ok' })
  // 重置密码：批G 观察2② 的新形态——HTTP 400 + 统一错误码 + 中文文案逐字保留
  if (url.includes('/api/auth/reset-password')) return jsonRes(400, { success: false, code: 'VALIDATION_ERROR', message: '验证码错误或已过期' })
  if (url.includes('/api/auth/sso-providers')) return jsonRes(200, { enabled: false, providers: [] })
  return jsonRes(200, { success: true })
}

beforeEach(() => {
  setLang('zh')
  outbox.length = 0
  vi.stubGlobal('fetch', vi.fn(async (u: unknown, o?: { body?: string }) => {
    outbox.push({ url: String(u), body: String(o?.body ?? '') })
    return routeFetch(u)
  }))
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (q: string) => ({ matches: false, media: q, onchange: null, addEventListener: () => {}, removeEventListener: () => {} }),
  })
})
afterEach(() => { cleanup(); vi.unstubAllGlobals() })

// 按文案找钮（登录卡内同名文案唯一，无需容器限定）
const btnByText = (s: string) => Array.from(document.querySelectorAll('button')).find((b) => (b.textContent || '').includes(s))
const inputByAria = (s: string) => document.querySelector<HTMLInputElement>(`input[aria-label="${s}"]`)

describe('Login 观察2② · 重置密码 4xx 归一后失败文案必须上屏', () => {
  it('第一步发码成功→第二步重置收 400：提示槽整串精确等于后端 message（非「请求失败 (400)」兜底串）', async () => {
    render(<ToastProvider><Login mode="home" onLogin={() => {}} /></ToastProvider>)
    // 进找回密码屏
    fireEvent.click(btnByText(t('auth.forgot'))!)
    // 第一步：填用户名提交找回申请（fetch 桩回 success:true → 进入已发码态）
    fireEvent.change(inputByAria(t('auth.username'))!, { target: { value: 'uat_user_01' } })
    fireEvent.click(btnByText(t('auth.sendCode'))!)
    await waitFor(() => {
      // 已发码态判据：出现第二步独有的「新密码」输入框
      expect(inputByAria(t('auth.newPassword'))).not.toBeNull()
    }, { timeout: 3000 })
    // 第二步：填验证码+新密码提交重置（后端批G 新形态：HTTP 400 + VALIDATION_ERROR）
    fireEvent.change(inputByAria(t('auth.fieldEmailCode'))!, { target: { value: '000000' } })
    fireEvent.change(inputByAria(t('auth.newPassword'))!, { target: { value: 'pw123456' } })
    fireEvent.click(btnByText(t('auth.resetPassword'))!)

    await waitFor(() => {
      const slot = document.querySelector('.auth-ok, .auth-err')
      // 等值锁：上屏文本精确等于后端错误体 message（证明 request() 把 400 的 JSON 体
      // 归一成 ApiError(message) 并整条交给 catch——而不是兜底串或后端 code）
      expect(slot?.textContent).toBe('验证码错误或已过期')
    }, { timeout: 3000 })
    // 出网锁：forgot-password 与 reset-password 各恰好 1 枪；重置载荷逐字段等值
    const resetCall = outbox.find((c) => c.url.includes('/api/auth/reset-password'))
    expect(resetCall, '重置请求必须真的发出（后端从 200 改 400 后前端链路未断）').toBeTruthy()
    expect(JSON.parse(resetCall!.body)).toEqual({ username: 'uat_user_01', code: '000000', new_password: 'pw123456' })
    expect(outbox.filter((c) => c.url.includes('/api/auth/reset-password')).length).toBe(1)
  })
})
