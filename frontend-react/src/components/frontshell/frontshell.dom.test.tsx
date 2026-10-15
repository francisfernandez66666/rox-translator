// ============================================================================
// components/frontshell/frontshell.dom.test.tsx — 前台外壳固定组件的行为锁
// ----------------------------------------------------------------------------
// ★ 2026-10-15 用户令「把这次的 UI 做成固定组件和布局方案」配套断言。
// 静态锁（FrontShell.layout.lock.test.ts）管结构口径；本文件管**行为**，
// 因为有一类缺陷静态锁看不见——最典型的是「点完即收」（pickNav）：
//   移动档抽屉里点一下「即时翻译」，若只靠「路由变了就收」，而当前已经在 /，
//   路由对象没有变化 ⇒ 抽屉赖在屏上、要再点一次遮罩才关。
//   这条**只在移动档 + 点当前项**时出现，源码字符串永远看不出真假。
// 覆盖面：
//   ① FrontMobileTopBar：汉堡 aria-label 本地化 + aria-expanded 跟随 props；
//      品牌名回落 t('app.title')；租户配 logo 时出图不出字。
//   ② FrontSideNav 桌面档：无抽屉档位、无遮罩、无折叠页脚；导航点击不触发 onClose。
//   ③ FrontSideNav 移动档：--drawer/--open 档位、遮罩可收起、导航点击**触发 onClose**
//      （桌面/移动共用同一份结构，这条是 pickNav 的真判据）。
//   ④ 折叠页脚：移动档才渲染，默认收起（<details> 无 open 属性）、点 summary 才展开；
//      传进 SiteFooter 的是 bare 档（抽屉里不许再自带一条顶缘线）。
//   ⑤ useMobileShell：断点走 matchMedia(≤640) 且能跟随 change 事件翻档（拖窗/横竖屏）。
// AccountMenu / SiteFooter 走替身：本测试只验外壳装配，不测它们内部（各有自己的用例）。
// 注意：本仓 vitest 未开 globals，RTL 不自动 cleanup，必须显式 afterEach(cleanup)。
// ============================================================================
// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest'
import { render, screen, cleanup, fireEvent, act } from '@testing-library/react'
import { MemoryRouter, useNavigate } from 'react-router-dom'
import { useEffect } from 'react'
import { useBrandingStore } from '@/branding'
import { t } from '@/i18n'

// jsdom 不实现 window.matchMedia（前台外壳判档全靠它）——这里给一个**可控**替身：
// 既能设初值，也能主动派发 change 事件，用来验证「档位会跟随变化」而不是只读挂载那一刻。
function stubMatchMedia(initial: boolean) {
  const listeners = new Set<(e: { matches: boolean }) => void>()
  let matches = initial
  window.matchMedia = ((q: string) => ({
    matches,
    media: q,
    addEventListener: (_: string, cb: (e: { matches: boolean }) => void) => { listeners.add(cb) },
    removeEventListener: (_: string, cb: (e: { matches: boolean }) => void) => { listeners.delete(cb) },
  })) as unknown as typeof window.matchMedia
  return {
    set(next: boolean) {
      matches = next
      act(() => { listeners.forEach((cb) => cb({ matches: next })) })
    },
  }
}

import FrontSideNav from './FrontSideNav'
import FrontMobileTopBar from './FrontMobileTopBar'
import { useMobileShell } from './useMobileShell'

// 替身件：把「注入的是组件、且 bare 档真的传进去了」变成可断言的 DOM 标记
const StubAccount = () => <span data-testid="stub-account">账号</span>
const StubFooter = ({ bare }: { bare?: boolean }) => <span data-testid="stub-footer">{bare ? 'bare' : 'full'}</span>

// 品牌位替身数据：品牌名走 store（不依赖 i18n 键值时断言才好写）
beforeEach(() => {
  useBrandingStore.setState({ brandName: '测试品牌', brandLogo: '' })
})

