// ============================================================================
// App.tsx — 根组件（react-router-dom 路由版）
// 路由：BrowserRouter + React.lazy 代码分割
// 头部：品牌 / Tab / 余额徽标 / Bell / 语言切换 / 账号菜单
// ============================================================================

import { lazy, Suspense, useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { Button, Tag, Drawer } from 'tdesign-react'
import { myPackage, meContext } from '@/api'
import { pointsOf } from '@/utils/points'
import { AuthProvider, useAuth } from '@/stores/auth'
import { AdminProvider, useAdminStore } from '@/stores/admin'
import { ChatProvider, useChat } from '@/hooks/useChat'
import { useT, t as gt, tpl as gtpl, toggleLang } from '@/i18n'
import { setAuthToken, setActiveTenantId, API_BASE } from '@/api'
import { applyTheme, cycleTheme, getTheme, watchSystemTheme } from '@/lib/theme'
watchSystemTheme()
applyTheme()
import { BrandingProvider, useBranding } from './branding'
import ErrorBoundary from './components/ErrorBoundary'
import { roleLevelSafe } from '@/lib/ui'

// 跨域登录跳转：品牌子域登录后通过 /?sso_code= 跳转回来（★ E3：一次性 code 换取 token，
// 不再让 JWT 出现在地址栏/Referer/浏览历史；60s TTL、单次消费，见后端 /api/auth/sso/exchange）
;(() => {
  const clean = () => {
    const p = new URLSearchParams(window.location.search)
    p.delete('sso_code')
    p.delete('token') // 历史链接兼容：仅抹掉参数，不再注入登录态
    const url = window.location.pathname + (p.toString() ? '?' + p.toString() : '') + window.location.hash
    window.history.replaceState({}, '', url)
  }
  try {
    const code = new URLSearchParams(window.location.search).get('sso_code')
    if (!code) return
    clean()
    // ★ E12：统一走 API_BASE（core.ts 单一后端地址口径）
    fetch(`${API_BASE}/api/auth/sso/exchange`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code }),
    }).then(async (res) => {
      const data = await res.json().catch(() => null) as { success?: boolean; token?: string } | null
      if (res.ok && data?.success && data.token) {
        setAuthToken(data.token)
        setActiveTenantId(0)
        window.location.reload() // 兑换成功后走正常会话恢复，避免半初始化状态
      }
      // 失败静默：停留在登录页，用户可重新发起 SSO
    }).catch(() => { /* 网络异常同样静默回登录 */ })
  } catch { /* ignore */ }
})()

// ---- 懒加载页面组件（路由级代码分割） ----
const Login = lazy(() => import('./components/Login'))
const Landing = lazy(() => import('./components/Landing')) // ★ S5 官网落地页（未登录 /）
const ChatWindow = lazy(() => import('./components/ChatWindow'))
const TicketsPage = lazy(() => import('./components/TicketsPage'))
const EditorPage = lazy(() => import('./components/EditorPage'))
const AdminDashboard = lazy(() => import('./components/admin/AdminDashboard'))
const Bell = lazy(() => import('./components/Bell'))
const AccountMenu = lazy(() => import('./components/AccountMenu'))
const SiteFooter = lazy(() => import('./components/SiteFooter'))
const KbUploadDialog = lazy(() => import('./components/KbUploadDialog'))

// 小组件保持静态导入（避免过度拆分）
import { ReferralPanel, MyPackagePanel, AccountPanel } from './components/selfservice'
import BillingCenter from './components/MyBilling' // ★ F8 账单中心（趋势+明细）
import { EmailBindModal } from './components/modals'

