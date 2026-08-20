// ============================================================================
// api/tenant.ts — 租户域接口（SaaS）
// 职责：租户 CRUD、状态、数据导出/清除（GDPR 数据主权）
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 租户信息数据结构
export interface TenantInfo {
  id: number
  code: string
  name: string
  status: string
  expires_at: string
  permissions: string
  created_at: string
  updated_at: string
}

export interface TenantResp {
  success: boolean
  message?: string
  tenants?: TenantInfo[]
  tenant?: TenantInfo
}

// 租户管理：统一走 JWT 登录认证（管理后台 admin 角色）
// 获取租户列表
export async function tenantList(): Promise<TenantResp> {
  return request('/api/tenant/list', { headers: authHeaders() })
}

// 创建新租户（可附带租户管理员账号）
export async function tenantCreate(
  data: { code: string; name: string; expires_at: string; permissions: string; admin_user?: string; admin_pass?: string },
): Promise<TenantResp> {
  return request('/api/tenant/create', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

// 更新租户信息（名称/有效期/权限）
export async function tenantUpdate(
  data: { id: number; name: string; expires_at: string; permissions: string },
): Promise<TenantResp> {
  return request('/api/tenant/update', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

// 启用/停用租户
export async function tenantSetStatus(id: number, status: string): Promise<TenantResp> {
  return request('/api/tenant/status', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, status }),
  })
}

// 删除租户（连同数据一并删除）
export async function tenantDelete(id: number): Promise<TenantResp> {
  return request('/api/tenant/delete', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id }),
  })
}

// 租户数据导出（数据主权）/ 清除（GDPR 删除权，super_admin）
// 导出租户全部数据（JSON 文件下载）
export async function tenantExport(id: number): Promise<AdminResp> {
  return request('/api/tenant/export', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// GDPR 清除租户全部业务数据（不可恢复）
export async function tenantErase(id: number): Promise<AdminResp> {
  return request('/api/tenant/erase', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}