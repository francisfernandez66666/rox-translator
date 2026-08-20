// ============================================================================
// components/admin/ui.ts — 后台共享 UI 工具
// 职责：各面板通用的格式化/渲染辅助函数（时间、状态、JSON、图表条高等）
// ============================================================================

// 时间格式化：ISO → "YYYY-MM-DD HH:MM:SS"，空值返回占位符
export function fmtTime(s?: string) { return s ? s.replace('T', ' ').slice(0, 19) : '—' }

// 租户状态中文标签
export function statusLabel(s: string) { return s === 'active' ? '启用' : s === 'disabled' ? '停用' : '已过期' }

// JSON 美化打印（解析失败则原样返回）
export function prettyJSON(s?: string) {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

// 压缩 JSON 摘要（去掉引号与冒号，便于表格内阅读）
export function shortJSON(s: string) {
  try { return JSON.stringify(JSON.parse(s)) } catch { return s }
}

// 条形高度/宽度：取 8% 下限保证可见，最高 100%
export function barHeight(val: number, max: number) {
  if (!val || !max) return '8%'
  const pct = Math.max(8, Math.round((val / max) * 100))
  return pct + '%'
}