// ============================================================================
// api/apikeys.ts — 开放 API Key 域接口
// 职责：开放平台 API Key 的签发、启停、轮换、删除
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 开放 API Key 列表
export async function apiKeys(): Promise<AdminResp> {
  return request('/api/apikeys', { headers: authHeaders() })
}

// 签发新 API Key
export async function apiKeyCreate(data: { name: string; perms: string; daily_call_limit?: number }): Promise<AdminResp> {
  return request('/api/apikeys/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 启用/停用 API Key
export async function apiKeyStatus(id: number, status: string): Promise<AdminResp> {
  return request('/api/apikeys/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, status }) })
}

// 轮换 API Key（旧 Key 立即失效）
export async function apiKeyRotate(id: number): Promise<AdminResp> {
  return request('/api/apikeys/rotate', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 删除 API Key
export async function apiKeyDelete(id: number): Promise<AdminResp> {
  return request('/api/apikeys/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
// ============================================================================
// 开放 API 文档在线维护（仅超管）
// ============================================================================

/** getOpenAPIDocs 读取当前生效的文档 MD 源码（is_default=true 表示内置默认未改过）。 */
export async function getOpenAPIDocs(): Promise<AdminResp & { md?: string; is_default?: boolean }> {
  return request('/api/admin/openapi-docs', { headers: authHeaders() })
}

/** saveOpenAPIDocs 保存文档 MD 源码（传空串=恢复内置默认）。 */
export async function saveOpenAPIDocs(md: string): Promise<AdminResp> {
  return request('/api/admin/openapi-docs/save', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ md }),
  })
}

/** previewOpenAPIDocs 预览渲染结果（不落库），返回完整 HTML。 */
export async function previewOpenAPIDocs(md: string): Promise<AdminResp & { html?: string }> {
  return request('/api/admin/openapi-docs/preview', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ md }),
  })
}

/** apiKeyLimit 设置 Key 每日调用上限（0=不限，R4 Key 级配额）。 */
export async function apiKeyLimit(id: number, dailyCallLimit: number): Promise<AdminResp> {
  return request('/api/apikeys/limit', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, daily_call_limit: dailyCallLimit }),
  })
}
