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
  mode?: string
  /** ★ 工单双模式（2026-09-13）：restore 还原文件模式（默认）/ text 纯文案模式 */
  delivery?: string
  /** ★ 纯文案 .md 产物路径（还原模式兜底附加物 / 纯文案模式主产物） */
  text_result_path?: string
  /** ★ 改造 4（2026-09-17）：质检存疑标记 1=有语言评估总分低于阈值，需人工复核（列表徽标数据源） */
  quality_flagged?: number
  /** ★ 改造 5：确定性质检 error 级问题数（列表徽标数据源，由 runQA 落列） */
  qa_errors?: number
  /** ★ 改造 5：确定性质检 warning 级提示数 */
  qa_warnings?: number
  created_at: string
  updated_at: string
}

/** 单条质检问题（对应后端 qa.Issue，改造 5 用户侧透出） */
export interface QAReportIssue {
  /** 目标语言代码 */
  lang: string
  /** 规则名：empty/same/number/placeholder/length/punctuation */
  rule: string
  /** 级别：error（已自动重译后仍存在）/ warning */
  level: 'error' | 'warning' | string
  /** 人读说明 */
  detail: string
}

/** 确定性质检报告（对应后端 qa.Report） */
export interface QAReport {
  /** error 级问题数 */
  errors: number
  /** warning 级问题数 */
  warnings: number
  /** 问题明细（后端上限 50 条） */
  issues?: QAReportIssue[]
  /** 无 error 视为通过 */
  pass: boolean
}

/** ★ 改造 4/5：工单质量视图（详情接口 quality 字段） */
export interface TicketQuality {
  /** 确定性质检报告（空译文/同文/数字/占位符/漏翻/标点） */
  qa_report?: QAReport | null
  /** 语言 → 初翻评估总分（0-100） */
  eval_scores?: Record<string, number>
  /** 语言 → 校对评估总分 */
  review_eval_scores?: Record<string, number>
  /** 评估低于阈值语言（「质检存疑」徽标数据源） */
  quality_flagged_langs?: string[]
}

/** 工单接口统一响应结构：tickets 列表/ticket 单对象/states 流程状态/ files 结果文件 */
export interface TicketResp {
  success: boolean
  message?: string
  tickets?: Ticket[]
  ticket?: Ticket
  states?: unknown[]
  files?: { id: number; file_name: string; result_path: string; text_result_path?: string; error: string }[]
  /** ★ 改造 4/5：详情接口附带的质量视图（列表接口不返回） */
  quality?: TicketQuality
}

/** 获取工单列表（mine=true 仅查看自己创建的） */

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
export async function ticketCreate(data: { title: string; source_text: string; target_langs: string; mode?: string; max_length?: number }): Promise<TicketResp> {
  return request('/api/tickets/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 运行工单（异步入队执行五步编排） */
export async function ticketRun(id: number): Promise<TicketResp> {
  return request('/api/tickets/run', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 获取工单详情（含步骤状态轨迹；并附带 QA 质量视图 quality 字段，列表接口 myTickets 不返回该字段） */
export async function ticketDetail(id: number): Promise<TicketResp> {
  return request(`/api/tickets/detail?id=${id}`, { headers: authHeaders() })
}

/** 下载工单结果文件（fetch→blob 触发保存，需鉴权头）；fmt='text' 仅下载译文纯文案(.md) */
export async function ticketDownload(id: number, opts?: { fmt?: 'text'; fileId?: number }): Promise<void> {
  let url = `${API_BASE}/api/tickets/download?id=${id}`
  if (opts?.fmt) url += `&fmt=${encodeURIComponent(opts.fmt)}`
  if (opts?.fileId) url += `&file_id=${opts.fileId}`
  const r = await fetch(url, { headers: authHeaders() })
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch {}
    throw new Error(msg)
  }
  // 从 Content-Disposition 提取文件名；无则用默认名
  const cd = r.headers.get('Content-Disposition') || ''
  const m = cd.match(/filename="?([^";]+)"?/)
  const blob = await r.blob()
  const url2 = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url2
  a.download = m ? m[1] : `ticket_${id}.xlsx`
  a.click()
  URL.revokeObjectURL(url2)
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
 * 文件工单创建：multipart 上传，≤40MB；支持 docx/xlsx/pptx/pdf/txt/csv。
 * 支持多文件（共享 40MB 上限）；mode 透传后端避免被静默吞掉。
 * delivery：restore 还原文件模式（默认）/ text 纯文案模式（anydoc 提取，交付译文 .md，
 * 额外准入 doc/xls/ppt/odt/ods/odp/rtf/epub 等老格式）。
 */
export async function ticketCreateFile(files: File | File[], meta: { title: string; target_langs: string; mode?: string; max_length?: number; delivery?: string }): Promise<TicketResp> {
  const fd = new FormData()
  const list = Array.isArray(files) ? files : [files]
  for (const f of list) fd.append('files', f)
  fd.append('title', meta.title)
  fd.append('target_langs', meta.target_langs)
  // ★ 整改 C2：此前 meta.mode 被收下但从不发送——文件工单永远按默认 pro 全流水线
  //   计费执行，用户选的「快速」被静默吞掉；后端经 FormValue("mode") 读取。
  if (meta.mode) fd.append('mode', meta.mode)
  // ★ 缩翻（任务7）：最长字符限制随表单透传后端（>0 启用缩翻）
  if (meta.max_length && meta.max_length > 0) fd.append('max_length', String(meta.max_length))
  // ★ 工单双模式（2026-09-13）：交付方式透传（restore/text）
  if (meta.delivery) fd.append('delivery', meta.delivery)
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

/** getSegmentsByKey 按 ID 或工单号读取逐段对照（后端兼容双标识解析） */
export async function getSegmentsByKey(ticketKey: string, lang: string): Promise<SegmentsResp> {
  return request(`/api/tickets/segments?id=${encodeURIComponent(ticketKey)}&lang=${encodeURIComponent(lang)}`, { headers: authHeaders() })
}

/** saveSegments 保存逐段编辑/通过/驳回批注 */
export async function saveSegments(ticketId: number, lang: string, edits: SegmentEdit[]): Promise<AdminResp> {
  return request(`/api/tickets/segments/save?id=${ticketId}&lang=${encodeURIComponent(lang)}`, {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ edits }),
  })
}
