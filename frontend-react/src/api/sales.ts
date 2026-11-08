// ============================================================================
// api/sales.ts — 二期「多语言销售能力」草稿 inbox 接口层（施工_二期 §9.4）
//
// ★ 失败收敛纪律（AGENTS §一·5）：本文件每个返回 {success,...} 信封的调用一律
//   `return bizResp(() => request(...))`，走 bizRespGate.test.ts 的**主判据**，
//   不进任何豁免档（EXEMPT／READ_THROW_EXEMPT／LEGACY_UNWRAPPED 全不登记）。
//
// 接口契约（以 backend-go/internal/api/sales_api.go 的 routesSales() 实读为准）：
//   GET  /api/sales/drafts?status=   草稿列表（租户作用域，空集出 []）
//   GET  /api/sales/draft?id=        草稿详情（跨租户/不存在 404）
//   POST /api/sales/draft/approve    人审转正（唯一写动作，留审计 sales_draft_approve）
//   GET  /api/sales/capability       两把闸三态读数 off/half/on（只读状态词）
// ★ 没有删除／批量／自动生效口——前端也不许造（§9.4 锁 1）。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 一条说法草稿（与 store.SalesDraft 的 json 标签逐字对应） */
export interface SalesDraft {
  id: string
  tenant_id: number
  anchor: string
  source_text: string
  suggested_text: string
  attribution_ref: string
  status: string
  created_at: string
  updated_at: string
}

/** 草稿列表响应（空集是 []，不是 null） */
export interface SalesDraftListResp extends AdminResp {
  drafts?: SalesDraft[]
}

/** 草稿详情响应 */
export interface SalesDraftDetailResp extends AdminResp {
  draft?: SalesDraft
}

/** 转正响应 */
export interface SalesDraftApproveResp extends AdminResp {
  id?: string
  status?: string
}

/** 两把闸三态读数（off/half/on 三者不同形；半套配置不许呈现为已生效） */
export interface SalesCapabilityResp extends AdminResp {
  status?: string
  engine_enabled?: boolean
  billing_enforced?: boolean
}

/** 草稿列表（登录态；status 可空＝全部） */
export async function salesDrafts(status?: string): Promise<SalesDraftListResp> {
  const q = status ? `?status=${encodeURIComponent(status)}` : ''
  return bizResp(() => request(`/api/sales/drafts${q}`, { headers: authHeaders() }))
}

/** 草稿详情（登录态；跨租户/不存在由后端回 404，bizResp 收敛成信封） */
export async function salesDraft(id: string): Promise<SalesDraftDetailResp> {
  return bizResp(() => request(`/api/sales/draft?id=${encodeURIComponent(id)}`, { headers: authHeaders() }))
}

/** 人审转正——本草稿面**唯一**的写动作（后端留审计 sales_draft_approve） */
export async function salesDraftApprove(id: string): Promise<SalesDraftApproveResp> {
  return bizResp(() => request('/api/sales/draft/approve', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ id }),
  }))
}

/** 两把闸三态读数（只读；探不到时调用方按 fail-open 处理，不钉死「永远没有」） */
export async function salesCapability(): Promise<SalesCapabilityResp> {
  return bizResp(() => request('/api/sales/capability', { headers: authHeaders() }))
}