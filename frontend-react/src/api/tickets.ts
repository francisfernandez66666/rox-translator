// ============================================================================
// api/tickets.ts — 工单与审批域接口
// 职责：翻译工单 CRUD、运行流程、详情、审批（批准/驳回）
// ============================================================================

/**
 * api/tickets.ts · 职责说明
 * 封装翻译工单与审批相关的所有接口，包括：
 * - 工单管理：创建文本/文件工单、运行流程、查看详情、删除、取消
 * - 审批流程：获取待审批列表、批准或驳回工单
 * - 结果下载：下载工单翻译结果文件
 * - 对照编辑：逐段对照编辑、保存编辑结果
 * - 通知中心：站内信列表、未读数量、标记已读
 */

import { request, authHeaders, API_BASE, type AdminResp } from './core'

/** 翻译工单信息结构：含编号/标题/状态/原文/目标语言/审批人等 */
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

/** 工单接口统一响应结构：tickets 列表/ticket 单对象/states 流程状态/ files 结果文件 */
export interface TicketResp {
  success: boolean
  message?: string
  tickets?: Ticket[]
  ticket?: Ticket
  states?: unknown[]
  files?: { id: number; file_name: string; result_path: string; error: string }[]
}

/** 获取工单列表（mine=true 仅查看自己创建的） */
export async function ticketList(mine?: boolean): Promise<TicketResp> {
  return request(`/api/tickets${mine ? '?mine=1' : ''}`, { headers: authHeaders() })
}

// 创建翻译工单

// 运行工单翻译流程

// 工单详情

// ==================== 审批 ====================

/** 获取待审批工单列表 */
export async function approveList(): Promise<TicketResp> {
  return request('/api/approve/list', { headers: authHeaders() })
}

/** 审批操作：批准或驳回（附原因/建议/审定译文） */
export async function approveAction(id: number, action: 'approve' | 'reject', reason: string, suggestion: string, approvedText: string): Promise<TicketResp> {
  return request('/api/approve/action', {
    method: 'POST', headers: authHeaders(),
    body: JSON.stringify({ id, action, reason, suggestion, approved_text: approvedText }),
  })
}
// ==================== 异步工单（队列模式）+ 通知中心 ====================

/** 我的工单列表（隐私隔离：非超管仅返回自己创建的） */
export async function myTickets(): Promise<TicketResp> {
  return request('/api/tickets', { headers: authHeaders() })
}

/** 创建文本翻译工单（入队即返回 ticket_no） */
export async function ticketCreate(data: { title: string; source_text: string; target_langs: string; mode?: string }): Promise<TicketResp> {
  return request('/api/tickets/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 运行工单（异步入队执行五步编排） */
export async function ticketRun(id: number): Promise<TicketResp> {
  return request('/api/tickets/run', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 获取工单详情（含步骤状态轨迹） */
export async function ticketDetail(id: number): Promise<TicketResp> {
  return request(`/api/tickets/detail?id=${id}`, { headers: authHeaders() })
}

/** 下载工单结果文件（fetch→blob 触发保存，需鉴权头） */
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

/** 获取我的站内信列表 */
export async function notifications(): Promise<AdminResp> {
  return request('/api/notifications', { headers: authHeaders() })
}

/** 获取未读通知数量 */
export async function notificationsUnread(): Promise<AdminResp> {
  return request('/api/notifications/unread', { headers: authHeaders() })
}

/** 标记单条通知已读 */
export async function notificationRead(id: number): Promise<AdminResp> {
  return request('/api/notifications/read', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 标记全部通知已读 */
export async function notificationsReadAll(): Promise<AdminResp> {
  return request('/api/notifications/read-all', { method: 'POST', headers: authHeaders(), body: JSON.stringify({}) })
}

/**
 * 文件工单创建：multipart 上传，≤10MB；支持 docx/xlsx/pptx/pdf/txt/csv。
 * 支持多文件（共享 10MB 上限）；mode 透传后端避免被静默吞掉。
 */
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

// ==================== 对照编辑器（工作流 D） ====================

/** 单段对照 */
export interface EditorSegment {
  index: number
  source: string
  target: string
  edited_text: string
  status: string   // pending / approved / rejected
  note: string
}

/** 对照编辑器读取响应 */
export interface SegmentsResp {
  success: boolean
  message?: string
  ticket_id: number
  lang: string
  langs: string[]
  type: string    // text / file / unsupported
  segments: EditorSegment[]
  terms: string[]
}

/** 保存单段编辑的请求体 */
export interface SegmentEdit {
  index: number
  edited_text: string
  status: string
  note: string
}

/** getSegments 读取工单逐段对照 + 术语表 */
export async function getSegments(ticketId: number, lang: string): Promise<SegmentsResp> {
  return request(`/api/tickets/segments?id=${ticketId}&lang=${encodeURIComponent(lang)}`, { headers: authHeaders() })
}

/** saveSegments 保存逐段编辑/通过/驳回批注 */
export async function saveSegments(ticketId: number, lang: string, edits: SegmentEdit[]): Promise<AdminResp> {
  return request(`/api/tickets/segments/save?id=${ticketId}&lang=${encodeURIComponent(lang)}`, {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ edits }),
  })
}
