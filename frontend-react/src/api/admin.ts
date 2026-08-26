// ============================================================================
// api/admin.ts — 后台管理域接口
// 职责：账户管理（用户 CRUD/重置密码）、充值订单（创建/确认收款）
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 用户列表
export async function adminUsers(): Promise<AdminResp> {
  return request('/api/admin/users', { headers: authHeaders() })
}

// 创建用户
export async function adminUserCreate(data: { username: string; password: string; display_name: string; role: string; org_id?: number; email?: string }): Promise<AdminResp> {
  return request('/api/admin/users/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 更新用户（显示名称/角色/状态）
export async function adminUserUpdate(id: number, data: { display_name?: string; role?: string; status?: string; org_id?: number; email?: string }): Promise<AdminResp> {
  return request('/api/admin/users/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, ...data }) })
}

// 删除用户账号（超管/租管/部门管理员按范围）
export async function adminUserDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/users/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 重置用户密码
export async function adminUserResetPassword(id: number, password: string): Promise<AdminResp> {
  return request('/api/admin/users/reset-password', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, password }) })
}

// ==================== 充值订单 ====================

// 创建充值订单
export async function adminOrderCreate(data: { tenant_id: number; tokens: number; money: number }): Promise<AdminResp> {
  return request('/api/admin/orders/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 确认收款（订单状态置为已支付）
export async function adminOrderPay(id: number): Promise<AdminResp> {
  return request('/api/admin/orders/pay', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
// 前台身份上下文：账号/租户/组织部门 + 可应用知识库包类型（登录后顶栏展示用）
export async function meContext(): Promise<AdminResp> {
  return request('/api/me/context', { headers: authHeaders() })
}
