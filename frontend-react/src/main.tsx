// ============================================================================
// main.tsx — 应用入口
// 职责：创建 React 18 根节点，引入全局样式，渲染 <App />。
//       并在最外层挂 LangCross <ToastProvider> + ToastBridge + DialogHost，
//       使全站提示/弹窗脱离 TDesign 的 MessagePlugin / Dialog 调用方式。
// ============================================================================

/**
 * main.tsx · 职责说明
 * 应用入口文件，负责：
 * - 创建 React 18 根节点
 * - 引入全局样式（项目主题 → LangCross 令牌/组件/动效 → 移动端）
 * - 挂载 LangCross 全局提示容器 ToastProvider，并挂上 ToastBridge / DialogHost
 * - 渲染 App 组件到 DOM 节点
 *
 * 样式引入次序（2026-09-17 换肤后确定的口径）：
 * 1) theme.css：只改「变量值」不改结构——把 --td-* / --npz-* / --adm-* 三套语义变量
 *    整体重定义到纯黑色值，从而一次覆盖全站约 130 处 tsx 内联样式；
 * 2) tokens.css：--lc-* 令牌的唯一来源（theme.css 里的动效类会引用它，CSS 变量取值
 *    与声明先后无关，故两者不存在顺序冲突）；
 * 3) components.css：.lc-* 组件结构样式；
 * 4) motion.css：.lc-mo-* 动效工具类——必须晚于 components.css，同权重下才能
 *    「只做叠加、不改外观」；
 * 5) mobile.css 最上层：窄屏断点覆写。
 *
 * ToastBridge / DialogHost 挂在 ToastProvider 内部：前者把 lib 层 toastBus 接回
 * useToast（供非组件环境 import { toastError } 使用），后者承接 confirmDialog 等
 * 命令式弹窗出口——两者都依赖 Provider 上下文，故不能放到 App 之外。
 */

import React from 'react'
import ReactDOM from 'react-dom/client'
// ★ 2026-10-17 批 B：App 与公开面入口**都改成懒加载**，按「有没有 token × 落在哪条路径」分诊
//   （见下方 pickEntry）：未登录访客走 PublicRoot（不带 react-router 的快车道），
//   其余一律走 App（下拉 react-router + 工作台/后台那一片静态依赖）。
//   ⚠️ 这里**不许**改回静态 `import App from './App'`：那一行就等于把 router 与后台面板
//   重新钉进入口包（体积回归只体现在产物里：tsc/vitest 全绿）。
//   机械锁＝src/lib/navShim.test.ts（A 分诊闭包 + B 公开面全量闭包里 router 导入者为 0）
//   ＋换源前 scripts/check_frontend_tar.sh 的 T8（入口 raw ≤420,000 B；现读数 339,296 B）。
const PublicRoot = React.lazy(() => import('./PublicRoot'))
const App = React.lazy(() => import('./App'))
// 启动/页面加载占位：与 index.html 首屏骨架同貌（同一份 CSS），让三段视觉连续；
// **不能给 null**——哨兵按 `#root` 有没有子节点判"界面起没起来"，空着会被误判成没加载起来
// （见 components/BootLoading.tsx 文件头）。
import BootLoading from './components/BootLoading'
// token 取值只读 api/core 的内存态（它在模块加载期已经 try 过 sessionStorage，
// 站点数据被拒时按未登录起步，同 AGENTS「入口包顶层零裸存储读」那条）。
import { getAuthToken } from './api/core'
// LangCross 纯黑组件库（@langcross/ui）
import { ToastProvider } from './ui/langcross/src'
// ToastBridge：把 lib/toastBus 的命令式 toast 转接到 ToastProvider（非组件环境也能提示）
import ToastBridge from './components/ToastBridge'
// DialogHost：confirmDialog / promptText 等命令式弹窗的挂载点（取代 TDesign Dialog 调用）
import { DialogHost } from './components/uiDialogs'
// ★ 2026-10-13 链路批④（#29）：懒加载分片取不到时的「自动补一次整页重载」恢复腿。
// 必须在 createRoot **之前**装：它监听的是 Vite 的 window 事件，与 React 挂载无关，
// 而 CSS 预载失败那一族根本不会走到渲染期（只有这个事件看得见）。
// 实现与三条口径见 src/lib/chunkRecovery.ts。
import { installChunkRecovery } from './lib/chunkRecovery'

// 项目主题覆盖样式（变量已重映射到 --lc-* 纯黑单色令牌）
import './styles/theme.css'
// LangCross 设计令牌（唯一来源，必须先于组件样式引入）
import './ui/langcross/css/tokens.css'
// LangCross 组件样式（.lc-* 前缀，与画布「00 · 设计令牌」一一对应）
import './ui/langcross/css/components.css'
// LangCross 动效层（.lc-mo-* 工具类 + 关键帧，对应「纯黑 UI 动效十原则」）
// 必须晚于 components.css：同权重下后者可见，动效层只做叠加不改外观
import './ui/langcross/css/motion.css'
// 移动端 / 窄屏自适应样式（叠加在最上层）
import './styles/mobile.css'
// ★ 2026-10-15 前台外壳布局方案样式（fs-* 一族唯一出处，配合 components/frontshell/）。
// 必须晚于 components.css：.lc-mob-topbar.fs-mob-topbar 这类同权重覆写靠源序决胜
// （本文件的选择器已加双类名兜底，此处顺序是第二道保险）。
import './styles/frontshell.css'
// ★ 2026-10-17 未登录门面移动壳样式（PublicShell：汉堡＋抽屉＋品牌顶栏）。
// 必须晚于 components.css：移动档收起各页自带头部、让位登录固定壳，靠源序决胜。
import './styles/publicshell.css'

