// ============================================================================
// components/frontshell/FrontSideNav.tsx — 前台外壳 · 左栏（唯一实现）
// ----------------------------------------------------------------------------
// 承载「除会话区外的一切」：品牌 / 租户身份 / 语言 / 工作台 Tab / 自助入口 /
// 套餐积分 / 账号菜单 / 通知中心；尾部挂一份折叠页脚（★ 2026-10-16 用户令：两档一致，
// 桌面档也从右主列列尾收进这一栏——旧形态「桌面常驻列尾 / 移动才折叠」已退役）。
// 同一节点两种形态，由 props 切换（样式见 src/styles/frontshell.css）：
//   · 桌面档（isMobile=false）：常驻左栏（.fs-sidebar 把位置钉回静态）
//   · 移动档（isMobile=true） ：汉堡抽屉（.fs-sidebar--drawer + --open 滑入）
//
// 三条硬口径（改本件前必读）：
//   ① 点导航项一律经 pickNav：先跳转、再在移动档收起抽屉。只靠「路由变即收」
//      兜不住「点的是当前那一项」（path 没变、effect 不触发），抽屉会赖在屏上。
//   ② AccountMenu / SiteFooter 以「组件」形式注入（壳侧 React.lazy 持有）：
//      本件若静态 import 它们，会把这两份字节拉进前台入口包（本仓有体积闸门）。
//   ③ 类名与结构是 e2e 判据靶子（.fs-mob-brand / .fs-foot-fold / .lc-side-item--active …），
//      改形态时同步 e2e/mobile_uat.spec.ts 与 FrontShell.layout.lock.test.ts，别只改一处。
// ============================================================================

import type { ComponentType, ReactNode } from 'react'
import { Icon, Badge } from '@/ui/langcross/src'
import { LangSelect } from '@/components/LangSelect'
import { useBranding } from '@/branding'
import { useT } from '@/i18n'

/** 工作台三个 Tab（前台外壳的页内路由档位，与 /  /tickets  /editor 一一对应） */
export type WorkbenchTab = 'workbench' | 'tickets' | 'editor'

/** 左栏要用的账号菜单面（与 components/AccountMenu.tsx 的 Props 结构等值） */
export interface FrontAccountMenuProps {
  showAdminConsole?: boolean
  onGotoAdmin?: () => void
  onGotoMy?: () => void
  showNotify?: boolean
}

/** 无边框页脚（与 components/SiteFooter.tsx 的 Props 结构等值） */
export interface FrontFooterProps {
  bare?: boolean
}

/** FrontSideNav 入参：档位/开合由 useMobileShell 给，业务数据由外壳给 */
export interface FrontSideNavProps {
  /** 是否移动档（≤640）：true 时本件化抽屉 */
  isMobile: boolean
  /** 抽屉是否展开（仅移动档有意义） */
  open: boolean
  /** 收起抽屉（移动档点导航项/遮罩后调用） */
  onClose: () => void
  /** 当前路径：决定自助入口的激活态 */
  path: string
  /** 当前工作台 Tab。★ 2026-10-19 G15-D8：本件**不再消费它**（激活态改由 path 单向派生，
   *  见组件内 activeKey），保留此字段仅为不影响调用方（外壳仍在传）；将来从 props 摘除属破坏性改动。 */
  tab: WorkbenchTab
  /** 切工作台 Tab */
  onTab: (tab: WorkbenchTab) => void
  /** 路由跳转（自助入口与账号菜单共用，落点由外壳的 navigate 决定） */
  onRoute: (to: string) => void
  /** 租户身份标签（企业简称；空串＝个人版，显示 t('app.personalPlan')） */
  tenantTag: string
  /** 已格式化的「套餐·积分」行（空串＝不展示这一格） */
  pkgLine: string
  /** 是否个人版：决定「我的邀请」入口是否露出（与 /invites 路由守卫同口径） */
  isPersonal: boolean
  /** 是否可上传知识库（roleLevelSafe(user.role) >= 2） */
  canUploadKb: boolean
  /** 打开知识库上传弹窗 */
  onUploadKb: () => void
  /** 用户角色等级：账号菜单「进入后台」的判据（>=2 才露出） */
  roleLevel: number
  /** 账号菜单件（壳侧懒加载件注入，见口径②） */
  AccountMenu: ComponentType<FrontAccountMenuProps>
  /** 页脚件（同上；移动档以 bare 档挂进抽屉折叠块） */
  SiteFooter: ComponentType<FrontFooterProps>
}

