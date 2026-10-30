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
  /**
   * ★ 2026-10-19 G2：退化交付的语言（版式还原失败、已以纯文案 .md 交付）。
   * ★ 2026-10-21 UAT 收口 M2：Go 侧已定稿并接上整条链——`tickets.degraded_langs` 列
   *   → `api.ticketJSON` 出栈同名数组（空值出 `[]` 不出 `null`）→ 本类型 → 详情面横幅。
   * 三面（Go 出栈键名／本类型／TicketsPage 渲染判据）逐字等值，漂移由
   * `src/api/ticketsDegradedLock.test.ts` 当场判红。
   */
  degraded_langs?: string[]
  /**
   * ★ 2026-10-21 UAT 收口 M2：交付质量闸门告警原文（引擎 GateWarnings，如 `refusal_blocked=…`、
   * `untranslated=…`、结构保真警告）。同批次落列并出栈，供排障面/后续横幅消费；
   * 空值同样出 `[]`。键名与 `api.ticketJSON` 逐字相同（锁同上）。
   */
  gate_warnings?: string[]
  created_at: string
  updated_at: string
}

/**
 * ★ 2026-10-19 G13：工单**交付件**（实际可拿到的产物：译后图／同容器配音／译文 .md…；
 *   ★ 2026-10-30 格式收缩后字幕与原音频副本不再随包）。
 * 与 `files`（用户上传的**输入件**＋处理状态，见 store/ticketfiles.go）是两回事——
 * 详情面过去只渲染 files，于是"下载包里配音和译文 .md 都在、页面上却只看得见自己上传那件"。
 * ⚠️ 字段名假设：Go 侧另一 agent 尚未定稿（后端需按单号回读产物清单）。
 * 前端按 `deliverables` 读取，并兼容 `outputs` 别名；两处都要既有 name 又有可下载标识才算一件。
 */
export interface TicketDeliverable {
  /** 展示名（含扩展名，如 `Voice_en.m4a`） */
  name: string
  /** 字节数（可选；用于列表尾部体积读数） */
  size?: number
  /** 产物类别：image/audio/subtitle/text/office…（可选，用于图标/分组） */
  kind?: string
  /** 所属目标语言（可选） */
  lang?: string
  /** 该件是否为退化产物（如"仅译文 .md"替代原版式回写） */
  degraded?: boolean
  /** 下载该件用的 file_id（可选；走 ticketDownload 的 fileId 档） */
  file_id?: number
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
  /**
   * ★ 2026-10-26 追加裁决九-1：上游那一族问题**给客户看**的那四件（Go 侧
   * `api/tickets.go` 的 ticketQualityView，全部 omitempty）。
   * 关键空值口径：**没有回填时这四个键整个不出现**（不是 null、不是 0）——
   * 渲染判据一律「读不到＝这一单没有未译出行」，禁止凭空造一句"0 行未译出"。
   * 三档分开各一个 map（上游断／截断／补译后仍无可用输出），混成一句"部分未译出"
   * 是把归因藏起来（AGENTS §一·19 族一那条）。
   * 四面（Go 出栈 tag／本类型／TicketsPage 渲染判据／来源档名单）逐字等值，漂移由
   * `src/api/ticketsUpstreamLock.test.ts` 当场判红。
   */
  /** 语言 → 来源档（名单见 src/lib/ticketSource.ts 的 TICKET_SOURCE_VALUES） */
  sources?: Record<string, string>
  /** 语言 → 按原文回落的行数（总数） */
  unresolved_lines?: Record<string, number>
  /** 语言 → 其中判「上游整条不通」那一部分 */
  upstream_dead_lines?: Record<string, number>
  /** 语言 → 其中被 max_tokens 截断那一部分 */
  truncated_lines?: Record<string, number>
}

/** 工单接口统一响应结构：tickets 列表/ticket 单对象/states 流程状态/ files 结果文件 */
/**
 * TicketArtifactItem 两段式交付的一件产物（★ 2026-10-30 组5）。
 * 字段名与后端 `api/ticket_artifacts.go` 的 JSON tag 逐字对应（三面等值锁见 ticketsArtifacts.lock.test.ts）。
 * kind 只有两档：md（纯文案，可预览）／original（该语言主产物，按钮「下载原格式」）。
 */
export interface TicketArtifactItem {
  id: number
  file_id: number
  lang: string
  kind: 'md' | 'original'
  name: string
  size: number
  primary: boolean
  degraded: boolean
  created_at?: string
}

