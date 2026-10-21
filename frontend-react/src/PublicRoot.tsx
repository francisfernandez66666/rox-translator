// ============ PublicRoot.tsx · 职责说明 ============
// 未登录公开面的**独立入口树** —— ★ 2026-10-17 首屏轻量化批 B。
//
// 背景（一句话）：官网访客此前和后台共用 `App.tsx` 那棵树，于是"只想看一眼首页"也要下
// react-router（367 KB 源码，且打进的是 development build：实测换 dist/production 别名
// 尺寸几乎不变 375,382 vs 375,383）+ 工作台/后台的全部静态依赖。本文件是那条快车道：
// **不挂 react-router、不挂 ChatProvider/AdminProvider**，只有公开面真正需要的三样东西
// （登录态 AuthProvider、白标 BrandingProvider、导航最小接口 NavCtx 的 window 回落）。
//
// 分诊在 `main.tsx`：无 token 且落在公开路径（`/`、`/pricing`、`/compare`、`/login`、`/register`）
// ⇒ 本文件；其余（含 `/admin` 未登录）一律走 `App.tsx` —— **不要**把分诊改成"无 token 就全走这里"：
// `/admin` 未登录时 App 出的是 `Login mode="admin"`（管理员登录形态），与本文件的
// `Login mode="home"` 不是同一屏。
//
// 三条硬口径（改动前必读）：
//  ① **与 `App.tsx` 的 `Root()` 未登录三支逐支等值**：同一个地址在两棵树里必须出同一屏。
//     最容易漂的两处是 ①`/` 的门面开关注释里的行为（关掉时地址栏要换成 `/login` 且**不压历史栈**）
//     与 ②`PublicShell` 的 `auth` 档（登录/注册页不挂顶栏登录钮，见 AGENTS「移动档语言钮+登录钮」那条）。
//     改动任何一支时**两处一起改**，否则同一站点会出现"刷新前一套、刷新后另一套"。
//  ② **本文件禁止 import react-router**（一条都不许）：它整条存在的意义就是让公开面无 router。
//     需要路径/跳页走 `useNav()`（`lib/nav.ts`），那里在无 Provider 时回落 `window.location`
//     ——公开面本来就是"整页换页"的形态（见 lib/nav.ts 文件头口径②）。
//  ③ **禁止 import `App.tsx`／`stores/admin`／`hooks/useChat`／`stores/frontshell` 一族**：
//     它们会把 Router/后台面板拉回入口包（体积回归**只体现在产物里**：源码、tsc、vitest 全绿）。
//     机械锁＝`src/lib/navShim.test.ts`（A 入口分诊 + B router 不许泄漏 + C 桥接只许 App 引
//     + D 本文件与 App 的未登录三支等值／白名单同步，各配反证）。
import { lazy, Suspense, useEffect, type ReactNode } from 'react'
import { AuthProvider, useAuth } from '@/stores/auth'
import { BrandingProvider } from './branding'
import { landingEnabledForGuest } from '@/lib/siteFlags'
import { PublicShell } from './components/publicshell'
import RouteBoundary from './components/RouteBoundary'
import BootLoading from './components/BootLoading'
// ★ 2026-10-21 IAM P1-7：登录面与 App 树共用同一个调用点（不再各写一份 mode/onLogin/挂件）
import { LoginFace } from './components/LoginFace'
import { useNav } from '@/lib/nav'

// 与 App.tsx 同为 lazy：这四份都是"按路径才需要"的页面（首页访客不必下定价/比价页）
// 官网首页（未登录门面主力页）
const Landing = lazy(() => import('./components/Landing'))
// 定价页（未登录可达，与首页共用同一套营销壳）
const PricingPage = lazy(() => import('./components/PricingPage'))
// 比价页（未登录可达；三页与首页是同一批按路径才需要的分片）
const PriceComparePage = lazy(() => import('./components/PriceComparePage'))
// AI 顾问（官网入口点进来的独立页，同样按路径懒加载）
const AiAssist = lazy(() => import('./components/AiAssist'))

/** 营销门面外壳：未登录可达的三页共用（价格/比价/官网）——不带 auth 档顶栏，页内即完整营销页 */
function MarketingShell({ children }: { children: ReactNode }) {
  return (
    <PublicShell>
      <RouteBoundary>
        <Suspense fallback={<BootLoading />}>{children}</Suspense>
      </RouteBoundary>
    </PublicShell>
  )
}

// ★ 2026-10-21 IAM P1-7：原先这里的 `LoginScreen` 与 App.tsx 的未登录兜底各写一份，
//   现在统一走 `components/LoginFace`（`PublicShell auth` ＋ Login ＋ 挂件 ＋ applyLogin 后处理
//   都在那一处；admin 档下非租管账号弹回首页的第二道判定也在 `applyLogin` 里）。
//   占位档仍由各树自选：公开面树用 BootLoading（与首屏骨架同貌），App 树用 PageLoading。

