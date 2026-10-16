// ============================================================================
// components/frontshell/FrontPage.tsx — 路由页内容框（自助页统一外垫与限宽）
// ----------------------------------------------------------------------------
// ★ 2026-10-16 本批「侧栏 tab 页布局重做」：左栏各 tab（/packages /billing /my /invites）
//   此前各写各的外垫——有的卡片直接顶到左栏与视口右缘，MyBilling 又内联了一份
//   `maxWidth:900; margin:0 auto; padding:16`。同一套卡片在四个页面上左边界各不相同，
//   窄屏外垫也不一致。
//
// 现在**只有这一个框**（类名 .fs-page，唯一样式出处 src/styles/frontshell.css）：
//   · 限宽居中（1080）＋ 桌面 20/24/32 外垫，窄屏按 mobile.css 的 900/640 两档收垫；
//   · 纵向 flex + gap 16，页面内多张卡片/区块的间距不再各写 margin。
//
// ⚠ 只做「框」：卡片样式仍归 .ssc-card（selfservice.tsx）/ .mb-card（MyBilling.tsx）。
// ⚠ 页面自己**不要**再套一层带 padding/maxWidth 的容器（会双外垫）——想改框只改这里。
// ============================================================================

import type { ReactNode } from 'react'

// FrontPage：自助页/账单页共用的内容框。children 为页面根节点（通常是 .ss-grid）。
export default function FrontPage({ children }: { children: ReactNode }) {
  return <div className="fs-page">{children}</div>
}
