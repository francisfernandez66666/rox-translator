// ============================================================================
// api/apikeys.ts — 开放 API Key 域接口
// 职责：开放平台 API Key 的签发、启停、轮换、删除、限额、解密复制
// ============================================================================

/**
 * api/apikeys.ts · 职责说明
 * 封装开放平台 API Key 的完整生命周期管理接口，包括：
 * - API Key 的签发、启用/停用、轮换、删除
 * - 每日调用限额设置
 * - Key 明文解密复制（仅前端写入剪贴板）
 * - OpenAPI 文档的在线维护（中英双语）
 */

import { request, authHeaders, type AdminResp } from './core'

/** 获取开放平台 API Key 列表 */
export async function apiKeys(): Promise<AdminResp> {
  return request('/api/apikeys', { headers: authHeaders() })
}

/** 签发新 API Key（daily_call_limit 可选，0=不限） */
export async function apiKeyCreate(data: { name: string; perms: string; daily_call_limit?: number }): Promise<AdminResp> {
  return request('/api/apikeys/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 启用/停用指定 API Key */
export async function apiKeyStatus(id: number, status: string): Promise<AdminResp> {
  return request('/api/apikeys/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, status }) })
}

/** 轮换 API Key（旧 Key 立即失效） */
export async function apiKeyRotate(id: number): Promise<AdminResp> {
  return request('/api/apikeys/rotate', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 删除指定 API Key */
export async function apiKeyDelete(id: number): Promise<AdminResp> {
  return request('/api/apikeys/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 设置 Key 每日调用上限（0=不限） */
export async function apiKeyLimit(id: number, dailyCallLimit: number): Promise<AdminResp> {
  return request('/api/apikeys/limit', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, daily_call_limit: dailyCallLimit }) })
}

/** 解密返回 Key 明文（前端仅写入剪贴板，不展示） */
export async function apiKeyReveal(id: number): Promise<AdminResp & { api_key?: string }> {
  return request('/api/apikeys/reveal', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// ============================================================================
// 开放 API 文档在线维护（仅超管）
// ============================================================================

/** getOpenAPIDocs 读取中英双语文档源码 */
export async function getOpenAPIDocs(): Promise<AdminResp & { md_zh?: string; md_en?: string; default_zh?: boolean; default_en?: boolean }> {
  return request('/api/admin/openapi-docs', { headers: authHeaders() })
}

/** saveOpenAPIDocs 保存指定语言的文档源码（空串=恢复该语言内置默认） */
export async function saveOpenAPIDocs(data: { lang: string; md: string }): Promise<AdminResp> {
  return request('/api/admin/openapi-docs/save', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

/** previewOpenAPIDocs 预览渲染结果 */
export async function previewOpenAPIDocs(data: { lang?: string; md: string }): Promise<AdminResp & { html?: string }> {
  return request('/api/admin/openapi-docs/preview', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}
