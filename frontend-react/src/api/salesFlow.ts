// ============================================================================
// api/salesFlow.ts — 三期 W2 流程状态机**只读排障面**接口层
//
// 后端契约（backend-go/internal/api/sales_flow_api.go 实读）：
//   GET /api/admin/sales/flow/state?fiid=  单条状态机行
//
// ★ 查无回 success:true + found:false（不是 404）：「流程还没推进到」是正常态，
// 404 会把它打成告警噪音；状态机推进没有 HTTP 写口（二期 §9.2 定案：只走
// store.AdvanceFlowStateMachine 乐观锁），本面只服务排障。
// 纪律：信封调用一律 bizResp 包裹。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 状态机行（后端 handler 手拼 snake_case map，逐字对应） */
export interface SalesFlowStateRow {
  id: string
  tenant_id: number
  one_id: string
  flow_instance_id: number
  current_node: string
  state_json: string
  heartbeat_ts: string
  status: string
  version: number
  last_event_id: string
}

export interface SalesFlowStateResp extends AdminResp {
  found?: boolean
  state?: SalesFlowStateRow
}

/** 读单条状态机（读 ≥2 部门管理员；fiid 必须是正整数） */
export async function salesFlowState(fiid: number): Promise<SalesFlowStateResp> {
  return bizResp(() => request(`/api/admin/sales/flow/state?fiid=${fiid}`, { headers: authHeaders() }))
}
