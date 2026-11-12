// ============ salesRoles.ts · 职责说明 ============
// 三期 W3 销售角色权限矩阵接口层：一律 bizResp(() => request(...))。
// GET  /api/admin/sales/roles       回传权限项全集 / 四档等级 / 完整 4×10 行式矩阵 / 端点全集
// POST /api/admin/sales/roles/save  逐项保存一档 RoleLevel × perm 的 allowed（唯一写口）
// ★ 前端不抄权限项清单、不抄出厂缺省：矩阵由后端给，页面只显示与回写（单一真值口径）。
// 文案一律走界面层 t() 键，本文件不含任何界面字符串。
// ================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 行式权限的一行：RoleLevel × perm_key → allowed */
export interface SalesRoleRow {
  role_level: number
  perm_key: string
  allowed: boolean
}

/** 角色矩阵读响应（matrix 为补齐缺省后的完整 4×10 行） */
export interface SalesRolesResp extends AdminResp {
  perm_keys?: string[]
  levels?: number[]
  rows?: SalesRoleRow[]
  matrix?: SalesRoleRow[]
  endpoints?: string[]
}

/** 读完整角色权限矩阵（租户管理员及以上） */
export async function salesRolesGet(): Promise<SalesRolesResp> {
  return bizResp(() => request('/api/admin/sales/roles', { headers: authHeaders() }))
}

/** 逐项保存一档权限（唯一写口，后端留审计） */
export async function salesRolesSave(row: SalesRoleRow): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/roles/save', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(row),
  }))
}
