// ============================================================================
// api/salesAttribution.ts — 三期 W2 归因与素材效果的**只读**接口层
//
// 后端契约（backend-go/internal/api/sales_attribution_api.go 实读）：
//   GET /api/admin/sales/attribution/replies    归因行分页（?limit=&offset=）
//   GET /api/admin/sales/attribution/pack-stats 单组素材效果快照（未物化 stats=null）
//
// 纪律（bizRespGate 全集判）：两个调用都返回 {success,...} 信封，一律
// `bizResp(() => request(...))`，不进任何豁免档。本文件**零写侧**——
// reply_attributions 的唯一生产写口是主服务代理层 writeBackAttribution（A04/A08），
// 前端不许再造第二条写路径。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 回复归因行（出参键为后端 json tag 的 snake_case，逐字对应） */
export interface SalesAttributionRow {
  id: number
  tenant_id: number
  message_id: number
  conversation_id: number
  customer_id: number
  package_id: string
  version: string
  draft_id: string
  anchor_type: number
  route_result: string
  provider: string
  model_name: string
  intent_before: number
  intent_after: number
  hooked: number
  lead_captured: number
  pending_human: number
  eval_score: number
  outcome_stage: string
  arrived_at: string
  dealt_at: string
  created_at: string
  updated_at: string
}

/** 素材效果聚合快照行（9 个聚合列由后端 SyncPackStats 现算，读口不现场算） */
export interface SalesPackStatRow {
  tenant_id: number
  package_id: string
  version: string
  draft_id: string
  sample_count: number
  hook_rate: number
  lead_rate: number
  pending_human_rate: number
  arrive_rate: number
  deal_rate: number
  avg_intent_delta: number
  avg_eval_score: number
  computed_at: string
}

export interface SalesAttributionRepliesResp extends AdminResp {
  rows?: SalesAttributionRow[]
}

export interface SalesPackStatsResp extends AdminResp {
  /** 未物化时为 null（不是错误）：聚合现算留给写侧，读口只取快照 */
  stats?: SalesPackStatRow | null
}

/** 归因行分页列表（租户管理员及以上；limit/offset 缺省由后端回落） */
export async function salesAttributionReplies(
  limit?: number,
  offset?: number,
): Promise<SalesAttributionRepliesResp> {
  const q = new URLSearchParams()
  if (limit !== undefined) q.set('limit', String(limit))
  if (offset !== undefined) q.set('offset', String(offset))
  const qs = q.toString()
  return bizResp(() => request(`/api/admin/sales/attribution/replies${qs ? `?${qs}` : ''}`, { headers: authHeaders() }))
}

/** 单组素材效果快照（三元组定位一组素材；未物化 stats=null） */
export async function salesPackStats(
  packageID: string,
  version: string,
  draftID: string,
): Promise<SalesPackStatsResp> {
  const qs = new URLSearchParams({ package_id: packageID, version, draft_id: draftID }).toString()
  return bizResp(() => request(`/api/admin/sales/attribution/pack-stats?${qs}`, { headers: authHeaders() }))
}
