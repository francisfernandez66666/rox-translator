// @vitest-environment jsdom
// ============================================================================
// components/Login.admin_captcha.dom.test.tsx — 2026-10-23 /admin 人机验证报障回归
//
// 线上事故形态（用户报障原文）：主站后台登录页「无法调起 cloudflare 认证，
// 报错：请完成人机验证」。链路：/admin 未登录落 LoginFace mode="admin"，渲染的是
// 统一登录批（2026-10-22）之后主站同一张「邮箱+验证码」卡；后端 captcha_provider
// =turnstile 已开，email-code 与 email-login 两道都强制校验 token
// （captcha.go verifyCaptcha）。而 Login.tsx 的 registerConfig 拉取与登录屏挂件
// 挂载两处 effect 都门控在 mode==='home' ⇒ admin 档 captchaOn 恒 false、挂件容器
// 根本不渲染，前端守卫全部放行，请求裸奔到后端吃 403「请完成人机验证」。
// 现网实测读数：/admin 登录卡完整、challenges.cloudflare.com 零请求、
// window.turnstile 不存在。
//
// 本文件锁四段：
//   ① admin 档必须拉 registerConfig（旧代码直接 return，一次都不拉）；
//   ② admin 档必须渲染挂件容器，且 renderTurnstile 挂的就是这个容器；
//   ③ 反证：captcha_enabled=false 时两档都不渲染容器（守卫不误伤）；
//   ④ 切「使用密码登录」容器随 OTP 区块卸载，切回必须重挂（loginMode 进依赖的修复）。
// ============================================================================
import { cleanup, fireEvent, render, waitFor } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import Login from './Login'
import { ToastProvider } from '@/ui/langcross/src'
import { setLang, t } from '@/i18n'

// 录制 renderTurnstile 的调用对象（容器元素），供「挂在哪个容器、挂了几次」断言
const mounts: HTMLElement[] = []

vi.mock('@/lib/turnstile', () => ({
  // jsdom 没有 window.turnstile：loadTurnstile 直接同步回调一个假 API，
  // 让挂载链路在测试里真实走完（含容器 iframe 判据）
  loadTurnstile: (onReady: (ts: { render: (el: HTMLElement) => string; execute: () => void }) => void) => {
    onReady({ render: () => 'w1', execute: () => {} })
    return () => {}
  },
  renderTurnstile: (el: HTMLElement) => {
    mounts.push(el)
    const f = document.createElement('iframe') // 模拟真挂件往容器里塞 iframe（判据就靠它）
    el.appendChild(f)
    return 'wid'
  },
  reexecTurnstile: () => {},
}))

// AI 接管流程在登录屏不渲染，剔掉以免连带加载重依赖
vi.mock('./AiRegisterFlow', () => ({ default: () => null }))

// 网络层整体替换：registerConfig 的返回由用例按需改（vi.mocked 改实现），
// 其余出口给齐——缺一个导出就是 undefined is not a function。
const registerConfigMock = vi.fn(async () => ({
  success: true, captcha_enabled: true, captcha_site_key: 'k-test', email_verify_enabled: false,
}))
vi.mock('@/api', () => ({
  getAuthToken: vi.fn(() => null),
  login: vi.fn(async () => ({ success: false, message: 'mock' })),
  loginWithEmail: vi.fn(async () => ({ success: false, message: 'mock' })),
  profileSetup: vi.fn(),
  authRegister: vi.fn(),
  sendEmailCode: vi.fn(async () => ({ success: true })),
  registerConfig: (...args: unknown[]) => registerConfigMock(...(args as [])),
  changePassword: vi.fn(),
  setAuthToken: vi.fn(),
  setActiveTenantId: vi.fn(),
  ssoProviders: vi.fn(async () => ({ enabled: false, providers: [] as { name: string; display_name: string }[] })),
  ssoLoginUrl: (p: string) => `/api/sso/login?provider=${encodeURIComponent(p)}`,
}))

// 主站（tenantId 0 ⇒ unified=true）：/admin 未登录落的就是主站的后台登录屏
vi.mock('@/branding', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/branding')>()
  return {
    ...actual,
    useBranding: () => ({
      brandName: '', dedicatedRegister: false, tenantName: '',
      tenantId: 0, brandLogo: '', domain: '',
    }),
  }
})

