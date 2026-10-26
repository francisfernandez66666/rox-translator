// ============================================================================
// Login.registerIntent.dom.test.tsx — P1-01 注册意图文案锁
//
// 背景（2026-10-25 云端 UAT）：主站统一登录下 /register 与 /login 共用登录卡是
// 2026-10-22/23 用户令的刻意设计，不是缺注册表单；但旧形态在 /register 上仍显示
// “登录后进入翻译工作台”，新用户会以为走错地方。
//
// 本锁钉住：/register 的说明必须是“未注册的邮箱会自动创建空白账号”（auth.autoCreateHint）；
// /login 仍是 auth.loginDesc；租户站（unified=false）行为不变。
// 反证：把 registerIntent 判断摘掉 ⇒ 第一条红。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup } from '@testing-library/react'
import { setLang, t } from '@/i18n'
import { ToastProvider } from '@/ui/langcross/src'
import Login from './Login'

vi.mock('./AiRegisterFlow', () => ({ default: () => null }))

vi.mock('@/api', () => ({
  getAuthToken: vi.fn(() => null),
  login: vi.fn(async () => ({ success: false, message: 'mock' })),
  loginWithEmail: vi.fn(async () => ({ success: false, message: 'mock' })),
  profileSetup: vi.fn(),
  authRegister: vi.fn(),
  sendEmailCode: vi.fn(),
  registerConfig: vi.fn(async () => ({ success: false })),
  changePassword: vi.fn(),
  setAuthToken: vi.fn(),
  setActiveTenantId: vi.fn(),
  ssoProviders: vi.fn(async () => ({ enabled: false, providers: [] })),
  ssoLoginUrl: (p: string) => `/api/sso/login?provider=${encodeURIComponent(p)}`,
  inviteLinkPublic: vi.fn(async () => ({ success: false })),
}))

// brandCtx 用 hoisted 保证 vi.mock 工厂先于模块体执行时不撞 TDZ；0=主站、7=租户站。
const brandCtx = vi.hoisted(() => ({ tenantId: 0 }))

vi.mock('@/branding', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@/branding')>()
  return {
    ...actual,
    useBranding: () => ({
      brandName: '', dedicatedRegister: false, tenantName: '',
      tenantId: brandCtx.tenantId, brandLogo: '', domain: '',
    }),
  }
})

// renderLogin 挂载登录屏；tid=0 主站（统一登录）/ 7 租户站（保留注册分支）。
function renderLogin(tid = 0) {
  brandCtx.tenantId = tid
  return render(<ToastProvider><Login mode="home" onLogin={() => { /* noop */ }} /></ToastProvider>)
}

// cardText 只取认证卡正文，避免把页面其它文案混进断言。
const cardText = () => document.querySelector('.lc-auth-card')?.textContent || ''

beforeEach(() => {
  cleanup()
  setLang('zh')
  window.history.pushState({}, '', '/')
})

afterEach(() => {
  cleanup()
  window.history.pushState({}, '', '/')
})

// 三条：/register 注册意图、/login 不回归、租户站不受影响。
describe('Login /register 注册意图文案', () => {
  it('/register 主站：说明切换为“未注册邮箱自动创建账号”', () => {
    window.history.pushState({}, '', '/register')
    renderLogin(0)
    const text = cardText()
    expect(text).toContain(t('auth.autoCreateHint'))
    expect(text).not.toContain(t('auth.loginDesc'))
  })

  it('/login 主站：说明保持登录口径（不回归）', () => {
    window.history.pushState({}, '', '/login')
    renderLogin(0)
    const text = cardText()
    expect(text).toContain(t('auth.loginDesc'))
    expect(text).not.toContain(t('auth.autoCreateHint'))
  })

  it('租户站 /register：仍走原注册分支，不受主站文案改动影响', () => {
    window.history.pushState({}, '', '/register')
    renderLogin(7)
    // 租户站 unified=false，/register 会展开注册屏；不应出现“登录后进入翻译工作台”的登录卡说明
    expect(cardText()).not.toContain(t('auth.loginDesc'))
  })
})
