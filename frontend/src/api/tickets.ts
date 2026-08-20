// ============================================================================
// api/tickets.ts — 工单与审批域接口
// 职责：翻译工单 CRUD、运行流程、详情、审批（批准/驳回）
// ============================================================================

import { request, authHeaders } from './core'

// 翻译工单信息
export interface Ticket {
  id: number
  tenant_id: number
  ticket_no: string
  title: string
  status: string
  source_text: string
  file_path: string
  target_langs: string
  created_by: number
  approver_id: number
  reviewer_id: number
  reject_reason: string
  final_result: string
  created_at: string
  updated_at: string
}

export interface TicketResp {
  success: boolean
  message?: string
  tickets?: Ticket[]
  ticket?: Ticket
  states?: unknown[]
}

// 工单列表（mine=true 仅查看自己的）
export async function ticketList(mine?: boolean): Promise<TicketResp> {
  return request(`/api/tickets${mine ? '?mine=1' : ''}`, { headers: authHeaders() })
}

// 创建翻译工单
export async function ticketCreate(data: { title: string; source_text: string; target_langs: string }): Promise<TicketResp> {
  return request('/api/tickets/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 运行工单翻译流程
export async function ticketRun(id: number): Promise<TicketResp> {
  return request('/api/tickets/run', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 工单详情
export async function ticketDetail(id: number): Promise<TicketResp> {
  return request(`/api/tickets/detail?id=${id}`, { headers: authHeaders() })
}

// ==================== 审批 ====================

// 待审批工单列表
export async function approveList(): Promise<TicketResp> {
  return request('/api/approve/list', { headers: authHeaders() })
}

// 审批操作：批准或驳回（附原因/建议/审定译文）
export async function approveAction(id: number, action: 'approve' | 'reject', reason: string, suggestion: string, approvedText: string): Promise<TicketResp> {
  return request('/api/approve/action', {
    method: 'POST', headers: authHeaders(),
    body: JSON.stringify({ id, action, reason, suggestion, approved_text: approvedText }),
  })
}