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
// 2026-09-22 全站还原 UI 真值（用户指令）：顶栏按 UI-ANNOTATIONS §2.2 收为 38 高、
//   品牌 14、Tab 13 胶囊；#67 放大的内联字阶（汉堡 24/品牌 23/租户徽标 15 等）全部还原。
// ============================================================================

import { lazy, Suspense, useCallback, useContext, useEffect, useRef, useState } from 'react'
import { BrowserRouter, Routes, Route, Navigate, useLocation, useNavigate } from 'react-router-dom'
import { myPackage, meContext } from '@/api'
import { AuthProvider, useAuth } from '@/stores/auth'
import { AdminProvider, useAdminStore } from '@/stores/admin'
import { ChatProvider, useChat, PkgRefreshCtx, type PkgRefreshHandle } from '@/hooks/useChat'
import { useT, t as gt, tpl as gtpl } from '@/i18n'
import { setAuthToken, setActiveTenantId, API_BASE } from '@/api'
import { applyTheme } from '@/lib/theme'
// 主题在「模块求值期」就落到 <html> 上，而不是等 FrontShell 挂载后再 effect 里做：
// 首帧之前 data-theme/color-scheme 未定的话，浏览器会按浅色画一屏再翻黑（换肤期最刺眼的一次白闪）。
// ★ 〇-N：light/auto 档已删（整站只有暗色一套真值），这里恒写 dark 并清掉历史偏好。
applyTheme()
import { BrandingProvider, useBranding } from './branding'
import ErrorBoundary from './components/ErrorBoundary'
import { roleLevelSafe } from '@/lib/ui'
import { intlLocale } from './lib/format'

