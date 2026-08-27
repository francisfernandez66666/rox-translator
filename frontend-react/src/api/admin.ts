// ============================================================================
// api/admin.ts — 后台管理域接口
// 职责：账户管理（用户 CRUD/重置密码）、充值订单（创建/确认收款）
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

/** 获取后台用户列表 */
export async function adminUsers(): Promise<AdminResp> {
  return request('/api/admin/users', { headers: authHeaders() })
}

/** 创建后台用户（可指定组织与角色） */
export async function adminUserCreate(data: { username: string; password: string; display_name: string; role: string; org_id?: number; email?: string }): Promise<AdminResp> {
  return request('/api/admin/users/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 更新用户（显示名称/角色/状态/组织/邮箱） */
export async function adminUserUpdate(id: number, data: { display_name?: string; role?: string; status?: string; org_id?: number; email?: string }): Promise<AdminResp> {
  return request('/api/admin/users/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, ...data }) })
}

/** 删除用户账号（按角色范围限定可删对象） */
export async function adminUserDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/users/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 重置指定用户的登录密码 */
export async function adminUserResetPassword(id: number, password: string): Promise<AdminResp> {
  return request('/api/admin/users/reset-password', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, password }) })
}

// ==================== 充值订单 ====================

/** 创建充值订单（租户/代币数/金额） */
export async function adminOrderCreate(data: { tenant_id: number; tokens: number; money: number }): Promise<AdminResp> {
  return request('/api/admin/orders/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 确认收款（将订单状态置为已支付） */
export async function adminOrderPay(id: number): Promise<AdminResp> {
  return request('/api/admin/orders/pay', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// ==================== 邮件模板（仅超管） ====================

/** 邮件模板条目 */
export interface MailTplItem {
  code: string
  name: string
  desc: string
  vars: string[]
  subject: string
  body: string
  cc: string
  is_modified: boolean
}

/** 获取全部邮件模板（当前生效内容 + 用途/变量说明） */
export async function mailTemplatesGet(): Promise<AdminResp & { templates?: MailTplItem[] }> {
  return request('/api/admin/mail-templates', { headers: authHeaders() })
}

/** 保存（覆盖）指定模板的 subject/body/cc；templates 为部分更新的映射 */
export async function mailTemplatesSave(templates: Record<string, { subject?: string; body?: string; cc?: string }>): Promise<AdminResp> {
  return request('/api/admin/mail-templates', { method: 'PUT', headers: authHeaders(), body: JSON.stringify({ templates }) })
}
/** 获取前台身份上下文：账号/租户/组织部门 + 可应用知识库包类型（登录后顶栏展示用） */
export async function meContext(): Promise<AdminResp> {
  return request('/api/me/context', { headers: authHeaders() })
}
