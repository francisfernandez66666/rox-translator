// ============================================================================
// api/system.ts — 系统/看板/运维域接口
// 职责：系统健康、审计日志、监控告警、evals 评估记录
// ============================================================================

/**
 * api/system.ts · 职责说明
 * 封装系统运维相关的所有接口，包括：
 * - 系统健康检查：知识库、余额、流程、用量、模型等状态
 * - 审计日志：查看系统操作日志
 * - 监控告警：获取告警列表、解决告警
 * - 评估记录：查看 evals 评估结果
 */

import { request, authHeaders, type AdminResp } from './core'

/** 获取系统健康状态（知识库/余额/流程/用量/模型等） */
export async function systemHealth(): Promise<AdminResp> {
  return request('/api/system/health', { headers: authHeaders() })
}

/** 获取审计日志列表 */
export async function systemAudit(): Promise<AdminResp> {
  return request('/api/system/audit', { headers: authHeaders() })
}

// ==================== 监控告警 ====================

/** 获取告警列表（可按状态过滤） */
export async function systemAlerts(status?: string): Promise<AdminResp> {
  const q = status ? `?status=${status}` : ''
  return request(`/api/system/alerts${q}`, { headers: authHeaders() })
}

/** 解决指定告警 */
export async function alertResolve(id: number): Promise<AdminResp> {
  return request('/api/system/alerts/resolve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 获取 evals 评估记录列表 */
export async function evalsList(): Promise<AdminResp> {
  return request('/api/evals/list', { headers: authHeaders() })
}