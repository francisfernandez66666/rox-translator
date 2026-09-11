// ============================================================================
// api/webhooks.ts — Webhook 回调配置域接口
// 职责：租户配置「翻译完成」事件回调 URL / 签名密钥 / 事件订阅 / 重试策略
// 提供：列表 / 新增或更新 / 删除 / 测试投递 / 投递历史 / 手动重试
// ============================================================================

/**
 * api/webhooks.ts · 职责说明
 * 封装 Webhook 回调配置的所有接口，包括：
 * - Webhook 列表：查询当前租户的 webhook 配置
 * - Webhook 管理：新增或更新 webhook 配置、删除 webhook
 * - Webhook 测试：向指定 webhook 发送测试 ping 验证连通性
 * - 投递历史：查询指定 webhook 的投递记录（含成功/失败/死信）
 * - 手动重试：重试失败或死信投递
 */

import { request, authHeaders } from './core'
import type { AdminResp } from './core'

/** Webhook 配置（含重试策略字段） */
export interface WebhookConfig {
  id: number
  tenant_id: number
  url: string
  secret: string
  events: string
  enabled: number
  max_retries: number
  retry_interval: number
  last_delivery_at: string
  failure_count: number
  created_at: string
  updated_at: string
}

/** Webhook 投递记录 */
export interface WebhookDelivery {
  id: number
  webhook_id: number
  tenant_id: number
  event: string
  payload: string
  status: string        // pending/success/failed/dead
  status_code: number
  response: string
  attempts: number
  max_retries: number
  next_retry_at: string
  error: string
  created_at: string
  updated_at: string
}

/** 投递统计 */
export interface DeliveryStats {
  total: number
  success: number
  failed: number
  dead: number
}

/** 查询当前租户 webhook 配置列表 */
export async function webhooks(): Promise<AdminResp> {
  return request('/api/webhooks', { headers: authHeaders() })
}

/** 新增或更新 webhook（id<=0 新增，否则更新） */
export async function webhookSave(data: {
  id?: number
  url: string
  secret?: string
  events?: string
  enabled?: number
  max_retries?: number
  retry_interval?: number
}): Promise<AdminResp> {
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

/** 查询指定 webhook 的投递历史 */
export async function webhookDeliveries(webhookId: number, limit = 50): Promise<AdminResp & { deliveries?: WebhookDelivery[]; stats?: DeliveryStats }> {
  return request(`/api/webhooks/deliveries?webhook_id=${webhookId}&limit=${limit}`, { headers: authHeaders() })
}

/** 重试一条失败/死信投递 */
export async function webhookRetry(deliveryId: number): Promise<AdminResp> {
  return request('/api/webhooks/retry', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ delivery_id: deliveryId }) })
}
