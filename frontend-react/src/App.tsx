// ============================================================================
// App.tsx — 根组件（Vue 版 App.vue 等价）
// 路由语义沿用：pathname 手搓（/admin → 后台；/tickets → 工单页；其余工作台）。
// 头部：品牌 / 双 Tab / 余额徽标(pkgLine) / Bell / 语言切换 / 账号菜单
//       （改密、绑邮箱、注销、退出、进入后台）。meContext 无邮箱时强制绑定弹窗。
// ============================================================================
// 依赖引入：React 基础 Hooks、TDesign 组件、API/状态/i18n 模块与各类页面/组件
import { useCallback, useEffect, useState } from 'react'
import { Button, Tag, Drawer } from 'tdesign-react'
import { myPackage, meContext } from '@/api'
import { AuthProvider, useAuth } from '@/stores/auth'
import { AdminProvider } from '@/stores/admin'
import { ChatProvider, useChat } from '@/hooks/useChat'
import { useT, t as gt, toggleLang } from '@/i18n'
import { setAuthToken, setActiveTenantId } from '@/api'
import Login from './components/Login'

// ============ 本文件职责中文说明 ============
// 根组件：手搓路由、头部布局与登录跳转处理。
// ========================================

// 跨域登录跳转：品牌子域登录后通过 /?token= 跳转回来，此处把 token 写入本地会话并清除 URL 参数。
;(() => {
  try {
    const p = new URLSearchParams(window.location.search)
    const tk = p.get('token')
    if (tk) {
      setAuthToken(tk)
      setActiveTenantId(0)
      p.delete('token')
      const url = window.location.pathname + (p.toString() ? '?' + p.toString() : '') + window.location.hash
      window.history.replaceState({}, '', url)
    }
  } catch { /* ignore */ }
})()

