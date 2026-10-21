// ============================================================================
// components/AccountMenu.tsx — 右上角账号菜单（前台与后台共用）
// 职责：提供进入后台（前台专用）、修改密码、换绑邮箱、注销账号、退出登录功能。
// 改密/换绑弹窗统一在此托管，确保前台与后台体验一致（含邮箱验证码、强制流程）。
// 呈现层替换：TDesign Dropdown→langcross ContextMenu、Button(variant=text)→自定义触发按钮、
//             MessagePlugin→useToast。
// ============================================================================
import { useEffect, useRef, useState, useLayoutEffect } from 'react'
import { createPortal } from 'react-dom'
import { Icon, CaretDownIcon, ContextMenu, useToast } from '@/ui/langcross/src'
import type { MenuItem } from '@/ui/langcross/src'
import { useAuth } from '@/stores/auth'
import { useT } from '@/i18n'
import { meContext } from '@/api'
import { useNotify, NotifyPanel } from './Bell'
import { PasswordModal, EmailBindModal, DeactivateModal, JobRoleModal } from './modals'

// 菜单图标内联样式：基线对齐 + 与文案留 8px（图标 16×16，文案左对齐）
const MI: React.CSSProperties = { verticalAlign: '-3px', marginInlineEnd: 8, flex: 'none' }

// Props 账号菜单组件的入参（区分前台/后台的入口配置）
interface Props {
  /** 前台专用：是否展示「进入后台」入口 */
  showAdminConsole?: boolean
  /** 前台点击「进入后台」的回调 */
  onGotoAdmin?: () => void
  /** 后台专用：是否展示「返回工作台」入口 */
  showWorkbench?: boolean
  /** 后台点击「返回工作台」的回调 */
  onGotoWorkbench?: () => void
  /** ★ 2026-09-24 〇-S（#7）前台专用：传入即在下拉露出「套餐/账单/账号」自助入口。
   *  原左侧汉堡抽屉退役，抽屉里的四个路由入口迁到这里（回调只给 path，跳转由外壳 navigate 做） */
  selfNav?: (path: string) => void
  /** 邀请有礼入口仅个人用户可见（与 /invites 路由守卫同口径，企业用户点了会被弹回） */
  showInvites?: boolean
  /** ★ 2026-10-15 用户令：传入即在菜单露出「我的账号」（侧栏那一项已删，入口收进此下拉） */
  onGotoMy?: () => void
  /** ★ 2026-10-15 用户令：传入即在菜单露出「消息(N)」，点开弹通知浮层（前台不再挂独立铃铛） */
  showNotify?: boolean
}

