// ============================================================================
// lib/instantUpsell.ts — 即时翻译「升级卡」的三分钟节流（★ 2026-10-30 D-5 / G24）
//
// 为什么是模块级内存量而不是 useState/localStorage：
//   · W-5 要的是「同一会话三分钟内只出一次」，而「会话」跨路由存在（用户从工作台去工单页再回来）；
//     放在组件 state 里，一次跳转就等于重新开始计时，节流形同虚设；
//   · G24 的第二条腿要求**刷新页面即重置**（dismissed 不落库）——写 localStorage 会让
//     「关掉一次就永远不再提示」，那是另一头的事故。
//   ⇒ 模块级变量正好同时满足两条：进程内（含跨路由）记得住，整页刷新即归零。
//
// ⚠️ 只有「升级卡」受这条节流约束；墙本身的引导卡（工单/按原图付费）每次都出——
//   那是每一档墙的**唯一出路**，藏起来等于把人留在死胡同里（W-1）。
// ============================================================================

/** 升级卡最小间隔：3 分钟（用户口径 W-5） */
export const INSTANT_UPGRADE_THROTTLE_MS = 3 * 60 * 1000

/** 上一次升级卡出现的时刻（0＝本会话还没出过；刷新页面即归零） */
let lastShownAt = 0

/** shouldShowInstantUpgrade 现在允许出升级卡吗（距上次 ≥3 分钟） */
export function shouldShowInstantUpgrade(now: number = Date.now()): boolean {
  return now - lastShownAt >= INSTANT_UPGRADE_THROTTLE_MS
}

/** markInstantUpgradeShown 记下这一次出现的时刻（调用点必须在**真的渲染出来**之后） */
export function markInstantUpgradeShown(now: number = Date.now()): void {
  lastShownAt = now
}

/** resetInstantUpgradeThrottle 归零（测试专用；生产没有调用点，刷新页面自然归零） */
export function resetInstantUpgradeThrottle(): void {
  lastShownAt = 0
}