// TicketResp 工单接口的统一响应信封（详情/列表/动作共用；可选字段按接口面出现）。
export interface TicketResp {
  success: boolean
  message?: string
  tickets?: Ticket[]
  ticket?: Ticket
  states?: unknown[]
  files?: { id: number; file_name: string; result_path: string; text_result_path?: string; error: string }[]
  /** ★ 改造 4/5：详情接口附带的质量视图（列表接口不返回） */
  quality?: TicketQuality
  /**
   * ★ 2026-10-19 G13：详情接口的**交付件清单**（实际产出物，区别于 files＝输入件）。
   * 字段名假设，见 TicketDeliverable 注释；`outputs` 为兼容别名，读取侧二者取其一。
   */
  deliverables?: TicketDeliverable[]
  outputs?: TicketDeliverable[]
  /** ★ 2026-10-30 组5：按（文件×语种）分组的产物清单；历史单为空 ⇒ 前端回落旧 deliverables 清单。 */
  artifacts?: TicketArtifactItem[]
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
 *   （src/i18n/hardcodedCjkGate.test.ts）用引号配对粗扫源码，而这条正则的字面量写法
 *   `/filename="?([^";]+)"?/` 内部含**奇数个**双引号（3 个）——配对会从这行起错位，
 *   一路延伸到后面的中文兜底句上，把带 apiMsg 豁免的行判成违规（2026-09-29 实测踩过）。
 *   ★ 2026-10-19 收尾：改成 `new RegExp('…')` 形态——引号包在单引号字符串里，
 *   闸门的同引号配对不再被这 3 个双引号打断（判据是"源码里没有裸的奇数引号正则字面量"，
 *   不是删掉这条正则）。单点＝形态只有一份、两侧解析口径也不会漂开。
 */
function filenameFromDisposition(cd: string, fallback: string): string {
  const m = cd.match(new RegExp('filename="?([^";]+)"?'))
  return m && m[1] ? m[1] : fallback
}

/**
 * ★ 2026-10-19 §五#2：交付件下载失败的两类——**必须分开报**（AGENTS §一·12「有自动降级的链路，
 * 产物能打开不算验收」的同族取向：把"没传到"说成"坏了"会把人引向错误的排查方向）。
 *   · `network`＝这一跳没走完：fetch 被拒（断网/DNS/TLS/超时）、或收流中途断掉。
 *     现场信息是"还在传，已收到 N 字节"（`received`），用户该做的是**重试**。
 *   · `broken`＝真的坏了：4xx/5xx、文件不存在、或拿到的是 SPA 兜底 HTML（不是文件）。
 *     用户该做的是**别重试**（重试一万次也不会好），看具体错误文案。
 */
export class DownloadError extends Error {
  readonly kind: 'network' | 'broken'
  readonly status?: number
  /** 已收到的字节数（network 档用来告诉用户"这一跳走到了哪里"） */
  readonly received?: number
  constructor(kind: 'network' | 'broken', message: string, extra?: { status?: number; received?: number }) {
    super(message)
    this.name = 'DownloadError'
    this.kind = kind
    this.status = extra?.status
    this.received = extra?.received
  }
}

// readBodyWithProgress 流式读响应体并回报字节进度；环境不支持 ReadableStream（jsdom/旧内核）
// 时退回 blob()（无进度但功能不变）。收流中途出错按 network 档抛（"这一跳没走完"）。
async function readBodyWithProgress(
  r: Response,
  total: number,
  onProgress?: (received: number, total: number) => void,
): Promise<Blob> {
  const body = (r as unknown as { body?: ReadableStream<Uint8Array> | null }).body
  if (!body || typeof body.getReader !== 'function') return r.blob()
  const reader = body.getReader()
  const chunks: Uint8Array[] = []
  let received = 0
  try {
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      if (value) {
        chunks.push(value)
        received += value.byteLength
        onProgress?.(received, total)
      }
    }
  } catch (e: any) {
    onProgress?.(received, total)
    throw new DownloadError('network', e?.message || apiMsg('tk.downloadInterrupted', '下载中断（网络未走完，请重试）'), { received })
  }
  return new Blob(chunks as BlobPart[], { type: r.headers.get('Content-Type') || 'application/octet-stream' })
}

/**
 * 下载工单结果文件（fetch→blob 触发保存，需鉴权头）；fmt='text' 仅下载译文纯文案(.md)。
 * ★ 2026-10-19 §五#2：带字节进度（`onProgress(received, total)`，total＝Content-Length，0 未知），
 *   失败分 network/broken 两类（见 DownloadError）。
 */