// 页面加载中占位组件（旋转动画 + 提示文字），路由懒加载时展示
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
  const [depleted, setDepleted] = useState(false) // ★ E11
  const [tenantTag, setTenantTag] = useState('') // ★ F1
  const [ctxNoEmail, setCtxNoEmail] = useState(false)
  const [isPersonal, setIsPersonal] = useState(true)
  const [kbUploadOpen, setKbUploadOpen] = useState(false)
  // ★ F5：主题偏好（按钮显示当前态并循环切换）
  const [themeTick, setThemeTick] = useState(0)
  void themeTick
  const themeNow = getTheme()
  const themeIcon = themeNow === 'auto' ? '🌗' : themeNow === 'light' ? '☀️' : '🌙'
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
          const personal = (c as unknown as { is_personal?: boolean }).is_personal !== false
          setIsPersonal(personal)
          // ★ F1：顶栏租户名徽标数据源（企业=租户名，个人=个人版）
          setTenantTag(personal ? '' : String((c as unknown as { tenant_name?: string }).tenant_name || ''))
        }
      } catch { /* ignore */ }
      try {
        const p = await myPackage() as unknown as { success?: boolean; balance_tokens?: number; balance_sentences_approx?: number }
        if (p.success && typeof p.balance_tokens === 'number') {
          const approx = typeof p.balance_sentences_approx === 'number'
            ? p.balance_sentences_approx
            : Math.floor(p.balance_tokens / 500)
          const nf = new Intl.NumberFormat()
          setPkgLine(gtpl('app.pkgLineFmt', { points: nf.format(pointsOf(p.balance_tokens)), approx: nf.format(approx) }))
          setDepleted(p.balance_tokens <= 0) // ★ E11：billing_stopped 顶部横幅信号
        }
      } catch { /* ignore */ }
    })()
  }, [user])

  // 从 pathname 推导当前 Tab
  const tab = path.startsWith('/tickets') ? 'tickets' : path.startsWith('/editor') ? 'editor' : 'workbench'
  // 切换工作台 Tab（workbench/tickets/editor），通过 navigate 跳转
  function switchTab(to: 'workbench' | 'tickets' | 'editor') {
    const target = to === 'tickets' ? '/tickets' : to === 'editor' ? '/editor' : '/'
    if (path !== target) navigate(target)
  }

  return (
    <Suspense fallback={<PageLoading />}>
    <div style={{ minHeight: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--npz-page-bg, #fafbfd)' }}>
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
        <Button variant="text" shape="square" onClick={() => setMenuOpen(true)} aria-label={t('app.openNav')} style={{ fontSize: 20, padding: '0 8px' }}>☰</Button>
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
                onClick={() => switchTab('editor')}>✍️ {t('app.tabEditor')}</Button>
        <div style={{ flex: 1 }} />
        {/* ★ F1：租户身份徽标——个人用户「个人版」，企业用户显示所属租户名（title 全文） */}
        <span className="tenant-tag" title={tenantTag || t('app.personalPlan')} style={{ fontSize: 12, color: '#5f6368', whiteSpace: 'nowrap' }}>
          {tenantTag ? `🏢 ${tenantTag.length > 12 ? tenantTag.slice(0, 12) + '…' : tenantTag}` : `👤 ${t('app.personalPlan')}`}
        </span>
        {!!pkgLine && <Tag theme="primary" variant="light" className="pkg-line-tag">{pkgLine}</Tag>}
        {canUploadKb && (
          <Button size="small" variant="outline" theme="primary" onClick={() => setKbUploadOpen(true)}>
            {t('kb.topbarUpload')}
          </Button>
        )}
        <Bell />
        {/* ★ F5：明暗主题三态切换（auto/light/dark，localStorage 记忆，auto 跟随系统） */}
        <Button size="small" variant="text" title={t(`app.theme.${getTheme()}`)} onClick={() => { cycleTheme(); setThemeTick((n) => n + 1) }}>{themeIcon}</Button>
        <Button size="small" variant="text" onClick={toggleLang}>{lang === 'zh' ? 'EN' : t('app.langSwitch')}</Button>
        <AccountMenu showAdminConsole={roleLevelSafe(user?.role) >= 2} onGotoAdmin={() => navigate('/admin')} />
      </header>

      {/* ★ E11：余额耗尽（billing_stopped 口径）常驻横幅——旧版仅深藏于自助面板
          ★ 2026-09-16 整改：旧版一律跳 /packages（只读页）——租户管理员及以上直跳
            后台计费 Hub（真正的收银台所在），避免「点充值→落只读页」死胡同 */}
      {depleted && (
        <div style={{ background: '#fff1f0', color: '#a8071a', padding: '6px 16px', fontSize: 13, display: 'flex', gap: 12, alignItems: 'center', borderBottom: '1px solid #ffccc7' }}>
          <span>{t('ss.exhaustedHint')}</span>
          <Button size="small" theme="danger" onClick={() => {
            if (roleLevelSafe(user?.role) >= 3) { useAdminStore.getState().gotoPanel('billing'); navigate('/admin') }
            else navigate('/packages')
          }}>{t('ss.gotoRecharge')}</Button>
        </div>
      )}
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
              <Route path="/billing" element={<BillingCenter />} />
              <Route path="/invites" element={isPersonal ? <ReferralPanel /> : <Navigate to="/" replace />} />
              <Route path="/packages" element={<MyPackagePanel />} />
              <Route path="/my" element={<AccountPanel />} />
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        )}
      </div>

      <Drawer visible={menuOpen} onClose={() => setMenuOpen(false)} header={t('app.more')} size="340px" footer={false}>
        <nav className="ss-drawer-nav">
          {[['/billing',t('app.navBilling')],['/invites',t('app.navInvites')],['/packages',t('app.navPackages')],['/my',t('app.navMy')]].filter(([p]) => p !== '/invites' || isPersonal).map(([p,l]) => (
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
    </Suspense>
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
    // ★ S5 门面：未登录访问 `/` 出官网落地页；登录/注册走 /login、/register
    if (path === '/') {
      return (
        <Suspense fallback={<PageLoading />}>
          <Landing />
        </Suspense>
      )
    }
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
