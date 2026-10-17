// ============================================================================
// components/publicshell/PublicShell.tsx — 未登录「营销门面」移动壳（唯一实现）
// ----------------------------------------------------------------------------
// 未登录门面（官网首页 / 定价 / 比价 / 登录注册）在移动档（≤640）复用**登录态前台外壳**
// 的同一套形态：顶部「汉堡 ＋ 品牌簇」，左栏化汉堡抽屉（沿用 .lc-sidebar 同款类名与令牌）。
// 桌面档一字不动——各页保留各自的营销头部（.lc-mkt-nav / .lc-prc-nav / .lc-cmp-nav）。
//
// 三条硬口径（改本件前必读）：
//   ① 断点只有一档：直接复用 frontshell 的 useMobileShell()（≤640），
//      本文件不得再写第二份 matchMedia/断点（历史上「两处各判一次档」最后必然打架）。
//   ② 抽屉只放**跨页**公开入口（定价 / 比价与算价 / API 文档 / 免费试用）；
//      语言钮与登录钮**必须留在顶栏右上角**（★ 2026-10-17 用户令「小屏幕把 language 选择器和登录按钮
//      拿出来放右上角」——它们是最高频的两个跨页入口，埋在抽屉里要多一次点击才够得到）；
//      各页自己的页内锚点与条款链仍留在桌面头部里，不在这里再抄一份（改一处漏一处的温床）。
//   ③ 移动档必须收起各页自带头部：publicshell.css 按 .pub-shell 作用域隐藏三支导航，
//      否则会出现「壳顶栏 ＋ 页面自己的顶栏」两条栏。登录/注册页把固定壳下移到顶栏之下。
//
// 类名是 e2e 判据靶子（.pub-mob-topbar / .pub-topbar-actions / .pub-topbar-login /
// .pub-drawer / .pub-drawer__cta），改形态时同步 e2e/mobile_uat.spec.ts 与本目录的锁，别只改一处。
// ============================================================================

import type { ReactNode } from 'react'
import { Icon } from '@/ui/langcross/src'
import type { IconName } from '@/ui/langcross/src/Icon'
import { LangSelect } from '@/components/LangSelect'
import { useBranding } from '@/branding'
import { useT } from '@/i18n'
import { openAPIDocsUrl } from '@/api/core'
import { useMobileShell } from '@/components/frontshell/useMobileShell'

/** PublicShell 入参：children＝门面内容；auth＝登录/注册页（固定壳需让位顶栏） */
export interface PublicShellProps {
  children: ReactNode
  /** true＝登录/注册页：给固定定位的 .lc-auth-bg 让出顶栏高度（publicshell.css 按 --auth 分支） */
  auth?: boolean
}

/**
 * 未登录门面移动壳。桌面档只渲染 children（各页自带头部）；
 * 移动档在前面加「顶栏 ＋ 抽屉」，并隐藏各页头部（CSS 收口）。
 */
export default function PublicShell({ children, auth = false }: PublicShellProps) {
  const shell = useMobileShell()
  return (
    <div className={'pub-shell' + (auth ? ' pub-shell--auth' : '')}>
      {shell.isMobile && <PublicMobileTopBar onMenu={shell.open} drawerOpen={shell.drawerOpen} auth={auth} />}
      {shell.isMobile && <PublicDrawer open={shell.drawerOpen} onClose={shell.close} />}
      <div className="pub-main">{children}</div>
    </div>
  )
}

/** 移动档顶栏：汉堡（左）＋ 品牌簇 ＋ 右簇（语言钮 / 登录钮）。与登录态顶栏同形同令牌。 */
function PublicMobileTopBar({ onMenu, drawerOpen, auth }: { onMenu: () => void; drawerOpen: boolean; auth: boolean }) {
  const [, t] = useT()
  const branding = useBranding()
  return (
    <div className="lc-mob-topbar fs-mob-topbar pub-mob-topbar">
      <button type="button" className="lc-mob-topbar__icon" onClick={onMenu}
              aria-label={t('app.openMenu')} aria-expanded={drawerOpen}>
        <Icon n="menu" size={22} />
      </button>
      <a className="fs-mob-brand pub-mob-brand" href="/" aria-label={branding.brandName || t('app.title')}>
        {branding.brandLogo
          ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 24, flex: 'none' }} />
          : (
            <>
              <Icon n="brand" style={{ fontSize: 24, flex: 'none' }} />
              <span className="fs-mob-brand__name">{branding.brandName || t('app.title')}</span>
            </>
          )}
      </a>
      {/* 右簇（★ 2026-10-17 用户令：语言选择器与登录钮从抽屉搬到这里）：
          margin-inline-start:auto 顶到右端；品牌名限宽省略号，窄机（320）下让位给右簇。
          登录/注册页（auth）不挂登录钮——用户就在那一页，再给一枚「登录」是自指重复。 */}
      <div className="pub-topbar-actions">
        <LangSelect align="right" />
        {!auth && <a className="pub-topbar-login" href="/login">{t('land.navLogin')}</a>}
      </div>
    </div>
  )
}

/** 抽屉：品牌行（回首页）＋ 跨页入口 ＋ 底部免费试用 CTA（语言/登录已在顶栏右簇）。 */
function PublicDrawer({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [, t] = useT()
  const branding = useBranding()
  const links: { href: string; icon: IconName; label: string; ext?: boolean }[] = [
    { href: '/pricing', icon: 'card', label: t('land.pNavPricing') },
    { href: '/compare', icon: 'chart', label: t('cmp.navEntry') },
    { href: openAPIDocsUrl(), icon: 'terminal', label: t('land.footApiDoc'), ext: true },
  ]
  return (
    <>
      {open && <div className="lc-shell-scrim pub-scrim" onClick={onClose} />}
      <aside className={'lc-sidebar fs-sidebar fs-sidebar--drawer pub-drawer' + (open ? ' fs-sidebar--open' : '')}>
        {/* 品牌行＝回首页入口（抽屉里不再单列一条「首页」，省一枚键） */}
        <div className="lc-sidebar__logo">
          <a className="lc-sidebar__brand pub-drawer__brand" href="/" onClick={onClose}>
            {branding.brandLogo
              ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 28, flex: 'none' }} />
              : (
                <>
                  <Icon n="brand" style={{ fontSize: 26, flex: 'none' }} />
                  <span className="lc-sidebar__brandname">{branding.brandName || t('app.title')}</span>
                </>
              )}
          </a>
        </div>

        <nav className="fs-nav">
          {links.map((it) => (
            <a key={it.href} className="lc-side-item pub-drawer__item" href={it.href}
               {...(it.ext ? { target: '_blank', rel: 'noopener' } : {})} onClick={onClose}>
              <span className="lc-side-item__icon"><Icon n={it.icon} /></span>{it.label}
            </a>
          ))}
        </nav>

        <div style={{ flex: 1, minHeight: 0 }} />

        <div className="lc-sidebar__foot">
          <a className="pub-drawer__cta" href="/register" onClick={onClose}>{t('land.ctaFree')}</a>
        </div>
      </aside>
    </>
  )
}
