// ============================================================================
// api/system.ts — 系统/看板/运维域接口
// 职责：系统健康、审计日志、监控告警、evals 评估记录
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 系统健康状态（知识库/余额/流程/用量/模型状态等）
export async function systemHealth(): Promise<AdminResp> {
  return request('/api/system/health', { headers: authHeaders() })
}

// 审计日志列表
export async function systemAudit(): Promise<AdminResp> {
  return request('/api/system/audit', { headers: authHeaders() })
}

// ==================== 监控告警 ====================

// 告警列表（可按状态过滤）
export async function systemAlerts(status?: string): Promise<AdminResp> {
  const q = status ? `?status=${status}` : ''
  return request(`/api/system/alerts${q}`, { headers: authHeaders() })
}

// 解决告警
export async function alertResolve(id: number): Promise<AdminResp> {
  return request('/api/system/alerts/resolve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// evals 评估记录列表
export async function evalsList(): Promise<AdminResp> {
  return request('/api/evals/list', { headers: authHeaders() })
}