function renderShell(props: Partial<React.ComponentProps<typeof FrontSideNav>> = {}, mobile = false) {
  const onClose = vi.fn()
  const onTab = vi.fn()
  const onRoute = vi.fn()
  const utils = render(
    <MemoryRouter>
      <FrontSideNav
        isMobile={mobile}
        open={props.open ?? false}
        onClose={onClose}
        path="/"
        tab="workbench"
        onTab={onTab}
        onRoute={onRoute}
        tenantTag=""
        pkgLine=""
        isPersonal
        canUploadKb
        onUploadKb={vi.fn()}
        roleLevel={3}
        AccountMenu={StubAccount}
        SiteFooter={StubFooter}
        {...props}
      />
    </MemoryRouter>,
  )
  return { ...utils, onClose, onTab, onRoute }
}

afterEach(() => {
  cleanup()
  useBrandingStore.setState({ brandName: '测试品牌', brandLogo: '' })
})

describe('FrontMobileTopBar · 汉堡 + 品牌簇（★ 2026-10-15 用户令）', () => {
  it('汉堡 aria-label 按当前语言本地化、aria-expanded 跟随 props', () => {
    const onMenu = vi.fn()
    const { rerender } = render(<FrontMobileTopBar onMenu={onMenu} drawerOpen={false} />)
    const btn = screen.getByRole('button')
    // 汉堡不是「语言入口」那一类固定文案（见 AGENTS §一·5）：它按当前语言本地化
    expect(btn.getAttribute('aria-label')).toBe(t('app.openMenu'))
    expect(btn.getAttribute('aria-expanded')).toBe('false')
    fireEvent.click(btn)
    expect(onMenu).toHaveBeenCalledTimes(1)
    rerender(<FrontMobileTopBar onMenu={onMenu} drawerOpen />)
    expect(screen.getByRole('button').getAttribute('aria-expanded')).toBe('true')
  })

  it('无租户 logo：出品牌名（品牌名缺失时回落 app.title）', () => {
    render(<FrontMobileTopBar onMenu={vi.fn()} drawerOpen={false} />)
    expect(document.querySelector('.fs-mob-brand__name')?.textContent).toBe('测试品牌')
    cleanup()
    useBrandingStore.setState({ brandName: '' })
    render(<FrontMobileTopBar onMenu={vi.fn()} drawerOpen={false} />)
    expect(document.querySelector('.fs-mob-brand__name')?.textContent).toBe(t('app.title'))
  })

  it('租户配了 logo：出图、不出品牌名文字（白标口径）', () => {
    useBrandingStore.setState({ brandLogo: 'https://cdn.example/logo.png' })
    render(<FrontMobileTopBar onMenu={vi.fn()} drawerOpen={false} />)
    const img = document.querySelector('.fs-mob-brand img') as HTMLImageElement
    expect(img?.getAttribute('src')).toBe('https://cdn.example/logo.png')
    expect(document.querySelector('.fs-mob-brand__name')).toBeNull()
  })
})

describe('FrontSideNav · 桌面档（常驻左栏）', () => {
  it('不带抽屉档位、不渲染遮罩与折叠页脚；导航点击不触发 onClose', () => {
    const { onClose, onTab } = renderShell({ open: true }, false)
    const aside = document.querySelector('aside.lc-sidebar')!
    expect(aside.className).not.toContain('--drawer')
    expect(aside.className).not.toContain('--open')
    expect(document.querySelector('.lc-shell-scrim')).toBeNull()
    expect(document.querySelector('.fs-foot-fold')).toBeNull()
    fireEvent.click(screen.getByText(t('app.tabTickets')))
    expect(onTab).toHaveBeenCalledWith('tickets')
    expect(onClose, '桌面档没有抽屉可收，不该调 onClose').not.toHaveBeenCalled()
  })

  it('自助入口与账号菜单注入件都在栏内', () => {
    renderShell({}, false)
    expect(screen.getByTestId('stub-account')).toBeTruthy()
    expect(screen.getByText(t('hub.menuBilling'))).toBeTruthy()
    expect(screen.getByText(t('kb.topbarUpload'))).toBeTruthy()
  })
})

