// ============================================================================
// api/tickets.ts — 工单与审批域接口
// 职责：翻译工单 CRUD、运行流程、详情、审批（批准/驳回）
// ============================================================================

import { request, authHeaders, API_BASE, type AdminResp } from './core'

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

// TicketResp 工单接口统一响应结构：tickets 为列表、ticket 为单个对象、states 为流程状态历史。
export interface TicketResp {
  success: boolean
  message?: string
  tickets?: Ticket[]
  ticket?: Ticket
  states?: unknown[]
  files?: { id: number; file_name: string; result_path: string; error: string }[]
}

// 工单列表（mine=true 仅查看自己的）
export async function ticketList(mine?: boolean): Promise<TicketResp> {
  return request(`/api/tickets${mine ? '?mine=1' : ''}`, { headers: authHeaders() })
}

// 创建翻译工单

// 运行工单翻译流程

// 工单详情

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
// ==================== 异步工单（队列模式）+ 通知中心 ====================

// 我的工单列表（隐私隔离：非超管仅返回自己创建的）
export async function myTickets(): Promise<TicketResp> {
  return request('/api/tickets', { headers: authHeaders() })
}

// 创建文本翻译工单（入队即返回 ticket_no）
export async function ticketCreate(data: { title: string; source_text: string; target_langs: string; mode?: string }): Promise<TicketResp> {
  return request('/api/tickets/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 运行工单（异步入队执行五步编排）
export async function ticketRun(id: number): Promise<TicketResp> {
  return request('/api/tickets/run', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 工单详情（含步骤状态轨迹）
export async function ticketDetail(id: number): Promise<TicketResp> {
  return request(`/api/tickets/detail?id=${id}`, { headers: authHeaders() })
}

// 工单结果下载地址（需带鉴权头，用 fetch→blob 触发保存）
export async function ticketDownload(id: number): Promise<void> {
  const r = await fetch(`${API_BASE}/api/tickets/download?id=${id}`, { headers: authHeaders() })
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch {}
    throw new Error(msg)
  }
  // 从 Content-Disposition 提取文件名；无则用默认名
  const cd = r.headers.get('Content-Disposition') || ''
  const m = cd.match(/filename="?([^";]+)"?/)
  const blob = await r.blob()
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = m ? m[1] : `ticket_${id}.xlsx`
  a.click()
  URL.revokeObjectURL(url)
}

// 我的站内信列表
export async function notifications(): Promise<AdminResp> {
  return request('/api/notifications', { headers: authHeaders() })
}

// 未读数量
export async function notificationsUnread(): Promise<AdminResp> {
  return request('/api/notifications/unread', { headers: authHeaders() })
}

// 标记单条已读
export async function notificationRead(id: number): Promise<AdminResp> {
  return request('/api/notifications/read', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 全部已读
export async function notificationsReadAll(): Promise<AdminResp> {
  return request('/api/notifications/read-all', { method: 'POST', headers: authHeaders(), body: JSON.stringify({}) })
}

// 文件工单创建（multipart 上传，≤10MB；支持 docx/xlsx/pptx/pdf/txt/csv）
// 文件建单：支持多文件（files 字段可重复），共享 10MB 上限
export async function ticketCreateFile(files: File | File[], meta: { title: string; target_langs: string; mode?: string }): Promise<TicketResp> {
  const fd = new FormData()
  const list = Array.isArray(files) ? files : [files]
  for (const f of list) fd.append('files', f)
  fd.append('title', meta.title)
  fd.append('target_langs', meta.target_langs)
  // ★ 整改 C2：此前 meta.mode 被收下但从不发送——文件工单永远按默认 pro 全流水线
  //   计费执行，用户选的「快速」被静默吞掉；后端经 FormValue("mode") 读取。
  if (meta.mode) fd.append('mode', meta.mode)
  return request('/api/tickets/create-file', { method: 'POST', headers: authHeaders(), body: fd })
}

/** ticketDelete 删除已完成工单及其关联文件 */
export async function ticketDelete(id: number): Promise<AdminResp> {
  return request("/api/tickets/delete", { method: "POST", headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** ticketCancel 取消工单（排队中/翻译中；仅创建者或超管） */
export async function ticketCancel(id: number): Promise<AdminResp> {
  return request("/api/tickets/cancel", { method: "POST", headers: authHeaders(), body: JSON.stringify({ id }) })
}
