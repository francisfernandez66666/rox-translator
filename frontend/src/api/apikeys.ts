// ============================================================================
// api/apikeys.ts — 开放 API Key 域接口
// 职责：开放平台 API Key 的签发、启停、轮换、删除、限额、解密复制
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 开放 API Key 列表
export async function apiKeys(): Promise<AdminResp> {
  return request('/api/apikeys', { headers: authHeaders() })
}

// 签发新 API Key（daily_call_limit 可选，0=不限）
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

// 设置 Key 每日调用上限（0=不限）
export async function apiKeyLimit(id: number, dailyCallLimit: number): Promise<AdminResp> {
  return request('/api/apikeys/limit', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, daily_call_limit: dailyCallLimit }) })
}

// 解密返回 Key 明文（固定复制能力；前端仅写入剪贴板，不展示）
export async function apiKeyReveal(id: number): Promise<AdminResp & { api_key?: string }> {
  return request('/api/apikeys/reveal', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
