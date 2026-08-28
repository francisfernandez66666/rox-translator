// ============================================================================
// api/webhooks.ts — Webhook 回调配置域接口
// 职责：租户配置「翻译完成」事件回调 URL / 签名密钥 / 事件订阅
// 提供：列表 / 新增或更新 / 删除 / 测试投递
// ============================================================================

import { request, authHeaders } from './core'
import type { AdminResp } from './core'

// ============ 本文件职责中文说明 ============
// 封装租户 Webhook 回调配置(列表/保存/删除/测试投递)等接口
// ========================================

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