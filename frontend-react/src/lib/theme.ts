// ============================================================================
// lib/theme.ts — 明暗主题（★ F5）
// 三态：light / dark / auto（auto 跟随系统 prefers-color-scheme）。
// 偏好持久化 localStorage('app_theme')；双轨生效：
//   1) html[data-theme] + color-scheme —— 驱动自绘 CSS（styles/theme.css 的 dark 覆写块）
//   2) TDesign 组件走 --td-* CSS 变量覆写（同样在 dark 块中收敛）
// ============================================================================

export type Theme = 'light' | 'dark' | 'auto'

const KEY = 'app_theme'

/** 读取已持久化的主题偏好（默认 auto 跟随系统） */
export function getTheme(): Theme {
  const v = localStorage.getItem(KEY)
  return v === 'light' || v === 'dark' || v === 'auto' ? v : 'auto'
}

/** 计算偏好对应的实际明暗（auto 时查询系统） */
export function isDark(pref: Theme = getTheme()): boolean {
  if (pref === 'dark') return true
  if (pref === 'light') return false
  return !!window.matchMedia?.('(prefers-color-scheme: dark)')?.matches
}

/** 应用主题到 documentElement（data-theme + color-scheme） */
export function applyTheme(pref: Theme = getTheme()): void {
  const dark = isDark(pref)
  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light')
  document.documentElement.style.colorScheme = dark ? 'dark' : 'light'
}

/** 循环切换：auto → light → dark → auto，并持久化+应用 */
export function cycleTheme(): Theme {
  const order: Theme[] = ['auto', 'light', 'dark']
  const next = order[(order.indexOf(getTheme()) + 1) % order.length]
  try { localStorage.setItem(KEY, next) } catch { /* ignore */ }
  applyTheme(next)
  return next
}

// auto 模式下跟随系统实时切换（matchMedia 监听，注册一次即可）
let wired = false
// 监听系统深浅色主题变化（全局仅接线一次）
export function watchSystemTheme(): void {
  if (wired) return
  wired = true
  try {
    window.matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
      if (getTheme() === 'auto') applyTheme('auto')
    })
  } catch { /* 老浏览器忽略 */ }
}
