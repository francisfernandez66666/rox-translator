// ============================================================================
// api/feedback.ts — 用户反馈域接口
// 职责：前台翻译结果反馈提交 + 超管反馈列表/处理
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// Feedback 反馈记录结构
export interface FeedbackItem {
  id: number
  tenant_id: number
  user_id: number
  target_type: string   // text | ticket
  ticket_id: number
  source_text: string
  translations: string  // JSON 字符串（语言→译文）
  target_langs: string
  mode: string          // fast | pro
  content: string
  with_context: boolean
  status: string        // open | resolved
  handle_note: string
  created_at: string
  handled_at: string
}

// createFeedback 提交翻译反馈（文本气泡/工单详情入口）。
export async function createFeedback(payload: {
  target_type: string
  ticket_id?: number
  content: string
  with_context?: boolean
  source_text?: string
  translations?: Record<string, string>
  target_langs?: string
  mode?: string
}): Promise<AdminResp> {
  return request('/api/feedback', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(payload),
  })
}

// adminFeedbacks 超管查询反馈列表（status=open|resolved|空=全部）。
export async function adminFeedbacks(status = ''): Promise<AdminResp & { feedbacks?: FeedbackItem[] }> {
  return request(`/api/admin/feedbacks${status ? '?status=' + encodeURIComponent(status) : ''}`, { headers: authHeaders() })
}

// resolveFeedback 超管标记已处理并附备注。
export async function resolveFeedback(id: number, note = ''): Promise<AdminResp> {
  return request('/api/admin/feedbacks/resolve', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, note }),
  })
}
