// ============================================================================
// Login.brand_login.dom.test.tsx — F-71「品牌登录页背景/布局」消费侧回归（2026-09-27）
//
// 缺陷原貌：`brand_home_bg` / `brand_login_layout` / `brand_login_card_pos` 三个品牌字段
// 只有后台 BrandP 的本地预览在用，登录页零消费 ⇒ 租户「配了不生效」。
//
// 本锁的三件事（全部写成**等值锁**，不写单向锁）：
//   ① 默认形态零变化：没配背景图时，登录壳只输出改造前那个 `.lc-auth-bg`，
//      品牌相关节点（背景图层 / 遮罩 / 定位卡 / 注入样式）**一个都不许出现**——
//      这条是 〇-P 交付档与 pixel/e2e 锁不被品牌功能污染的红线；
//   ② 两种布局各自接线正确：full＝背景 + `rgba(0,0,0,0.42)` 遮罩 + 卡片按 cardPos 定位，
//      split＝一侧背景、一侧登录容器（容器在左/右随 side 切换），卡片位置相对登录容器；
//   ③ 预览与实际同值：遮罩与分栏容器底色必须与 BrandP 预览里的字面值逐字相等，
//      否则「后台看到的」和「登录页得到的」又会分家（这正是 F-71 的成因）。
//
// 运行：npx vitest run src/components/Login.brand_login.dom.test.tsx
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { render, cleanup } from '@testing-library/react'
import { ToastProvider } from '@/ui/langcross/src'
import { useBrandingStore, type Branding } from '@/branding'
import Login from './Login'

// 品牌夹具的默认值＝平台品牌（全部字段为空/未定制）
const BRAND_DEFAULT: Branding = {
  tenantId: 0, tenantName: '', brandName: '', brandLogo: '', domain: '',
  brandHomeBg: '', brandHomeBgStyle: '', brandLoginCardPos: '', brandLoginLayout: '',
  code: '', industry: '', industryName: '', dedicatedRegister: false,
}

// AI 接管流程与网络层不参与本锁：登录屏默认视图即够
vi.mock('./AiRegisterFlow', () => ({ default: () => null }))
vi.mock('@/api', () => ({
  getAuthToken: vi.fn(() => null),
  login: vi.fn(async () => ({ success: false, message: 'mock' })),
  authRegister: vi.fn(),
  sendEmailCode: vi.fn(),
  registerConfig: vi.fn(async () => ({ success: false })),
  forgotPassword: vi.fn(),
  resetPassword: vi.fn(),
  changePassword: vi.fn(),
  setAuthToken: vi.fn(),
  setActiveTenantId: vi.fn(),
  ssoProviders: vi.fn(async () => ({ enabled: false, providers: [] as { name: string; display_name: string }[] })),
  ssoLoginUrl: (p: string) => `/api/sso/login?provider=${encodeURIComponent(p)}`,
}))

// 品牌注入走 zustand 单一状态源，**不 mock @/branding**：
// BrandLoginShell 内部是同模块直调 useBranding()，模块内调用绕过 mock，
// 只有 setState 能让「壳」和「Login 本体」同时看到同一份品牌配置。
function setBrand(patch: Partial<Branding>) {
  useBrandingStore.setState({ ...BRAND_DEFAULT, ...patch })
}

// 遮罩与分栏容器底色的交付字面值：必须与 BrandP 预览用的完全一致
const VEIL = 'rgba(0,0,0,0.42)'
const SPLIT_FORM_BG = 'rgba(231,233,234,0.06)'

// 渲染登录屏并返回常用节点查询器
function renderSignin() {
  render(<ToastProvider><Login mode="home" onLogin={() => { /* noop */ }} /></ToastProvider>)
  return {
    shell: () => document.querySelector('.lc-auth-bg') as HTMLElement | null,
    brandShell: () => document.querySelector('.lc-brand-login') as HTMLElement | null,
    bgImg: () => document.querySelector('.lc-auth-bg img') as HTMLImageElement | null,
    veil: () => document.querySelector('.lc-brand-login__veil') as HTMLElement | null,
    cardWrap: () => document.querySelector('.lc-brand-login__card') as HTMLElement | null,
    panes: () => [...document.querySelectorAll('.lc-brand-login__pane')] as HTMLElement[],
    card: () => document.querySelector('.lc-auth-card') as HTMLElement | null,
  }
}

