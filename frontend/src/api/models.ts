// ============================================================================
// api/models.ts — 模型/路由/策略域接口
// 职责：平台统一网关模型配置（全局主模型 + 多供应商路由）、分阶段模型、匹配策略参数
// ★ 2026-08-26 BYOK 移除：所有 LLM 调用一律经平台网关，租户侧无模型配置入口；
//   本文件全部接口均为超管专用（后端 requireAdminUser 把关）。
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 读取平台网关模型配置（全局默认单模型 + model_routes 多供应商路由，密钥掩码回显）
export async function adminModels(): Promise<AdminResp> {
  return request('/api/admin/models', { headers: authHeaders() })
}

// 保存平台网关模型配置（单模型字段合并为主路由 + routes 全量覆盖；掩码 Key=未修改）
export async function adminModelsSave(data: {
  api_base?: string; api_key?: string; model?: string;
  routes?: { provider: string; api_base: string; api_key: string; model: string; weight: number }[]
}): Promise<AdminResp> {
  return request('/api/admin/models/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 各流程阶段模型配置（super_admin） ====================

export interface StageModelConfig {
  provider: string
  api_base: string
  api_key: string
  model: string
}

// 读取各流程阶段模型配置（kb_match/ai_initial/evals/review）
export async function stageModels(): Promise<AdminResp> {
  return request('/api/admin/models/stage', { headers: authHeaders() })
}

// 保存各流程阶段模型配置（全量提交；某项 api_base/model 为空=清空该阶段）
export async function stageModelsSave(stages: Record<string, StageModelConfig>): Promise<AdminResp> {
  return request('/api/admin/models/stage/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ stages }) })
}

// 读取匹配策略参数
export async function adminPolicy(): Promise<AdminResp> {
  return request('/api/admin/policy', { headers: authHeaders() })
}

// 保存匹配策略参数（crossDeptFallback=★ 跨部门降级检索开关，2026-08-26 KB继承链）
export async function adminPolicySave(policy: Record<string, number>, crossDeptFallback?: boolean): Promise<AdminResp> {
  return request('/api/admin/policy/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ policy, cross_dept_fallback: crossDeptFallback }) })
}