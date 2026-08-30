// ============================================================================
// api/tenant.ts — 租户域接口（SaaS）
// 职责：租户 CRUD、状态、数据导出/清除（GDPR 数据主权）
// ============================================================================

/**
 * api/tenant.ts · 职责说明
 * 封装租户管理相关的所有接口，包括：
 * - 租户 CRUD：创建、更新、删除租户
 * - 租户状态：启用/停用租户
 * - 数据主权：导出租户全部数据（GDPR 数据可携权）
 * - 数据清除：GDPR 删除权，不可恢复地清除租户业务数据
 * - 试用额度：向待审核租户发放试用额度
 * - 邀请开关：读取/设置租户的邀请好友功能开关
 */

import { request, authHeaders, type AdminResp } from './core'

/** 租户信息数据结构：含编码/名称/状态/有效期/权限 */
export interface TenantInfo {
  id: number
  code: string
  name: string
  status: string
  expires_at: string
  permissions: string
  invite_enabled?: boolean // 是否开通「邀请好友」功能（超管按租户开关）
  created_at: string
  updated_at: string
}

/** 租户接口统一响应结构：success 标记结果，tenants 列表/tenant 单对象 */
export interface TenantResp {
  success: boolean
  message?: string
  tenants?: TenantInfo[]
  tenant?: TenantInfo
}

// 租户管理：统一走 JWT 登录认证（管理后台 admin 角色）
/** 获取租户列表（需 admin 角色 JWT 认证） */
export async function tenantList(): Promise<TenantResp> {
  return request('/api/tenant/list', { headers: authHeaders() })
}

/** 创建新租户（可附带租户管理员账号） */
export async function tenantCreate(
  data: { code: string; name: string; expires_at: string; permissions: string; admin_user?: string; admin_pass?: string },
): Promise<TenantResp> {
  return request('/api/tenant/create', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

/** 更新租户信息（名称/有效期/权限/邀请好友开关） */
export async function tenantUpdate(
  data: { id: number; name: string; expires_at: string; permissions: string; invite_enabled?: boolean },
): Promise<TenantResp> {
  return request('/api/tenant/update', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

/** 读取当前生效租户的「邀请好友」功能开关与租户类型（tenant_admin 及以上） */
export async function tenantInviteEnabledGet(): Promise<{ success: boolean; invite_enabled?: boolean; is_personal?: boolean }> {
  return request('/api/tenant/invite-enabled', { headers: authHeaders() })
}

/** 启用/停用租户 */
export async function tenantSetStatus(id: number, status: string): Promise<TenantResp> {
  return request('/api/tenant/status', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, status }),
  })
}

/** 删除租户（连同数据一并删除） */
export async function tenantDelete(id: number): Promise<TenantResp> {
  return request('/api/tenant/delete', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id }),
  })
}

// 租户数据导出（数据主权）/ 清除（GDPR 删除权，super_admin）
/** 导出租户全部数据（JSON 文件下载，数据主权） */
export async function tenantExport(id: number): Promise<AdminResp> {
  return request('/api/tenant/export', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** GDPR 清除租户全部业务数据（不可恢复，删除权） */
export async function tenantErase(id: number): Promise<AdminResp> {
  return request('/api/tenant/erase', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 向待审核租户发放试用额度（super_admin，幂等） */
export async function tenantGrantTrial(id: number): Promise<AdminResp> {
  return request('/api/admin/tenants/grant-trial', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ tenant_id: id }) })
}