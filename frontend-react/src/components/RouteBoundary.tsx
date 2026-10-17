// ============ components/RouteBoundary.tsx · 职责说明 ============
// 页面级错误边界：懒加载的页面**取不到分片**时只让这一屏出话，外壳照常可用。
//
// ★ 2026-10-13 链路批④（#29）建立，★ 2026-10-17 批 B 从 App.tsx 搬到这里，原因：
//   批 B 把公开面拆成不带 react-router 的 `PublicRoot`，而公开面的页面同样是懒加载的，
//   它们**也需要**这一层边界（原定义在 App.tsx 里用 `useLocation()` 取 resetKey，
//   公开面没有 router ⇒ 一引就把 App 与 react-router 整个拉回来）。
//   于是边界改成读 `lib/nav.ts` 的最小接口（带 router 时是真 location，不带时是 window.location），
//   两棵树共用同一份实现——**不许**各写一层（两份边界的失效形态是"某一边忘了复位"，
//   而路由级复位的判据只有一条：resetKey 跟着当前路径变）。
//
// 现网症状（这条边界存在的理由）：发版换源后浏览器里那一页还留着上一代的哈希 chunk 名，
// 切工单＝第一次去拉那个已经不存在的文件，spa.go 对不存在路径回退成 index.html（200 但内容是 HTML），
// 于是评估模块当场抛 "Importing a module script failed."，不包这一层就会一路撞到最外层全局边界
// （顶栏、导航、页脚全没，只剩「返回首页」）。
// resetKey 吃 pathname：用户切到别的页面即自动复位，异常态不许跟着历史栈粘住。
import type { ReactNode } from 'react'
import ErrorBoundary from './ErrorBoundary'
import { useNav } from '@/lib/nav'

/** RouteBoundary：把「当前页面路径」交给 ErrorBoundary 做复位键（见文件头） */
export default function RouteBoundary({ children }: { children: ReactNode }) {
  const { pathname } = useNav()
  return <ErrorBoundary resetKey={pathname}>{children}</ErrorBoundary>
}
