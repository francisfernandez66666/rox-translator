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
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react'
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
  // ★ #38：登录卡会拉取 SSO 身份源；默认「未启用」，SSO 用例里再单独改返回值
  ssoProviders: vi.fn(async () => ({ enabled: false, providers: [] as { name: string; display_name: string }[] })),
  // 纯函数拼接也要给：'@/api' 是整体替换式 mock，漏一个导出被调用就是 undefined is not a function
  ssoLoginUrl: (p: string) => `/api/sso/login?provider=${encodeURIComponent(p)}`,
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

  it('⑤ 卡右上角语言入口为 12 语种下拉（★ #23 取代旧 zh/en 胶囊），选择即切换', () => {
    renderLogin()
    const btn = card().querySelector('.lc-auth-card__corner .lang-sel-btn') as HTMLButtonElement
    expect(btn).toBeTruthy()
    // 中文态触发钮显示当前语种自称（LANG_OPTIONS 首位 native）
    expect(btn.textContent).toContain('简体中文')

    fireEvent.click(btn)
    const items = card().querySelectorAll('.lang-sel-menu [role="option"]')
    expect(items.length).toBe(12) // 中/英/俄/法/阿/西/葡/德/日/韩/泰/繁中
    const en = [...items].find((el) => el.textContent?.includes('English')) as HTMLElement
    fireEvent.click(en)
    // 切到英文后卡片重渲染（品牌标题仍在=渲染未崩），触发钮回显当前语种
    expect(card().querySelector('.lc-auth-card__title')?.textContent).toBe(DEFAULT_BRAND_NAME)
    expect(card().querySelector('.lang-sel-btn')?.textContent).toContain('English')
    setLang('zh') // 复位，别把英文态漏给后续用例
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

// ============================================================================
// ★ #38（2026-09-21）登录屏 SSO 身份源入口回归
// 缺陷背景：后端 /api/sso/{providers,login,callback} 三件套早已齐备，登录页却没有任何入口，
//   接了 IdP 的客户仍然只能用密码登录（配置白做）。
// 守护点：① 未启用（enabled=false / 请求失败）时整块不渲染——不能留死按钮；
//   ② 启用时按 display_name 出入口；③ 入口必须是 <a href="/api/sso/login?provider=…">，
//      用 fetch 走接口会把 302 到 IdP 的重定向链连同 state cookie 一起丢掉。
// ============================================================================
const { ssoProviders } = await import('@/api')

describe('登录屏 · SSO 身份源入口（#38）', () => {
  beforeEach(() => { vi.mocked(ssoProviders).mockReset() })

  it('⑨ 未启用 SSO 时不渲染任何第三方入口', async () => {
    vi.mocked(ssoProviders).mockResolvedValue({ enabled: false, providers: [] })
    renderLogin()
    await waitFor(() => expect(ssoProviders).toHaveBeenCalled())
    expect(document.querySelector('.auth-sso')).toBeFalsy()
  })

  it('⑩ 启用后按 display_name 渲染链接入口，href 指向 SSO 发起端点', async () => {
    vi.mocked(ssoProviders).mockResolvedValue({
      enabled: true,
      providers: [{ name: 'okta', display_name: 'Okta' }, { name: 'feishu', display_name: '' }],
    })
    renderLogin()
    await waitFor(() => expect(document.querySelectorAll('.auth-sso a').length).toBe(2))
    const links = [...document.querySelectorAll('.auth-sso a')] as HTMLAnchorElement[]
    expect(links[0].textContent).toContain('Okta')
    // display_name 缺省时回落 name，入口文案不会变成「使用  登录」这种空槽
    expect(links[1].textContent).toContain('feishu')
    expect(links[0].getAttribute('href')).toContain('/api/sso/login?provider=okta')
    expect(links[1].getAttribute('href')).toContain('/api/sso/login?provider=feishu')
  })
})
