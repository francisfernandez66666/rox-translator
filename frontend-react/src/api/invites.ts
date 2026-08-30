// ============================================================================
// api/invites.ts — 邀请码域接口
// 职责：自助注册邀请码的查询与生成
// ============================================================================

/**
 * api/invites.ts · 职责说明
 * 封装自助注册邀请码的管理接口，包括：
 * - 邀请码列表：获取当前租户的邀请码列表
 * - 邀请码生成：创建新的邀请码，可绑定租户或留空新建租户
 */

import { request, authHeaders, type AdminResp } from './core'

/** 获取自助注册邀请码列表 */
export async function inviteCodes(): Promise<AdminResp> {
  return request('/api/admin/invite-codes', { headers: authHeaders() })
}

/** 生成邀请码（绑定租户或留空新建租户） */
export async function inviteCodeCreate(data: { code: string; tenant_id: number; org_id?: number }): Promise<AdminResp> {
  return request('/api/admin/invite-codes/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}