describe('FrontSideNav · 移动档（汉堡抽屉）', () => {
  it('抽屉两档随 open 切换，遮罩只在展开时出现且点击即收', () => {
    const { onClose, rerender } = { ...renderShell({ open: false }, true) }
    let aside = document.querySelector('aside.lc-sidebar')!
    expect(aside.className).toContain('fs-sidebar--drawer')
    expect(aside.className).not.toContain('fs-sidebar--open')
    expect(document.querySelector('.lc-shell-scrim')).toBeNull()
    rerender(
      <MemoryRouter>
        <FrontSideNav isMobile open onClose={onClose} path="/" tab="workbench" onTab={vi.fn()}
          onRoute={vi.fn()} tenantTag="" pkgLine="" isPersonal canUploadKb onUploadKb={vi.fn()}
          roleLevel={3} AccountMenu={StubAccount} SiteFooter={StubFooter} />
      </MemoryRouter>,
    )
    aside = document.querySelector('aside.lc-sidebar')!
    expect(aside.className).toContain('fs-sidebar--open')
    fireEvent.click(document.querySelector('.lc-shell-scrim')!)
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('★ 点导航项即收抽屉（点的是**当前项**也要收——路由不变化，靠的不是路由副作用）', () => {
    const { onClose, onTab } = renderShell({ open: true }, true)
    // path 就是 '/'、tab 就是 workbench：这一击不产生任何路由变化
    fireEvent.click(screen.getByText(t('app.tabWorkbench')))
    expect(onTab).toHaveBeenCalledWith('workbench')
    expect(onClose, 'pickNav 失守：点当前项后抽屉会赖在屏上').toHaveBeenCalledTimes(1)
  })

  it('自助入口与上传口在移动档同样点完即收', () => {
    const { onClose, onRoute } = renderShell({ open: true }, true)
    fireEvent.click(screen.getByText(t('hub.menuBilling')))
    expect(onRoute).toHaveBeenCalledWith('/billing')
    expect(onClose).toHaveBeenCalledTimes(1)
  })

  it('折叠页脚：默认收起、内容即 bare 档页脚、点 summary 才展开', () => {
    renderShell({ open: true }, true)
    const fold = document.querySelector('.fs-foot-fold') as HTMLDetailsElement
    expect(fold).toBeTruthy()
    expect(fold.hasAttribute('open'), '折叠页脚默认必须是收起态').toBe(false)
    expect(screen.getByTestId('stub-footer').textContent).toBe('bare')
    fireEvent.click(document.querySelector('.fs-foot-fold__sum')!)
    // jsdom 里 <details> 的 open 由浏览器默认行为驱动，jsdom 会实现 toggle；
    // 断到 open 属性上（不是只断「点得到」）——收起的价值就在这里
    expect((document.querySelector('.fs-foot-fold') as HTMLDetailsElement).hasAttribute('open')).toBe(true)
  })
})

describe('useMobileShell · 档位与抽屉状态机', () => {
  function Probe({ onState }: { onState: (s: ReturnType<typeof useMobileShell>) => void }) {
    const s = useMobileShell()
    useEffect(() => { onState(s) })
    return <span data-testid="mobile">{String(s.isMobile)}</span>
  }

  it('≤640 判档走 matchMedia，且跟随 change 事件翻档（拖窗/横竖屏）', () => {
    const mm = stubMatchMedia(false)
    const seen: ReturnType<typeof useMobileShell>[] = []
    render(<MemoryRouter><Probe onState={(s) => seen.push(s)} /></MemoryRouter>)
    expect(screen.getByTestId('mobile').textContent).toBe('false')
    mm.set(true)
    expect(screen.getByTestId('mobile').textContent).toBe('true')
    expect(seen[seen.length - 1].isMobile).toBe(true)
  })

  it('路由一变自动收起抽屉（浏览器前进/后退也兜住）', () => {
    stubMatchMedia(true)
    const seen: ReturnType<typeof useMobileShell>[] = []
    function Probe() {
      const s = useMobileShell()
      const nav = useNavigate()
      useEffect(() => { seen.push(s) })
      return (
        <>
          <span data-testid="mobile">{String(s.drawerOpen)}</span>
          <button type="button" data-testid="go" onClick={() => nav('/tickets')}>go</button>
        </>
      )
    }
    render(<MemoryRouter initialEntries={['/']}><Probe /></MemoryRouter>)
    act(() => { seen[seen.length - 1].open() })
    expect(screen.getByTestId('mobile').textContent).toBe('true')
    // 切路由（等价于点导航项 / 浏览器后退）：抽屉必须自己收起来
    act(() => { fireEvent.click(screen.getByTestId('go')) })
    expect(screen.getByTestId('mobile').textContent).toBe('false')
  })
})