/**
 * 门面关闭（体验机/演示站：后端在首页 HTML 里直出 `window.__SITE_FLAGS__={"landing":false}`）时
 * 的等价物：App.tsx 那里是 `<Navigate to="/login" replace />`（SPA 内换地址、不压历史栈），
 * 这里没有 router，于是 ①地址栏走 `navigate(..., {replace:true})`（window 回落＝`location.replace`）
 * ②**同时直接渲染登录屏**，不然会先空一帧再整页重载（那一帧正是纯黑，本批要治的东西）。
 * 射程只有 `/`：/pricing、/compare 仍对访客可达（体验机也要让人看到价）——与 App.tsx 注释一致。
 */
// LoginScreen 公开面树内登录页的唯一写法：两个调用点（门面关闭重定向、兜底）共用同一份
// path/fallback 口径，避免下一批只改一处。⚠️ 本组件内部仍只渲染一次 <LoginFace>——
// navShim 的「每棵树恰一处 <LoginFace>」判据靠这条成立，别再展开回来。
function LoginScreen({ path }: { path: string }) {
  return <LoginFace path={path} fallback={<BootLoading />} />
}

/** GuestLoginRedirect 门面关闭时把地址栏 replace 到 /login 并同步渲染登录屏，避免空白帧。 */
function GuestLoginRedirect() {
  const { navigate } = useNav()
  useEffect(() => { navigate('/login', { replace: true }) }, [navigate])
  // 地址栏此刻正在换成 /login；登录面按 home 档出（path 显式给 /login，不依赖尚未刷新的 pathname）
  return <LoginScreen path="/login" />
}

/** 公开面分诊（与 App.tsx `Root()` 的 `!user` 三支等值，见文件头口径①） */
function PublicRoutes() {
  const { pathname } = useNav()
  const path = pathname

  // 定价页属营销门面：未登录访客同样可直接访问（否则会落到登录页）
  if (path === '/pricing') {
    return (
      <MarketingShell>
        <PricingPage />
        {/* ★ autosales：定价页常驻 AI 接待挂件 */}
        <AiAssist />
      </MarketingShell>
    )
  }
  // ★ 〇-X #55：比价与算价页与 /pricing 同级营销门面，访客可直达
  if (path === '/compare') {
    return (
      <MarketingShell>
        <PriceComparePage />
        <AiAssist />
      </MarketingShell>
    )
  }
  // ★ S5 门面：未登录访问 `/` 出官网落地页；登录/注册走 /login、/register
  if (path === '/') {
    if (!landingEnabledForGuest()) return <GuestLoginRedirect />
    return (
      <MarketingShell>
        <Landing />
        {/* ★ autosales：落地页常驻 AI 接待挂件 */}
        <AiAssist />
      </MarketingShell>
    )
  }
  // 未登录兜底：非营销路径一律出登录页（公开面白名单里只会来 /login、/register，
  // 但保留与 App.tsx 同样的 `startsWith('/admin')` 判定——两处逐字相同，见文件头口径①）
  return <LoginScreen path={path} />
}

/**
 * 登录成功后**换树**：公开面树里没有工作台/后台，而 `Login` 自身不感知登录态
 * （它只把 user 回调上来，见 components/Login.tsx），所以不换树的话访客会对着登录页一直站着。
 * 与 App 树的等价性核对：App 里 `onLogin` 让 `Root()` 重渲染成 `FrontShell`，而 FrontShell 的
 * `path="*"` 兜底把 `/login` 重定向到 `/`（`<Navigate to="/" replace />`）⇒ 落到 `/` + 聊天工作台；
 * 这里用一次整页导航到 `/`（`useNav` 在无 router 时＝`location.replace`，同样不压历史栈）——
 * 新文档会走 main.tsx 的分诊（此时 token 已在 sessionStorage 里）⇒ 进 App 树、落到同一屏。
 * ⚠️ 用 `navigate`/`replace` 而不是 `location.reload()`：后者会把地址留在 `/login`，
 * 与 App 树那一屏的地址（`/`）不一致，也与文件头口径①「同一地址两棵树同一屏」相抵。
 */
function HandoffToApp() {
  const { user } = useAuth()
  const { navigate } = useNav()
  useEffect(() => { if (user) navigate('/', { replace: true }) }, [user, navigate])
  return null
}

/**
 * PublicRoot：公开面入口树。
 * Provider 只有两个，且都对 router 无感：
 *  · AuthProvider——Landing/定价/比价/登录都要 `useAuth()`（身份位、CTA 跳转、onLogin）；
 *  · BrandingProvider——白标按访问域名解析（可被 index.html 注入的 `window.__BRANDING__` 抢先）。
 * 最外层不套 ErrorBoundary：`main.tsx` 的 `BootErrorBoundary` 已经在最外侧（同 App.tsx 的处境）。
 */
export default function PublicRoot() {
  return (
    <AuthProvider>
      <BrandingProvider>
        <HandoffToApp />
        <PublicRoutes />
      </BrandingProvider>
    </AuthProvider>
  )
}
