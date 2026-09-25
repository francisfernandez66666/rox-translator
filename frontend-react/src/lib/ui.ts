// ============ lib/ui.ts · 职责说明 ============
// 展示工具函数：时间格式化、状态标签、紧凑 JSON、千分位与密钥打码
// 自 Vue 版 components/admin/ui.ts 移植，行为一致。
// =============================================
// 依赖引入：i18n 翻译函数 t
import { t } from '@/i18n'
// ★ F-14（批G）：fmtTime 的 local 口径复用 format.ts 的 fmtDateTime（〇-Q：按界面语种 + 运行时本地时区出 Intl 格式）
import { fmtDateTime } from './format'

/** fmtTime ISO → "YYYY-MM-DD HH:MM:SS"（与 Vue 版一致，UTC-ish 原样切片）
 * ★ F-14（批G）：新增 local 参数——后端时间串多为 UTC ISO，东八区用户直接裸切片会在跨日
 * 边界「订单显示错一天」；local=true 改按本地时区显示（同 TicketsPage 私有 fmtTime 的口径，
 * 委托 lib/format 的 fmtDateTime，非法值 fail-closed 回原串）。
 * 默认 false 保持旧的「原样切片」行为不变，既有调用点（后台列表/铃铛等）零影响。
 * @param s - ISO 时间字符串
 * @param local - 是否按本地时区显示（缺省 false = 旧口径 UTC 串裸切片，勿改默认值）
 * @returns 格式化后的时间字符串；空值返回 "—"
 */
export function fmtTime(s?: string, local = false): string {
  if (!s) return '—'
  // 本地口径：fmtDateTime 自身已 fail-closed（空值回 "—"、非法日期回原串），不再另包 try/catch
  if (local) return fmtDateTime(s)
  return s.replace('T', ' ').slice(0, 19)
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

import { fmtNum as fmtNumByLang } from './format'

/** fmtNum 千分位（★ 〇-Q：千分位与小数位按**界面语种**出，原写死 'en-US'）
 * @param n - 数字或字符串形式的数字
 * @returns 千分位格式化文本；无效值返回 "0"
 */
export function fmtNum(n?: number | string): string {
  const x = Number(n || 0)
  return x ? fmtNumByLang(x, { max: 2 }) : '0'
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

/** roleLevelSafe 安全版角色等级
 * @param r - 角色标识字符串
 * @returns 角色等级数字（super_admin/admin=4, tenant_admin/approver=3, dept_admin=2, 其他=1）
 */
export function roleLevelSafe(r?: string): number {
  if (r === 'super_admin' || r === 'admin') return 4
  if (r === 'tenant_admin' || r === 'approver') return 3
  if (r === 'dept_admin') return 2
  return 1
}
