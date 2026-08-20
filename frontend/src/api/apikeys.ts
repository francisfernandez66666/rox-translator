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
export async function apiKeyCreate(data: { name: string; perms: string }): Promise<AdminResp> {
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