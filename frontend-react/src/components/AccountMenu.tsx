// ============================================================================
// components/AccountMenu.tsx — 右上角账号菜单（前台与后台共用）
// 职责：提供进入后台（前台专用）、修改密码、换绑邮箱、注销账号、退出登录功能。
// 改密/换绑弹窗统一在此托管，确保前台与后台体验一致（含邮箱验证码、强制流程）。
// 呈现层替换：TDesign Dropdown→langcross ContextMenu、Button(variant=text)→自定义触发按钮、
//             MessagePlugin→useToast。
// ============================================================================
import { useEffect, useRef, useState } from 'react'
import { Icon, CaretDownIcon, ContextMenu, useToast } from '@/ui/langcross/src'
import type { MenuItem } from '@/ui/langcross/src'
import { useAuth } from '@/stores/auth'
import { useT } from '@/i18n'
import { meContext } from '@/api'
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
}

/** 右上角账号菜单组件：进入后台、改密、换绑邮箱、注销与退出登录（前台与后台共用） */
export default function AccountMenu({ showAdminConsole, onGotoAdmin, showWorkbench, onGotoWorkbench }: Props) {
  const { user, logout } = useAuth()
  const { toast } = useToast()
  const [, t] = useT() // ★ E14：lang 未使用
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

  // 构建菜单项列表：
  // 后台不展示「进入后台」；仅普通用户显示「注销」；其余为改密/换绑/退出
  const options = [
    // 后台专用：返回前台工作台
    ...(showWorkbench ? [{ content: <><Icon n="chat" style={MI} />{t('menu.backWorkbench')}</>, value: 'workbench', onClick: () => onGotoWorkbench?.() }] : []),
    // 前台专用：跳转后台管理控制台
    ...(showAdminConsole ? [{ content: <><Icon n="wrench" style={MI} />{t('menu.adminConsole')}</>, value: 'admin', onClick: () => onGotoAdmin?.() }] : []),
    // 修改密码：打开邮箱验证码 + 新密码弹窗
    { content: <><Icon n="lock" style={MI} />{t('pwd.title')}</>, value: 'pwd', onClick: () => setOpenPwd(true) },
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
.am-trigger{display:inline-flex;align-items:center;gap:4px;background:transparent;border:0;color:var(--lc-text);
  font-size:15px;cursor:pointer;font-family:var(--lc-font);padding:6px 8px;border-radius:var(--lc-r-bar)}
.am-trigger:hover{color:var(--lc-text);background:var(--lc-raised)}
.am-trigger:focus-visible{outline: 2px solid var(--lc-border-input);outline-offset:2px}
`