beforeEach(() => {
  setLang('zh')
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: (q: string) => ({ matches: false, media: q, onchange: null, addEventListener: () => {}, removeEventListener: () => {} }),
  })
})
afterEach(() => { cleanup(); mounts.length = 0; vi.clearAllMocks() })

const widgetBox = () => document.querySelector('#__ts_login_widget__') as HTMLElement | null

describe('Login admin 档 · 人机验证挂件（2026-10-23 /admin 报障回归）', () => {
  it('① admin 档也必须拉 registerConfig（旧代码 mode!=home 直接 return，一次都不拉）', async () => {
    render(<ToastProvider><Login mode="admin" onLogin={() => { /* noop */ }} /></ToastProvider>)
    await waitFor(() => {
      expect(registerConfigMock, 'admin 档登录屏必须拉取注册配置（人机验证开关与站点钥匙从这里来）').toHaveBeenCalled()
    })
  })

  it('② admin 档必须渲染挂件容器，且 renderTurnstile 挂的就是这个容器', async () => {
    render(<ToastProvider><Login mode="admin" onLogin={() => { /* noop */ }} /></ToastProvider>)
    await waitFor(() => {
      expect(widgetBox(), 'captcha 开启时 admin 档登录卡内必须有人机验证容器').not.toBeNull()
    }, { timeout: 3000 })
    expect(mounts.length, 'admin 档必须挂载一次挂件').toBe(1)
    expect(mounts[0]).toBe(widgetBox())
    expect(widgetBox()!.querySelector('iframe'), '用户必须看得见可操作的人机验证框').not.toBeNull()
  })

  it('③ 反证：captcha_enabled=false 时 admin 档不渲染容器（守卫不误伤）', async () => {
    registerConfigMock.mockResolvedValueOnce({ success: true, captcha_enabled: false, captcha_site_key: '', email_verify_enabled: false })
    render(<ToastProvider><Login mode="admin" onLogin={() => { /* noop */ }} /></ToastProvider>)
    await waitFor(() => { expect(registerConfigMock).toHaveBeenCalled() })
    // 等一个宏任务确保没有迟到的挂载
    await new Promise((r) => setTimeout(r, 50))
    expect(widgetBox()).toBeNull()
    expect(mounts.length).toBe(0)
  })

  it('④ 切「使用密码登录」容器卸载，切回「使用验证码」必须重挂', async () => {
    render(<ToastProvider><Login mode="admin" onLogin={() => { /* noop */ }} /></ToastProvider>)
    await waitFor(() => { expect(widgetBox()!.querySelector('iframe')).not.toBeNull() }, { timeout: 3000 })
    expect(mounts.length).toBe(1)

    // 切密码档：OTP 区块连同挂件容器一起卸载
    fireEvent.click([...document.querySelectorAll('.lc-auth-card__foot button')]
      .find((b) => (b.textContent || '').includes(t('auth.usePassword')))!)
    expect(widgetBox(), '密码档下挂件容器应随 OTP 区块卸载').toBeNull()

    // 切回验证码档：容器重渲染，effect 必须对着新容器再挂一次
    fireEvent.click([...document.querySelectorAll('.lc-auth-card__foot button')]
      .find((b) => (b.textContent || '').includes(t('auth.useCode')))!)
    await waitFor(() => {
      expect(mounts.length, '切回验证码档后挂件必须重挂').toBe(2)
    }, { timeout: 3000 })
    expect(widgetBox()!.querySelector('iframe'), '重挂后用户必须再次看得见人机验证框').not.toBeNull()
  })

  it('⑤ home 档行为不变：同样拉配置、同样挂件（锁既有行为不回归）', async () => {
    render(<ToastProvider><Login mode="home" onLogin={() => { /* noop */ }} /></ToastProvider>)
    await waitFor(() => { expect(widgetBox()!.querySelector('iframe')).not.toBeNull() }, { timeout: 3000 })
    expect(registerConfigMock).toHaveBeenCalled()
    expect(mounts.length).toBe(1)
  })
})
