// ============================================================================
// components/AccountMenu.tsx — 右上角账号菜单（前台与后台共用）
// 提供：进入后台（前台专用）、修改密码、换绑邮箱、注销账号、退出登录。
// 改密/换绑弹窗统一在此托管，确保前台与后台体验一致（含邮箱验证码、强制流程）。
// ============================================================================
import { useEffect, useState } from 'react'
import { Button, Dropdown, MessagePlugin } from 'tdesign-react'
import { useAuth } from '@/stores/auth'
import { useT } from '@/i18n'
import { meContext } from '@/api'
import { PasswordModal, EmailBindModal, DeactivateModal } from './modals'

interface Props {
  showAdminConsole?: boolean // 前台专用：是否展示「进入后台」入口
  onGotoAdmin?: () => void // 前台点击「进入后台」的回调
}

export default function AccountMenu({ showAdminConsole, onGotoAdmin }: Props) {
  const { user, logout } = useAuth()
  const [t] = useT()
  const [curEmail, setCurEmail] = useState('')
  const [openPwd, setOpenPwd] = useState(false)
  const [openBind, setOpenBind] = useState(false)
  const [openDeact, setOpenDeact] = useState(false)

  // 读取当前邮箱，供改密验证码与换绑弹窗使用
  useEffect(() => {
    if (!user) return
    ;(async () => {
      try {
        const c = await meContext()
        if (c.success) setCurEmail(String((c as unknown as { email?: string }).email || ''))
      } catch { /* ignore */ }
    })()
  }, [user])

  // 菜单项：后台不展示「进入后台」；仅普通用户显示「注销」；其余为改密/换绑/退出
  const options = [
    // 前台专用：跳转后台管理控制台
    ...(showAdminConsole ? [{ content: '🛠 ' + t('menu.adminConsole'), value: 'admin', onClick: () => onGotoAdmin?.() }] : []),
    // 修改密码：打开邮箱验证码 + 新密码弹窗
    { content: '🔒 ' + t('pwd.title'), value: 'pwd', onClick: () => setOpenPwd(true) },
    // 换绑邮箱：打开绑定/换绑邮箱弹窗
    { content: '📧 ' + t('menu.changeEmail'), value: 'email', onClick: () => setOpenBind(true) },
    // 仅普通用户可注销账号
    ...(user?.role === 'user' ? [{ content: '🗑 ' + t('menu.deactivate'), value: 'deact', onClick: () => setOpenDeact(true) }] : []),
    // 退出登录并提示
    { content: '⎋ ' + t('common.logout'), value: 'logout', onClick: () => { logout(); void MessagePlugin.success(t('app.bye')) } },
  ]

  return (
    <>
      <Dropdown trigger="click" options={options}>
        <Button variant="text" size="small">👤 {user?.username || user?.display_name || ''} ▾</Button>
      </Dropdown>
      {openPwd && <PasswordModal email={curEmail} onClose={() => setOpenPwd(false)} />}
      {openBind && <EmailBindModal hasOldEmail={!!curEmail} oldEmail={curEmail} onClose={() => setOpenBind(false)} />}
      {openDeact && <DeactivateModal onClose={() => setOpenDeact(false)} />}
    </>
  )
}
