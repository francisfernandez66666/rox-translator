// ============================================================================
// api/models.ts — 模型/路由/策略域接口
// 职责：在线模型配置、多供应商路由策略、匹配策略参数
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 读取在线模型配置
export async function adminModels(): Promise<AdminResp> {
  return request('/api/admin/models', { headers: authHeaders() })
}

// 保存在线模型配置
export async function adminModelsSave(data: { api_base: string; api_key: string; model: string }): Promise<AdminResp> {
  return request('/api/admin/models/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 模型路由策略（super_admin） ====================

// 读取模型路由策略
export async function modelRoutes(): Promise<AdminResp> {
  return request('/api/admin/models/routes', { headers: authHeaders() })
}

// 保存模型路由策略（多供应商按权重路由 + 降级）
export async function modelRoutesSave(data: { routes: { provider: string; api_base: string; api_key: string; model: string; weight: number }[] }): Promise<AdminResp> {
  return request('/api/admin/models/routes/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 读取匹配策略参数
export async function adminPolicy(): Promise<AdminResp> {
  return request('/api/admin/policy', { headers: authHeaders() })
}

// 保存匹配策略参数
export async function adminPolicySave(policy: Record<string, number>): Promise<AdminResp> {
  return request('/api/admin/policy/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ policy }) })
}