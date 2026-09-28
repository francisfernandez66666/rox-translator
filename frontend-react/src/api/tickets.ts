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

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { bizResp, request, authHeaders, API_BASE, handleUnauthorized, handleForbidden, ApiError, apiMsg, type AdminResp } from './core'

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
  return bizResp(() => request('/api/approve/list', { headers: authHeaders() }))
}

/** 审批操作：批准或驳回（附原因/建议/审定译文） */
export async function approveAction(id: number, action: 'approve' | 'reject', reason: string, suggestion: string, approvedText: string): Promise<TicketResp> {
  return bizResp(() => request('/api/approve/action', {
    method: 'POST', headers: authHeaders(),
    body: JSON.stringify({ id, action, reason, suggestion, approved_text: approvedText }),
  }))
}
// ==================== 异步工单（队列模式）+ 通知中心 ====================

/** 我的工单列表（隐私隔离：非超管仅返回自己创建的） */
export async function myTickets(): Promise<TicketResp> {
  return bizResp(() => request('/api/tickets', { headers: authHeaders() }))
}

/** 创建文本翻译工单（入队即返回 ticket_no） */
export async function ticketCreate(data: { title: string; source_text: string; target_langs: string; mode?: string; max_length?: number }): Promise<TicketResp> {
  return bizResp(() => request('/api/tickets/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 运行工单（异步入队执行五步编排） */
export async function ticketRun(id: number): Promise<TicketResp> {
  return bizResp(() => request('/api/tickets/run', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 获取工单详情（含步骤状态轨迹；并附带 QA 质量视图 quality 字段，列表接口 myTickets 不返回该字段） */
export async function ticketDetail(id: number): Promise<TicketResp> {
  return bizResp(() => request(`/api/tickets/detail?id=${id}`, { headers: authHeaders() }))
}

// 分页/下载共用的小工具放在接口旁边（★ D-4 抽出，见函数注释）
/**
 * filenameFromDisposition 从 Content-Disposition 取落盘文件名，取不到用调用方给的兜底名。
 * 为什么抽成单点而不是两侧各写一遍正则：本仓的「写死中文闸门」
 *   （src/i18n/hardcodedCjkGate.test.ts）用引号配对粗扫源码，而这条正则
 *   `/filename="?([^";]+)"?/` 内部含**奇数个**双引号（3 个），同一文件里出现两次就会让配对
 *   错位一路延伸到后面的中文兜底句上，把带 apiMsg 豁免的行判成违规（2026-09-29 实测踩过）。
 *   单点＝形态只有一份、两侧解析口径也不会漂开。
 */
function filenameFromDisposition(cd: string, fallback: string): string {
  const m = cd.match(/filename="?([^";]+)"?/)
  return m && m[1] ? m[1] : fallback
}

/** 下载工单结果文件（fetch→blob 触发保存，需鉴权头）；fmt='text' 仅下载译文纯文案(.md) */
export async function ticketDownload(id: number, opts?: { fmt?: 'text'; fileId?: number }): Promise<void> {
  // ★ §4.2-2：结果文件走 fetch→blob（需二进制响应，无法经 request() 的 JSON 出口），属正当裸用；
  //   但 401/403 必须与统一 client 同源——复用 core 的 handleUnauthorized/handleForbidden，
  //   不再像旧实现那样把越权当普通 Error 抛出、丢失登录态失效与权限提示语义。
  let url = `${API_BASE}/api/tickets/download?id=${id}`
  if (opts?.fmt) url += `&fmt=${encodeURIComponent(opts.fmt)}`
  if (opts?.fileId) url += `&file_id=${opts.fileId}`
  const r = await fetch(url, { headers: authHeaders() })
  if (r.status === 401) handleUnauthorized(url)
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch {}
    // 403 越权：文案走统一解析器（本地化既有键）、附稳定码 FORBIDDEN，与 request() 口径一致
    if (r.status === 403) throw new ApiError(handleForbidden(msg), 403, 'FORBIDDEN')
    throw new Error(msg)
  }
  // 从 Content-Disposition 提取文件名；无则用默认名（★ D-4：解析收进 filenameFromDisposition 单点）
  const blob = await r.blob()
  const url2 = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url2
  a.download = filenameFromDisposition(r.headers.get('Content-Disposition') || '', `ticket_${id}.xlsx`)
  a.click()
  // 延迟释放 blob：立即 revoke 会在部分浏览器取消尚未开始的下载（口径同 ChatWindow 导出）
  setTimeout(() => URL.revokeObjectURL(url2), 5000)
}

/** 获取我的站内信列表 */
export async function notifications(): Promise<AdminResp> {
  return bizResp(() => request('/api/notifications', { headers: authHeaders() }))
}

/** 获取未读通知数量 */
export async function notificationsUnread(): Promise<AdminResp> {
  return bizResp(() => request('/api/notifications/unread', { headers: authHeaders() }))
}

/** 标记单条通知已读 */
export async function notificationRead(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/notifications/read', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 标记全部通知已读 */
export async function notificationsReadAll(): Promise<AdminResp> {
  return bizResp(() => request('/api/notifications/read-all', { method: 'POST', headers: authHeaders(), body: JSON.stringify({}) }))
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
  return bizResp(() => request('/api/tickets/create-file', { method: 'POST', headers: authHeaders(), body: fd }))
}

/** ticketDelete 删除已完成工单及其关联文件 */
export async function ticketDelete(id: number): Promise<AdminResp> {
  return request("/api/tickets/delete", { method: "POST", headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** ticketCancel 取消工单（排队中/翻译中；仅创建者或超管） */
export async function ticketCancel(id: number): Promise<AdminResp> {
  return bizResp(() => request("/api/tickets/cancel", { method: "POST", headers: authHeaders(), body: JSON.stringify({ id }) }))
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

/** SegmentsExportResp 回写导出出参：成功带 download 相对路径（★ D-4，2026-09-29 补界面入口） */
export interface SegmentsExportResp extends AdminResp { download?: string }

/**
 * segmentsExport 让后端按逐段编辑稿（edited_text 优先于机翻稿）回写结果 docx，返回下载相对路径。
 * ★ D-4（2026-09-29 全量审计）：后端两支路由（internal/api/editor.go:46-47）自 2026-08 起
 *   只有 scripts/uat/api_uat_txn.sh T56 在消费，界面零入口 ⇒ 审批人在线改过的修订**带不回交付件**，
 *   只能人工抄回原文重排——正是 F-44「修订在交付件里凭空消失」那条链的最后一公里。
 * 返回形状＝{success,download} 信封，且调用点（EditorPage 导出钮）判 resp.success，
 *   故按 AGENTS §一·5 走 bizResp（不是「读取保持抛出」那一档：失败要出后端文案，
 *   面板不会把 {success:false} 当数据载进任何列表）。
 */
export async function segmentsExport(ticketId: number, lang: string): Promise<SegmentsExportResp> {
  return bizResp(() => request(`/api/tickets/segments/export?id=${ticketId}&lang=${encodeURIComponent(lang)}`, {
    method: 'POST',
    headers: authHeaders(),
    body: '{}',
  }))
}

/**
 * editorExportFetch 按 segmentsExport 给的相对路径取回落写稿并触发浏览器下载（★ D-4）。
 * 为什么走 fetch→blob 而不是 <a href>/window.open：`/api/editor/export/download` 先 s.authUser(r)
 *   再做 B8 产物归属校验，**要带 Authorization 头**，导航式下载带不上必然 401；
 *   口径同上方 ticketDownload（§4.2-2 二进制响应属正当裸用，401/403 与统一 client 同源）。
 * ★ 出门前真验字节（AGENTS §一·6 的兜底陷阱）：spa.go 对不存在的路径回 200 + 整页 index.html，
 *   所以「状态码 200」不算拿到文件——docx 是 ZIP，魔数必须是 'PK' 且体积过下限，
 *   否则如实抛错，绝不能把一个 HTML 壳存成 .docx 交给客户。
 * 返回落盘文件名（供调用方提示「已开始下载」）。
 */
export async function editorExportFetch(download: string): Promise<string> {
  const url = `${API_BASE}${download}`
  const r = await fetch(url, { headers: authHeaders() })
  if (r.status === 401) handleUnauthorized(url)
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch { /* 非 JSON 错误体（网关页等）保持 HTTP 状态码文案 */ }
    if (r.status === 403) throw new ApiError(handleForbidden(msg, url), 403, 'FORBIDDEN')
    throw new Error(msg)
  }
  const buf = await r.arrayBuffer()
  const head = new Uint8Array(buf.slice(0, 2))
  if (buf.byteLength < 800 || head[0] !== 0x50 || head[1] !== 0x4b) {
    // 800B 下限与 UAT T56 同口径（真 docx 远大于此）；PK＝0x50 0x4b＝'PK'
    throw new Error(apiMsg('common.exportBadFile', '导出产物校验失败（不是 docx 文件，下载链可能未通）'))
  }
  const name = filenameFromDisposition(r.headers.get('Content-Disposition') || '', 'edited.docx')
  const objUrl = URL.createObjectURL(new Blob([buf], { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }))
  const a = document.createElement('a')
  a.href = objUrl
  a.download = name
  a.click()
  // 延迟释放：立即 revoke 会在部分浏览器取消尚未开始的下载（口径同 ticketDownload）
  setTimeout(() => URL.revokeObjectURL(objUrl), 5000)
  return name
}
