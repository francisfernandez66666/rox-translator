// ============================================================================
// App.tsx — 根组件（Vue 版 App.vue 等价）
// 路由语义沿用：pathname 手搓（/admin → 后台；/tickets → 工单页；其余工作台）。
// 头部：品牌 / 双 Tab / 余额徽标(pkgLine) / Bell / 语言切换 / 账号菜单
//       （改密、绑邮箱、注销、退出、进入后台）。meContext 无邮箱时强制绑定弹窗。
// ============================================================================
import { useCallback, useEffect, useState } from 'react'
import { Button, Tag, Dropdown, MessagePlugin } from 'tdesign-react'
import { myPackage, meContext } from '@/api'
import { AuthProvider, useAuth } from '@/stores/auth'
import { AdminProvider } from '@/stores/admin'
import { ChatProvider, useChat } from '@/hooks/useChat'
import { useT, t as gt, toggleLang } from '@/i18n'
import Login from './components/Login'
import ChatWindow from './components/ChatWindow'
import TicketsPage from './components/TicketsPage'
import Bell from './components/Bell'
import AdminDashboard from './components/admin/AdminDashboard'
import { PasswordModal, EmailBindModal, DeactivateModal } from './components/modals'

// 监听浏览器 popstate，同步当前 pathname 状态
function usePath(): string {
  const [p, setP] = useState(window.location.pathname)
  useEffect(() => {
    const onPop = () => setP(window.location.pathname)
    window.addEventListener('popstate', onPop)
    return () => window.removeEventListener('popstate', onPop)
  }, [])
  return p
}

