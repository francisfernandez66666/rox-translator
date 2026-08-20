// ============================================================================
// api/invites.ts — 邀请码域接口
// 职责：自助注册邀请码的查询与生成
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 邀请码列表
export async function inviteCodes(): Promise<AdminResp> {
  return request('/api/admin/invite-codes', { headers: authHeaders() })
}

// 生成邀请码（绑定租户或留空新建租户）
export async function inviteCodeCreate(data: { code: string; tenant_id: number }): Promise<AdminResp> {
  return request('/api/admin/invite-codes/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}