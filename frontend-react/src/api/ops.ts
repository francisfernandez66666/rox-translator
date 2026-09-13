// ============================================================================
// api/ops.ts — 运营策略引擎接口
// 职责：计费/模式/套餐/时间窗/邀请等运营参数因子配置的读取与保存
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

/** 读取运营策略（平台默认/租户覆盖/基础/最终/窗口命中态） */
/** ★ H11 SLO/SLI 燃烧率快照（超管） */
/** ★ H7 供应商路由实时统计（超管） */
export async function opsRoutes(): Promise<AdminResp & { routes?: Array<{ route: string; samples: number; p50_ms: number; p95_ms: number; ok: number; fail: number; err_rate: number; tokens_per_call: number }>; dynamic_routing?: boolean; hedge_enabled?: boolean }> {
  return request('/api/admin/ops/routes', { headers: authHeaders() })
}

// 后台 SLO 指标（各链路可用性/延迟/错误率）
export async function opsSlo(): Promise<AdminResp & { slos?: Array<{ key: string; name: string; target: number; level?: string; burn_1h: number; burn_6h: number; budget_left_pct?: number; stats?: Array<{ window: string; samples: number; err_rate: number; burn: number }> }> }> {
  return request('/api/admin/ops/slo', { headers: authHeaders() })
}

// 查询运营策略（促销窗口等生效配置）
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
