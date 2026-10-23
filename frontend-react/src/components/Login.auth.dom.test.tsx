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
//   ③ 底部链接 = 两行、文案与顺序为 忘记密码？ / 没有账号？自助注册试用（★ 2026-10-22 起仅租户站）；
//   ④ 密码框眼睛按钮可切换 type password ↔ text，并同步 aria-pressed；
//   ⑤ 卡右上角存在语言胶囊，点击可切换语言；
//   ⑥ 认证屏字段没有可见 label（可访问名由 aria-label 承担）（★ 2026-10-22 起仅租户站）。
// ★ 2026-10-22 统一登录批新增（只影响主站，租户站逐字不动）：
//   ③-bis 主站底部只剩「使用密码登录」；⑥-bis 主站四枚字段都有可见标签；
//   ⑬ 主站撤下 otpDesc 段落、提示改驻邮箱框 placeholder（auth.autoCreateHint）；
//   ⑭ 主站 profile_setup_required 直接 onLogin（补全交给根级 ProfileSetupGate，不再弹本屏内联对话框）。
//
// 运行：npx vitest run src/components/Login.auth.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, cleanup, fireEvent, waitFor } from '@testing-library/react'
import { setLang, t } from '@/i18n'
import { ToastProvider } from '@/ui/langcross/src'
import Login from './Login'
// ★ 2026-10-13 用户后令：触发钮可见文字固定英文，判据从组件取同一常量（不在此重写字面量）
import { LANG_BTN_FIXED_TEXT } from './LangSelect'

// AI 接管流程在登录屏不渲染，剔掉以免连带加载重依赖
vi.mock('./AiRegisterFlow', () => ({ default: () => null }))

// '@/api' 网络层 mock：整体替换实现，但保留其余导出（@/stores/auth 在模块初始化时
// 会调用 getAuthToken()，缺一个导出整个 suite 就起不来）
vi.mock('@/api', () => ({
  getAuthToken: vi.fn(() => null),
  login: vi.fn(async () => ({ success: false, message:'mock'})),
  loginWithEmail: vi.fn(async () => ({ success: false, message: 'mock' })),
  profileSetup: vi.fn(),
  authRegister: vi.fn(),
  sendEmailCode: vi.fn(),
  registerConfig: vi.fn(async () => ({ success: false })),
  changePassword: vi.fn(),
  setAuthToken: vi.fn(),
  setActiveTenantId: vi.fn(),
  // ★ #38：登录卡会拉取 SSO 身份源；默认「未启用」，SSO 用例里再单独改返回值
  ssoProviders: vi.fn(async () => ({ enabled: false, providers: [] as { name: string; display_name: string }[] })),
  // 纯函数拼接也要给：'@/api' 是整体替换式 mock，漏一个导出被调用就是 undefined is not a function
  ssoLoginUrl: (p: string) => `/api/sso/login?provider=${encodeURIComponent(p)}`,
}))

// ★ 2026-10-22 统一登录批：品牌租户可切（主站 0 / 租户站 7）。
//   vi.hoisted 保证工厂被调用时（静态 import 阶段）brandCtx 已初始化——
//   普通 let 会撞 TDZ（vi.mock 工厂先于模块体语句执行）。
const brandCtx = vi.hoisted(() => ({ tenantId: 0 }))

// 只把 useBranding 的远端品牌置空（回落默认品牌），DEFAULT_BRAND_NAME 仍取真实常量
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

const { DEFAULT_BRAND_NAME } = await import('@/branding')

/** 挂载登录屏；tid=0 主站（默认）/ 7 租户站（注册分支仍在的那个站） */
function renderLogin(tid = 0, onLogin: (u: unknown) => void = () => { /* noop */ }) {
  brandCtx.tenantId = tid
  return render(<ToastProvider><Login mode="home"onLogin={onLogin} /></ToastProvider>)
}

const card = () => document.querySelector('.lc-auth-card') as HTMLElement
const footButtons = () => [...document.querySelectorAll('.lc-auth-card__foot button')] as HTMLButtonElement[]

