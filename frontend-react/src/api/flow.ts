// ============================================================================
// api/flow.ts — 流程引擎域接口
// 职责：工单翻译流程步骤配置的读取与保存
// ============================================================================

/**
 * api/flow.ts · 职责说明
 * 封装工单翻译流程引擎的配置管理接口，包括：
 * - 流程配置读取：获取当前流程步骤的启停状态
 * - 流程配置保存：保存各步骤的启用/禁用状态
 */

import { request, authHeaders, type AdminResp } from './core'

/** 流程步骤配置项：key 标识/name 展示名/enable 启停 */
export interface FlowStepItem {
  key: string
  name: string
  enable: boolean
}

/** 读取流程引擎配置 */
export async function flowConfig(): Promise<AdminResp> {
  return request('/api/admin/flow', { headers: authHeaders() })
}

/** 保存流程引擎配置（各步骤启停） */
export async function flowSave(steps: FlowStepItem[]): Promise<AdminResp> {
  return request('/api/admin/flow/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ steps }) })
}