// 跨域登录跳转：品牌子域登录后通过 /?sso_code= 跳转回来（★ E3：一次性 code 换取 token，
// 不再让 JWT 出现在地址栏/Referer/浏览历史；60s TTL、单次消费，见后端 /api/auth/sso/exchange）
;(() => {
  // 抹掉地址栏参数（sso_code / 历史 token），保留其余 query 与 hash：
  // 用 replaceState 而非 pushState，不往历史栈里塞一条「带 code 的脏 URL」，后退也不会再命中它
  const clean = () => {
    const p = new URLSearchParams(window.location.search)
    p.delete('sso_code')
    p.delete('token') // 历史链接兼容：仅抹掉参数，不再注入登录态
    const url = window.location.pathname + (p.toString() ? '?' + p.toString() : '') + window.location.hash
    window.history.replaceState({}, '', url)
  }
  try {
    const code = new URLSearchParams(window.location.search).get('sso_code')
    if (!code) return // 绝大多数访问走这条：没有 code 就整段不执行，一次网络请求都不发
    clean() // 先清 URL 再发请求：兑换失败也不会在地址栏留下一个已被消费/过期的一次性 code
    // ★ E12：统一走 API_BASE（core.ts 单一后端地址口径）
    // 这里刻意用裸 fetch 而非 @/api 封装：封装会带上本地已有 token 与租户头，
    // 而这条链路的意义正是「尚无登录态时用一次性 code 换 token」
    fetch(`${API_BASE}/api/auth/sso/exchange`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ code }),
    }).then(async (res) => {
      const data = await res.json().catch(() => null) as { success?: boolean; token?: string } | null
      if (res.ok && data?.success && data.token) {
        setAuthToken(data.token)
        // 超管「生效租户」存在 sessionStorage 并随请求带 X-Tenant-ID：SSO 落地后归零，
        // 免得带着上一个域里切好的租户上下文进系统（0＝不下发该头，由后端按 JWT 判租户）
        setActiveTenantId(0)
        window.location.reload() // 兑换成功后走正常会话恢复，避免半初始化状态
        // （reload 而不是 setState 驱动：AuthProvider 及各 Provider 的初始化都在挂载期）
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
// ★ 2026-09-22：useWordSwapGate 载入闸门——冷启动占位即使接口已返回，也要把动效演满
//   3 个语种拍次再收场（读得完 + 给首屏前后端调用留时间），路由懒加载不受此约束。
import WordSwap, { useWordSwapGate } from './components/WordSwap'
// ★ #23：12 语种界面语言下拉（顶栏/登录卡/后台共用）
import { LangSelect } from './components/LangSelect'
// LangCross 纯黑组件库：前台骨架只依赖这三个件（Button/Badge/Icon），
// 替代原 tdesign-react 的 Button/Tag/Drawer —— 换肤期不再引 TDesign 组件。
// ★ 2026-09-24 〇-S（#7）：Drawer 随汉堡抽屉退役，本文件不再 import。
import { Badge, Button, Icon } from '@/ui/langcross/src'

// 页面加载中占位组件（★ D2 #24：旋转圈已换成落地页同源的「划掉错词→亮起正词」换词动效；
// ★ 2026-09-20 反馈③：minHeight 100dvh 撑满首屏，动效真正垂直居中而非挤在半高容器里；
// ★ 2026-09-24 用户反馈：去掉「加载中」提示文字——换词动效本身就是加载语义，纯动效更干净，
//   文字仅保留为 WordSwap 的 ariaLabel 供读屏），
// 路由懒加载时展示；onBeat 只在冷启动闸门（会话恢复）传入，用于「演满 3 个语种再放行」
// 用普通函数而非 React.memo：它只在 Suspense 解析期短暂挂载，没有可优化的重复渲染路径。
// 标题走模块级 gt（而非 useT）：占位通常只活几百毫秒，不值得为它订阅语言变更。
function PageLoading({ label = gt('app.loading'), onBeat }: { label?: string; onBeat?: (done: number) => void } = {}) {
  return (
    // flex:1 + minHeight:100dvh 同时给：外层是 flex 列时用 100dvh 撑住；
    // dvh（非 vh）是为了移动端地址栏收起/展开时不把动效顶偏。
    <div style={{ flex: 1, minHeight: '100dvh', width: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
      <WordSwap className="ws--lg" ariaLabel={label} onBeat={onBeat} />
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
  // ★ 2026-09-24 〇-S（#7）：menuOpen（汉堡抽屉开关）随抽屉一并退役，自助入口改走 AccountMenu 下拉
  // ★ 2026-09-22 载入闸门：后端探活撤销后，占位仍留到 WordSwap 演满 3 个语种拍次
  // 闸门放在 FrontShell 这一层：它只替换 .app-main 里的路由出口，顶栏照常渲染
  //   （用户看得见自己在哪个产品/能切语言），而路由内容在占位期间根本不挂载，两者不会同屏打架。
  // loading 源是 chat store 的 isBackendLoading（初值 true）：探活成功即撤；
  //   离线时 useChat 内部按 1s×30 次重试，全失败则该标记保持 true（闸门本身不加超时兜底，
  //   收场完全跟着 loading 走，这是刻意的——后端没起来时进工作台也只会满屏报错）。
  const bootGate = useWordSwapGate(chat.isBackendLoading)
  // 顶栏「上传知识库」入口守卫：主管及以上（roleLevelSafe>=2）才露出该按钮，普通成员不可见
  const canUploadKb = roleLevelSafe(user?.role) >= 2

  // ★ F-11（批G 2026-09-25）：顶栏「当前套餐/积分行」拉取提取成 useCallback——
  // 挂载时仍照旧拉一次，同时经 pkgHandle 注册给聊天层：即时翻译 done 帧（扣点）后
  // debounce 2s 回调这里，顶栏余额不再停在旧值（套餐到期/变更也在下一次刷新时被感知）。
  const refreshPkgLine = useCallback(async () => {
    if (!user) return // 未登录不打余额接口，避免 401 噪声（与挂载 effect 同口径）
    try {
      const p = await myPackage() as unknown as { success?: boolean; points_balance?: number; balance_sentences_approx?: number }
      if (p.success && typeof p.points_balance === 'number') {
        const nf = new Intl.NumberFormat(intlLocale()) // ★ 〇-Q：按**界面语种**（原为浏览器默认，切语种后数字不跟）
        setPkgLine(gtpl('app.pkgLineFmt', { points: nf.format(p.points_balance), approx: nf.format(p.balance_sentences_approx ?? 0) }))
        setDepleted(p.points_balance <= 0) // ★ E11：billing_stopped 顶部横幅信号
      }
    } catch { /* ignore */ } // 拉不到就保持上一次的积分行，不打断工作台
  }, [user])

  // ★ F-11：把刷新函数登记进 App 根持有的可变句柄（PkgRefreshCtx）。注册的是包一层的
  // 稳定闭包，聊天层 done 帧后读句柄现取现调；本组件卸载（如切去 /admin）时摘除，
  // 避免聊天在后台路由下也去刷一个已经不在顶栏的积分行。
  const pkgHandle = useContext(PkgRefreshCtx)
  useEffect(() => {
    if (!pkgHandle) return
    pkgHandle.refresh = () => { void refreshPkgLine() }
    return () => { pkgHandle.refresh = null }
  }, [pkgHandle, refreshPkgLine])

  useEffect(() => {
    if (!user) return // 未登录（含 SSO 兑换前的那一帧）不打这两个接口，避免 401 噪声
    ;(async () => {
      // 两段各自 try/catch 而不是合并：meContext（身份/租户）失败不该连带把余额也吞掉，反之同理
      // （余额段已在 refreshPkgLine 内部自带 try/catch，这里直接 await）
      try {
        const c = await meContext()
        if (c.success) {
          const email = String((c as unknown as { email?: string }).email || '')
          setCtxNoEmail(!email) // 无邮箱账号 → 弹强制绑定邮箱（下方 EmailBindModal，不可关闭）
          const personal = (c as unknown as { is_personal?: boolean }).is_personal !== false
          setIsPersonal(personal)
          // ★ F1：顶栏租户名徽标数据源（企业=租户名，个人=个人版）
          setTenantTag(personal ? '' : String((c as unknown as { tenant_name?: string }).tenant_name || ''))
        }
      } catch { /* ignore */ }
      await refreshPkgLine()
    })()
    // deps 挂 user（登录态对象）+ refreshPkgLine（随 user 换身份，见上）：这两个值是
    // 「进工作台读一次」的身份/余额快照，刻意不跟 location/path 走——切 Tab、换路由不该
    // 把顶栏的两条查询重新打一遍。
  }, [user, refreshPkgLine])

  // 从 pathname 推导当前 Tab
  const tab = path.startsWith('/tickets') ? 'tickets' : path.startsWith('/editor') ? 'editor' : 'workbench'
  // 切换工作台 Tab（workbench/tickets/editor），通过 navigate 跳转
  function switchTab(to: 'workbench' | 'tickets' | 'editor') {
    const target = to === 'tickets' ? '/tickets' : to === 'editor' ? '/editor' : '/'
    if (path !== target) navigate(target)
  }

  return (
    // 最外层这层 Suspense 主要兜顶栏的懒加载件（Bell / AccountMenu / SiteFooter）；
    // 页面级懒加载另有 .app-main 里的内层 Suspense，两层各管一段，互不顶掉。
    // 这层的 fallback 渲染在外层 div 之外、没有卡片底可依托，所以文档底由 theme.css 开头
    // 那条 html,body{background:#000}（★ #66）铺黑，否则切页瞬间会看到
    // 「灰字换词动效糊在白纸」的对比度反转。
    <Suspense fallback={<PageLoading />}>
    {/* ★ 2026-09-24 〇-S（#7）：minHeight:100vh → height:100dvh——页脚常驻外壳列尾后，
        聊天区不能再按「视口-顶栏」自算高度（否则页脚被顶出首屏、永远滚不到），
        改由本层把视口高度锁死、.app-main 内部消化滚动，页脚始终占住列底（元宝同款常驻底部口径）。 */}
    <div style={{ height: '100dvh', display: 'flex', flexDirection: 'column', background: 'var(--npz-page-bg, #000000)' }}>
      {/* ★ D2 #24：appspin 旋转 keyframes 已随两处 spinner 退役（全站唯一消费点消失） */}
      {/* 前台自助区 + 顶栏的局部类：纯黑换肤后卡片/表格/抽屉一律走白字 + 描边令牌
          （#E7E9EA / var(--lc-border-faint)），.ss-ghost-btn（顶栏幽灵按钮）与 .app-tab
          （工作台 Tab）直接引用 --lc-* 令牌，hover 只改色不投影。
          ⚠ 名义上是「局部类」，实际上是随外壳注入的全局块：.ss-grid/.ss-table/.ss-row 被
            selfservice 的自助面板（ReferralPanel/MyPackagePanel/AccountPanel）与 MyBilling（账单表格）
            直接复用，所以这些页面只在 FrontShell 下可达，单独挂载（不经本外壳）就会掉样式
            ——MyBilling.tsx 尾部亦已注明这一依赖。
          ⚠ 顶栏字号唯一来源就是本文件与 theme.css 基线：历史上 theme.css §十一 曾用
            `html .app-header` 前缀整档放大顶栏控件（#67/#68），2026-09-22 全站还原 UI 真值时
            已随该层一并删除（真值见《UI-ANNOTATIONS.md》§2.2：顶栏 38 高、品牌 14、导航 13 胶囊）。 */}
      <style>{`
        .ss-grid{display:grid;gap:16px;grid-template-columns:repeat(auto-fill,minmax(340px,1fr))}
        .ss-grid .ssc-card{width:100%}
        .ss-row{display:flex;justify-content:space-between;align-items:center;padding:8px 0;border-bottom:1px solid var(--lc-border-faint)}
        .ss-row span{color:var(--lc-text-2)}.ss-row b{font-size:18px;color:#E7E9EA}
        .ss-copy{display:flex;align-items:center;gap:8px}
        .ss-stats{display:flex;gap:24px;padding:12px 0}
        .ss-stat{text-align:center}
        .ss-stat b{display:block;font-size:20px;color:#E7E9EA}
        .ss-table{width:100%;border-collapse:collapse}
        .ss-table th,.ss-table td{border:1.2px solid var(--lc-border-faint);padding:6px 8px;text-align:start}
        .ss-table th{background:var(--npz-surface-2);color:#E7E9EA}
        /* ★ 2026-09-24 〇-S（#7）：.ss-drawer-nav/.ss-drawer-item 随汉堡抽屉退役 */
        .ss-quick{display:flex;flex-wrap:wrap;gap:8px}
        .ss-loading{display:flex;justify-content:center;padding:40px}
        /* 顶栏控件字阶（★ 2026-09-22 还原 UI-ANNOTATIONS §2.2 真值）：幽灵按钮 14px/32 高、
           工作台 Tab 13px 胶囊（活跃=面 #16181C + 文字 #E7E9EA，即 --lc-raised/--lc-text）；
           历史上 #67/#68 曾整档放大到 16/17px 与 38/40 高，已随页面级覆写层一并撤销。
           描边/分隔统一走 --lc-* 令牌，hover 只改色不投影。 */
        .ss-ghost-btn{display:inline-flex;align-items:center;justify-content:center;height:32px;padding:0 8px;border:0;border-radius:8px;background:transparent;color:var(--lc-text-2);font-size:16px;font-family:var(--lc-font);cursor:pointer;transition:color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out)}
        .ss-ghost-btn:hover{color:var(--lc-text);background:var(--lc-raised)}
        .app-tab{display:inline-flex;align-items:center;height:28px;padding:0 14px;border:0;border-radius:999px;background:transparent;color:var(--lc-text-2);font-size:15px;font-family:var(--lc-font);cursor:pointer;transition:color var(--lc-mo-release) var(--lc-mo-out),background var(--lc-mo-release) var(--lc-mo-out)}
        .app-tab:hover{color:var(--lc-text)}
        .app-tab--on{background:var(--lc-raised);color:var(--lc-text)}
      `}</style>
      {/* 顶栏：导航抽屉入口 / 品牌 / 三个工作台 Tab / 租户身份 / 余额徽标 / 上传口 /
          Bell / 主题 / 语言 / 账号菜单。原生 button + .ss-ghost-btn / .app-tab 成钮，
          图标一律 <Icon/>（文字按钮仍保留 i18n 取词，零 emoji）。 */}
      <header className="app-header">
        {/* ★ 2026-09-24 〇-S（#7）：左侧「更多」汉堡钮退役——用户拍板「右侧不是汉堡是下拉，
            套餐/余额/账号并入右上角下拉、页脚回页脚位置」。抽屉四项自助入口迁 AccountMenu
            （selfNav 传入即露出），SiteFooter 移到外壳列尾部常驻，本行不再有任何钮。
            顶栏左缘直接是品牌位，与交付稿单汉堡口径分岔由用户后令覆盖。 */}
        <span className="brand">
          {/* 白标：租户配了 logo 就出图（alt 用品牌名），否则出「品牌名」前缀一枚 18 图标，
              品牌名缺省回落到 app.title（内置「能言」），不留空品牌位。
              字阶/字重由 theme.css .brand（§2.2 真值 14）统一给，图片高度收进 38 高顶栏。 */}
          {branding.brandLogo
            ? <img src={branding.brandLogo} alt={branding.brandName || 'logo'} style={{ height: 24 }} />
            : <span><Icon n="brand" style={{ fontSize: 18, verticalAlign: '-3px', marginInlineEnd: 8 }} />{branding.brandName || t('app.title')}</span>}
        </span>
        {/* 三个工作台 Tab：选中态从 URL 反推（见上面的 tab 推导），本组件不再持有 Tab state——
            浏览器前进/后退、深链进来都能让高亮自动跟上，单一事实源是地址栏 */}
        <button className={'app-tab' + (tab === 'workbench' ? ' app-tab--on' : '')}
                onClick={() => switchTab('workbench')}><Icon n="chat" style={{ verticalAlign: '-3px', marginInlineEnd: 6 }} />{t('app.tabWorkbench')}</button>
        <button className={'app-tab' + (tab === 'tickets' ? ' app-tab--on' : '')}
                onClick={() => switchTab('tickets')}><Icon n="clipboard" style={{ verticalAlign: '-3px', marginInlineEnd: 6 }} />{t('app.tabTickets')}</button>
        <button className={'app-tab' + (tab === 'editor' ? ' app-tab--on' : '')}
                onClick={() => switchTab('editor')}><Icon n="pencil" style={{ verticalAlign: '-3px', marginInlineEnd: 6 }} />{t('app.tabEditor')}</button>
        <div style={{ flex: 1 }} /> {/* 空占位把后面的控件推到行尾（顶栏无 justify-content:space-between，靠它撑） */}
        {/* ★ F1：租户身份徽标——个人用户「个人版」，企业用户显示所属租户名（title 全文）
            超过 12 字就地截断加省略号：顶栏一行放不下长租户名（§2.2 顶栏不折行），
            完整值交给 title 悬浮提示；字号 12 = §2.2「套餐标识」真值档 */}
        <span className="tenant-tag" title={tenantTag || t('app.personalPlan')} style={{ fontSize: 14, color: 'var(--lc-text-2)', whiteSpace: 'nowrap' }}>
          {tenantTag
            ? <><Icon n="building" style={{ verticalAlign: '-3px', marginInlineEnd: 4 }} />{tenantTag.length > 12 ? tenantTag.slice(0, 12) + '…' : tenantTag}</>
            : <><Icon n="user" style={{ verticalAlign: '-3px', marginInlineEnd: 4 }} />{t('app.personalPlan')}</>}
        </span>
        {/* 余额/套餐徽标：LangCross Badge 取代原 TDesign Tag（窄屏由 mobile.css 隐藏），
            文案仍是 gtpl 组装的积分口径。
            !!pkgLine：myPackage 回之前它是空串，此处不渲染而不是露一个空胶囊 */}
        {!!pkgLine && <Badge className="pkg-line-tag">{pkgLine}</Badge>}
        {/* 上传知识库入口：只有主管及以上露出（canUploadKb），点击只是把弹窗置开 */}
        {canUploadKb && (
          <Button size="sm" variant="secondary" onClick={() => setKbUploadOpen(true)}>
            {t('kb.topbarUpload')}
          </Button>
        )}
        {/* 站内通知铃铛：懒加载件，与 AccountMenu 一起被最外层 Suspense 兜住
            （首次解析期外壳整体回退成 PageLoading，之后常驻不再重建） */}
        <Bell />
        {/* ★ 〇-N：明暗切换钮随 light/auto 档一并退役——整站只交付一套暗色真值，
            可切到「没有配色的亮色档」正是黑底黑字缺陷的来源（见 lib/theme.ts）。 */}
        {/* ★ #23：二元 EN 切换钮退役，换 12 语种 LangSelect 下拉（词表源 @/i18n LANG_OPTIONS） */}
        <LangSelect align="right" />
        {/* ★ 〇-S（#7）：selfNav 让下拉露出「套餐/账单/账号」自助入口（原汉堡抽屉四项迁到这里），
            邀请有礼仅个人用户可见（与 /invites 路由守卫同口径） */}
        <AccountMenu showAdminConsole={roleLevelSafe(user?.role) >= 2} onGotoAdmin={() => navigate('/admin')}
                     selfNav={(p) => navigate(p)} showInvites={isPersonal} />
      </header>

      {/* ★ E11：余额耗尽（billing_stopped 口径）常驻横幅——旧版仅深藏于自助面板
          ★ 2026-09-16 整改：旧版一律跳 /packages（只读页）——租户管理员及以上直跳
            后台计费 Hub（真正的收银台所在），避免「点充值→落只读页」死胡同 */}
      {depleted && (
        // 配色随纯黑主题调整：半透明红底 + #E5484D 文字（旧的 #fff1f0 浅底浅字在暗色下不可读）；
        // 字号 12 = §2.2 InlineBanner 文案真值档
        <div style={{ background: 'rgba(229,72,77,0.10)', color: '#E5484D', padding: '4px 16px', fontSize: 14, display: 'flex', gap: 12, alignItems: 'center', borderBottom: '1px solid rgba(229,72,77,0.30)' }}>
          <span>{t('ss.exhaustedHint')}</span>
          <Button size="sm" variant="danger" onClick={() => {
            // 用 useAdminStore.getState() 而不是 useAdmin()：顶栏只为点一下钮取个 action，
            // 订阅 store 会把后台面板/租户切换的每次变化都灌进整壳重渲染
            if (roleLevelSafe(user?.role) >= 3) { useAdminStore.getState().gotoPanel('billing'); navigate('/admin') }
            else navigate('/packages')
          }}>{t('ss.gotoRecharge')}</Button>
        </div>
      )}
      <div className="app-main" style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0, overflowY: 'auto' }}>
        {/* ★ 2026-09-24 〇-S（#7）：补 overflowY:auto——外壳改 height:100dvh 后，
            长页（工单/定价/自助面板）的整页滚动从这里接（此前靠文档滚动）；
            短页/聊天页不受影响（聊天区自带内滚，见 ChatWindow cw-root）。
            minHeight:0 是 flex 列里的必需项：不给 0，子页面的固有高度会把
            外壳顶出视口，顶栏 sticky 也跟着晃 */}
        {bootGate.busy ? (
          // 后端冷启动占位（★ D2 #24）：spinner 换成与路由懒加载同一份 WordSwap 换词动效，
          // 纯黑页面上不再闪白，两处加载态共用唯一实现；
          // ★ 2026-09-22：接口先回来也不立刻收场，演满 3 拍（读得完三个语种）再进工作台
          // .loading-screen 在本仓没有任何 CSS 定义（Vue 版遗留名），排版全靠同行内联样式
          // 这里与 Root 的 restoreGate 是两个独立闸门实例：各自持有 played/need，也各自挂一份
          // WordSwap 从第一拍数起，只有各自真的见过 loading 才会补拍（不会互相借用对方的拍数）
          <div className="loading-screen" style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
            <WordSwap className="ws--lg" ariaLabel={t('app.starting')} onBeat={bootGate.onBeat} />
            <p style={{ fontSize: 18, color: 'var(--lc-text-2)' }}>{t('app.starting')}</p>
          </div>
        ) : (
          <Suspense fallback={<PageLoading />}>
            <Routes>
              <Route path="/" element={<ChatWindow />} />
              <Route path="/tickets" element={<TicketsPage />} />
              <Route path="/editor" element={<EditorPage />} />
              <Route path="/billing" element={<BillingCenter />} />
              {/* 路由守卫：/invites 仅个人版可达（isPersonal 取自 meContext），企业用户命中即弹回工作台。
                  isPersonal 初值是 true，所以 meContext 尚未回来的那一帧放行——深链直达时可能先闪一下邀请页 */}
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

      {/* ★ 2026-09-24 〇-S（#7）：页脚回页脚位置（参考元宝的底部常驻口径）——
          原先只藏在汉堡抽屉里，抽屉退役后挂在外壳 flex 列尾部；
          SiteFooter 自带 marginTop:auto，内容不足一屏时也贴列底不半途悬着。
          懒加载件由最外层 Suspense（PageLoading 兜底）接管，首帧不会闪挂。 */}
      <SiteFooter />

      {/* KbUploadDialog 是本项目自有弹窗，受控属性仍是历史的 visible（LangCross Drawer 才改叫 open） */}
      {canUploadKb && <KbUploadDialog visible={kbUploadOpen} onClose={() => setKbUploadOpen(false)} />}
      {/* 无邮箱账号（meContext 回 email 为空）的强制补绑：dismissible=false，不绑就关不掉 */}
      {ctxNoEmail && (
        <EmailBindModal hasOldEmail={false} dismissible={false}
                        onClose={() => setCtxNoEmail(false)}
                        onDone={() => setCtxNoEmail(false)} />
      )}
      {/* ★ autosales：AI 销售/客服挂件（前台常驻，登录/注册页自动隐藏）。
          fallback 给 null 而不是 PageLoading：挂件是右下角的旁路件，解析期若给整屏占位，
          等于为了一个可选控件把工作台上刚画出来的内容整个抹掉 */}
      <Suspense fallback={null}><AiAssist /></Suspense>
    </div>
    </Suspense>
  )
}

// ---- 根路由 ----
// 这里不用 <Routes>：Root 是按「登录态 × 路径前缀」手工分诊的组件（营销页/登录页/后台壳
// 三棵互斥子树），路由表只存在于 FrontShell 内部。因此本组件每次渲染都重新读 location 判定，
// 各分支自带 Suspense，缺一条路由不会 404，只会落到下面的兜底分支。
function Root() {
  const { user, restoring, onLogin } = useAuth()
  const location = useLocation()
  const navigate = useNavigate()
  const path = location.pathname
  const t = gt // 别名而非 useT()：Root 不订阅语言变更，切语种后这里的文案等下一次重渲染才跟上

  // 会话恢复守卫：本地有 token 时 authMe 还没返回（restoring 初值 = !!token），
  // 这帧既不能当「已登录」出工作台，也不能当「未登录」闪出登录页，只出占位。
  // ★ 2026-09-22：占位由一行文字升级为全站同一份 WordSwap 换词动效，并演满 3 个语种
  // 拍次再放行（authMe 早回来也留足首屏时间）；无 token 时 restoring 恒 false，不补拍。
  const restoreGate = useWordSwapGate(restoring)
  if (restoreGate.busy) {
    // 闸门期间必须在所有分支判定之前直接 return：这帧 user 还没回来，
    // 往下走会被当成未登录渲染出登录页，会话恢复成功后再翻回工作台（闪一次登录页）
    return <PageLoading onBeat={restoreGate.onBeat} />
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
// Provider 从外到内的顺序即依赖方向（内层可以取外层，反向取不到）：
//   ErrorBoundary 最外——它不碰任何上下文，却要能兜住下面任意一层的挂载异常；
//   BrowserRouter 在所有业务 Provider 之外——ChatProvider 与 AdminProvider 都要 useNavigate
//     （前者决定消息记录归属键，后者把 navigate 注入 zustand 供 store 动作跳转）；
//   ChatProvider 在 AuthProvider 之内：它直接 useAuth() 取 user；
//   PkgRefreshCtx（★ F-11 批G）包在 ChatProvider 外、AuthProvider 内：它只是把 App 根持有的
//     可变句柄递给子树（ChatProvider 读、FrontShell 写），不参与任何数据依赖，位置跟着消费点最外层放；
//   AdminProvider 读登录态走 useAuthStore 全局单例（对层级不敏感），放这里只为把
//     后台上下文排在聊天之后、品牌之前；
//   BrandingProvider 最内：白标只按访问域名解析（可被 index.html 注入 window.__BRANDING__ 抢先），
//     不依赖其它上下文，贴近 useBranding() 的消费点即可。
export default function App() {
  // ★ F-11（批G）：顶栏积分刷新句柄——App 根持有一份可变对象，经 PkgRefreshCtx 同时下发给
  // ChatProvider（done 帧后延迟回调）与 FrontShell（注册真正的 refreshPkgLine）。
  // 用惰性初始化（同 ChatProvider 的 storeRef 手法）：Provider 的 value 引用必须终生稳定，
  // 否则每次重渲染都会让 ChatProvider 的注册 effect 重跑一遍。
  const pkgHandleRef = useRef<PkgRefreshHandle | null>(null)
  if (pkgHandleRef.current === null) pkgHandleRef.current = { refresh: null }
  return (
    <ErrorBoundary>
      <BrowserRouter>
        <AuthProvider>
          <PkgRefreshCtx.Provider value={pkgHandleRef.current}>
            <ChatProvider>
              <AdminProvider>
                <BrandingProvider>
                  <Root />
                </BrandingProvider>
              </AdminProvider>
            </ChatProvider>
          </PkgRefreshCtx.Provider>
        </AuthProvider>
      </BrowserRouter>
    </ErrorBoundary>
  )
}
