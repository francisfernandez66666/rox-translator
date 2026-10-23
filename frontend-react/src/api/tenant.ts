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

import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 租户信息数据结构：含编码/名称/状态/有效期/权限 */
export interface TenantInfo {
  id: number
  code: string
  name: string
  status: string
  expires_at: string
  permissions: string
  invite_enabled?: boolean // 是否开通「邀请好友」功能（超管按租户开关）
  industry?: string // 注册行业编码（功能②：决定共享行业包载入范围）
  industry_name?: string // 行业展示名
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
//
// ★ 写接口一律包 bizResp（2026-10-16）：后端已按语义给 4xx（F-64①），而 request() 对非 2xx
//   一律抛异常；调用点（TenantsP）全是 `if (!r.success) toastError(r.message)` 形态 ⇒
//   旧写法下「编码已存在」这类 400 会变成未处理 rejection：**弹窗不关、列表不动、零提示**
//   （用户报障「点确定完全没反应」，服务端日志里连一次 POST 都没有的另一半原因是这里把
//   失败面的提示整条抹掉了）。收敛后 4xx 还原成 {success:false,message}，后端那句原文直达界面。
/** 获取租户列表（需 admin 角色 JWT 认证） */
// ★ 读取保持抛出：纯读取，调用点（TenantsP.load 经 runGuarded、admin store 经 try/catch）
//   走的是异常通道；包上 bizResp 会把「列表取不到」伪装成「一个租户都没有」＝空表假绿。
export async function tenantList(): Promise<TenantResp> {
  return request('/api/tenant/list', { headers: authHeaders() })
}

/** 创建新租户（可附带租户管理员账号） */
export async function tenantCreate(
  data: { code: string; name: string; expires_at: string; permissions: string; admin_user?: string; admin_pass?: string; industry?: string },
): Promise<TenantResp> {
  return bizResp(() => request('/api/tenant/create', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  }))
}

/** 更新租户信息（名称/有效期/权限/邀请好友开关） */
export async function tenantUpdate(
  data: { id: number; name: string; expires_at: string; permissions: string; invite_enabled?: boolean; industry?: string },
): Promise<TenantResp> {
  return bizResp(() => request('/api/tenant/update', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  }))
}

/** 读取当前生效租户的「邀请好友」功能开关与租户类型（tenant_admin 及以上） */
// ★ 读取保持抛出：纯读取，调用点（admin store.loadInviteEnabled）走异常通道；
//   包 bizResp 会把「取不到」伪装成「开关关闭」，与真实配置无法区分。
export async function tenantInviteEnabledGet(): Promise<{ success: boolean; invite_enabled?: boolean; is_personal?: boolean; tenant_name?: string }> {
  return request('/api/tenant/invite-enabled', { headers: authHeaders() })
}

/** 启用/停用租户 */
export async function tenantSetStatus(id: number, status: string): Promise<TenantResp> {
  return bizResp(() => request('/api/tenant/status', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, status }),
  }))
}

/**
 * 删除租户（连同该租户下全部数据一并删除，不可逆）。
 *
 * ★ 2026-10-23：改走 /api/admin/tenants/delete。旧入口 /api/tenant/delete 已下线——
 * 它不校验「超管必须处在平台上下文（tenant_id=0）」，也不要求确认串，与新接口构成两条
 * 强弱不同的路径；现在只留一条。
 *
 * confirm 必须**原样**填该租户的 code 或 name（服务端 strings.TrimSpace 后逐字比对）。
 * 这个参数不是给用户手打的，而是把「调用方要删的那一行」绑进请求：前端列表若是旧数据
 * （租户被改名/重建过），服务端会判 400 而不是照 id 删掉另一个租户。
 */
export async function tenantDelete(id: number, confirm: string): Promise<TenantResp> {
  return bizResp(() => request('/api/admin/tenants/delete', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, confirm }),
  }))
}

// 租户数据导出（数据主权）/ 清除（GDPR 删除权，super_admin）
/** 导出租户全部数据（JSON 文件下载，数据主权） */
export async function tenantExport(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/tenant/export', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** GDPR 清除租户全部业务数据（不可恢复，删除权） */
export async function tenantErase(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/tenant/erase', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 向待审核租户发放试用额度（super_admin，幂等） */
export async function tenantGrantTrial(id: number): Promise<AdminResp> {
  // ★ F-64②（批 I-10）：走 bizResp——发放试用失败（租户不存在/已发放）如实抛错
  return bizResp(() => request('/api/admin/tenants/grant-trial', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ tenant_id: id }) }))
}