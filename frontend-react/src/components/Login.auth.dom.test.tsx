// ============================================================================
// Login.auth.dom.test.tsx — 认证屏「设计稿对齐」回归（2026-09-18）
//
// 背景：登录屏原本与画布 57:2/57:3 明显偏离——
//   · 卡标题渲染成「登录」，而画布 57:4 是品牌名「能言 LangCross」26/Bold；
//   · 卡内多出一颗设计稿不存在的通栏「免费注册」次按钮（57:3 的 children 里没有它）；
//   · 底部两行链接被拆成一行 space-between（画布 57:16/57:17 是上下两行、居中、行距 14）；
//   · 密码框没有眼睛按钮（画布 57:8 内含三段矢量图标）；
//   · 卡右上角没有语言胶囊（画布 57:18）；
//   · 字段带可见 label（画布 57:6/57:8 只有 placeholder）。
//
// 覆盖（每条对应上面一条缺陷）：
//   ① 卡标题 = DEFAULT_BRAND_NAME，而不是「登录」；
//   ② 登录卡内不存在「免费注册」按钮（回归防护）；
//   ③ 底部链接 = 两行、文案与顺序为 忘记密码？ / 没有账号？自助注册试用；
//   ④ 密码框眼睛按钮可切换 type password ↔ text，并同步 aria-pressed；
//   ⑤ 卡右上角存在语言胶囊，点击可切换语言；
//   ⑥ 认证屏字段没有可见 label（可访问名由 aria-label 承担）。
//
// 运行：npx vitest run src/components/Login.auth.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent } from '@testing-library/react'
import { setLang, t } from '@/i18n'
import { ToastProvider } from '@/ui/langcross/src'
import Login from './Login'

// AI 接管流程在登录屏不渲染，剔掉以免连带加载重依赖
vi.mock('./AiRegisterFlow', () => ({ default: () => null }))

// '@/api' 网络层 mock：整体替换实现，但保留其余导出（@/stores/auth 在模块初始化时
// 会调用 getAuthToken()，缺一个导出整个 suite 就起不来）
vi.mock('@/api', () => ({
  getAuthToken: vi.fn(() => null),
  login: vi.fn(async () => ({ success: false, message:'mock'})),
  authRegister: vi.fn(),
  sendEmailCode: vi.fn(),
  registerConfig: vi.fn(async () => ({ success: false })),
  forgotPassword: vi.fn(),
  resetPassword: vi.fn(),
  changePassword: vi.fn(),
  setAuthToken: vi.fn(),
  setActiveTenantId: vi.fn(),
}))

// 只把 useBranding 的远端品牌置空（回落默认品牌），DEFAULT_BRAND_NAME 仍取真实常量
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

const { DEFAULT_BRAND_NAME } = await import('@/branding')

function renderLogin() {
  return render(<ToastProvider><Login mode="home"onLogin={() => { /* noop */ }} /></ToastProvider>)
}

const card = () => document.querySelector('.lc-auth-card') as HTMLElement
const footButtons = () => [...document.querySelectorAll('.lc-auth-card__foot button')] as HTMLButtonElement[]

beforeEach(() => { cleanup(); setLang('zh') })