import ChatWindow from './components/ChatWindow'
import TicketsPage from './components/TicketsPage'
import EditorPage from './components/EditorPage'
import Bell from './components/Bell'
import AdminDashboard from './components/admin/AdminDashboard'
import AccountMenu from './components/AccountMenu'
import SiteFooter from './components/SiteFooter'
import { BalancePanel, ReferralPanel, MyPackagePanel, AccountPanel } from './components/selfservice'
import KbUploadDialog from './components/KbUploadDialog'
import { EmailBindModal } from './components/modals'
import { BrandingProvider, useBranding } from './branding'

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
  // 当前顶部 Tab：工作台 / 工单 / 对照编辑
  const [tab, setTab] = useState<'workbench' | 'tickets' | 'editor'>(
    window.location.pathname.startsWith('/tickets')
      ? 'tickets'
      : window.location.pathname.startsWith('/editor')
        ? 'editor'
        : 'workbench',
  )
  // 端用户自服务路径（余额 / 我的邀请 / 我的套餐 / 我的账号）
  const path = usePath()
  function navigate(to: string) {
    if (window.location.pathname !== to) { window.history.pushState({}, '', to); window.dispatchEvent(new PopStateEvent('popstate')) }
  }
  // 余额徽标展示文本
  const [pkgLine, setPkgLine] = useState('')
  // 是否未绑定邮箱（强制弹窗）
  const [ctxNoEmail, setCtxNoEmail] = useState(false)
  // 前台「上传知识库」弹窗（仅部门管理员及以上可见，复用后台 recognize→import 流程）
  const [kbUploadOpen, setKbUploadOpen] = useState(false)
  // 侧边隐藏菜单（原页脚内容收入此处，顶部汉堡按钮唤起）
  const [menuOpen, setMenuOpen] = useState(false)
  const canUploadKb = roleLevelSafe(user?.role) >= 2
  // 租户级品牌定制（按访问域名解析）
  const branding = useBranding()

  // meContext：无邮箱强制绑定；余额徽标：myPackage 计算 token ≈ 句单语言
  useEffect(() => {
    if (!user) return
    ;(async () => {
      try {
        const c = await meContext()
        if (c.success) {
          const email = String((c as unknown as { email?: string }).email || '')
          setCtxNoEmail(!email) // 无邮箱 → 不可关闭的绑定弹窗（行为同 Vue 版 dismissible=false）
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
  function switchTab(to: 'workbench' | 'tickets' | 'editor') {
    setTab(to)
    const target = to === 'tickets' ? '/tickets' : to === 'editor' ? '/editor' : '/'
    if (window.location.pathname !== target) window.history.pushState({}, '', target)
  }

  return (
    <div style={{ minHeight: '100vh', display: 'flex', flexDirection: 'column', background: '#fafbfd' }}>
      <style>{'@keyframes appspin{to{transform:rotate(360deg)}}'}</style>
      <style>{`
        .ss-grid{display:grid;gap:16px;grid-template-columns:repeat(auto-fill,minmax(340px,1fr))}
        .ss-grid .t-card{width:100%}
        .ss-row{display:flex;justify-content:space-between;align-items:center;padding:8px 0;border-bottom:1px solid #f0f0f0}
        .ss-row span{color:#666}.ss-row b{font-size:18px}
        .ss-copy{display:flex;align-items:center;gap:8px}
        .ss-stats{display:flex;gap:24px;padding:12px 0}
        .ss-stat{text-align:center}
        .ss-stat b{display:block;font-size:20px;color:#2f47f5}
        .ss-table{width:100%;border-collapse:collapse}
        .ss-table th,.ss-table td{border:1px solid #e8e8e8;padding:6px 8px;text-align:left}
        .ss-table th{background:#fafbfc}
        .ss-quick{display:flex;flex-wrap:wrap;gap:8px}
        .ss-drawer-nav{display:flex;flex-direction:column;padding:8px 0;border-bottom:1px solid #f0f0f0}
        .ss-drawer-item{padding:12px 16px;cursor:pointer;border-bottom:1px solid #f5f5f5;font-size:15px}
        .ss-drawer-item:hover{background:#f0f5ff}
        .ss-loading{display:flex;justify-content:center;padding:40px}
      `}</style>`
      {/* 顶部导航栏：品牌、Tab、余额、通知、语言、账号菜单 */}
      <header className="app-header">
        <Button variant="text" shape="square" onClick={() => setMenuOpen(true)} aria-label="menu" style={{ fontSize: 20, padding: '0 8px' }}>☰</Button>
        <span className="brand">
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 60 }} />
            : <span style={{ fontSize: 20, fontWeight: 700 }}>🌐 {branding.brandName || t('app.title')}</span>}
        </span>
        <Button variant={tab === 'workbench' ? 'base' : 'text'} theme="primary" size="small"
                onClick={() => switchTab('workbench')}>💬 {t('app.tabWorkbench')}</Button>
        <Button variant={tab === 'tickets' ? 'base' : 'text'} theme="primary" size="small"
                onClick={() => switchTab('tickets')}>📋 {t('app.tabTickets')}</Button>
        <Button variant={tab === 'editor' ? 'base' : 'text'} theme="primary" size="small"
                onClick={() => switchTab('editor')}>✍️ 对照编辑</Button>
        <div style={{ flex: 1 }} />
        {!!pkgLine && <Tag theme="primary" variant="light">{pkgLine}</Tag>}
        {canUploadKb && (
          <Button size="small" variant="outline" theme="primary" onClick={() => setKbUploadOpen(true)}>
            {t('kb.topbarUpload')}
          </Button>
        )}
        <Bell />
        <Button size="small" variant="text" onClick={toggleLang}>{lang === 'zh' ? 'EN' : '中文'}</Button>
        <AccountMenu showAdminConsole={roleLevelSafe(user?.role) >= 2} onGotoAdmin={onGotoAdmin} />
      </header>

      {/* 主内容区：后端启动中显示 Loading，否则根据 Tab 渲染页面 */}
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        {chat.isBackendLoading ? (
          <div className="loading-screen" style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
            {/* 旋转 Loading 图标 */}
            <div className="loading-spinner" style={{ width: 48, height: 48, border: '4px solid #e0e0e0', borderTopColor: 'var(--td-brand-color, #2f47f5)', borderRadius: '50%', animation: 'appspin 0.8s linear infinite' }} />
            {/* 启动提示文案 */}
            <p style={{ fontSize: 16, color: '#5f6368' }}>{t('app.starting')}</p>
          </div>
        ) : path === '/billing' ? <BalancePanel />
          : path === '/invites' ? <ReferralPanel />
          : path === '/packages' ? <MyPackagePanel />
          : path === '/my' ? <AccountPanel />
          : tab === 'tickets' ? <TicketsPage /> : tab === 'editor' ? <EditorPage /> : <ChatWindow />}
      </div>

      {/* 侧边隐藏菜单：原前台页脚内容收入此处（汉堡按钮唤起） */}
      <Drawer visible={menuOpen} onClose={() => setMenuOpen(false)} header={t('app.more') || '更多'} size="340px" footer={false}>
        <nav className="ss-drawer-nav">
          {[['/billing','💰 我的余额'],['/invites','🔗 我的邀请'],['/packages','💎 我的套餐'],['/my','👤 我的账号']].map(([p,l]) => (
            <div key={p} className="ss-drawer-item" onClick={() => { navigate(p); setMenuOpen(false) }}>{l}</div>
          ))}
        </nav>
        <SiteFooter />
      </Drawer>

      {/* 前台「上传知识库」弹窗（部门管理员及以上） */}
      {canUploadKb && <KbUploadDialog visible={kbUploadOpen} onClose={() => setKbUploadOpen(false)} />}

      {/* 强制绑邮箱（不可关闭）；改密/换绑/注销统一由 AccountMenu 托管 */}
      {ctxNoEmail && (
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
          <BrandingProvider>
            <Root />
          </BrandingProvider>
        </AdminProvider>
      </ChatProvider>
    </AuthProvider>
  )
}
