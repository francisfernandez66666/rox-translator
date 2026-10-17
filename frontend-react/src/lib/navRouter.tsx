// ============ lib/navRouter.tsx · 职责说明 ============
// `lib/nav.ts` 的**真路由桥接**：把 react-router 的 location/navigate 灌进 NavCtx，
// 让公开面那几个组件（AiAssist／定价／比价／移动壳）在后台树里拿到的是 SPA 跳转而不是整页跳转。
//
// ★ 2026-10-17 批 B：本文件是**唯一允许 import react-router** 的 nav 侧文件，
//   且只许被 `App.tsx` 引（公开面走 `PublicRoot`，那条路不加载本文件 ⇒ 也不加载 react-router）。
//   判定"谁引了它"是机械的：`src/lib/navShim.test.ts` 扫 src 下全部源文件，只许 App.tsx 出现本路径。
//   反向：`lib/nav.ts` 里出现 react-router 一行都不许（那等于把 367 KB 的 router 又塞回公开面）。
//
// 三个口径：
//  ① `api` 必须**按路由变化重建**（useMemo 依赖 pathname/search）：AiAssist 用
//     `pathname + search` 做"路径变了要重取开场白"的判据，值变了而引用没变＝那条 effect 不重跑。
//     `navigate` 来自 useNavigate（react-router 自己保证稳定），进依赖即可。
//  ② `replace` 透传：定价/比价页的 CTA 用 `NavApi.navigate`，而 App 里 `<Navigate replace>` 那种
//     "不压历史栈"的语义必须一致，否则后退会撞回跳转前的页形成来回跳（见 App.tsx 里 / 的门面开关注释）。
//  ③ **它不渲染 DOM**（只包一层 Context.Provider）：位置在 `BrowserRouter` 之内、业务 Provider 之外，
//     对既有组件树零结构影响（`App.tsx` 那一串 Provider 的依赖方向说明仍成立）。
import { useMemo, type ReactNode } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { NavCtx, type NavApi } from './nav'

/** RouterNavBridge：把 react-router 接到 NavCtx 上（仅在 App 树里使用） */
export function RouterNavBridge({ children }: { children: ReactNode }) {
  const location = useLocation()
  const navigate = useNavigate()
  const api: NavApi = useMemo(() => ({
    pathname: location.pathname,
    search: location.search,
    navigate: (to, opts) => navigate(to, opts?.replace ? { replace: true } : undefined),
  }), [location.pathname, location.search, navigate])
  return <NavCtx.Provider value={api}>{children}</NavCtx.Provider>
}
