// ============================================================================
// components/frontshell/FrontMobileTopBar.tsx — 前台外壳 · 移动档顶栏（唯一实现）
// ----------------------------------------------------------------------------
// 移动档（≤640）顶栏只剩两件：汉堡（左）＋ 品牌簇（右邻，logo + 品牌名）。
// ★ 2026-10-15 用户令「汉堡旁边显示更大 logo 和品牌名」。
//
// 三条口径：
//   ① 原来的「当前页名」位在此件里**已不存在**——页名与页内标题重复（用户看过截图
//      后明令去掉），品牌簇占的正是这一格，顶栏高度（52）不变。
//   ② 语言 / 账号 / 通知 / 余额 / 上传口全部收进抽屉（FrontSideNav），
//      本件刻意不挂任何业务读点——顶栏每渲染一次就多一次订阅，得不偿失。
//   ③ 图标 24px / 品牌名 20px-600：与页标题同档，视觉上「这一页的标题＝品牌」。
//      字号写在 frontshell.css（.fs-mob-brand__name），本件不带内联字号，改档位只改一处。
// ============================================================================

import { Icon } from '@/ui/langcross/src'
import { useBranding } from '@/branding'
import { useT } from '@/i18n'

/** FrontMobileTopBar 入参：开抽屉动作 + 抽屉态（只用于 aria-expanded，读屏用户要的是语义） */
export interface FrontMobileTopBarProps {
  /** 点汉堡：打开抽屉 */
  onMenu: () => void
  /** 抽屉是否展开：映射到 aria-expanded（视觉上顶栏不因此变化） */
  drawerOpen: boolean
}

/**
 * 移动档顶栏。桌面档不渲染（桌面档的入口都在左栏常驻，顶栏整体退役）。
 */
export default function FrontMobileTopBar({ onMenu, drawerOpen }: FrontMobileTopBarProps) {
  const [, t] = useT()
  const branding = useBranding()
  return (
    <div className="lc-mob-topbar fs-mob-topbar">
      {/* 汉堡＝抽屉唯一开关。aria-label 走 t('app.openMenu')（本地化）：
          这不是「语言入口」那一类「给看不懂当前界面的人用的固定文案」，
          汉堡的语义是「打开菜单」，按当前语言本地化才对读屏用户友好。 */}
      <button type="button" className="lc-mob-topbar__icon" onClick={onMenu}
              aria-label={t('app.openMenu')} aria-expanded={drawerOpen}>
        <Icon n="menu" size={22} />
      </button>
      {/* 品牌簇：租户配了 logo 出图（24 高），否则图标 + 品牌名（缺省回落 app.title）。
          品牌名限宽省略号（超长企业名不许把汉堡挤走）。 */}
      <span className="fs-mob-brand">
        {branding.brandLogo
          ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 24, flex: 'none' }} />
          : (
            <>
              <Icon n="brand" style={{ fontSize: 24, flex: 'none' }} />
              <span className="fs-mob-brand__name">{branding.brandName || t('app.title')}</span>
            </>
          )}
      </span>
    </div>
  )
}
