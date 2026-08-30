// ============================================================================
// lib/ui.ts — 展示工具（自 Vue 版 components/admin/ui.ts 移植，行为一致）
// ============================================================================
// 依赖引入：i18n 翻译函数 t
import { t } from '@/i18n'

// ============ 本文件职责中文说明 ============
// 展示工具函数：时间格式化、状态标签、紧凑 JSON、千分位与密钥打码。
// ========================================

/** fmtTime ISO → "YYYY-MM-DD HH:MM:SS"（与 Vue 版一致，UTC-ish 原样切片）
 * @param s - ISO 时间字符串
 * @returns 格式化后的时间字符串；空值返回 "—"
 */
export function fmtTime(s?: string): string {
  return s ? s.replace('T', ' ').slice(0, 19) : '—'
}

/** 租户/订单状态标签（i18n）
 * @param s - 状态标识：active / disabled / 其他
 * @returns 本地化后的状态展示文本
 */
export function statusLabel(s: string): string {
  return s === 'active' ? t('common.active') : s === 'disabled' ? t('common.disabled') : t('common.expired')
}

/** shortJSON 对象紧凑序列化（表格预览用），超长截断
 * @param v - 待序列化的任意值
 * @param max - 最大长度，默认 120
 * @returns 紧凑字符串；超长时截断并追加 "…"
 */
export function shortJSON(v: unknown, max = 120): string {
  let s = ''
  try { s = typeof v === 'string' ? v : JSON.stringify(v) } catch { s = String(v) }
  return s.length > max ? s.slice(0, max) + '…' : s
}

/** fmtNum 千分位
 * @param n - 数字或字符串形式的数字
 * @returns 千分位格式化文本；无效值返回 "0"
 */
export function fmtNum(n?: number | string): string {
  const x = Number(n || 0)
  return x ? x.toLocaleString('en-US') : '0'
}

/** maskKey 密钥打码展示
 * @param k - 原始密钥字符串
 * @returns 打码后的展示文本；空值返回 "—"
 */
export function maskKey(k?: string): string {
  if (!k) return '—'
  if (k.length <= 12) return k.slice(0, 4) + '****'
  return k.slice(0, 8) + '****' + k.slice(-4)
}