/** 右上角账号菜单组件：进入后台、消息、我的账号、改密、换绑邮箱、注销与退出登录（前台与后台共用） */
export default function AccountMenu({ showAdminConsole, onGotoAdmin, showWorkbench, onGotoWorkbench, selfNav, showInvites, onGotoMy, showNotify }: Props) {
  const { user, logout } = useAuth()
  const { toast } = useToast()
  const [, t] = useT() // ★ E14：lang 未使用
  // ★ 2026-10-15：通知数据层（未读轮询/列表/已读）与浮层，复用 Bell.tsx 的 useNotify/NotifyPanel
  const notify = useNotify()
  // 通知浮层开关：作为「消息(N)」菜单项的落点（菜单项选完即关，浮层另开）
  const [notifyOpen, setNotifyOpen] = useState(false)
  // 当前用户邮箱，用于改密验证码与换绑弹窗
  const [curEmail, setCurEmail] = useState('')
  // 当前职业角色 code（2026-09-19）：与邮箱同一次 meContext 取回，供角色弹窗预置
  const [curJobRole, setCurJobRole] = useState('')
  // 弹窗开关状态
  const [openPwd, setOpenPwd] = useState(false)
  const [openBind, setOpenBind] = useState(false)
  const [openDeact, setOpenDeact] = useState(false)
  const [openRole, setOpenRole] = useState(false)
  // ContextMenu 开关与锚点坐标
  const [menuOpen, setMenuOpen] = useState(false)
  const [menuPos, setMenuPos] = useState({ x: 0, y: 0 })
  const triggerRef = useRef<HTMLButtonElement>(null)
  // 通知浮层的外点关闭基准：触发器 + 浮层都在这一层里
  const wrapRef = useRef<HTMLDivElement>(null)
  // ★ 2026-10-17 用户报「打开通知中心会强制左边菜单栏拖动」：浮层改成 portal 到 body 的固定定位，
  //   它的 DOM 已不在 wrapRef 里，故外点判定另留一个浮层节点基准。
  const noticeRef = useRef<HTMLDivElement>(null)
  // 浮层落点（开在触发器上沿、四边夹进视口）；现算而非写死，见 placeNotice()
  const [notifyPos, setNotifyPos] = useState<{ left: number; bottom: number; width: number; maxH: number } | null>(null)

  // 读取当前邮箱，供改密验证码与换绑弹窗使用
  useEffect(() => {
    if (!user) return
    ;(async () => {
      try {
        const c = await meContext()
        if (c.success) {
          setCurEmail(String((c as unknown as { email?: string }).email || ''))
          setCurJobRole(String((c as unknown as { job_role?: string }).job_role || ''))
        }
      } catch { /* 忽略接口错误 */ }
    })()
  }, [user])

  // 通知浮层的外点/Esc 关闭（与 ContextMenu 同一交互口径：点哪儿都能关是底线）
  useEffect(() => {
    if (!notifyOpen) return
    const onDown = (e: MouseEvent) => {
      const t = e.target as Node
      if (wrapRef.current?.contains(t)) return
      // 浮层已 portal 到 body（见下方 render）：它不再是 wrapRef 的后代，必须单独放行内部点击
      if (noticeRef.current?.contains(t)) return
      setNotifyOpen(false)
    }
    const onKey = (e: KeyboardEvent) => { if (e.key === 'Escape') setNotifyOpen(false) }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [notifyOpen])

  // 浮层打开期间：视口尺寸变化、或左栏自身滚动（capture 抓祖先滚动）都要重锚，
  // 否则固定定位的浮层会停在旧坐标、与触发器脱锚。
  useLayoutEffect(() => {
    if (!notifyOpen) { setNotifyPos(null); return }
    const onMove = () => placeNotice()
    window.addEventListener('resize', onMove)
    window.addEventListener('scroll', onMove, true)
    return () => {
      window.removeEventListener('resize', onMove)
      window.removeEventListener('scroll', onMove, true)
    }
  }, [notifyOpen])

  // 构建菜单项列表：
  // 后台不展示「进入后台」；仅普通用户显示「注销」；其余为消息/账号/改密/换绑/退出
  const options = [
    // 后台专用：返回前台工作台
    ...(showWorkbench ? [{ content: <><Icon n="chat" style={MI} />{t('menu.backWorkbench')}</>, value: 'workbench', onClick: () => onGotoWorkbench?.() }] : []),
    // 前台专用：跳转后台管理控制台
    ...(showAdminConsole ? [{ content: <><Icon n="wrench" style={MI} />{t('menu.adminConsole')}</>, value: 'admin', onClick: () => onGotoAdmin?.() }] : []),
    // ★ 2026-09-24 〇-S（#7）：原汉堡抽屉四入口并入本下拉（selfNav 传入才露出，后台侧不露）；
    //   ★ 2026-10-15：桌面侧栏已承载套餐/余额/邀请，故桌面不再传 selfNav（只移动档传，移动无侧栏）
    ...(selfNav ? [
      { content: <><Icon n="package" style={MI} />{t('app.navPackages')}</>, value: 'nav-packages', onClick: () => selfNav('/packages') },
      { content: <><Icon n="card" style={MI} />{t('app.navBilling')}</>, value: 'nav-billing', onClick: () => selfNav('/billing') },
      ...(showInvites ? [{ content: <><Icon n="chain" style={MI} />{t('app.navInvites')}</>, value: 'nav-invites', onClick: () => selfNav('/invites') }] : []),
      { content: <><Icon n="gear" style={MI} />{t('app.navMy')}</>, value: 'nav-my', onClick: () => selfNav('/my') },
    ] : []),
    // ★ 2026-10-15 用户令：消息(N) 收进本下拉（前台不再挂独立铃铛）。未读>0 时尾随计数。
    ...(showNotify ? [{
      content: (
        <span style={{ display: 'inline-flex', alignItems: 'center', width: '100%' }}>
          <Icon n="bell" style={MI} />{t('bell.title')}
          {notify.unread > 0 && <span className="am-count">{notify.unread}</span>}
        </span>
      ),
      value: 'notify',
      onClick: () => { placeNotice(); setNotifyOpen(true); notify.setOpen(true); void notify.loadList() },
    }] : []),
    // ★ 2026-10-15 用户令：我的账号（侧栏那一项已删）——落 /my（账号设置 + 职业角色）。
    //   selfNav 已含 nav-my（移动档无侧栏），两者同时传会撞出两个同名项，故只在没走 selfNav 时补。
    ...(onGotoMy && !selfNav ? [{ content: <><Icon n="gear" style={MI} />{t('app.navMy')}</>, value: 'my', onClick: () => onGotoMy() }] : []),
    // 修改密码：打开邮箱验证码 + 新密码弹窗；无邮箱先引导绑定，避免弹窗发码必失败
    {
      content: <><Icon n="lock" style={MI} />{t('pwd.title')}</>,
      value: 'pwd',
      onClick: () => {
        if (curEmail) setOpenPwd(true)
        else setOpenBind(true)
      },
    },
    // 换绑邮箱：打开绑定/换绑邮箱弹窗
    { content: <><Icon n="mail" style={MI} />{t('menu.changeEmail')}</>, value: 'email', onClick: () => setOpenBind(true) },
    // 职业角色维护（2026-09-19）：角色只绑用户不绑企业，转岗/转行随时切换
    { content: <><Icon n="user" style={MI} />{t('role.menu')}</>, value: 'role', onClick: () => setOpenRole(true) },
    // 仅普通用户可注销账号
    ...(user?.role === 'user' ? [{ content: <><Icon n="trash" style={MI} />{t('menu.deactivate')}</>, value: 'deact', onClick: () => setOpenDeact(true) }] : []),
    // 退出登录并提示（末项 danger，ContextMenu 会自动在其上方加 1px 分隔）
    { content: <><Icon n="logout" style={MI} />{t('common.logout')}</>, value: 'logout', onClick: () => { logout(); toast({ title: t('app.bye'), tone: 'success' }) } },
  ]

  // 打开菜单：以触发按钮右下方为锚点（宽 220，右对齐触发按钮）
  // 220 是 ContextMenu 的固定宽度，右缘贴触发按钮右缘；
  // Math.max(8, …) 防按钮距视口左缘不足 220px（窄屏）时算出负坐标，
  // 与 ContextMenu 内部 useLayoutEffect 的夹边形成双保险（调用侧先给出合法锚点）。
  function openMenu() {
    const el = triggerRef.current
    if (!el) return
    const r = el.getBoundingClientRect()
    setMenuPos({ x: Math.max(8, r.right - 220), y: r.bottom + 6 })
    setMenuOpen(true)
  }

  // 通知浮层落点（★ 2026-10-17 用户报「通知中心不自适应、会强制左边菜单栏拖动」）：
  // 锚点＝触发器矩形，向上展开（账号菜单贴着左栏底缘，向下开必溢出视口），左右与高度都夹进视口。
  // 为什么必须现算、且必须 portal + position:fixed：左栏（.lc-sidebar）宽仅 237（桌面）／295（移动抽屉）
  // 且自带 overflow-y:auto——按 CSS 的 overflow 归一规则，另一轴因此也算 auto，
  // 于是旧形态里那个写死 300px、静态留在栏内的浮层会把左栏撑出横向滚动条（实测溢出 73/15px，
  // 正是用户看到的「强制左边菜单栏拖动」，而页面级 document 溢出是 0，所以 P7 那类整页判据看不见它）。
  function placeNotice() {
    const el = triggerRef.current
    if (!el) return
    const r = el.getBoundingClientRect()
    const width = Math.min(300, window.innerWidth - 24) // 窄屏下不许顶破视口
    const left = Math.max(8, Math.min(r.left, window.innerWidth - width - 8))
    const bottom = Math.max(8, window.innerHeight - r.top + 6) // 触发器上沿之上 6px
    const maxH = Math.max(160, Math.min(420, r.top - 16)) // 不越过视口上缘
    setNotifyPos({ left, bottom, width, maxH })
  }

  // 退出项标 danger：ContextMenu 对末项 danger 会自动绘制上方分隔线，无需额外分隔配置
  const menuItems: MenuItem[] = options.map((o) => ({
    key: o.value,
    label: o.content,
    danger: o.value === 'logout',
    onSelect: o.onClick,
  }))

  return (
    <>
      <style>{AM_CSS}</style>
      {/* am-wrap 是通知浮层的定位基准（触发器与浮层同层，外点判定才能一眼判"点的是自己"） */}
      <div className="am-wrap" ref={wrapRef}>
        <button
          type="button"
          ref={triggerRef}
          className="am-trigger"
          onClick={openMenu}
          // 自绘触发按钮：下拉语义原先由 TDesign Dropdown 注入，改用原生 button 后必须自己补
          // haspopup/expanded，否则读屏软件听不出「这是一个会展开菜单的按钮、当前是否已展开」
          aria-haspopup="menu"
          aria-expanded={menuOpen}
        >
          <Icon n="user" style={MI} />{user?.username || user?.display_name || ''}
          <CaretDownIcon size={12} style={{ verticalAlign: '-2px', marginInlineStart: 4 }} />
        </button>
        {/* ★ 2026-10-15：通知浮层开在触发器**上方**（账号菜单贴着左栏底缘，向下开会溢出视口）
            ★ 2026-10-17 用户报「打开通知中心会强制左边菜单栏拖动」：改为 **portal 到 body + 固定定位**。
            旧形态（position:absolute + 写死 300px 留在栏内）会把只有 237/295px 宽、且 overflow-y:auto
            的左栏撑出横向滚动条；portal 出去后浮层不参与左栏滚动容器的排版流，宽度/高度改为现算夹边。 */}
        {notifyOpen && notifyPos && createPortal(
          <div
            className="am-notice"
            ref={noticeRef}
            style={{ left: notifyPos.left, bottom: notifyPos.bottom, width: notifyPos.width, maxHeight: notifyPos.maxH }}
          >
            <NotifyPanel ctl={notify} />
          </div>,
          document.body,
        )}
      </div>
      <ContextMenu open={menuOpen} x={menuPos.x} y={menuPos.y} items={menuItems} onClose={() => setMenuOpen(false)} />
      {/* 密码修改弹窗 */}
      {openPwd && <PasswordModal email={curEmail} onClose={() => setOpenPwd(false)} />}
      {/* 邮箱绑定/换绑弹窗 */}
      {openBind && <EmailBindModal hasOldEmail={!!curEmail} oldEmail={curEmail} onClose={() => setOpenBind(false)} />}
      {/* 账号注销弹窗 */}
      {openDeact && <DeactivateModal onClose={() => setOpenDeact(false)} />}
      {openRole && <JobRoleModal current={curJobRole} onClose={() => setOpenRole(false)} onSaved={(code) => setCurJobRole(code)} />}
    </>
  )
}

// —— 触发按钮（am- 前缀，避免与组件库类名重名）——
const AM_CSS = `
.am-wrap{position:relative;display:inline-flex}
.am-trigger{display:inline-flex;align-items:center;gap:4px;background:transparent;border:0;color:var(--lc-text);
  font-size:15px;cursor:pointer;font-family:var(--lc-font);padding:6px 8px;border-radius:var(--lc-r-bar)}
.am-trigger:hover{color:var(--lc-text);background:var(--lc-raised)}
.am-trigger:focus-visible{outline: 2px solid var(--lc-border-input);outline-offset:2px}
/* 消息浮层：portal 到 body 的固定定位（落点由 placeNotice() 现算；账号菜单在左栏底部，
   向下开必溢出视口，故开在触发器上沿）。⚠ 宽度不许写死：左栏只有 237/295px 宽且 overflow-y:auto
   ⇒ 写死 300px 的栏内浮层会把左栏撑出横向滚动条（2026-10-17 用户报障的根因）。 */
.am-notice{position:fixed;z-index:70}
/* box-sizing:border-box 必写：面板自带 8px 内垫 + 1.2px 描边，content-box 下 width:100% 会再溢出约 18px */
.am-notice .bell-panel{position:static;width:100%;box-sizing:border-box;max-height:inherit}
.am-count{margin-inline-start:auto;padding:0 6px;border-radius:99px;background:var(--lc-invert-bg);
  color:var(--lc-invert-fg);font-size:12px;font-weight:600;line-height:18px}
`
