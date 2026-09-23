// ============================================================================
// lib/theme.ts — 主题（★ 〇-N：全站只有暗色一档）
// 历史形态：light / dark / auto 三态 + localStorage('app_theme') 持久化 +
//   matchMedia 跟随系统偏好 + 设置页循环切换按钮。
// 为什么删：整站只交付了一套暗色真值（#000 面 / #E7E9EA 字，见 tokens.css 与
//   UI-ANNOTATIONS §1.1），**亮色档从来没有对应配色**，而默认 auto 跟随系统 ⇒
//   OS/浏览器偏浅色的用户整站落到 light 档，文字色又只写在
//   `html[data-theme='dark']` 覆写层里，于是浏览器回退成默认**黑字** ——
//   纯黑 UI + 黑字，语种面板未选项与后台统计卡标签全部看不见（用户 2026-09-23 截图）。
// 结论（用户明令「干掉 light/auto 档」）：删三态与切换入口，data-theme 恒为 dark，
//   并清掉老用户本地遗留的 app_theme，避免历史偏好继续左右渲染。
// ============================================================================

/** 已废弃的历史偏好键：只做清理，不再读写 */
const LEGACY_KEY = 'app_theme'

/** 把恒暗主题落到 <html>：data-theme + color-scheme（后者管原生控件/滚动条） */
export function applyTheme(): void {
  document.documentElement.setAttribute('data-theme', 'dark')
  document.documentElement.style.colorScheme = 'dark'
  // 清掉三态时代写入的偏好；隐私模式下 removeItem 也可能抛，忽略即可
  try { localStorage.removeItem(LEGACY_KEY) } catch { /* ignore */ }
}