describe('登录屏 · 设计稿对齐（画布 57:2 / 57:3）', () => {
  it('① 卡标题是品牌名，不是「登录」', () => {
    renderLogin()
    const title = card().querySelector('.lc-auth-card__title') as HTMLElement
    expect(title.textContent).toBe(DEFAULT_BRAND_NAME)
    expect(title.textContent).not.toBe('登录')
  })

  it('② 登录卡内没有设计稿不存在的「免费注册」通栏按钮', () => {
    renderLogin()
    const labels = [...card().querySelectorAll('button')].map((b) => b.textContent?.trim())
    expect(labels).not.toContain('免费注册')
    expect(labels).not.toContain('自助注册试用')
  })

  it('③ 底部是两行链接，文案与顺序与画布一致', () => {
    renderLogin()
    expect(footButtons().map((b) => b.textContent?.trim()))
      .toEqual(['忘记密码？', '没有账号？自助注册试用'])
  })

  it('④ 密码框眼睛按钮可切换明文/密文', () => {
    renderLogin()
    const pwd = card().querySelector('input[placeholder="密码"]') as HTMLInputElement
    const eye = card().querySelector('.lc-input-affix') as HTMLButtonElement
    expect(pwd.type).toBe('password')
    expect(eye.getAttribute('aria-pressed')).toBe('false')

    fireEvent.click(eye)
    expect(pwd.type).toBe('text')
    expect(eye.getAttribute('aria-pressed')).toBe('true')

    fireEvent.click(eye)
    expect(pwd.type).toBe('password')
  })

  it('⑤ 卡右上角有语言胶囊，点击可切换语言', () => {
    renderLogin()
    const pill = card().querySelector('.lc-auth-card__corner .auth-lang') as HTMLButtonElement
    expect(pill).toBeTruthy()
    expect(pill.textContent?.trim()).toBe('EN')

    fireEvent.click(pill)
    expect(card().querySelector('.lc-auth-card__title')?.textContent).toBe(DEFAULT_BRAND_NAME)
    // 切到英文后胶囊显示回切目标（与顶栏同一套约定：app.langSwitch 的当前语言译名）
    expect(card().querySelector('.lc-auth-card__corner .auth-lang')?.textContent?.trim())
      .toBe(t('app.langSwitch'))
    expect(card().querySelector('.lc-auth-card__corner .auth-lang')?.textContent?.trim())
      .not.toBe('EN')
  })

  it('⑥ 字段没有可见 label，但仍保留可访问名', () => {
    renderLogin()
    expect(card().querySelectorAll('.lc-field__label').length).toBe(0)
    expect(card().querySelector('input[aria-label="用户名"]')).toBeTruthy()
    expect(card().querySelector('input[aria-label="密码"]')).toBeTruthy()
  })
})

// ============================================================================
// 注册屏整屏渲染回归（2026-09-18 线上实测崩溃）
//
// 现象：点「没有账号？自助注册试用」后整页变成「页面出现异常」
//   （生产构建压成 React #137「Rendered more hooks than during the previous
//    render」，极易误判成 hooks 顺序问题）。
// 真因：Login 把 JSX children 传给了 <Checkbox>，而 Checkbox 内部是 <input>
//   （void 元素），children 随 {...rest} 被摊上去 → 抛
//   「input is a void element tag and must neither have children…」。
// 这条断言在**整屏**层面守门：任何把 void 元素喂出 children 的改动都会让它红。
// ============================================================================
describe('注册屏 · 整屏可渲染（回归：void 元素被喂 children）', () => {
  it('⑦ 点「自助注册试用」后注册卡正常渲染，协议勾选框存在且是 void 元素', () => {
    renderLogin()
    const regBtn = footButtons().find((b) => b.textContent?.includes('自助注册'))
    expect(regBtn).toBeTruthy()
    fireEvent.click(regBtn!)

    // 注册卡必须在（崩溃时这里已被 ErrorBoundary 替换成「页面出现异常」）
    expect(card()).toBeTruthy()
    expect(document.body.textContent).not.toContain('页面出现异常')

    const cb = card().querySelector('.lc-checkbox') as HTMLInputElement | null
    expect(cb).toBeTruthy()
    expect(cb!.type).toBe('checkbox')
    // 关键：void 元素里不得有任何子节点
    expect(cb!.childNodes.length).toBe(0)
    // 协议文案仍要完整渲染（落在 label 的 span 里）
    expect(card().textContent).toContain(t('auth.agreeTerms'))
  })

  it('⑧ 注册屏勾选框可交互：点击后 checked 翻转', () => {
    renderLogin()
    fireEvent.click(footButtons().find((b) => b.textContent?.includes('自助注册'))!)
    const cb = card().querySelector('.lc-checkbox') as HTMLInputElement
    expect(cb.checked).toBe(false)
    fireEvent.click(cb)
    expect(cb.checked).toBe(true)
  })
})
