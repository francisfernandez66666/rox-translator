// ============================================================================
// api/webhooks.ts — Webhook 回调配置域接口
// 职责：租户配置「翻译完成」事件回调 URL / 签名密钥 / 事件订阅
// 提供：列表 / 新增或更新 / 删除 / 测试投递
// ============================================================================

/**
 * api/webhooks.ts · 职责说明
 * 封装 Webhook 回调配置的所有接口，包括：
 * - Webhook 列表：查询当前租户的 webhook 配置
 * - Webhook 管理：新增或更新 webhook 配置、删除 webhook
 * - Webhook 测试：向指定 webhook 发送测试 ping 验证连通性
 */

import { request, authHeaders } from './core'
import type { AdminResp } from './core'

/** 查询当前租户 webhook 配置列表 */
export async function webhooks(): Promise<AdminResp> {
  return request('/api/webhooks', { headers: authHeaders() })
}

/** 新增或更新 webhook（id<=0 新增，否则更新） */
export async function webhookSave(data: { id?: number; url: string; secret?: string; events?: string; enabled?: number }): Promise<AdminResp> {
  return request('/api/webhooks/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 删除指定 webhook */
export async function webhookDelete(id: number): Promise<AdminResp> {
  return request('/api/webhooks/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 向指定 webhook 发送测试 ping */
export async function webhookTest(id: number): Promise<AdminResp> {
  return request('/api/webhooks/test', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}