// ★ 2026-10-22：cleanup 之外还要 clearAllMocks——⑫ 会真调 profileSetup（skip），
//   不清调用记录的话 ⑭ 的 `not.toHaveBeenCalled()` 会读到上一例的调用而误判。
//   clearAllMocks 只清 calls/results，不动机厂里给的默认实现（mockReset 才会清实现）。
beforeEach(() => { cleanup(); vi.clearAllMocks(); setLang('zh'); brandCtx.tenantId = 0 })

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

  it('③ 底部链接 = 验证码主登录 + 密码备用 + 注册（★ 2026-10-22 起此形态仅租户/演示站）', () => {
    renderLogin(7) // 租户站：注册分支仍在
    expect(footButtons().map((b) => b.textContent?.trim()))
      .toEqual(['使用密码登录', '没有账号？自助注册试用'])
    expect(card().textContent).not.toContain('忘记密码')
  })

  it('③-bis ★2026-10-22 主站统一登录：底部只剩「使用密码登录」，注册入口全撤', () => {
    renderLogin() // 主站（tenantId 0）
    expect(footButtons().map((b) => b.textContent?.trim())).toEqual(['使用密码登录'])
    expect(card().textContent).not.toContain('自助注册试用')
    expect(card().textContent).not.toContain('忘记密码')
  })

  it('④ 密码备用档的眼睛按钮可切换明文/密文', () => {
    renderLogin()
    fireEvent.click(footButtons().find((b) => b.textContent?.includes(t('auth.usePassword')))!)
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
    // ★ 判据翻转（用户后令 2026-10-13，非回归）：触发钮可见文字**不分界面语言**统一固定英文
    //   固定英文词（现为 "Lang"），本地字只留在读屏 aria-label；自称名在菜单选中项
    renderLogin()
    const btn = card().querySelector('.lc-auth-card__corner .lang-sel-btn') as HTMLButtonElement
    expect(btn).toBeTruthy()
    // 中文态触发钮显示固定英文词，不显示本地字「语言」、也不显示自称名
    expect(btn.textContent).toContain(LANG_BTN_FIXED_TEXT)
    expect(btn.textContent).not.toContain('语言')
    expect(btn.getAttribute('aria-label')).toBe('语言') // 读屏腿仍按界面语言念

    fireEvent.click(btn)
    const items = card().querySelectorAll('.lang-sel-menu [role="option"]')
    expect(items.length).toBe(12) // 中/英/俄/法/阿/西/葡/德/日/韩/泰/繁中
    const en = [...items].find((el) => el.textContent?.includes('English')) as HTMLElement
    fireEvent.click(en)
    // 切到英文后卡片重渲染（品牌标题仍在=渲染未崩），触发钮回显同一个固定词
    expect(card().querySelector('.lc-auth-card__title')?.textContent).toBe(DEFAULT_BRAND_NAME)
    expect(card().querySelector('.lang-sel-btn')?.textContent).toContain(LANG_BTN_FIXED_TEXT)
    // 选中项（is-cur）携带英文自称名（自称名词表一字未动，仍在菜单里）；点选后菜单已收起，重开再验
    fireEvent.click(card().querySelector('.lang-sel-btn') as HTMLElement)
    const cur = card().querySelector('.lang-sel-item.is-cur') as HTMLElement
    expect(cur?.textContent).toContain('English')
    setLang('zh') // 复位，别把英文态漏给后续用例
  })

  it('⑥ 字段没有可见 label，但仍保留可访问名（验证码主档；★ 2026-10-22 起仅租户站）', () => {
    renderLogin(7) // 租户站保持原形态
    expect(card().querySelectorAll('.lc-field__label').length).toBe(0)
    expect(card().querySelector('input[aria-label="' + t('auth.boundEmail') + '"]')).toBeTruthy()
    expect(card().querySelector('input[aria-label="' + t('auth.fieldEmailCode') + '"]')).toBeTruthy()
  })

  it('⑥-bis ★2026-10-22 主站：邮箱/验证码两枚字段都有可见标签，可访问名不丢', () => {
    renderLogin() // 主站
    const labels = [...card().querySelectorAll('.lc-field__label')].map((el) => el.textContent?.trim())
    expect(labels).toEqual([t('auth.fieldEmail'), t('auth.fieldEmailCode')])
    // 可见标签不替换 aria-label：读屏与点击聚焦（label↔input 的 htmlFor）都还在
    expect(card().querySelector('input[aria-label="' + t('auth.boundEmail') + '"]')).toBeTruthy()
    expect(card().querySelector('input[aria-label="' + t('auth.fieldEmailCode') + '"]')).toBeTruthy()
  })

  it('⑥-ter ★2026-10-22 主站：密码备用档的用户名/密码也都有可见标签', () => {
    renderLogin() // 主站
    fireEvent.click(footButtons().find((b) => b.textContent?.includes(t('auth.usePassword')))!)
    const labels = [...card().querySelectorAll('.lc-field__label')].map((el) => el.textContent?.trim())
    expect(labels).toEqual([t('auth.fieldUsername'), t('auth.fieldPassword')])
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
// ★ 2026-10-22 统一登录批：注册屏只存在于租户/演示站，本 describe 固定走租户上下文。
// ============================================================================
describe('注册屏 · 整屏可渲染（回归：void 元素被喂 children）', () => {
  it('⑦ 点「自助注册试用」后注册卡正常渲染，协议勾选框存在且是 void 元素', () => {
    renderLogin(7) // 租户站才有注册分支
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
    renderLogin(7) // 租户站才有注册分支
    fireEvent.click(footButtons().find((b) => b.textContent?.includes('自助注册'))!)
    const cb = card().querySelector('.lc-checkbox') as HTMLInputElement
    expect(cb.checked).toBe(false)
    fireEvent.click(cb)
    expect(cb.checked).toBe(true)
  })

  it('⑧-bis ★2026-10-22 主站没有注册屏可点：底部无注册钮', () => {
    renderLogin() // 主站
    expect(footButtons().find((b) => b.textContent?.includes('自助注册'))).toBeUndefined()
  })

  it('⑧-ter ★2026-10-22 主站：地址栏 /register 也不展开注册屏（落回登录屏）', () => {
    window.history.pushState({}, '', '/register')
    try {
      renderLogin() // 主站
      expect(card().querySelector('.lc-auth-card__title')?.textContent).toBe(DEFAULT_BRAND_NAME)
      expect(document.body.textContent).not.toContain(t('auth.registerTitle'))
      expect(footButtons().find((b) => b.textContent?.includes('自助注册'))).toBeUndefined()
    } finally {
      window.history.pushState({}, '', '/')
    }
  })
})


// ============================================================================
// ★ 2026-10-21 IAM 重做：邮箱验证码成为主登录入口，找回密码公开入口下线。
// 守护点：① 默认就是验证码档，发码必须带 purpose=login；
//   ② 提交走 loginWithEmail（不是旧 login），成功后进 onLogin；
//   ③ 首登资料提醒可跳过，跳过也必须重签 token 并进入工作台。
// ============================================================================
const { loginWithEmail, profileSetup } = await import('@/api')

describe('登录屏 · 邮箱验证码主登录（2026-10-21 IAM 重做）', () => {
  const email = 'otp@example.com'

  it('⑪ 默认验证码档：填邮箱+验证码+协议即可登录，发码走 purpose=login', async () => {
    const onLogin = vi.fn()
    const send = vi.mocked((await import('@/api')).sendEmailCode)
    send.mockResolvedValueOnce({ success: true } as never)
    vi.mocked(loginWithEmail).mockResolvedValueOnce({
      success: true, token: 'tok', user: { id: 1, username: 'otp', role: 'user', tenant_id: 0 },
    } as never)
    renderLogin(0, onLogin)
    fireEvent.change(card().querySelector(`input[aria-label="${t('auth.boundEmail')}"]`)!, { target: { value: email } })
    fireEvent.click([...card().querySelectorAll('button')].find((b) => b.textContent?.includes(t('auth.sendCode')))!)
    await waitFor(() => expect(send).toHaveBeenCalledWith(email, undefined, 'login'))
    fireEvent.change(card().querySelector(`input[aria-label="${t('auth.fieldEmailCode')}"]`)!, { target: { value: '123456' } })
    fireEvent.click(card().querySelector('input[type="checkbox"]') as HTMLElement)
    fireEvent.click(card().querySelector('.lc-auth-card__submit') as HTMLElement)
    await waitFor(() => expect(loginWithEmail).toHaveBeenCalledWith(email, '123456', expect.objectContaining({ agreed: true })))
    await waitFor(() => expect(onLogin).toHaveBeenCalled())
  })

  it('⑫ 首登资料提醒：暂时跳过只结束本轮，标记仍在（下次登录继续提醒）', async () => {
    const onLogin = vi.fn()
    vi.mocked(loginWithEmail).mockResolvedValueOnce({
      success: true, token: 'tok', user: { id: 1, username: 'newbie', role: 'user', tenant_id: 7, profile_setup_required: 1 },
    } as never)
    vi.mocked(profileSetup).mockResolvedValueOnce({
      success: true, token: 'tok2', user: { id: 1, username: 'newbie', role: 'user', tenant_id: 7, profile_setup_required: 1 },
    } as never)
    renderLogin(7, onLogin) // 租户站：内联资料对话框仍在（主站已交给根级闸门，见 ⑭）
    fireEvent.change(card().querySelector(`input[aria-label="${t('auth.boundEmail')}"]`)!, { target: { value: email } })
    fireEvent.change(card().querySelector(`input[aria-label="${t('auth.fieldEmailCode')}"]`)!, { target: { value: '123456' } })
    fireEvent.click(card().querySelector('input[type="checkbox"]') as HTMLElement)
    fireEvent.click(card().querySelector('.lc-auth-card__submit') as HTMLElement)
    await waitFor(() => expect(document.body.textContent).toContain(t('auth.profileTitle')))
    expect(onLogin).not.toHaveBeenCalled()
    fireEvent.click([...document.querySelectorAll('button')].find((b) => b.textContent?.trim() === t('auth.skipForNow'))!)
    await waitFor(() => expect(profileSetup).toHaveBeenCalledWith(expect.objectContaining({ skip: true })))
    await waitFor(() => expect(onLogin).toHaveBeenCalledWith(expect.objectContaining({ profile_setup_required: 1 })))
  })

  it('⑬ ★2026-10-22 主站撤下 otpDesc 段落、提示改驻邮箱框 placeholder；租户站两样都保留', () => {
    renderLogin() // 主站
    expect(card().textContent).not.toContain(t('auth.otpDesc'))
    const emailBox = card().querySelector(`input[aria-label="${t('auth.boundEmail')}"]`) as HTMLInputElement
    expect(emailBox.getAttribute('placeholder')).toBe(t('auth.autoCreateHint'))
    // ★ 同一用例内换站必须先 cleanup：card() 取的是 document 里第一张卡，
    //   两份并存时读到的永远是主站那张（旧写法因此误判「租户站没有 otpDesc」）
    cleanup()
    // 租户站：原段落与原 placeholder 逐字不动（只影响主站）
    renderLogin(7)
    expect(card().textContent).toContain(t('auth.otpDesc'))
    const tenantBox = card().querySelector(`input[aria-label="${t('auth.boundEmail')}"]`) as HTMLInputElement
    expect(tenantBox.getAttribute('placeholder')).toBe(t('auth.boundEmail'))
  })

  it('⑭ ★2026-10-22 主站：profile_setup_required 直接 onLogin，本屏不再弹内联资料对话框', async () => {
    const onLogin = vi.fn()
    vi.mocked(loginWithEmail).mockResolvedValueOnce({
      success: true, token: 'tok', user: { id: 1, username: 'newbie', role: 'user', tenant_id: 0, profile_setup_required: 1 },
    } as never)
    renderLogin(0, onLogin) // 主站
    fireEvent.change(card().querySelector(`input[aria-label="${t('auth.boundEmail')}"]`)!, { target: { value: email } })
    fireEvent.change(card().querySelector(`input[aria-label="${t('auth.fieldEmailCode')}"]`)!, { target: { value: '123456' } })
    fireEvent.click(card().querySelector('input[type="checkbox"]') as HTMLElement)
    fireEvent.click(card().querySelector('.lc-auth-card__submit') as HTMLElement)
    // 直接放行：补全由根级 ProfileSetupGate 的 AI 完善面板接手（登录与会话恢复共用）
    await waitFor(() => expect(onLogin).toHaveBeenCalledWith(expect.objectContaining({ profile_setup_required: 1 })))
    expect(document.body.textContent).not.toContain(t('auth.profileTitle'))
    expect(profileSetup).not.toHaveBeenCalled()
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
