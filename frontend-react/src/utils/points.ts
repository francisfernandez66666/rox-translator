// ============================================================================
// utils/points.ts — S1 积分制统一换算器（2026-09-14）
// 对外展示口径：1 积分 = points_tokens_rate 个内部计量 token。
// 汇率由 /api/auth/me 下发（会话恢复时注入），默认 300；SPA 内所有
// 「token 数量」展示一律经本模块换算成积分，token 原值不再外露。
// ============================================================================

let rate = 300

/** 会话恢复时由 auth store 注入后端下发的积分汇率（非法值忽略） */
export function setPointsRate(n?: number | null): void {
  if (typeof n === 'number' && n > 0) rate = n
}

/** 当前积分汇率（1 积分 = N token） */
export function pointsRate(): number {
  return rate
}

/** 内部 token 数 → 积分数（四舍五入，最小非零消耗显示 1 积分） */
export function pointsOf(tokens: number): number {
  const p = Math.round((tokens || 0) / rate)
  return p === 0 && (tokens || 0) > 0 ? 1 : p
}

/** 积分数 → 内部 token 数（超管表单以积分录入时反算落库值） */
export function pointsToTokens(points: number): number {
  return Math.max(0, Math.round((points || 0) * rate))
}

/** token 数 → 千分位积分字符串 */
export function fmtPoints(tokens: number): string {
  return pointsOf(tokens).toLocaleString('en-US')
}
