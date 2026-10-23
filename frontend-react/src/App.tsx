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
import { AuthProvider, useAuth, isPlatformBillingContext } from '@/stores/auth'
import { AdminProvider, useAdminStore } from '@/stores/admin'
import { ChatProvider, useChat, PkgRefreshCtx, createPkgRefreshHub, type PkgRefreshHub } from '@/hooks/useChat'
import { useT, t as gt, tpl as gtpl, ensureAppPanels, appPanelsLoaded } from '@/i18n'
import { setAuthToken, setActiveTenantId, API_BASE } from '@/api'
import { applyTheme } from '@/lib/theme'
// 主题在「模块求值期」就落到 <html> 上，而不是等 FrontShell 挂载后再 effect 里做：
// 首帧之前 data-theme/color-scheme 未定的话，浏览器会按浅色画一屏再翻黑（换肤期最刺眼的一次白闪）。
// ★ 〇-N：light/auto 档已删（整站只有暗色一套真值），这里恒写 dark 并清掉历史偏好。
applyTheme()
// useBranding 在左栏/移动顶栏那两件里用（品牌位），本文件自 2026-10-23 起也读一次：
// 未登录分诊要给 landingEnabledForGuest 传 tenantId（租户定制页不展示平台落地页）
import { BrandingProvider, useBranding } from './branding'
import ErrorBoundary from './components/ErrorBoundary'
// ★ 2026-10-21 IAM P1-7：登录面收成唯一调用点（PublicRoot 那一棵树用同一个组件，见其文件头口径①）
import { LoginFace } from './components/LoginFace'
// ★ 2026-10-17 批 B：把 react-router 的 location/navigate 灌进 NavCtx（公开面走 PublicRoot 不挂 router，
//   那些共用组件改读 NavCtx，见 lib/nav.ts）。本文件是**唯一**允许引 navRouter 的地方。
import { RouterNavBridge } from './lib/navRouter'
import { roleLevelSafe } from '@/lib/ui'
// ★ 〇-Z：站点门面标记（演示站访客打开 `/` 直接进登录注册页，判据由后端首页注入）
import { landingEnabledForGuest } from '@/lib/siteFlags'
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
// 登录屏不在这里懒加载：它已收进 components/LoginFace（与 PublicRoot 共用同一份分片）
// ★ S5 官网落地页（未登录 /）
const Landing = lazy(() => import('./components/Landing'))
// 公开定价页（/pricing，设计图 05；营销门面，未登录可直达）
const PricingPage = lazy(() => import('./components/PricingPage'))
// ★ 〇-X #55（2026-09-28）公开「比价与算价」页（/compare）：公示算价公式 + 自算器 + 人工价出处
const PriceComparePage = lazy(() => import('./components/PriceComparePage'))
// ChatWindow 对话翻译主窗口（懒加载分包）
const ChatWindow = lazy(() => import('./components/ChatWindow'))
// TicketsPage 文件工单页（懒加载分包）
const TicketsPage = lazy(() => import('./components/TicketsPage'))
// EditorPage 在线编辑器页（懒加载分包）
const EditorPage = lazy(() => import('./components/EditorPage'))
// AdminDashboard 管理后台壳（懒加载分包，内含全部后台面板）
const AdminDashboard = lazy(() => import('./components/admin/AdminDashboard'))
// ★ 2026-10-15 用户令：前台不再有独立通知铃铛——通知以「消息(N)」菜单项收进 AccountMenu
// （AccountMenu 内部复用 Bell.tsx 导出的 NotifyPanel + useNotify），故此处不再懒加载 Bell。
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
//   MIN_READ_BEATS 拍再收场。★ 2026-10-13 用户令：拍数下限由 3 改 **1**（强制演示一次即止），
//    readiness 之后不再补拍；路由懒加载不受此约束。
import WordSwap, { useWordSwapGate } from './components/WordSwap'
// ★ 2026-10-15 前台外壳布局方案：左栏 / 移动档顶栏 / 移动档状态机
//   （唯一实现，结构见 components/frontshell/index.ts，样式见 styles/frontshell.css）
import { FrontSideNav, FrontMobileTopBar, FrontPage, useMobileShell } from './components/frontshell'
// ★ 2026-10-17 未登录门面移动壳（官网首页/定价/比价/登录注册 在 ≤640 复用登录态那套
//   汉堡＋抽屉＋品牌顶栏；结构见 components/publicshell/，样式见 styles/publicshell.css）
import { PublicShell } from './components/publicshell'
// ★ 〇-AM：对话历史记录页（工作台 SSE 翻译通道的持久化话记录查看）
const ChatHistory = lazy(() => import('./components/ChatHistory'))
// LangCross 纯黑组件库：前台外壳只剩 Button 一件（Badge/Icon/LangSelect 已随
// 左栏与移动顶栏迁进 components/frontshell，AccountMenu/SiteFooter 仍是懒加载件）。
// 替代原 tdesign-react 的 Button/Tag/Drawer —— 换肤期不再引 TDesign 组件。
// ★ 2026-09-24 〇-S（#7）：Drawer 随汉堡抽屉退役，本文件不再 import。
import { Button } from '@/ui/langcross/src'

