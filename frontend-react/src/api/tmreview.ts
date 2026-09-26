// ============================================================================
// api/tmreview.ts — TM 自闭环审核接口（超管审核台 + 租户侧只读进度）
// ============================================================================

/**
 * api/tmreview.ts · 职责说明
 * 封装翻译记忆（TM）自闭环审核相关接口，包括：
 * - 待审列表：获取待审核的翻译记忆候选条目
 * - 审核操作：通过（落库为正式翻译记忆）或驳回（废弃不落库）
 * - 反馈采纳：从用户反馈提取修正译文生成待审候选
 * ★ F-62（2026-09-26 批 I-8）新增租户侧**只读**进度视图 listMyTmReview()：
 *   前台导入双语/TMX 后回执说的是「已提交，待平台审核」，但此前只有超管看得到审核台，
 *   租户永远查不到去向 ⇒ 补本租户裁剪的三态进度（后端按 token 内租户过滤，前端不传租户号）。
 */

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** TM 待审池候选条目（对应后端 store.TmReview） */
export interface TmReviewItem {
  id: number
  tenant_id: number
  zh: string
  lang: string
  trans: string
  source: string      // bitext | tmx | hit_threshold | feedback
  ref_type: string
  ref_id: number
  hit_count: number
  status: string      // pending | approved | rejected
  reviewer: string
  reviewed_at: string
  created_at: string
}

/** 拉取 TM 待审候选列表（status=pending/approved/rejected，空=全部） */
export async function listTmReview(status = ''): Promise<AdminResp & { candidates?: TmReviewItem[] }> {
  return bizResp(() => request(`/api/admin/tm-review/list${status ? '?status=' + encodeURIComponent(status) : ''}`, { headers: authHeaders() }))
}
/** 审核通过：候选条目落库为正式翻译记忆（tm_segments, module=manual） */
export async function approveTmReview(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/tm-review/approve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}
/** 驳回候选条目（废弃不落库） */
export async function rejectTmReview(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/tm-review/reject', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}
/** 反馈修正采纳：从用户反馈提取修正译文生成待审候选 */
export async function adoptFeedbackTranslation(feedbackId: number, zh: string, lang: string, trans: string): Promise<AdminResp> {
  return request('/api/admin/tm-review/adopt', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ feedback_id: feedbackId, zh, lang, trans }) })
}

// ============ ★ F-62（2026-09-26 批 I-8）租户侧只读进度视图 ============

/** 租户侧候选条目：后端按 token 内租户裁剪，**不含** reviewer / tenant_id / 工单主键等平台侧字段 */
export interface MyTmReviewItem {
  id: number
  zh: string
  lang: string
  trans: string
  source: string // bitext | tmx | hit_threshold | feedback
  status: string // pending | approved | rejected
  hit_count: number
  created_at: string
  reviewed_at: string
}

/** 本租户三态真计数（列表受 200 条上限截断时，摘要仍是全量口径） */
export interface MyTmReviewSummary {
  pending: number
  approved: number
  rejected: number
  total: number
}

/** 租户侧列表出参（truncated=true 表示候选多于一次回看的 200 条） */
export type MyTmReviewResp = AdminResp & {
  candidates?: MyTmReviewItem[]
  summary?: MyTmReviewSummary
  truncated?: boolean
}

/**
 * 拉取本租户的 TM 候选审核进度（只读；status='' 表示全部）。
 * 说明：接口不带任何租户参数——租户由后端从登录态取（F-55 的教训：读写同源、头不能换租户）。
 */
export async function listMyTmReview(status = ''): Promise<MyTmReviewResp> {
  return request(`/api/me/tm-review/list${status ? '?status=' + encodeURIComponent(status) : ''}`, { headers: authHeaders() })
}