beforeEach(() => { setBrand({}) })
afterEach(() => { cleanup() })

describe('F-71 ① 默认形态零变化（未配背景图）', () => {
  it('壳只输出 .lc-auth-bg，品牌节点与注入样式一律不出现', () => {
    setBrand({ brandHomeBg: '' })
    const q = renderSignin()
    expect(q.shell()).toBeTruthy()
    expect(q.brandShell()).toBe(null)
    expect(q.bgImg()).toBe(null)
    expect(q.veil()).toBe(null)
    expect(q.cardWrap()).toBe(null)
    // 卡片仍直接挂在 .lc-auth-bg 下（改造前的 DOM 形状）
    expect(q.card()?.parentElement).toBe(q.shell())
    // CSS_BRAND_LOGIN 只在品牌形态注入：默认态文档里不得出现品牌壳样式表
    const css = [...document.querySelectorAll('style')].map((s) => s.textContent || '').join('\n')
    expect(css).not.toContain('.lc-brand-login__card')
    // 负向清零：旧「首页背景图」消费面也不许被顺手加回来（公开落地页外观属交付档）
    const landing = readFileSync(resolve(__dirname, 'Landing.tsx'), 'utf8')
    expect(landing).not.toContain('brandHomeBg')
    expect(landing).not.toContain('BrandLoginShell')
  })
})

describe('F-71 ② 全屏（full）形态接线', () => {
  it('背景图层 + 遮罩 + 卡片按 cardPos 定位，三者在同一壳内', () => {
    setBrand({
      brandHomeBg: '/brand/acme-bg.png',
      brandHomeBgStyle: JSON.stringify({ scale: 1.2, x: 40, y: 60, mode: 'cover' }),
      brandLoginLayout: JSON.stringify({ mode: 'full', side: 'right' }),
      brandLoginCardPos: JSON.stringify({ x: 28, y: 66 }),
    })
    const q = renderSignin()
    expect(q.brandShell()?.getAttribute('data-brand-login')).toBe('full')
    // 样式 JSON 真落到图层：x/y/scale 逐值等值（left/top/transform 都挂在 <img> 上，
    // 外层容器只负责 inset:-5% 外扩与裁切）
    const img = q.bgImg() as HTMLImageElement
    // src 原样落到 <img>（F-46 起后端只回图片地址，不再有 dataURI）
    expect(img.getAttribute('src')).toBe('/brand/acme-bg.png')
    expect(img.style.left).toBe('40%')
    expect(img.style.top).toBe('60%')
    expect(img.style.transform).toBe('translate(-50%, -50%) scale(1.2)')
    expect(img.style.objectFit).toBe('cover')
    const layer = img.parentElement as HTMLElement
    expect(layer.style.inset).toBe('-5%')
    expect(q.veil()).toBeTruthy()
    const wrap = q.cardWrap() as HTMLElement
    expect(wrap.style.getPropertyValue('--lc-brand-x')).toBe('28%')
    expect(wrap.style.getPropertyValue('--lc-brand-y')).toBe('66%')
    expect(q.card()?.parentElement).toBe(wrap)
  })

  it('布局 JSON 缺失/坏值 → 回落 full + 居中 50/50（解析器不抛，界面不崩）', () => {
    setBrand({ brandHomeBg: 'https://cdn.example.com/bg.jpg', brandLoginLayout: '{', brandLoginCardPos: 'null' })
    const q = renderSignin()
    expect(q.brandShell()?.getAttribute('data-brand-login')).toBe('full')
    const wrap = q.cardWrap() as HTMLElement
    expect(wrap.style.getPropertyValue('--lc-brand-x')).toBe('50%')
    expect(wrap.style.getPropertyValue('--lc-brand-y')).toBe('50%')
  })
})

