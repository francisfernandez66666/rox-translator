// ============================================================================
// api/tmreview.ts — TM 自闭环审核台接口（仅超管）
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

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

export async function listTmReview(status = ''): Promise<AdminResp & { candidates?: TmReviewItem[] }> {
  return request(`/api/admin/tm-review/list${status ? '?status=' + encodeURIComponent(status) : ''}`, { headers: authHeaders() })
}
export async function approveTmReview(id: number): Promise<AdminResp> {
  return request('/api/admin/tm-review/approve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
export async function rejectTmReview(id: number): Promise<AdminResp> {
  return request('/api/admin/tm-review/reject', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
export async function adoptFeedbackTranslation(feedbackId: number, zh: string, lang: string, trans: string): Promise<AdminResp> {
  return request('/api/admin/tm-review/adopt', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ feedback_id: feedbackId, zh, lang, trans }) })
}
