// ============================================================================
// api/feedback.ts — 用户反馈域接口
// 职责：前台翻译结果反馈提交 + 超管反馈列表/处理
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// ============ 本文件职责中文说明 ============
// 封装前台翻译反馈提交与超管反馈列表/处理/回复等接口
// ========================================

/** 反馈记录结构：含目标类型/原文/译文/状态等字段 */
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

/** 反馈 BBS 回复线程元素：含用户/角色/内容/时间 */
export interface FeedbackReply {
  u: number
  name: string
  role: string
  content: string
  at: string
}

/** 反馈记录（列表视图，含回复线程与状态） */
export interface FeedbackRecord {
  id: number
  user_id: number
  user_name: string
  target_type: string
  ticket_id: number
  content: string
  target_langs: string
  mode: string
  with_context: boolean
  source_text: string
  status: string            // open=反馈中 | resolved=已完成
  replies: FeedbackReply[]
  created_at: string
  handled_at: string
}

// feedbackList 反馈列表（角色化）：超管=全部，其他用户=本人提交。
export async function feedbackList(status = ''): Promise<AdminResp & { feedbacks?: FeedbackRecord[] }> {
  return request(`/api/feedback/list${status ? '?status=' + encodeURIComponent(status) : ''}`, { headers: authHeaders() })
}

// feedbackReply 追加回复（超管或提交者本人；已完成禁止）。
export async function feedbackReply(id: number, content: string): Promise<AdminResp & { replies?: FeedbackReply[] }> {
  return request('/api/feedback/reply', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, content }),
  })
}