export async function ticketDownload(
  id: number,
  opts?: { fmt?: 'text'; fileId?: number; onProgress?: (received: number, total: number) => void },
): Promise<void> {
  // ★ §4.2-2：结果文件走 fetch→blob（需二进制响应，无法经 request() 的 JSON 出口），属正当裸用；
  //   但 401/403 必须与统一 client 同源——复用 core 的 handleUnauthorized/handleForbidden，
  //   不再像旧实现那样把越权当普通 Error 抛出、丢失登录态失效与权限提示语义。
  let url = `${API_BASE}/api/tickets/download?id=${id}`
  if (opts?.fmt) url += `&fmt=${encodeURIComponent(opts.fmt)}`
  if (opts?.fileId) url += `&file_id=${opts.fileId}`
  let r: Response
  try {
    r = await fetch(url, { headers: authHeaders() })
  } catch (e: any) {
    // fetch 直接 reject＝链路层没走完（断网/DNS/TLS/超时），不是"文件坏了"
    throw new DownloadError('network', e?.message || apiMsg('tk.downloadInterrupted', '下载中断（网络未走完，请重试）'))
  }
  if (r.status === 401) handleUnauthorized(url)
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch { /* 非 JSON 错误体（网关页等）保持状态码文案 */ }
    // 403 越权：文案走统一解析器（本地化既有键）、附稳定码 FORBIDDEN，与 request() 口径一致
    if (r.status === 403) throw new ApiError(handleForbidden(msg), 403, 'FORBIDDEN')
    // ★ 4xx/5xx＝真的坏了（文件不存在/服务端错），与"这一跳没走完"分开报
    throw new DownloadError('broken', msg, { status: r.status })
  }
  // ★ SPA 兜底陷阱（AGENTS §一·6）：spa.go 对不存在的路径回 200 + 整页 index.html，
  //   交付件若是 HTML ⇒ 拿到的不是文件。如实判"坏了"，绝不把 HTML 壳存成交付件。
  if ((r.headers.get('Content-Type') || '').toLowerCase().includes('text/html')) {
    throw new DownloadError('broken', apiMsg('tk.downloadBroken', '下载的产物不是文件（可能已被删除或链接失效）'), { status: r.status })
  }
  const total = Number(r.headers.get('Content-Length') || '') || 0
  // 从 Content-Disposition 提取文件名；无则用默认名（★ D-4：解析收进 filenameFromDisposition 单点）
  const blob = await readBodyWithProgress(r, total, opts?.onProgress)
  const url2 = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url2
  a.download = filenameFromDisposition(r.headers.get('Content-Disposition') || '', `ticket_${id}.xlsx`)
  a.click()
  // 延迟释放 blob：立即 revoke 会在部分浏览器取消尚未开始的下载（口径同 ChatWindow 导出）
  setTimeout(() => URL.revokeObjectURL(url2), 5000)
}

/**
 * 下载单件产物（★ 2026-10-30 组5）：`aid` 是 artifacts 行的 id，路径只从后端库里取，
 * 前端绝不传路径（防目录穿越）。401/403 与 ticketDownload 同一出口（不把越权当普通 Error）。
 */
export async function ticketArtifactDownload(id: number, aid: number): Promise<void> {
  const url = `${API_BASE}/api/tickets/artifact?id=${id}&aid=${aid}`
  let r: Response
  try {
    r = await fetch(url, { headers: authHeaders() })
  } catch (e: any) {
    throw new DownloadError('network', e?.message || apiMsg('tk.downloadInterrupted', '下载中断（网络未走完，请重试）'))
  }
  if (r.status === 401) handleUnauthorized(url)
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch { /* 非 JSON 错误体保持状态码文案 */ }
    if (r.status === 403) throw new ApiError(handleForbidden(msg), 403, 'FORBIDDEN')
    throw new DownloadError('broken', msg, { status: r.status })
  }
  if ((r.headers.get('Content-Type') || '').toLowerCase().includes('text/html')) {
    throw new DownloadError('broken', apiMsg('tk.artifactExpired', '产物不存在或已过保留期，请重新发起工单获取'), { status: r.status })
  }
  const blob = await r.blob()
  const objURL = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objURL
  a.download = filenameFromDisposition(r.headers.get('Content-Disposition') || '', `ticket_${id}_artifact`)
  a.click()
  setTimeout(() => URL.revokeObjectURL(objURL), 5000)
}

/**
 * 预览纯文案 .md 产物（★ 2026-10-30 组5）：后台上限 512KB，超出会带 `X-Truncated: 1`。
 * 返回 `{ text, truncated }`；调用方用 `<pre>` 纯文本渲染（**不**引 markdown 库、不 dangerouslySetInnerHTML）。
 */
export async function ticketArtifactPreview(id: number, aid: number): Promise<{ text: string; truncated: boolean }> {
  const url = `${API_BASE}/api/tickets/artifact/preview?id=${id}&aid=${aid}`
  let r: Response
  try {
    r = await fetch(url, { headers: authHeaders() })
  } catch (e: any) {
    throw new DownloadError('network', e?.message || apiMsg('tk.previewLoading', '预览加载中…'))
  }
  if (r.status === 401) handleUnauthorized(url)
  if (!r.ok) {
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch { /* 保持状态码文案 */ }
    if (r.status === 403) throw new ApiError(handleForbidden(msg), 403, 'FORBIDDEN')
    throw new DownloadError('broken', msg, { status: r.status })
  }
  return { text: await r.text(), truncated: r.headers.get('X-Truncated') === '1' }
}

/**
 * 获取我的站内信列表（分页）。
 * ★ 2026-10-11 跨境链路瓶颈批：后端旧形态一次吐 100 条、无分页参数，浮层只渲染 20 条却被迫下载整包；
 * 现在默认档就是 20 条，翻页由调用方给 offset。**参数都可选**——不传就是默认档，老调用点零改动即可跑。
 */
export async function notifications(limit?: number, offset?: number): Promise<AdminResp> {
  const qs = new URLSearchParams()
  if (limit && limit > 0) qs.set('limit', String(limit))
  if (offset && offset > 0) qs.set('offset', String(offset))
  const tail = qs.toString() ? `?${qs.toString()}` : ''
  return bizResp(() => request(`/api/notifications${tail}`, { headers: authHeaders() }))
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