// 前台工作台外壳：顶部导航、Tab 切换、余额徽标、通知铃铛、账号菜单
function FrontShell({ onGotoAdmin }: { onGotoAdmin: () => void }) {
  // 聊天全局状态
  const chat = useChat()
  // 当前登录用户与登出方法
  const { user, logout } = useAuth()
  // 当前语言与翻译函数
  const [lang, t] = useT()
  // 当前顶部 Tab：工作台或工单
  const [tab, setTab] = useState<'workbench' | 'tickets'>(window.location.pathname.startsWith('/tickets') ? 'tickets' : 'workbench')
  // 余额徽标展示文本
  const [pkgLine, setPkgLine] = useState('')
  // 是否未绑定邮箱（强制弹窗）
  const [ctxNoEmail, setCtxNoEmail] = useState(false)
  // 当前用户邮箱（可能为空）
  const [curEmail, setCurEmail] = useState('')
  // 修改密码弹窗开关
  const [openPwd, setOpenPwd] = useState(false)
  // 绑定/修改邮箱弹窗开关
  const [openBind, setOpenBind] = useState(false)
  // 注销账号弹窗开关
  const [openDeact, setOpenDeact] = useState(false)

  // meContext：无邮箱强制绑定；余额徽标：myPackage 计算 token ≈ 句单语言
  useEffect(() => {
    if (!user) return
    ;(async () => {
      try {
        const c = await meContext()
        if (c.success) {
          const email = String((c as unknown as { email?: string }).email || '')
          setCtxNoEmail(!email) // 无邮箱 → 不可关闭的绑定弹窗（行为同 Vue 版 dismissible=false）
          setCurEmail(email)
        }
      } catch { /* ignore */ }
      try {
        const p = await myPackage() as unknown as { success?: boolean; balance_tokens?: number; balance_sentences_approx?: number }
        if (p.success && typeof p.balance_tokens === 'number') {
          const approx = typeof p.balance_sentences_approx === 'number'
            ? p.balance_sentences_approx
            : Math.floor(p.balance_tokens / 500)
          const nf = new Intl.NumberFormat()
          setPkgLine(`${nf.format(p.balance_tokens)} token ≈ ${nf.format(approx)} 句单语言`)
        }
      } catch { /* ignore */ }
    })()
  }, [user])

  // 切换顶部 Tab 并同步浏览器历史路径
  function switchTab(to: 'workbench' | 'tickets') {
    setTab(to)
    const target = to === 'tickets' ? '/tickets' : '/'
    if (window.location.pathname !== target) window.history.pushState({}, '', target)
  }

  return (
    <div style={{ minHeight: '100vh', display: 'flex', flexDirection: 'column', background: '#fafbfd' }}>
      <style>{'@keyframes appspin{to{transform:rotate(360deg)}}'}</style>
      {/* 顶部导航栏：品牌、Tab、余额、通知、语言、账号菜单 */}
      <header className="app-header">
        <span className="brand">🌐 {t('app.title')}</span>
        <Button variant={tab === 'workbench' ? 'base' : 'text'} theme="primary" size="small"
                onClick={() => switchTab('workbench')}>💬 {t('app.tabWorkbench')}</Button>
        <Button variant={tab === 'tickets' ? 'base' : 'text'} theme="primary" size="small"
                onClick={() => switchTab('tickets')}>📋 {t('app.tabTickets')}</Button>
        <div style={{ flex: 1 }} />
        {!!pkgLine && <Tag theme="primary" variant="light">{pkgLine}</Tag>}
        <Bell />
        <Button size="small" variant="text" onClick={toggleLang}>{lang === 'zh' ? 'EN' : '中'}</Button>
        <Dropdown
          trigger="click"
          options={[
            ...(roleLevelSafe(user?.role) >= 2 ? [{ content: '🛠 ' + t('menu.adminConsole'), value: 'admin', onClick: onGotoAdmin }] : []),
            { content: '🔒 ' + t('pwd.title'), value: 'pwd', onClick: () => setOpenPwd(true) },
            { content: '📧 ' + t('menu.changeEmail'), value: 'email', onClick: () => setOpenBind(true) },
            ...(user?.role === 'user' ? [{ content: '🗑 ' + t('menu.deactivate'), value: 'deact', onClick: () => setOpenDeact(true) }] : []),
            { content: '⎋ ' + t('common.logout'), value: 'logout', onClick: () => { logout(); void MessagePlugin.success(t('app.bye')) } },
          ]}
        >
          <Button variant="text" size="small">👤 {user?.username || user?.display_name || ''} ▾</Button>
        </Dropdown>
      </header>

      {/* 主内容区：后端启动中显示 Loading，否则根据 Tab 渲染页面 */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        {chat.isBackendLoading ? (
          <div className="loading-screen" style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
            {/* 旋转 Loading 图标 */}
            <div className="loading-spinner" style={{ width: 48, height: 48, border: '4px solid #e0e0e0', borderTopColor: '#1a73e8', borderRadius: '50%', animation: 'appspin 0.8s linear infinite' }} />
            {/* 启动提示文案 */}
            <p style={{ fontSize: 16, color: '#5f6368' }}>{t('app.starting')}</p>
          </div>
        ) : tab === 'tickets' ? <TicketsPage /> : <ChatWindow />}
      </div>

      {/* 账号相关弹窗：改密、绑邮箱、注销 */}
      {openPwd && <PasswordModal email={curEmail} onClose={() => setOpenPwd(false)} />}
      {openBind && <EmailBindModal hasOldEmail={!!curEmail} oldEmail={curEmail} onClose={() => setOpenBind(false)} />}
      {openDeact && <DeactivateModal onClose={() => setOpenDeact(false)} />}
      {/* 强制绑邮箱（不可关闭） */}
      {ctxNoEmail && !openBind && (
        <EmailBindModal hasOldEmail={false} dismissible={false}
                        onClose={() => setCtxNoEmail(false)}
                        onDone={() => setCtxNoEmail(false)} />
      )}
    </div>
  )
}

// 安全版角色等级：未登录或未知角色返回最低级 1
function roleLevelSafe(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 4
  if (r === 'tenant_admin' || r === 'approver') return 3
  if (r === 'dept_admin') return 2
  return 1
}

// 根路由组件：根据会话恢复状态、登录态与 pathname 渲染登录/后台/前台
function Root() {
  // 认证上下文
  const { user, restoring, onLogin } = useAuth()
  // 当前浏览器路径
  const path = usePath()
  // 全局翻译函数
  const t = gt

  // 跳转后台管理路径，并触发 popstate 让 usePath 同步更新
  const gotoAdmin = useCallback(() => { window.history.pushState({}, '', '/admin'); window.dispatchEvent(new PopStateEvent('popstate')) }, [])

  if (restoring) {
    return <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>{t('app.loading')}</div>
  }
  if (!user) {
    return <Login mode={path.startsWith('/admin') ? 'admin' : 'home'} onLogin={(u) => { onLogin(u); if (path.startsWith('/admin') && roleLevelSafe(u.role) < 2) window.history.pushState({}, '', '/') }} />
  }
  if (path.startsWith('/admin')) {
    return roleLevelSafe(user.role) >= 2 ? <AdminDashboard /> : (
      <div style={{ padding: 40 }}>
        <Button onClick={() => { window.history.pushState({}, '', '/'); window.location.reload() }}>{t('app.backHome')}</Button>
      </div>
    )
  }
  return <FrontShell onGotoAdmin={gotoAdmin} />
}

// 应用根组件：嵌套全局认证、聊天、后台 Provider
export default function App() {
  return (
    <AuthProvider>
      <ChatProvider>
        <AdminProvider>
          <Root />
        </AdminProvider>
      </ChatProvider>
    </AuthProvider>
  )
}