/**
 * 前台外壳左栏。桌面档常驻、移动档化汉堡抽屉；两个档位共用同一份结构，
 * 不用「桌面一套 + 移动一套」两棵子树（那种写法迟早漂移出两种观感）。
 */
export default function FrontSideNav({
  isMobile, open, onClose, path, onTab, onRoute,
  tenantTag, pkgLine, isPersonal, canUploadKb, onUploadKb, roleLevel,
  AccountMenu, SiteFooter,
}: FrontSideNavProps) {
  const [, t] = useT()
  const branding = useBranding()

  // ★ 2026-10-19 G15-D8：「当前在哪一项」由 path **单向派生**，不再吃外壳传来的 tab。
  //   缺陷形态（现网 D8 实测）：外壳的 tab 派生对 /billing、/packages 落默认 'workbench'，
  //   于是这三个路径下「即时翻译」与「计费与套餐」**各亮一次**（两把尺子）。
  //   派生口径与外壳一致（workbench 兜底保持 /my、/chat-history 仍亮「即时翻译」，
  //   只有 /billing、/packages 由 2 改 1）。激活态旁落项为零是本条判据，见 frontshell.dom.test.tsx。
  const activeKey: WorkbenchTab | 'billing' | 'invites' =
    path.startsWith('/tickets') ? 'tickets'
      : path.startsWith('/editor') ? 'editor'
        : (path.startsWith('/billing') || path.startsWith('/packages')) ? 'billing'
          : path.startsWith('/invites') ? 'invites'
            : 'workbench'

  // 点完即收（移动档）：只靠路口 effect 对「点当前那一项」无效（path 没变）——见口径①
  // 普通函数即可（不是 hook）：本件不做 memo 稳定性优化，闭包每次渲染重建的成本远低于误用的风险
  const pickNav = (fn: () => void) => { fn(); if (isMobile) onClose() }

  // 移动档 = 抽屉（--drawer）＋ 展开态（--open）；桌面档只有基础类（位置由 CSS 钉回静态）
  const asideClass = 'lc-sidebar fs-sidebar' + (isMobile ? ' fs-sidebar--drawer' + (open ? ' fs-sidebar--open' : '') : '')

  return (
    <>
      {/* 抽屉遮罩：点它收起。只在「移动档 + 已展开」时存在——桌面档没有抽屉可收。
          遮罩先于 aside 渲染，靠 z-index（60）与 aside 分层，不用 DOM 顺序。 */}
      {isMobile && open && <div className="lc-shell-scrim" onClick={onClose} />}
      <aside className={asideClass} aria-label={t('app.tabWorkbench')}>
        {/* 品牌位：复用 .lc-sidebar__logo（后台侧栏同款字阶/内垫）。白标口径不变——
            租户配了 logo 出图，否则出「品牌名」前缀一枚图标，缺省回落 app.title。
            字阶放大类只对中文加：英文品牌名放大反而显笨重（用户明令只改中文）。 */}
        <div className={'lc-sidebar__logo' + (branding.brandName ? '' : '') + (isZhLocale() ? ' lc-sidebar__logo--zh' : '')}>
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 28, flex: 'none' }} />
            : (
              <span className="lc-sidebar__brand">
                <Icon n="brand" style={{ fontSize: 26, flex: 'none' }} />
                <span className="lc-sidebar__brandname">{branding.brandName || t('app.title')}</span>
              </span>
            )}
          <span style={{ flex: 1 }} />
          {/* 租户身份（个人版 / 企业简称）留在 logo 行右侧。
              品牌项 flex:0 1 auto 可省略号，身份项 flex:none 不收缩（窄栏不许把品牌挤成竖排）。 */}
          <span className="lc-sidebar__tools">
            <div className="fs-tenant" title={tenantTag || t('app.personalPlan')}>
              {tenantTag
                ? <><Icon n="building" style={{ verticalAlign: '-3px', marginInlineEnd: 4 }} />{tenantTag.length > 10 ? tenantTag.slice(0, 10) + '…' : tenantTag}</>
                : <>{t('app.personalPlan')}</>}
            </div>
          </span>
        </div>

        {/* 语言钮单独一行（logo 下方、导航上方），左对齐与下方导航同起手 */}
        <div className="fs-langrow">
          <LangSelect align="left" />
        </div>

        {/* 功能菜单：三个工作台 Tab（原桌面顶栏三枚 .app-tab 迁此），选中态从 URL 反推。
            行样式复用 .lc-side-item / .lc-side-item--active（白底黑字反相胶囊，同后台侧栏）。 */}
        <nav className="fs-nav">
          <button type="button" className={'lc-side-item' + (activeKey === 'workbench' ? ' lc-side-item--active' : '')} onClick={() => pickNav(() => onTab('workbench'))}>
            <span className="lc-side-item__icon"><Icon n="chat" /></span>{t('app.tabWorkbench')}
          </button>
          <button type="button" className={'lc-side-item' + (activeKey === 'tickets' ? ' lc-side-item--active' : '')} onClick={() => pickNav(() => onTab('tickets'))}>
            <span className="lc-side-item__icon"><Icon n="clipboard" /></span>{t('app.tabTickets')}
          </button>
          <button type="button" className={'lc-side-item' + (activeKey === 'editor' ? ' lc-side-item--active' : '')} onClick={() => pickNav(() => onTab('editor'))}>
            <span className="lc-side-item__icon"><Icon n="pencil" /></span>{t('app.tabEditor')}
          </button>
        </nav>

        {/* 撑杆：把底部信息带顶到栏底（否则信息带紧贴导航，下半栏留大片空档）。
            原「新建翻译」钮与「对话历史」列表已按用户令撤除，这个 spacer 顶替它们的 flex:1。 */}
        <div style={{ flex: 1, minHeight: 0 }} />

        {/* 底部信息带：自助入口 + 套餐积分 + 账号菜单 */}
        <div className="lc-sidebar__foot">
          <nav className="fs-nav">
            {/* 我的套餐 + 我的余额已合并为一项（落 /billing 账单中心，该页含余额+流水+发票）；
                /packages 也点亮这一项：它已并入账单中心，两个路径都算「在账单区」 */}
            <button type="button" className={'lc-side-item' + (activeKey === 'billing' ? ' lc-side-item--active' : '')} onClick={() => pickNav(() => onRoute('/billing'))}>
              <span className="lc-side-item__icon"><Icon n="card" /></span>{t('hub.menuBilling')}
            </button>
            {isPersonal && (
              <button type="button" className={'lc-side-item' + (activeKey === 'invites' ? ' lc-side-item--active' : '')} onClick={() => pickNav(() => onRoute('/invites'))}>
                <span className="lc-side-item__icon"><Icon n="chain" /></span>{t('app.navInvites')}
              </button>
            )}
            {canUploadKb && (
              <button type="button" className="lc-side-item" onClick={() => pickNav(onUploadKb)}>
                <span className="lc-side-item__icon"><Icon n="upload" /></span>{t('kb.topbarUpload')}
              </button>
            )}
          </nav>
          {!!pkgLine && <Badge className="pkg-line-tag">{pkgLine}</Badge>}
          {/* 底部这一行只留账号菜单：账号菜单内含「消息(N)」（通知浮层）与「我的账号」（跳 /my），
              取代被删的侧栏项与前台独立铃铛。roleLevel>=2 才露出「进入后台」。 */}
          <div className="fs-footrow">
            <AccountMenu showAdminConsole={roleLevel >= 2} onGotoAdmin={() => onRoute('/admin')}
                         onGotoMy={() => onRoute('/my')} showNotify />
          </div>
        </div>

        {/* 页脚收进左栏尾部并折叠（默认收起，展开＝版权行 + 品宣尾注 + 手册 + 协议）。
            ★ 2026-10-16 用户令：两档一致——桌面档也从右主列列尾收进这一栏（旧形态已退役）。
            传 bare 去掉「贴外壳列尾」那身形态（auto 上边距 / 深色面 / 自成一条的顶缘线）——
            这一栏尾部已有 .fs-foot-fold 的顶缘分隔，页脚再自带来一条就是双线。 */}
        <details className="fs-foot-fold">
          <summary className="fs-foot-fold__sum">
            <Icon n="book" style={{ fontSize: 14, verticalAlign: '-2px', marginInlineEnd: 6 }} />
            {t('app.legalLinks')}
          </summary>
          <SiteFooter bare />
        </details>
      </aside>
    </>
  )
}

/** 中文书写体系（简/繁）判据：品牌字号放大只对这两档生效（与 i18n 的 lang 取值同口径） */
function isZhLocale(): boolean {
  // 从 document 读当前语种（与 i18n 的 app_lang 落点同源，读不到按非中文处理＝不放大）
  try {
    const v = document.documentElement.lang || ''
    return v === 'zh' || v === 'zh_hant' || v.startsWith('zh')
  } catch { return false }
}

/** 供外壳复用的显式类型导出（避免调用方 import type 到深层路径） */
export type { ReactNode }
