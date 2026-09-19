// ============================================================================
// App.tsx — 根组件（react-router-dom 路由版）
// 路由：BrowserRouter + React.lazy 代码分割
// 头部：品牌 / Tab / 余额徽标 / Bell / 语言切换 / 账号菜单
// ----------------------------------------------------------------------------
// 2026-09-17/18 纯黑换肤（前台骨架部分）：
//   - 顶部控件由 tdesign-react（Button/Tag/Drawer）整体切到 LangCross 组件库
//     `@/ui/langcross/src`（Button/Badge/Drawer/Icon），故本文件不再 import tdesign；
//   - 表意不再依赖 emoji 字符（💬/📋/✍️/🏢/👤 等），一律换成 <Icon n="..."/>，
//     图标走 --lc-* 单色，明暗主题下都能跟随文字颜色；
//   - 工作台 Tab 与头部幽灵按钮改吃本文件内联的 .app-tab / .ss-ghost-btn 类
//     （纯黑主题下无需组件库皮肤，避免 TDesign 蓝底残留）；
//   - 新增公开路由 /pricing（未登录访客亦可直达，见 Root 内的营销门面分支）。
// ============================================================================

import { lazy, Suspense, useEffect, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { myPackage, meContext } from '@/api'
import { AuthProvider, useAuth } from '@/stores/auth'
import { AdminProvider, useAdminStore } from '@/stores/admin'
import { ChatProvider, useChat } from '@/hooks/useChat'
import { useT, t as gt, tpl as gtpl } from '@/i18n'
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
// ★ S5 官网落地页（未登录 /）
const Landing = lazy(() => import('./components/Landing'))
// 公开定价页（/pricing，设计图 05；营销门面，未登录可直达）
const PricingPage = lazy(() => import('./components/PricingPage'))
// ChatWindow 对话翻译主窗口（懒加载分包）
const ChatWindow = lazy(() => import('./components/ChatWindow'))
// TicketsPage 文件工单页（懒加载分包）
const TicketsPage = lazy(() => import('./components/TicketsPage'))
// EditorPage 在线编辑器页（懒加载分包）
const EditorPage = lazy(() => import('./components/EditorPage'))
// AdminDashboard 管理后台壳（懒加载分包，内含全部后台面板）
const AdminDashboard = lazy(() => import('./components/admin/AdminDashboard'))
// Bell 顶部站内通知铃铛（懒加载分包）
const Bell = lazy(() => import('./components/Bell'))
// AccountMenu 顶部账号菜单（懒加载分包）
const AccountMenu = lazy(() => import('./components/AccountMenu'))
// SiteFooter 全站页脚（懒加载分包）
const SiteFooter = lazy(() => import('./components/SiteFooter'))
// KbUploadDialog 知识库上传对话框（懒加载分包）
const KbUploadDialog = lazy(() => import('./components/KbUploadDialog'))
// AiAssist AI 销售/客服常驻挂件（除登录/注册页外全站显示；★ autosales）
const AiAssist = lazy(() => import('./components/AiAssist'))

// 小组件保持静态导入（避免过度拆分）
import { ReferralPanel, MyPackagePanel, AccountPanel } from './components/selfservice'
import BillingCenter from './components/MyBilling' // ★ F8 账单中心（趋势+明细）
import { EmailBindModal } from './components/modals'
// ★ D2 #24：全站加载态复用落地页「划掉错词→亮起正词」换词动效（实现与样式唯一份在 WordSwap/theme.css）
import WordSwap from './components/WordSwap'
// ★ #23：12 语种界面语言下拉（顶栏/登录卡/后台共用）
import { LangSelect } from './components/LangSelect'
// LangCross 纯黑组件库：前台骨架只依赖这四个件（Button/Badge/Drawer/Icon），
// 替代原 tdesign-react 的 Button/Tag/Drawer —— 换肤期不再引 TDesign 组件。
import { Badge, Button, Drawer, Icon } from '@/ui/langcross/src'

// 页面加载中占位组件（★ D2 #24：旋转圈已换成落地页同源的「划掉错词→亮起正词」换词动效 + 提示文字；
// ★ 2026-09-20 反馈③：minHeight 100dvh 撑满首屏，动效真正垂直居中而非挤在半高容器里），
// 路由懒加载时展示
function PageLoading() {
  return (
    <div style={{ flex: 1, minHeight: '100dvh', width: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
      <WordSwap className="ws--lg" ariaLabel={gt('app.loading')} />
      <p style={{ fontSize: 16, color: '#9AA0AA' }}>{gt('app.loading')}</p>
    </div>
  )
}

// ---- 前台工作台外壳 ----
function FrontShell() {
  const chat = useChat()
  const { user } = useAuth()
  const [, t] = useT() // ★ #23：lang 位不再消费（切换器移进 LangSelect），仅订阅语言刷新
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
  // themeNow 只用于按钮 title（说明当前处于 auto/light/dark 哪一态）；
  // themeIcon 固定一枚单色图标——旧版用 🌗/☀️/🌙 三个 emoji 表意，纯黑主题下
  // 彩色 emoji 会破坏单色体系，故三态区分改由 title 文案承担。
  const themeNow = getTheme()
  const themeIcon = <Icon n="theme" style={{ verticalAlign: '-3px' }} />
  const [menuOpen, setMenuOpen] = useState(false)
  // 顶栏「上传知识库」入口守卫：主管及以上（roleLevelSafe>=2）才露出该按钮，普通成员不可见
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
        const p = await myPackage() as unknown as { success?: boolean; points_balance?: number; balance_sentences_approx?: number }
        if (p.success && typeof p.points_balance === 'number') {
          const nf = new Intl.NumberFormat()
          setPkgLine(gtpl('app.pkgLineFmt', { points: nf.format(p.points_balance), approx: nf.format(p.balance_sentences_approx ?? 0) }))
          setDepleted(p.points_balance <= 0) // ★ E11：billing_stopped 顶部横幅信号
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
    <div style={{ minHeight: '100vh', display: 'flex', flexDirection: 'column', background: 'var(--npz-page-bg, #0E1014)' }}>
      {/* ★ D2 #24：appspin 旋转 keyframes 已随两处 spinner 退役（全站唯一消费点消失） */}
      {/* 前台自助区 + 顶栏的局部类：纯黑换肤后卡片/表格/抽屉一律走白字灰边
          （#E7E9EA / #3A404C），.ss-ghost-btn（顶栏幽灵按钮）与 .app-tab
          （工作台 Tab）直接引用 --lc-* 令牌，hover 只改色不投影。 */}
      <style>{`
        .ss-grid{display:grid;gap:16px;grid-template-columns:repeat(auto-fill,minmax(340px,1fr))}
        .ss-grid .ssc-card{width:100%}
        .ss-row{display:flex;justify-content:space-between;align-items:center;padding:8px 0;border-bottom:1px solid #3A404C}
        .ss-row span{color:#9AA0AA}.ss-row b{font-size:18px;color:#E7E9EA}
        .ss-copy{display:flex;align-items:center;gap:8px}
        .ss-stats{display:flex;gap:24px;padding:12px 0}
        .ss-stat{text-align:center}
        .ss-stat b{display:block;font-size:20px;color:#E7E9EA}
        .ss-table{width:100%;border-collapse:collapse}
        .ss-table th,.ss-table td{border:1.2px solid #3A404C;padding:6px 8px;text-align:left}
        .ss-table th{background:#16181C}
        .ss-quick{display:flex;flex-wrap:wrap;gap:8px}
        .ss-drawer-nav{display:flex;flex-direction:column;padding:8px 0;border-bottom:1px solid #3A404C}
        .ss-drawer-item{padding:12px 16px;cursor:pointer;border-bottom:1px solid #3A404C;font-size:15px;color:#E7E9EA}
        .ss-drawer-item:hover{background:rgba(231,233,234,0.10)}
        .ss-loading{display:flex;justify-content:center;padding:40px}
        .ss-ghost-btn{display:inline-flex;align-items:center;justify-content:center;height:32px;padding:0 8px;border:0;border-radius:8px;background:transparent;color:var(--lc-text-2);font-size:14px;font-family:var(--lc-font);cursor:pointer;transition:color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out)}
        .ss-ghost-btn:hover{color:var(--lc-text);background:var(--lc-raised)}
        .app-tab{display:inline-flex;align-items:center;height:32px;padding:0 14px;border:0;border-radius:8px;background:transparent;color:var(--lc-text-3);font-size:14px;font-family:var(--lc-font);cursor:pointer;transition:color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out)}
        .app-tab:hover{color:var(--lc-text)}
        .app-tab--on{background:var(--lc-raised);color:var(--lc-text)}
      `}</style>
      {/* 顶栏：导航抽屉入口 / 品牌 / 三个工作台 Tab / 租户身份 / 余额徽标 / 上传口 /
          Bell / 主题 / 语言 / 账号菜单。原生 button + .ss-ghost-btn / .app-tab 成钮，
          图标一律 <Icon/>（文字按钮仍保留 i18n 取词，零 emoji）。 */}
      <header className="app-header">
        <button className="ss-ghost-btn" onClick={() => setMenuOpen(true)} aria-label={t('app.openNav')} style={{ fontSize: 20, padding: '0 8px' }}><Icon n="menu" style={{ verticalAlign: '-3px' }} /></button>
        <span className="brand">
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 60 }} />
            : <span style={{ fontSize: 20, fontWeight: 700 }}><Icon n="brand" style={{ verticalAlign: '-3px', marginRight: 8 }} />{branding.brandName || t('app.title')}</span>}
        </span>
        <button className={'app-tab' + (tab === 'workbench' ? ' app-tab--on' : '')}
                onClick={() => switchTab('workbench')}><Icon n="chat" style={{ verticalAlign: '-3px', marginRight: 6 }} />{t('app.tabWorkbench')}</button>
        <button className={'app-tab' + (tab === 'tickets' ? ' app-tab--on' : '')}
                onClick={() => switchTab('tickets')}><Icon n="clipboard" style={{ verticalAlign: '-3px', marginRight: 6 }} />{t('app.tabTickets')}</button>
        <button className={'app-tab' + (tab === 'editor' ? ' app-tab--on' : '')}
                onClick={() => switchTab('editor')}><Icon n="pencil" style={{ verticalAlign: '-3px', marginRight: 6 }} />{t('app.tabEditor')}</button>
        <div style={{ flex: 1 }} />
        {/* ★ F1：租户身份徽标——个人用户「个人版」，企业用户显示所属租户名（title 全文） */}
        <span className="tenant-tag" title={tenantTag || t('app.personalPlan')} style={{ fontSize: 12, color: '#9AA0AA', whiteSpace: 'nowrap' }}>
          {tenantTag
            ? <><Icon n="building" style={{ verticalAlign: '-3px', marginRight: 4 }} />{tenantTag.length > 12 ? tenantTag.slice(0, 12) + '…' : tenantTag}</>
            : <><Icon n="user" style={{ verticalAlign: '-3px', marginRight: 4 }} />{t('app.personalPlan')}</>}
        </span>
        {/* 余额/套餐徽标：LangCross Badge 取代原 TDesign Tag（窄屏由 mobile.css 隐藏），
            文案仍是 gtpl 组装的积分口径 */}
        {!!pkgLine && <Badge className="pkg-line-tag">{pkgLine}</Badge>}
        {canUploadKb && (
          <Button size="sm" variant="secondary" onClick={() => setKbUploadOpen(true)}>
            {t('kb.topbarUpload')}
          </Button>
        )}
        <Bell />
        {/* ★ F5：明暗主题三态切换（auto/light/dark，localStorage 记忆，auto 跟随系统） */}
        <button className="ss-ghost-btn" title={t(`app.theme.${themeNow}`)} onClick={() => { cycleTheme(); setThemeTick((n) => n + 1) }}>{themeIcon}</button>
        {/* ★ #23：二元 EN 切换钮退役，换 12 语种 LangSelect 下拉（词表源 @/i18n LANG_OPTIONS） */}
        <LangSelect align="right" />
        <AccountMenu showAdminConsole={roleLevelSafe(user?.role) >= 2} onGotoAdmin={() => navigate('/admin')} />
      </header>

      {/* ★ E11：余额耗尽（billing_stopped 口径）常驻横幅——旧版仅深藏于自助面板
          ★ 2026-09-16 整改：旧版一律跳 /packages（只读页）——租户管理员及以上直跳
            后台计费 Hub（真正的收银台所在），避免「点充值→落只读页」死胡同 */}
      {depleted && (
        // 配色随纯黑主题调整：半透明红底 + #E5484D 文字（旧的 #fff1f0 浅底浅字在暗色下不可读）
        <div style={{ background: 'rgba(229,72,77,0.10)', color: '#E5484D', padding: '6px 16px', fontSize: 13, display: 'flex', gap: 12, alignItems: 'center', borderBottom: '1px solid rgba(229,72,77,0.30)' }}>
          <span>{t('ss.exhaustedHint')}</span>
          <Button size="sm" variant="danger" onClick={() => {
            if (roleLevelSafe(user?.role) >= 3) { useAdminStore.getState().gotoPanel('billing'); navigate('/admin') }
            else navigate('/packages')
          }}>{t('ss.gotoRecharge')}</Button>
        </div>
      )}
      <div className="app-main" style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 }}>
        {chat.isBackendLoading ? (
          // 后端冷启动占位（★ D2 #24）：spinner 换成与路由懒加载同一份 WordSwap 换词动效，
          // 纯黑页面上不再闪白，两处加载态共用唯一实现
          <div className="loading-screen" style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
            <WordSwap className="ws--lg" ariaLabel={t('app.starting')} />
            <p style={{ fontSize: 16, color: '#9AA0AA' }}>{t('app.starting')}</p>
          </div>
        ) : (
          <Suspense fallback={<PageLoading />}>
            <Routes>
              <Route path="/" element={<ChatWindow />} />
              <Route path="/tickets" element={<TicketsPage />} />
              <Route path="/editor" element={<EditorPage />} />
              <Route path="/billing" element={<BillingCenter />} />
              {/* 路由守卫：/invites 仅个人版可达（isPersonal 取自 meContext），企业用户命中即弹回工作台 */}
              <Route path="/invites" element={isPersonal ? <ReferralPanel /> : <Navigate to="/" replace />} />
              <Route path="/packages" element={<MyPackagePanel />} />
              <Route path="/my" element={<AccountPanel />} />
              {/* 公开定价页：营销门面（无需登录，Root 内另有未登录分支），挂在 FrontShell
                  下是为了让已登录用户也能从工作台直达 */}
              <Route path="/pricing" element={<PricingPage />} />
              {/* 前台兜底：壳内未匹配的路径一律 replace 回工作台，既不留空白页也不污染后退栈 */}
              <Route path="*" element={<Navigate to="/" replace />} />
            </Routes>
          </Suspense>
        )}
      </div>

      {/* 窄屏导航抽屉：LangCross Drawer 受控属性为 open / title（旧 TDesign 是
          visible / header / size / footer），宽度与遮罩走组件库自身令牌 */}
      <Drawer open={menuOpen} onClose={() => setMenuOpen(false)} title={t('app.more')}>
        <nav className="ss-drawer-nav">
          {/* 抽屉项与前台路由一一对应；企业用户在此就过滤掉「邀请有礼」，与 /invites 守卫同口径，省得点了被弹回 */}
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
      {/* ★ autosales：AI 销售/客服挂件（前台常驻，登录/注册页自动隐藏） */}
      <Suspense fallback={null}><AiAssist /></Suspense>
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

  // 会话恢复守卫：本地有 token 时 authMe 还没返回（restoring 初值 = !!token），
  // 这帧既不能当「已登录」出工作台，也不能当「未登录」闪出登录页，只出占位。
  if (restoring) {
    return <div style={{ display: 'grid', placeItems: 'center', height: '100vh' }}>{t('app.loading')}</div>
  }
  // 未登录分诊：营销门面（/pricing、/）直出内容页，其余路径一律落到登录页
  if (!user) {
    // 定价页属营销门面：未登录访客同样可直接访问（否则会落到登录页）
    // 这里不带 FrontShell 外壳（无工作台 Tab / 余额徽标），页面自身即完整营销页；
    // 与 FrontShell 内的 /pricing 路由分别覆盖「访客」与「已登录」两种入口。
    if (path === '/pricing') {
      return (
        <Suspense fallback={<PageLoading />}>
          <PricingPage />
          {/* ★ autosales：定价页常驻 AI 接待挂件 */}
          <AiAssist />
        </Suspense>
      )
    }
    // ★ S5 门面：未登录访问 `/` 出官网落地页；登录/注册走 /login、/register
    if (path === '/') {
      return (
        <Suspense fallback={<PageLoading />}>
          <Landing />
          {/* ★ autosales：落地页常驻 AI 接待挂件 */}
          <AiAssist />
        </Suspense>
      )
    }
    // 未登录兜底：非营销路径一律出登录页。/admin 前缀以 admin 态出——Login 内部会拦掉
    // 非租管账号，并关掉注册等前台专属流程；onLogin 再补一次角色校验，不够格立即弹回工作台
    return (
      <Suspense fallback={<PageLoading />}>
        <Login mode={path.startsWith('/admin') ? 'admin' : 'home'} onLogin={(u) => { onLogin(u); if (path.startsWith('/admin') && roleLevelSafe(u.role) < 2) navigate('/') }} />
        {/* ★ autosales：挂件内部对 /login、/register 自隐藏，其余未登录路径仍可接待 */}
        <AiAssist />
      </Suspense>
    )
  }
  // 后台路由守卫：/admin 只对租管及以上（roleLevelSafe>=2）开放，AdminDashboard 自身不再判权；
  // 不够格的登录用户走 else 分支——不渲染任何后台数据，只给一个返回首页的出口（Button 现为 LangCross 件）
  if (path.startsWith('/admin')) {
    return roleLevelSafe(user.role) >= 2 ? (
      <Suspense fallback={<PageLoading />}>
        <AdminDashboard />
        {/* ★ autosales：管理后台常驻挂件 */}
        <AiAssist />
      </Suspense>
    ) : (
      <div style={{ padding: 40 }}>
        <Button onClick={() => navigate('/')}>{t('app.backHome')}</Button>
      </div>
    )
  }
  return (
    <>
      <FrontShell />
      {/* ★ autosales：FrontShell 内已含挂件，此处不重复渲染 */}
    </>
  )
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
