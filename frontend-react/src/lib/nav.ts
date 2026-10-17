// ============ lib/nav.ts · 职责说明 ============
// 「我在哪一页、怎么跳页」的最小接口 —— ★ 2026-10-17 首屏轻量化批 B。
//
// 为什么要有它：公开面（官网 `/`、定价 `/pricing`、比价 `/compare`、登录注册）此前与后台共用
// 同一棵树（`App.tsx` → `BrowserRouter`），于是**只想看首页的未登录访客**也要下 react-router
// 那 367 KB 源码（实测 `react-router@7.18.3` 被打进来的是 development build，换 dist/production
// 别名零收益：`package.json` 的 exports 里 production 出现 0 次，两个 chunk 尺寸几乎相同
// 375,382 vs 375,383）。批 B 把公开面拆成不带 router 的 `PublicRoot`，但公开面那几个组件
// （AiAssist 的动作跳转、定价/比价的按钮、移动壳的"路由变了收抽屉"）确实要知道路径与跳页
// ⇒ 这里的 context 就是那个"最小接口"：**带 router 时由桥接实现，不带 router 时回落 window 直跳**。
//
// 三条口径（改动前必读）：
//  ① **本文件禁止 import react-router**（这正是它存在于 `lib/` 而不是组件里的原因）：
//     谁在这一行加了 router，公开面的 chunk 就会把它重新拉回来，体积回归**只体现在产物里**
//     （源码、tsc、vitest 全绿）。真路由桥接在 `lib/navRouter.tsx`，只许 `App.tsx` 引。
//  ② **回落实现必须真能跳**：`windowNav()` 用 `location.assign/replace`（整页跳转）。
//     公开面本来就是"整页换页"的形态（未登录访客点"定价"→ `/pricing` 走一次文档导航，
//     与之前 SPA 内跳转的用户可见差异只有"多一次整页加载"，而这一档访客本来就在首访白屏期）。
//     ⚠️ 别为了"顺滑"在这里手搓 history.pushState：那会让 URL 变了而 React 树没换页（假跳转）。
//  ③ **取值每次现读**（`windowNav()` 不缓存 pathname）：调用点把它用在 effect 依赖里
//     （"路径变了收抽屉/重取开场白"），缓存会把那条依赖钉死成常量。
// 锁＝`src/lib/navShim.test.ts`（源文件级：本文件零 react-router import＋桥接文件只在 App 引；
//   dom 级：无 Provider 时 useNav() 的 pathname 等于 window.location.pathname）。
import { createContext, useContext } from 'react'

/** NavApi：公开面与后台共用的最小导航接口（pathname/search + 跳页） */
export type NavApi = {
  /** 当前路径（不含 query），等价 react-router 的 location.pathname */
  pathname: string
  /** 当前 query（含前导 `?`），等价 location.search */
  search: string
  /** 跳页；replace=true 时不压历史栈（等价 navigate(to, { replace: true })） */
  navigate: (to: string, opts?: { replace?: boolean }) => void
}

/** 不带 router 时的回落实现：直接让浏览器换页（见文件头口径②）。
 *  ⚠️ 对象**稳定**、值用 getter 现读——调用点会把 `navigate` 放进 useCallback/effect 依赖里
 *  （如 AiAssist 的 doAction），每次渲染造一个新函数会让那些 effect 每帧重跑（同"快照必须缓存"那条的老坑）。 */
export const windowNavApi: NavApi = {
  get pathname() { return window.location.pathname },
  get search() { return window.location.search },
  navigate: (to, opts) => {
    if (opts?.replace) window.location.replace(to)
    else window.location.assign(to)
  },
}

/** NavCtx：由 `lib/navRouter.tsx` 的桥接组件在后台树里填上真 router；公开面恒为 null */
export const NavCtx = createContext<NavApi | null>(null)

/** useNav：取当前导航接口（无 router 时恒回同一个稳定对象，见 windowNavApi） */
export function useNav(): NavApi {
  return useContext(NavCtx) ?? windowNavApi
}
