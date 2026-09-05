// ============================================================================
// api/ops.ts — 运营策略引擎接口
// 职责：计费/模式/套餐/时间窗/邀请等运营参数因子配置的读取与保存
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

/** 读取运营策略（平台默认/租户覆盖/基础/最终/窗口命中态） */
export async function opsPolicy(): Promise<AdminResp> {
  return request('/api/admin/ops/policy', { headers: authHeaders() })
}

/** 保存运营策略：scope=platform（超管）| tenant（租户管理员，默认） */
export async function opsPolicySave(scope: 'platform' | 'tenant', policy: Record<string, unknown>): Promise<AdminResp> {
  return request('/api/admin/ops/policy/save', {
    method: 'POST', headers: authHeaders(), body: JSON.stringify({ scope, policy }),
  })
}

/** 保存单个推广时间窗（超管） */
export async function opsWindowSave(window: Record<string, unknown>): Promise<AdminResp> {
  return request('/api/admin/ops/policy/window/save', {
    method: 'POST', headers: authHeaders(), body: JSON.stringify({ window }),
  })
}

/** 重置当前套餐月度用量（租户管理员+） */
export async function opsPackageReset(tenantId?: number): Promise<AdminResp> {
  return request('/api/admin/billing/package/reset', {
    method: 'POST', headers: authHeaders(),
    body: tenantId ? JSON.stringify({ tenant_id: tenantId }) : '{}',
  })
}
