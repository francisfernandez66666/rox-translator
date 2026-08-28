// ============================================================================
// api/flow.ts — 流程引擎域接口
// 职责：工单翻译流程步骤配置的读取与保存
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// ============ 本文件职责中文说明 ============
// 封装工单翻译流程步骤配置的读取与保存等接口
// ========================================

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