// 挂载应用到 DOM 节点，并开启严格模式
// ToastProvider：@langcross/ui 全局提示容器，等价替换 TDesign 的 MessagePlugin
//
// ★ 2026-10-12 启动哨兵的「挂载之后」那一半（与 index.html 里那段内联脚本配成一对）：
//   index.html 的哨兵只在 #root 一直空着时才出话，能治「入口包评估中断／脚本没加载」；
//   但**挂载成功之后**再抛异常（某块面板渲染炸了）时 React 会把整棵树卸掉，
//   #root 已经非空 ⇒ 哨兵早就收摊了 ⇒ 用户看到的仍然是一片黑。
//   所以这一层用 ErrorBoundary 兜住：出话而不是出黑屏，且文案是人话、不外泄堆栈。
//   判据锁见 src/bootStorageGate.test.ts（哨兵在位 + 顶层零裸存储访问）。
class BootErrorBoundary extends React.Component<{ children: React.ReactNode }, { failed: boolean }> {
  constructor(props: { children: React.ReactNode }) {
    super(props)
    this.state = { failed: false }
  }
  static getDerivedStateFromError() {
    return { failed: true }
  }
  componentDidCatch(err: unknown) {
    // 只进控制台供排障，屏幕上一个技术字眼都不给访客
    console.error('[boot] 界面渲染中断', err)
  }
  render() {
    if (!this.state.failed) return this.props.children
    const box: React.CSSProperties = {
      position: 'fixed',
      top: 0,
      left: 0,
      right: 0,
      bottom: 0,
      background: '#0B0C0E',
      color: '#E7E9EA',
      padding: 32,
      font: '15px/1.7 system-ui,-apple-system,"Segoe UI",sans-serif',
      overflow: 'auto',
      zIndex: 99999,
    }
    const btn: React.CSSProperties = {
      marginTop: 8,
      padding: '10px 20px',
      font: '15px/1 system-ui,-apple-system,"Segoe UI",sans-serif',
      background: '#FFFFFF',
      color: '#0B0C0E',
      borderRadius: 8,
      cursor: 'pointer',
    }
    return (
      <div style={box} role="alert">
        <p style={{ margin: '0 0 10px', fontWeight: 600 }}>页面出错了</p>
        <p style={{ margin: '0 0 10px' }}>
          界面在显示的过程中被打断了。重新加载通常就能恢复；如果每次都停在这一页，请把浏览器控制台里的第一行红字发给技术支持。
        </p>
        <button style={btn} onClick={() => location.reload()}>
          重新加载
        </button>
      </div>
    )
  }
}

// 先把分片恢复腿装上（一次性 window 监听，见 lib/chunkRecovery 的三条口径注释），
// 再挂载应用：这样第一屏的懒加载解析失败也在这条腿的射程里。
installChunkRecovery()

// ============================================================================
// 入口分诊（★ 2026-10-17 批 B）
//   · 无 token 且落在公开路径 ⇒ PublicRoot：不加载 react-router，也不加载工作台/后台静态依赖
//     （官网访客此前要为整棵树付 367 KB router + 后台面板词条，本批拆开）。
//   · 其余一律 App：有 token 的用户、以及**未登录但落在 `/admin` 等非公开路径**的访客。
//     ⚠️ 后者不能走 PublicRoot：`/admin` 未登录时 App 出 `Login mode="admin"`（管理员登录形态），
//     与 PublicRoot 兜底的 `mode="home"` 不是同一屏（见 PublicRoot.tsx 文件头口径①）。
//   白名单**故意只收这五条**，与 App.tsx 的门面分支一一对应；新公开页要同时加进两处
//   （App.tsx 的 !user 分诊 + 这里），否则那一页会退回"要下 router 才打不开"的老形态。
//   `getAuthToken()` 读的是 api/core 的内存态（非现场读存储），见上方 import 处的注释。
// ============================================================================
const PUBLIC_PATHS = new Set(['/', '/pricing', '/compare', '/login', '/register'])
function pickEntry(): typeof App {
  return !getAuthToken() && PUBLIC_PATHS.has(window.location.pathname) ? PublicRoot : App
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BootErrorBoundary>
      <ToastProvider>
        <ToastBridge />
        <DialogHost />
        {/* Suspense 只等**入口分片**（PublicRoot 或 App）：它的 fallback 与 index.html 的
            首屏骨架同貌，且必须非空——见 BootLoading 文件头（哨兵判据）。 */}
        <React.Suspense fallback={<BootLoading />}>
          {React.createElement(pickEntry())}
        </React.Suspense>
      </ToastProvider>
    </BootErrorBoundary>
  </React.StrictMode>,
)