describe('F-71 ② 分栏（split）形态接线', () => {
  it('side=right：DOM 顺序＝背景侧在前、登录容器侧在后，且无遮罩', () => {
    setBrand({
      brandHomeBg: '/brand/acme-bg.png',
      brandLoginLayout: JSON.stringify({ mode: 'split', side: 'right' }),
      brandLoginCardPos: JSON.stringify({ x: 50, y: 40 }),
    })
    const q = renderSignin()
    const shell = q.brandShell() as HTMLElement
    expect(shell.getAttribute('data-brand-login')).toBe('split')
    expect(shell.getAttribute('data-brand-side')).toBe('right')
    expect(shell.style.flexDirection).toBe('')
    const panes = q.panes()
    expect(panes.length).toBe(2)
    expect(panes[0].className).toContain('lc-brand-login__pane--bg')
    expect(panes[1].className).toContain('lc-brand-login__pane--form')
    expect(panes[0].querySelector('img')?.getAttribute('src')).toBe('/brand/acme-bg.png')
    // 卡片定位挂在**登录容器**里（与预览的 splitFormRef 同量纲）
    expect(q.cardWrap()?.parentElement).toBe(panes[1])
    expect(q.cardWrap()?.style.getPropertyValue('--lc-brand-y')).toBe('40%')
    // 分栏靠容器底色分层，不该出现全屏遮罩
    expect(q.veil()).toBe(null)
  })

  it('side=left：登录容器侧在视觉左侧（row-reverse），背景侧仍在文档后', () => {
    setBrand({
      brandHomeBg: '/brand/acme-bg.png',
      brandLoginLayout: JSON.stringify({ mode: 'split', side: 'left' }),
    })
    const q = renderSignin()
    const shell = q.brandShell() as HTMLElement
    expect(shell.getAttribute('data-brand-side')).toBe('left')
    expect(shell.style.flexDirection).toBe('row-reverse')
    const panes = q.panes()
    expect(panes[0].className).toContain('lc-brand-login__pane--bg')
    expect(panes[1].className).toContain('lc-brand-login__pane--form')
  })
})

describe('F-71 ③ 预览与实际同值（防止「后台一套、线上一套」再分家）', () => {
  const brandLoginCss = (() => {
    const src = readFileSync(resolve(__dirname, '..', 'branding.tsx'), 'utf8')
    const m = src.match(/const CSS_BRAND_LOGIN = `([\s\S]*?)`/)
    expect(m).toBeTruthy()
    return (m as RegExpMatchArray)[1]
  })()
  const brandP = readFileSync(resolve(__dirname, 'admin', 'BrandP.tsx'), 'utf8')

  it('遮罩底色与 BrandP 全屏预览的遮罩字面值逐字相等', () => {
    expect(brandLoginCss).toContain(`.lc-brand-login__veil{position:absolute;inset:0;z-index:1;background:${VEIL};}`)
    expect(brandP).toContain(`background: '${VEIL}'`)
  })

  it('分栏登录容器底色与 BrandP 分栏预览的容器字面值逐字相等', () => {
    expect(brandLoginCss).toContain(`.lc-brand-login__pane--form{display:flex;align-items:center;justify-content:center;background:${SPLIT_FORM_BG};}`)
    expect(brandP).toContain(`background:'${SPLIT_FORM_BG}'`)
  })

  it('卡片定位用负外边距而非 transform（祖先 transform 会抢走 fixed 后代的包含块）', () => {
    const cardRule = (brandLoginCss.match(/\.lc-brand-login__card\{[^}]*\}/) || [''])[0]
    expect(cardRule).toContain('margin-left:calc(0px - var(--lc-brand-half-w,200px))')
    expect(cardRule).toContain('margin-top:calc(0px - var(--lc-brand-half-h,220px))')
    expect(cardRule).not.toContain('transform:')
  })
})
