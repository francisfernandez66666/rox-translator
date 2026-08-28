// ============================================================================
// api/tmreview.ts — TM 自闭环审核台接口（仅超管）
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// ============ 本文件职责中文说明 ============
// 封装 TM 自闭环审核台(待审列表/通过/驳回/采纳反馈)等接口
// ========================================

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
  return request(`/api/admin/tm-review/list${status ? '?status=' + encodeURIComponent(status) : ''}`, { headers: authHeaders() })
}
/** 审核通过：候选条目落库为正式翻译记忆（tm_segments, module=manual） */
export async function approveTmReview(id: number): Promise<AdminResp> {
  return request('/api/admin/tm-review/approve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
/** 驳回候选条目（废弃不落库） */
export async function rejectTmReview(id: number): Promise<AdminResp> {
  return request('/api/admin/tm-review/reject', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
/** 反馈修正采纳：从用户反馈提取修正译文生成待审候选 */
export async function adoptFeedbackTranslation(feedbackId: number, zh: string, lang: string, trans: string): Promise<AdminResp> {
  return request('/api/admin/tm-review/adopt', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ feedback_id: feedbackId, zh, lang, trans }) })
}