// 页面加载中占位组件（★ D2 #24：旋转圈已换成落地页同源的「划掉错词→亮起正词」换词动效；
// ★ 2026-09-20 反馈③：minHeight 100dvh 撑满首屏，动效真正垂直居中而非挤在半高容器里；
// ★ 2026-09-24 用户反馈：去掉「加载中」提示文字——换词动效本身就是加载语义，纯动效更干净，
//   文字仅保留为 WordSwap 的 ariaLabel 供读屏），
// 路由懒加载时展示；onBeat 只在冷启动闸门（会话恢复）传入，用于「至少完整演满一拍再放行」
// 用普通函数而非 React.memo：它只在 Suspense 解析期短暂挂载，没有可优化的重复渲染路径。
// 标题走模块级 gt（而非 useT）：占位通常只活几百毫秒，不值得为它订阅语言变更。
function PageLoading({ label = gt('app.loading'), onBeat, loop = true }: { label?: string; onBeat?: (done: number) => void; loop?: boolean } = {}) {
  return (
    // flex:1 + minHeight:100dvh 同时给：外层是 flex 列时用 100dvh 撑住；
    // dvh（非 vh）是为了移动端地址栏收起/展开时不把动效顶偏。
    <div style={{ flex: 1, minHeight: '100dvh', width: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
      {/* loop 透传：带闸门的调用点（冷启动/会话恢复）传 busy——内容就绪即停在终帧，
          还在加载就继续播；不带闸门的 Suspense 兜底保持缺省 true（解析完成即卸载）。 */}
      <WordSwap className="ws--lg" ariaLabel={label} onBeat={onBeat} loop={loop} />
    </div>
  )
}

// ---- 前台工作台外壳 ----
// ---- 页面级错误边界（★ 2026-10-17 批 B：搬到 components/RouteBoundary.tsx，与公开面 PublicRoot 共用
//   同一份实现，并改读 lib/nav 的最小接口——公开面没有 react-router，在 App 里定义会把它拉回来）----
import RouteBoundary from './components/RouteBoundary'

// ---- 前台工作台外壳 ----
function FrontShell() {
  const chat = useChat()
  const { user } = useAuth()
  // 本文件只消费 t（文案）；lang 已随「中文才放大品牌字阶」那条规则迁进 FrontSideNav
  const [, t] = useT()
  const location = useLocation()
  const navigate = useNavigate()

  const path = location.pathname
  const [pkgLine, setPkgLine] = useState('')
  const [depleted, setDepleted] = useState(false) // ★ E11
  const [tenantTag, setTenantTag] = useState('') // ★ F1
  const [ctxNoEmail, setCtxNoEmail] = useState(false)
  const [isPersonal, setIsPersonal] = useState(true)
  const [kbUploadOpen, setKbUploadOpen] = useState(false)
  // ★ 2026-10-15 用户令（第五批）「参考元宝重做移动端」＋本批「抽成固定组件」：
  //   移动档是否为 S 档（≤640）、抽屉开合、路由变自动收起、抽屉打开锁底层滚动——
  //   这一族状态全部收进 useMobileShell（见 components/frontshell/useMobileShell.ts）。
  //   S 档壳＝「极简标题栏（汉堡 + 品牌簇）」＋ 汉堡抽屉；假状态栏（.lc-statusbar）与
  //   第二信息带（.lc-mob-band）早在上一批退役；底部 TabBar 本批撤除，余额改由 /my 承载。
  const shell = useMobileShell()
  const isMobile = shell.isMobile
  // ★ 2026-09-24 〇-S（#7）：menuOpen（汉堡抽屉开关）随抽屉一并退役，自助入口改走 AccountMenu 下拉
  // ★ 2026-09-22 载入闸门：后端探活撤销后，占位仍留到 WordSwap 演满 MIN_READ_BEATS 拍
  //   （★ 2026-10-13 用户令：该下限由 3 改 1——强制演示一次即止，就绪了就放，没就绪继续等）
  // 闸门放在 FrontShell 这一层：它只替换 .app-main 里的路由出口，顶栏照常渲染
  //   （用户看得见自己在哪个产品/能切语言），而路由内容在占位期间根本不挂载，两者不会同屏打架。
  // loading 源是 chat store 的 isBackendLoading（初值 true）：探活成功即撤；
  //   离线时 useChat 内部按 1s×30 次重试，全失败则该标记保持 true（闸门本身不加超时兜底，
  //   收场完全跟着 loading 走，这是刻意的——后端没起来时进工作台也只会满屏报错）。
  const bootGate = useWordSwapGate(chat.isBackendLoading)
  // 顶栏「上传知识库」入口守卫：主管及以上（roleLevelSafe>=2）才露出该按钮，普通成员不可见
  const canUploadKb = roleLevelSafe(user?.role) >= 2

  // ★ F-11（批G 2026-09-25）：顶栏「当前套餐/积分行」拉取提取成 useCallback——
  // 挂载时仍照旧拉一次，同时经 PkgRefreshCtx 枢纽订阅进广播名单：即时翻译流终态（扣点）后
  // debounce 2s 广播这里，顶栏余额不再停在旧值（套餐到期/变更也在下一次刷新时被感知）。
  const refreshPkgLine = useCallback(async () => {
    if (!user) return // 未登录不打余额接口，避免 401 噪声（与挂载 effect 同口径）
    try {
      const p = await myPackage() as unknown as { success?: boolean; points_balance?: number; balance_sentences_approx?: number }
      if (p.success && typeof p.points_balance === 'number') {
        // ★ O-9（批 I-10）：平台上下文（超管未切入任何租户）不参与计费，后端固定回 0；
        //   照此渲染会得到「余额 0 积分」胶囊并被 points_balance<=0 点亮「余额不足」横幅。
        //   守卫放前端、不动后端出参（tid<=0 回 0 的语义是对的），判据见 isPlatformBillingContext。
        if (isPlatformBillingContext(user.role)) { setPkgLine(''); setDepleted(false); return }
        const nf = new Intl.NumberFormat(intlLocale()) // ★ 〇-Q：按**界面语种**（原为浏览器默认，切语种后数字不跟）
        setPkgLine(gtpl('app.pkgLineFmt', { points: nf.format(p.points_balance), approx: nf.format(p.balance_sentences_approx ?? 0) }))
        setDepleted(p.points_balance <= 0) // ★ E11：billing_stopped 顶部横幅信号
      }
    } catch { /* ignore */ } // 拉不到就保持上一次的积分行，不打断工作台
  }, [user])

  // ★ F-11：把刷新函数登记进 App 根持有的广播枢纽（PkgRefreshCtx）。注册的稳定闭包由聊天层
  // 流终态后广播触发；本组件卸载（如切去 /admin）时退订，避免聊天在后台路由下也去刷一个
  // 已经不在顶栏的积分行。
  // ★ F-48（批 I-5）：旧形态是单槽位 `handle.refresh = fn`——第二个余额位（工作台余额条）一注册
  // 就把顶栏那份**静默挤掉**（反之亦然），两处必有一处永远不刷且无报错。改成 subscribe/emit 广播，
  // 各余额位各自订阅、互不覆写。
  const pkgHub = useContext(PkgRefreshCtx)
  useEffect(() => {
    if (!pkgHub) return
    return pkgHub.subscribe(() => { void refreshPkgLine() })
  }, [pkgHub, refreshPkgLine])

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
  // ★ 2026-10-15 第五批：「点完即收抽屉」由 FrontSideNav 内部的 pickNav 承担
  //   （不能只靠「路由变了就收」——点当前 Tab 不产生路由变化，抽屉会赖着不走）。

  return (
    // 最外层这层 Suspense 主要兜顶栏的懒加载件（Bell / AccountMenu / SiteFooter）；
    // 页面级懒加载另有 .app-main 里的内层 Suspense，两层各管一段，互不顶掉。
    // 这层的 fallback 渲染在外层 div 之外、没有卡片底可依托，所以文档底由 theme.css 开头
    // 那条 html,body{background:#000}（★ #66）铺黑，否则切页瞬间会看到
    // 「灰字换词动效糊在白纸」的对比度反转。
    // ★ 2026-10-13 链路批④（#29）：这一层同样在懒加载**顶栏件**（Bell / AccountMenu / SiteFooter），
    //   换源后旧哈希失效时它和页面级懒加载撞的是同一个异常，所以也给一层路由级边界——
    //   内层 .app-main 的边界更近，页面崩由它先接住（顶栏保住）；只有顶栏件自己取不到时
    //   才落到这一层，出同一张「页面内容没有取到…请重新加载页面」的人话卡。
    <RouteBoundary>
    <Suspense fallback={<PageLoading />}>
    {/* ★ 2026-09-24 〇-S（#7）：minHeight:100vh → height:100dvh——页脚曾常驻外壳列尾，
        聊天区不能再按「视口-顶栏」自算高度（否则页脚被顶出首屏、永远滚不到），
        改由本层把视口高度锁死、.app-main 内部消化滚动。★ 2026-10-16 页脚收进左栏后
        本层仍按 100dvh 锁高（左栏与右主列各自内部滚动，口径不变）。 */}
    {/* ★ 2026-10-15 元宝式布局：外壳由「顶栏+列」改为「行」——左栏 + 右主列。
        左栏**两档共用同一节点**：桌面常驻、移动档降级为汉堡抽屉（.fs-sidebar--drawer）。
        ★ 2026-10-16：页脚两档一致，都收在左栏尾部（.fs-foot-fold，默认折叠）——
        右主列内因此只剩「标题栏（移动档）+ 余额横幅 + 路由出口」。 */}
    <div style={{ height: '100dvh', display: 'flex', background: 'var(--npz-page-bg, #000000)' }}>
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
        /* ★ 2026-10-16 侧栏 tab 页布局重做：align-items:start = 卡片按自身内容高，不再被同行最高的
           那张拉到同高（/my 的「快速入口」卡曾经被 SCIM 卡拉到 315px，里面空一大片）；
           auto-fit + minmax(min(320px,100%),1fr)：fit 让空轨道塌掉（/packages 两张卡摊满整行、/my 三张
           卡落成三列），min() 是窄屏安全写法（原 340px 硬下限在 <380px 视口会横向溢出）。
           页面外垫/限宽不在这里——四个自助页统一走 .fs-page（styles/frontshell.css）。 */
        .ss-grid{display:grid;gap:16px;align-items:start;grid-template-columns:repeat(auto-fit,minmax(min(320px,100%),1fr))}
        .ss-grid .ssc-card{width:100%}
        /* ss-row 是「标签 ─ 值」的两端行：标签 flex:none 不参与收缩（否则窄列里会被长 URL 挤成
           「SCIM 端 / 点」两行，CJK 默认任意位置可断）；值那一侧 min-width:0 + overflow-wrap:anywhere
           才是该断的地方（长 URL/Badge 在断点处折行，不再反向撑破卡片）。 */
        .ss-row{display:flex;justify-content:space-between;align-items:center;gap:12px;padding:8px 0;border-bottom:1px solid var(--lc-border-faint)}
        .ss-row>span{flex:none}
        .ss-row>:not(span){min-width:0;overflow-wrap:anywhere;text-align:end}
        .ss-row span{color:var(--lc-text-2)}.ss-row b{font-size:18px;color:#E7E9EA}
        .ss-copy{display:flex;align-items:center;gap:8px;flex-wrap:wrap;justify-content:flex-end}
        .ss-stats{display:flex;gap:24px;padding:12px 0}
        .ss-stat{text-align:center}
        /* ★ 批2 六档字阶：关键数值 26/700 + 等宽数字（原 20px 旧档作废，UI-ANNOTATIONS §1.2） */
        .ss-stat b{display:block;font-size:26px;font-weight:700;color:var(--lc-text);font-variant-numeric:tabular-nums}
        .ss-table{width:100%;border-collapse:collapse}
        .ss-table th,.ss-table td{border:1.2px solid var(--lc-border-faint);padding:6px 8px;text-align:start}
        /* ★ 批2 六档字阶：表头 14/600/text-2（原正文字色提为表头是档语义错位） */
        .ss-table th{background:var(--npz-surface-2);color:var(--lc-text-2);font-size:14px;font-weight:600}
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
        /* ★ 2026-10-15 本批「抽成固定组件」：左栏（fs-*）与移动顶栏的全部样式已迁到
           src/styles/frontshell.css（前台外壳那一族的唯一样式出处，入口包统一引入）。
           本文件只保留与自助面板共用的 .ss-* 与工作台 Tab（.app-tab）两块。
           ⚠ .ss-* 名义上是「局部类」，实际是随外壳注入的全局块：被 selfservice 的
             自助面板与 MyBilling（账单表格）直接复用，删/挪它们会波及那三页。 */
      `}</style>
      {/* ★ 2026-10-15 元宝式布局（本批抽成固定组件）：左栏承载除会话外的一切——
          品牌 / 身份 / 语言 / 功能菜单 / 自助入口 / 余额 / 账号菜单 / 通知中心 / **折叠页脚**；
          桌面档常驻，移动档（≤640）化汉堡抽屉；页脚**两档一致**挂在左栏尾部（默认折叠）。
          结构与类名都收在 components/frontshell/FrontSideNav.tsx（样式 styles/frontshell.css），
          本文件只给「这一屏此刻的数据与动作」，不再持有左栏的两档形态。 */}
      <FrontSideNav
        isMobile={isMobile}
        open={shell.drawerOpen}
        onClose={shell.close}
        path={path}
        tab={tab}
        onTab={switchTab}
        onRoute={navigate}
        tenantTag={tenantTag}
        pkgLine={pkgLine}
        isPersonal={isPersonal}
        canUploadKb={canUploadKb}
        onUploadKb={() => setKbUploadOpen(true)}
        roleLevel={roleLevelSafe(user?.role)}
        AccountMenu={AccountMenu}
        SiteFooter={SiteFooter}
      />

      {/* ★ 元宝式右主列：移动标题栏（S 档）+ 余额横幅 + 路由出口（页脚在左栏尾部）。
          左栏与主列是同一 flex 行的两个子项；主列 flex:1 吃掉剩余宽度。
          移动档左栏已化抽屉（浮在上层），主列独占整宽。 */}
      <div className="fs-main" style={{ flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}>

      {/* ★ 2026-10-15 用户令（第五批）「除会话外全部塞进汉堡」：S 档（≤640）顶栏只剩
          「汉堡（左）＋ 品牌 logo 与品牌名（右邻）」——用户后令「汉堡旁边显示更大 logo 和品牌名」，
          故原来的「当前页名」位换成品牌簇（页名与页内标题重复的问题也随之消失）。
          语言 / 账号 / 通知全部收进抽屉。件本身收在 components/frontshell/FrontMobileTopBar.tsx。 */}
      {isMobile && <FrontMobileTopBar onMenu={shell.open} drawerOpen={shell.drawerOpen} />}
      {/* ★ 2026-10-15 用户令「元宝式布局」：桌面顶栏（.app-header）整体退役——品牌 / 三个工作台 Tab /
          租户身份 / 余额 / 上传口 / Bell / 语言 / 账号菜单全部迁入左侧栏（见上方 <aside>）。
          移动档（≤640）由上方标题栏接管（原底部 TabBar 已按用户令撤除）。 */}

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
          // ★ 2026-09-22：接口先回来也不立刻收场，至少完整演满一拍再进工作台
          //   （★ 2026-10-13 用户令：由「演满 3 拍」改为「强制一次」，之后完全跟着就绪信号走）
          // .loading-screen 在本仓没有任何 CSS 定义（Vue 版遗留名），排版全靠同行内联样式
          // 这里与 Root 的 restoreGate 是两个独立闸门实例：各自持有 played/need，也各自挂一份
          // WordSwap 从第一拍数起，只有各自真的见过 loading 才会补拍（不会互相借用对方的拍数）
          <div className="loading-screen" style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 20 }}>
            <WordSwap className="ws--lg" ariaLabel={t('app.starting')} onBeat={bootGate.onBeat} loop={bootGate.busy} />
            <p style={{ fontSize: 18, color: 'var(--lc-text-2)' }}>{t('app.starting')}</p>
          </div>
        ) : (
          <RouteBoundary>
            <Suspense fallback={<PageLoading />}>
              <Routes>
                <Route path="/" element={<ChatWindow />} />
                <Route path="/tickets" element={<TicketsPage />} />
                <Route path="/editor" element={<EditorPage />} />
                <Route path="/billing" element={<FrontPage><BillingCenter /></FrontPage>} />
                {/* 路由守卫：/invites 仅个人版可达（isPersonal 取自 meContext），企业用户命中即弹回工作台。
                    isPersonal 初值是 true，所以 meContext 尚未回来的那一帧放行——深链直达时可能先闪一下邀请页 */}
                <Route path="/invites" element={isPersonal ? <FrontPage><ReferralPanel /></FrontPage> : <Navigate to="/" replace />} />
                <Route path="/packages" element={<FrontPage><MyPackagePanel /></FrontPage>} />
                <Route path="/my" element={<FrontPage><AccountPanel /></FrontPage>} />
                {/* 公开定价页：营销门面（无需登录，Root 内另有未登录分支），挂在 FrontShell
                    下是为了让已登录用户也能从工作台直达 */}
                <Route path="/pricing" element={<PricingPage />} />
                {/* ★ 〇-X #55：公开比价与算价页（访客在 Root 的未登录分支直出，这里覆盖已登录入口） */}
                <Route path="/compare" element={<PriceComparePage />} />
                {/* ★ 〇-AM：对话历史记录页（工作台 SSE 翻译通道的持久化话记录查看） */}
                <Route path="/chat-history" element={<ChatHistory />} />
                {/* 前台兜底：壳内未匹配的路径一律 replace 回工作台，既不留空白页也不污染后退栈 */}
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </Suspense>
          </RouteBoundary>
        )}
      </div>

      {/* ★ 2026-10-16 用户令：页脚两档一致，都收进左栏尾部并折叠（FrontSideNav 的 .fs-foot-fold）。
          右主列列尾这一份已退役——桌面档再把页脚贴列底，等于同一页出现两份页脚。 */}

      </div>{/* /fs-main：元宝式右主列闭口 */}

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
    </RouteBoundary>
  )
}

// ---- 根路由 ----
// 这里不用 <Routes>：Root 是按「登录态 × 路径前缀」手工分诊的组件（营销页/登录页/后台壳
// 三棵互斥子树），路由表只存在于 FrontShell 内部。因此本组件每次渲染都重新读 location 判定，
// 各分支自带 Suspense，缺一条路由不会 404，只会落到下面的兜底分支。
function Root() {
  // onLogin 已随登录面收进 LoginFace（本组件不再直接消费它）
  const { user, restoring } = useAuth()
  // ★ 2026-10-23 租户定制页门面：`tenantId > 0` 的域名（客户品牌子域/演示站）对访客不展示
  //   平台营销落地页，打开 `/` 直接进登录——判据本体在 lib/siteFlags.ts（两条腿的说明也在那）。
  const branding = useBranding()
  const location = useLocation()
  const navigate = useNavigate()
  const path = location.pathname
  const t = gt // 别名而非 useT()：Root 不订阅语言变更，切语种后这里的文案等下一次重渲染才跟上

  // 会话恢复守卫：本地有 token 时 authMe 还没返回（restoring 初值 = !!token），
  // 这帧既不能当「已登录」出工作台，也不能当「未登录」闪出登录页，只出占位。
  // ★ 2026-09-22：占位由一行文字升级为全站同一份 WordSwap 换词动效，并至少完整演满一拍；
  // ★ 2026-10-13 用户令：补拍下限由 3 改 1（强制演示一次即止，authMe 早回来就立刻放行）。
  // 无 token 时 restoring 恒 false，不补拍。
  const restoreGate = useWordSwapGate(restoring)
  // ★ 2026-10-17 批 A：后台/长尾面板词条是**懒加载**的（源 ≈197 KB，见 i18n/appPanels.ts）——
  //   只看官网首页的未登录访客不该背它们。所以「要不要等」按登录态分派：
  //   · 未登录 ⇒ 下面三支门面（/pricing、/compare、/、否则登录页）只用门面档四份面板，**不等**；
  //   · 已登录 ⇒ 进工作台/后台，词条必须先到手，否则首屏会短暂露出键名或回退文案。
  //   与 restoreGate 同一套占位（WordSwap 换词动效），两闸门互不重叠：restoring 期间 user 已定。
  const needPanels = !!user && !appPanelsLoaded()
  useEffect(() => { if (needPanels) void ensureAppPanels() }, [needPanels])
  const panelsGate = useWordSwapGate(needPanels)
  if (restoreGate.busy) {
    // 闸门期间必须在所有分支判定之前直接 return：这帧 user 还没回来，
    // 往下走会被当成未登录渲染出登录页，会话恢复成功后再翻回工作台（闪一次登录页）
    return <PageLoading onBeat={restoreGate.onBeat} loop={restoreGate.busy} />
  }
  if (panelsGate.busy) {
    // 词条没到手就渲染后台，会先出一屏键名/英文回退再被 emit 刷掉（跨境链路上肉眼可见地闪）
    return <PageLoading onBeat={panelsGate.onBeat} loop={panelsGate.busy} />
  }
  // 未登录分诊：营销门面（/pricing、/）直出内容页，其余路径一律落到登录页
  if (!user) {
    // 定价页属营销门面：未登录访客同样可直接访问（否则会落到登录页）
    // 这里不带 FrontShell 外壳（无工作台 Tab / 余额徽标），页面自身即完整营销页；
    // 与 FrontShell 内的 /pricing 路由分别覆盖「访客」与「已登录」两种入口。
    if (path === '/pricing') {
      return (
        <PublicShell>
          <RouteBoundary>
            <Suspense fallback={<PageLoading />}>
              <PricingPage />
              {/* ★ autosales：定价页常驻 AI 接待挂件 */}
              <AiAssist />
            </Suspense>
          </RouteBoundary>
        </PublicShell>
      )
    }
    // ★ 〇-X #55：比价与算价页与 /pricing 同级营销门面，访客可直达（否则会被兜底送去登录页，
    //   而"注册前能不能看到价"正是这个页存在的全部意义）
    if (path === '/compare') {
      return (
        <PublicShell>
          <RouteBoundary>
            <Suspense fallback={<PageLoading />}>
              <PriceComparePage />
              <AiAssist />
            </Suspense>
          </RouteBoundary>
        </PublicShell>
      )
    }
    // ★ S5 门面：未登录访问 `/` 出官网落地页；登录/注册走 /login、/register
    if (path === '/') {
      // ★ 〇-Z（演示站不进主页）：后端在首页 HTML 里直出 window.__SITE_FLAGS__={"landing":false} 时，
      //   访客打开 `/` 直接重定向到登录注册页。用 <Navigate> 而非原地渲染 <Login>——
      //   地址栏跟着变成 /login（用户口径），replace 不压历史栈，否则后退会再次撞 `/` 形成来回跳。
      //   射程只有 `/`：/pricing、/compare 仍对访客可达（体验机也要让人看到价），
      //   已登录用户根本不进这个分支（上面 `!user` 已分流），所以这一档只影响未登录访客。
      //   ★ 2026-10-23 判据加第二条腿：租户定制页（tenantId>0）同样关闭——客户的站点不该
      //     给访客看平台的营销落地页。两条腿的完整口径见 lib/siteFlags.ts 的注释。
      if (!landingEnabledForGuest(branding.tenantId)) return <Navigate to="/login" replace />
      return (
        <PublicShell>
          <RouteBoundary>
            <Suspense fallback={<PageLoading />}>
              <Landing />
              {/* ★ autosales：落地页常驻 AI 接待挂件 */}
              <AiAssist />
            </Suspense>
          </RouteBoundary>
        </PublicShell>
      )
    }
    // 未登录兜底：非营销路径一律出登录页。/admin 前缀以 admin 态出——Login 内部会拦掉
    // 非租管账号，并关掉注册等前台专属流程；onLogin 再补一次角色校验，不够格立即弹回工作台。
    // ★ 2026-10-21 IAM P1-7：整块收进 LoginFace（PublicRoot 那棵树用同一个组件），
    //   两处不再各写一份 mode/onLogin/挂件——改登录面只有这一个落点。
    return <LoginFace path={path} fallback={<PageLoading />} />
  }
  // 后台路由守卫：/admin 只对租管及以上（roleLevelSafe>=2）开放，AdminDashboard 自身不再判权；
  // 不够格的登录用户走 else 分支——不渲染任何后台数据，只给一个返回首页的出口（Button 现为 LangCross 件）
  if (path.startsWith('/admin')) {
    return roleLevelSafe(user.role) >= 2 ? (
      <RouteBoundary>
        <Suspense fallback={<PageLoading />}>
          <AdminDashboard />
          {/* ★ autosales：管理后台常驻挂件 */}
          <AiAssist />
        </Suspense>
      </RouteBoundary>
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

// ★ 2026-10-17 批 A：一进 App 模块就把懒加载的后台面板词条拨出去（`Root` 只在已登录时等它）。
//   写在模块层而不是 main.tsx，是因为**这个 chunk 只在进后台/工作台时才会被下载**
//   （批 B：main.tsx 按「无 token + 公开路径」分诊，门面走 PublicRoot、根本不加载本文件）——
//   于是官网访客不会因为这一行而下那 ≈197 KB；写在模块层又比写在 effect 里早一整个渲染周期，
//   与 App 自己的 chunk 解析并行，跨境链路上省的就是这一整段时间。
//   幂等（ensureAppPanels 内部有 ready/inflight 两档），重复 import 不会重复下载。
void ensureAppPanels()

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
  // ★ F-11（批G）：余额刷新广播枢纽——App 根持有唯一一份，经 PkgRefreshCtx 同时下发给
  // ChatProvider（流终态后延迟广播）与各余额位（顶栏积分行、工作台余额条各自订阅）。
  // ★ F-48（批 I-5）：旧版是「单槽位可写句柄」，两处余额位互相注册会静默挤掉对方，
  // 现改为 Set 广播——多面板共存是正常态。
  // 用惰性初始化（同 ChatProvider 的 storeRef 手法）：Provider 的 value 引用必须终生稳定，
  // 否则每次重渲染都会让订阅 effect 重跑一遍，把集合抖成「退订再订阅」。
  const pkgHubRef = useRef<PkgRefreshHub | null>(null)
  if (pkgHubRef.current === null) pkgHubRef.current = createPkgRefreshHub()
  return (
    <ErrorBoundary>
      <BrowserRouter>
        {/* ★ 2026-10-17 批 B：NavCtx 灌在 BrowserRouter 之内、业务 Provider 之外——
            位置对它不重要（只提供 location/navigate），贴着 router 放可保证「取到的 location
            与 App 自己 useLocation 读到的是同一份」。 */}
        <RouterNavBridge>
          <AuthProvider>
            <PkgRefreshCtx.Provider value={pkgHubRef.current}>
              <ChatProvider>
                <AdminProvider>
                  <BrandingProvider>
                    <Root />
                  </BrandingProvider>
                </AdminProvider>
              </ChatProvider>
            </PkgRefreshCtx.Provider>
          </AuthProvider>
        </RouterNavBridge>
      </BrowserRouter>
    </ErrorBoundary>
  )
}
