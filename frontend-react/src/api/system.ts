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

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 获取系统健康状态（知识库/余额/流程/用量/模型等） */
export async function systemHealth(): Promise<AdminResp> {
  return request('/api/system/health', { headers: authHeaders() })
}

/** 获取审计日志列表 */
export async function systemAudit(): Promise<AdminResp> {
  return bizResp(() => request('/api/system/audit', { headers: authHeaders() }))
}

// ==================== 监控告警 ====================

/** 获取告警列表（可按状态过滤） */
export async function systemAlerts(status?: string): Promise<AdminResp> {
  const q = status ? `?status=${status}` : ''
  return bizResp(() => request(`/api/system/alerts${q}`, { headers: authHeaders() }))
}

/** 解决指定告警 */
export async function alertResolve(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/system/alerts/resolve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

// ★ F9：对账视图（orders↔payments 勾稽，超管）
export interface ReconIssue { order_id: number; order_no: string; tenant_id: number; rule: string; detail: string; created_at: string }
// 触发全量余额对账（超管）
export async function adminReconcile(days: number): Promise<AdminResp> {
  return bizResp(() => request(`/api/admin/reconcile?days=${days}`, { headers: authHeaders() }))
}

// ★ F9：告警静音 / 解除静音（分钟数到点自动失效）
export async function alertSilence(tenantId: number, kind: string, minutes: number): Promise<AdminResp> {
  return bizResp(() => request('/api/system/alerts/silence', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ tenant_id: tenantId, kind, minutes }) }))
}
// 解除告警静音（超管）
export async function alertUnsilence(tenantId: number, kind: string): Promise<AdminResp> {
  return bizResp(() => request('/api/system/alerts/unsilence', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ tenant_id: tenantId, kind }) }))
}

/** 获取 evals 评估记录列表 */
export async function evalsList(): Promise<AdminResp> {
  return bizResp(() => request('/api/evals/list', { headers: authHeaders() }))
}