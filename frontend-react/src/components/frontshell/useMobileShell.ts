// ============================================================================
// components/frontshell/useMobileShell.ts — 前台外壳的「移动档」状态机（唯一实现）
// ----------------------------------------------------------------------------
// 前台外壳只有两档：桌面（>640，左栏常驻）与移动（≤640，左栏化汉堡抽屉）。
// 本 hook 收口这一族状态，避免每个用到「是不是移动档」「抽屉开没开」的组件
// 各写一份 matchMedia 监听与收起逻辑（历史上那类「两处各判一次档」最后必然打架）：
//
//   ① isMobile：断点只有 ≤640 一档（与 components.css / mobile.css 的 S 档同口径）。
//      用 matchMedia（不是 window.innerWidth 快照）：横竖屏旋转、桌面浏览器拖窗口、
//      移动端地址栏收展都要实时跟随，而不是只判挂载那一刻。
//   ② 抽屉开合 drawerOpen；路由一变自动收起——导航项点完不用各自记着关，
//      也顺带兜住浏览器前进/后退（用户点返回时抽屉还赖在屏上是最常见的体感 bug）。
//   ③ 抽屉打开期间锁底层滚动：移动端抽屉是浮层，不锁的话手指一滑会把下面的
//      会话区一起带着跑（穿透滚动）。
// ============================================================================

import { useCallback, useEffect, useState } from 'react'
import { useLocation } from 'react-router-dom'

/** 移动档断点（唯一口径；改这里＝改全壳档位，别在组件里再写第二份） */
export const MOBILE_SHELL_QUERY = '(max-width: 640px)'

/** useMobileShell 出参：档位 + 抽屉开合 + 三个动作 */
export interface MobileShell {
  /** 是否移动档（≤640）：true 时左栏化抽屉、顶栏出汉堡 */
  isMobile: boolean
  /** 抽屉是否展开（仅移动档有意义；桌面档恒 false 且不渲染抽屉壳） */
  drawerOpen: boolean
  /** 打开抽屉 */
  open: () => void
  /** 收起抽屉 */
  close: () => void
  /** 开合互切（汉堡钮的 aria-expanded 与点击可共用一份状态） */
  toggle: () => void
}

/**
 * 前台外壳移动档状态机。用法：
 *   const shell = useMobileShell()
 *   <FrontSideNav isMobile={shell.isMobile} open={shell.drawerOpen} onClose={shell.close} … />
 *   {shell.isMobile && <FrontMobileTopBar onMenu={shell.open} drawerOpen={shell.drawerOpen} />}
 */
export function useMobileShell(): MobileShell {
  const location = useLocation()
  // 惰性初始化读一次当前值，随后交给 matchMedia 的 change 事件跟随
  const [isMobile, setIsMobile] = useState(() => window.matchMedia(MOBILE_SHELL_QUERY).matches)
  useEffect(() => {
    const mq = window.matchMedia(MOBILE_SHELL_QUERY)
    const onChange = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])

  const [drawerOpen, setDrawerOpen] = useState(false)
  // 路由变化即收起抽屉（含浏览器前进/后退）
  useEffect(() => { setDrawerOpen(false) }, [location.pathname])
  // 抽屉打开时禁掉底层滚动（移动端穿透滚动会把会话区带着跑）；卸载/关闭时逐字还原
  useEffect(() => {
    if (!isMobile || !drawerOpen) return
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prev }
  }, [isMobile, drawerOpen])

  const open = useCallback(() => setDrawerOpen(true), [])
  const close = useCallback(() => setDrawerOpen(false), [])
  const toggle = useCallback(() => setDrawerOpen((v) => !v), [])

  return { isMobile, drawerOpen, open, close, toggle }
}
