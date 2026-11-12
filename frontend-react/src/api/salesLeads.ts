// ============================================================================
// api/salesLeads.ts — 三期 W6 线索 / 转人工 / 漏斗扩段接口层
//
// 后端契约（backend-go/internal/api/sales_crm_api.go 实读，统一 ≥3）：
//   GET  /api/admin/sales/leads?stage=&pending=1&limit=   线索分页
//   GET  /api/admin/sales/handoff?pending=1&limit=        转人工会话分页
//   POST /api/admin/sales/handoff/resolve                 人工完结（只降权）
//   GET  /api/admin/sales/funnel                          漏斗五段计数
//
// ★ 待人工只读 + 完结两动作：管理面不能把 pending_human 置 1（只允许守卫判定产生），
//   这里不提供「标记待人工」的写口（A24/M0263 的生产写口只在 writeBackAttribution）。
// 本文件零界面中文（文案一律 t() 键）。
// ============================================================================
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 线索行 */
export interface SalesLeadRow {
  id: number
  email: string
  stage: string
  score: number
  pending_human: number
  owner_user_id: number
  updated_at: string
}

export interface SalesLeadsResp extends AdminResp {
  rows?: SalesLeadRow[]
}

/** 线索分页（stage 空＝全阶段；pending=1 只看待人工） */
export async function salesLeadsList(stage: string, pending: boolean, limit = 100): Promise<SalesLeadsResp> {
  const q = new URLSearchParams()
  if (stage) q.set('stage', stage)
  if (pending) q.set('pending', '1')
  q.set('limit', String(limit))
  return bizResp(() => request(`/api/admin/sales/leads?${q.toString()}`, { headers: authHeaders() }))
}

/** 转人工会话行（会话映射 + 待人工状态） */
export interface HandoffSessionRow {
  id: number
  session_id: string
  message_id: number
  pending_human: number
  status: string
  created_at: string
}

export interface SalesHandoffResp extends AdminResp {
  rows?: HandoffSessionRow[]
}

export async function salesHandoffList(pending: boolean, limit = 100): Promise<SalesHandoffResp> {
  return bizResp(() => request(`/api/admin/sales/handoff?pending=${pending ? 1 : 0}&limit=${limit}`, { headers: authHeaders() }))
}

/** 人工完结（幂等；重复 resolve 不回翻） */
export async function salesHandoffResolve(sessionID: string): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/sales/handoff/resolve', {
    method: 'POST',
    headers: { ...authHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ session_id: sessionID }),
  }))
}

/** 漏斗五段计数（new/engaged/quoted/won/lost + pending_human） */
export interface SalesFunnelResp extends AdminResp {
  counts?: Record<string, number>
}

export async function salesFunnelCounts(): Promise<SalesFunnelResp> {
  return bizResp(() => request('/api/admin/sales/funnel', { headers: authHeaders() }))
}

/** 收件箱事件行（列表排障面；单条查询在 salesInbox.ts） */
export interface SalesInboxEventRow {
  id: number
  event_id: string
  one_id: string
  topic: string
  status: string
  created_at: string
  processed_at: string
}

export interface SalesInboxListResp extends AdminResp {
  rows?: SalesInboxEventRow[]
}

/** 收件箱事件分页（status 空＝全部） */
export async function salesInboxList(status: string, limit = 100): Promise<SalesInboxListResp> {
  const q = new URLSearchParams()
  if (status) q.set('status', status)
  q.set('limit', String(limit))
  return bizResp(() => request(`/api/admin/sales/inbox?${q.toString()}`, { headers: authHeaders() }))
}
