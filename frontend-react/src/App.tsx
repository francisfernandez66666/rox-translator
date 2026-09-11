// ============================================================================
// App.tsx — 根组件（react-router-dom 路由版）
// 路由：BrowserRouter + React.lazy 代码分割
// 头部：品牌 / Tab / 余额徽标 / Bell / 语言切换 / 账号菜单
// ============================================================================

import { lazy, Suspense, useCallback, useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { Button, Tag, Drawer } from 'tdesign-react'
import { myPackage, meContext } from '@/api'
import { AuthProvider, useAuth } from '@/stores/auth'
import { AdminProvider } from '@/stores/admin'
import { ChatProvider, useChat } from '@/hooks/useChat'
import { useT, t as gt, toggleLang } from '@/i18n'
import { setAuthToken, setActiveTenantId } from '@/api'
import { BrandingProvider, useBranding } from './branding'
import ErrorBoundary from './components/ErrorBoundary'
import { roleLevelSafe } from '@/lib/ui'

// 跨域登录跳转：品牌子域登录后通过 /?token= 跳转回来
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

// ---- 懒加载页面组件（路由级代码分割） ----
const Login = lazy(() => import('./components/Login'))
const ChatWindow = lazy(() => import('./components/ChatWindow'))
const TicketsPage = lazy(() => import('./components/TicketsPage'))
const EditorPage = lazy(() => import('./components/EditorPage'))
const AdminDashboard = lazy(() => import('./components/admin/AdminDashboard'))
const Bell = lazy(() => import('./components/Bell'))
const AccountMenu = lazy(() => import('./components/AccountMenu'))
const SiteFooter = lazy(() => import('./components/SiteFooter'))
const KbUploadDialog = lazy(() => import('./components/KbUploadDialog'))

// 小组件保持静态导入（避免过度拆分）
import { BalancePanel, ReferralPanel, MyPackagePanel, AccountPanel } from './components/selfservice'
import { EmailBindModal } from './components/modals'

// Loading fallback
function PageLoading() {
  return (
    <div style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
      <div style={{ width: 48, height: 48, border: '4px solid #e0e0e0', borderTopColor: 'var(--td-brand-color, #2f47f5)', borderRadius: '50%', animation: 'appspin 0.8s linear infinite' }} />
      <p style={{ fontSize: 16, color: '#5f6368' }}>{gt('app.loading')}</p>
    </div>
  )
}

// ---- 前台工作台外壳 ----
function FrontShell() {
  const chat = useChat()
  const { user } = useAuth()
  const [lang, t] = useT()
  const location = useLocation()
  const navigate = useNavigate()
  const branding = useBranding()

  const path = location.pathname
  const [pkgLine, setPkgLine] = useState('')
  const [ctxNoEmail, setCtxNoEmail] = useState(false)
  const [isPersonal, setIsPersonal] = useState(true)
  const [kbUploadOpen, setKbUploadOpen] = useState(false)
  const [menuOpen, setMenuOpen] = useState(false)
  const canUploadKb = roleLevelSafe(user?.role) >= 2

  useEffect(() => {
    if (!user) return
    ;(async () => {
      try {
        const c = await meContext()
        if (c.success) {
          const email = String((c as unknown as { email?: string }).email || '')
          setCtxNoEmail(!email)
          setIsPersonal((c as unknown as { is_personal?: boolean }).is_personal !== false)
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

  // 从 pathname 推导当前 Tab
  const tab = path.startsWith('/tickets') ? 'tickets' : path.startsWith('/editor') ? 'editor' : 'workbench'
  function switchTab(to: 'workbench' | 'tickets' | 'editor') {
    const target = to === 'tickets' ? '/tickets' : to === 'editor' ? '/editor' : '/'
    if (path !== target) navigate(target)
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
      `}</style>
      <header className="app-header">
        <Button variant="text" shape="square" onClick={() => setMenuOpen(true)} aria-label="打开导航菜单" style={{ fontSize: 20, padding: '0 8px' }}>☰</Button>
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
                onClick={() => switchTab('editor')}>✍️ {t('app.tabEditor') || '对照编辑'}</Button>
        <div style={{ flex: 1 }} />
        {!!pkgLine && <Tag theme="primary" variant="light" className="pkg-line-tag">{pkgLine}</Tag>}
        {canUploadKb && (
          <Button size="small" variant="outline" theme="primary" onClick={() => setKbUploadOpen(true)}>
            {t('kb.topbarUpload')}
          </Button>
        )}
        <Bell />
        <Button size="small" variant="text" onClick={toggleLang}>{lang === 'zh' ? 'EN' : '中文'}</Button>
        <AccountMenu showAdminConsole={roleLevelSafe(user?.role) >= 2} onGotoAdmin={() => navigate('/admin')} />
      </header>

      <div className="app-main" style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        {chat.isBackendLoading ? (
          <div className="loading-screen" style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
            <div className="loading-spinner" style={{ width: 48, height: 48, border: '4px solid #e0e0e0', borderTopColor: 'var(--td-brand-color, #2f47f5)', borderRadius: '50%', animation: 'appspin 0.8s linear infinite' }} />
            <p style={{ fontSize: 16, color: '#5f6368' }}>{t('app.starting')}</p>
          </div>
        ) : (
          <Suspense fallback={<PageLoading />}>
            <Routes>
              <Route path="/" element={<ChatWindow />} />
              <Route path="/tickets" element={<TicketsPage />} />
              <Route path="/editor" element={<EditorPage />} />
              <Route path="/billing" element={<BalancePanel />} />
              <Route path="/invites" element={isPersonal ? <ReferralPanel /> : <Navigate to="/" replace />} />
              <Route path="/packages" element={<MyPackagePanel />} />
              <Route path="/my" element={<AccountPanel />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        )}
      </div>

      <Drawer visible={menuOpen} onClose={() => setMenuOpen(false)} header={t('app.more') || '更多'} size="340px" footer={false}>
        <nav className="ss-drawer-nav">
          {[['/billing','💰 我的余额'],['/invites','🔗 我的邀请'],['/packages','💎 我的套餐'],['/my','👤 我的账号']].filter(([p]) => p !== '/invites' || isPersonal).map(([p,l]) => (
            <div key={p} className="ss-drawer-item" onClick={() => { navigate(p); setMenuOpen(false) }}>{l}</div>
          ))}
        </nav>
        <SiteFooter />
      </Drawer>

      {canUploadKb && <KbUploadDialog visible={kbUploadOpen} onClose={() => setKbUploadOpen(false)} />}
      {ctxNoEmail && (
        <EmailBindModal hasOldEmail={false} dismissible={false}
                        onClose={() => setCtxNoEmail(false)}
                        onDone={() => setCtxNoEmail(false)} />
      )}
    </div>
  )
}

// ---- 根路由 ----
function Root() {
  const { user, restoring, onLogin } = useAuth()
  const location = useLocation()
  const navigate = useNavigate()
  const path = location.pathname
  const t = gt

  if (restoring) {
    return <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>{t('app.loading')}</div>
  }
  if (!user) {
    return (
      <Suspense fallback={<PageLoading />}>
        <Login mode={path.startsWith('/admin') ? 'admin' : 'home'} onLogin={(u) => { onLogin(u); if (path.startsWith('/admin') && roleLevelSafe(u.role) < 2) navigate('/') }} />
      </Suspense>
    )
  }
  if (path.startsWith('/admin')) {
    return roleLevelSafe(user.role) >= 2 ? (
      <Suspense fallback={<PageLoading />}>
        <AdminDashboard />
      </Suspense>
    ) : (
      <div style={{ padding: 40 }}>
        <Button onClick={() => navigate('/')}>{t('app.backHome')}</Button>
      </div>
    )
  }
  return <FrontShell />
}

// ---- 应用根组件 ----
export default function App() {
  return (
    <ErrorBoundary>
      <BrowserRouter>
        <AuthProvider>
          <ChatProvider>
            <AdminProvider>
              <BrandingProvider>
                <Root />
              </BrandingProvider>
            </AdminProvider>
          </ChatProvider>
        </AuthProvider>
      </BrowserRouter>
    </ErrorBoundary>
  )
}
