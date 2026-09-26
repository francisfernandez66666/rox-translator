// ============================================================================
// api/kb.ts — 知识库（行业包）域接口
// 职责：行业包 CRUD、包内条目管理、批量导入
// ============================================================================

/**
 * api/kb.ts · 职责说明
 * 封装知识库（行业包）相关的所有接口，包括：
 * - 知识库包管理：创建、更新、删除、启停、跨部门共享
 * - 条目管理：添加、删除、批量导入知识库条目
 * - 文件上传：识别 KB 文件、导入双语语料、TMX 格式导入
 * - 向量索引：手动触发向量索引全量重建
 * - 安全句管理：语言文化规范的增删改查与审核
 */

import { ApiError, bizResp, request, authHeaders, API_BASE, handleUnauthorized, apiMsg, type AdminResp } from './core'

// ★ P1-16 修复（2026-09-14）：raw fetch 统一守卫——旧实现 `.json()` 裸调用不判 resp.ok、
// 不触发 401 拦截，登录过期表现为 JSON 解析异常或静默失败而非跳登录。
// ★ F-64②（批 I-10）：非 2xx 时改抛带**结构化错误体**的 ApiError，与 core.request() 同形。
//   后端这些端点已从「HTTP 200 承载失败」迁成诚实状态码（400/404/409…），旧写法只抛一句
//   「请求失败 (400)」——调用点拿不到后端原文（如「文件无有效数据」），界面从精确报错退化成
//   通用兜底。这里把响应体原样挂到 error.body，外层 bizResp() 即可还原 {success:false,message}。
async function fetchJSON(url: string, init?: RequestInit): Promise<any> {
  const resp = await fetch(url, init)
  if (resp.status === 401) handleUnauthorized(url)
  if (!resp.ok) {
    // 尝试解析后端错误信封；解析不出来（HTML 错误页 / 空体）时 body 留空，bizResp 会原样上抛
    let body: Record<string, unknown> | undefined
    try {
      const raw = await resp.clone().text()
      const parsed = raw ? JSON.parse(raw) : null
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) body = parsed as Record<string, unknown>
    } catch { /* 非 JSON 错误体：保持原通用文案上抛 */ }
    const msgField = body?.message
    const codeField = body?.code
    const fromBody: string = typeof msgField === 'string' ? msgField : ''
    const code: string | undefined = typeof codeField === 'string' ? codeField : undefined
    throw new ApiError(fromBody || apiMsg('common.reqFail', `请求失败 (${resp.status})`, { status: resp.status }), resp.status, code, body)
  }
  return resp.json()
}

/** 获取行业知识库包列表（不带头条目数 entry_count，后端一次 GROUP BY 附带） */
export async function kbPackages(): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-packages', { headers: authHeaders() }))
}

/** 创建行业知识库包 */
export async function kbPackageCreate(data: { code: string; name: string; pack_type: string; role: string; cross_all?: boolean; cross_orgs?: number[] }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-packages/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}


/** 删除行业知识库包 */
export async function kbPackageDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-packages/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 获取指定行业包内的条目列表（支持层/语言/关键词过滤与分页；count=true 仅返回 total） */
export async function kbEntries(packageId: number, params?: { layer?: number; target_lang?: string; q?: string; page?: number; page_size?: number; count?: boolean }): Promise<AdminResp> {
  const qs = new URLSearchParams({ package_id: String(packageId) })
  if (params?.layer) qs.set('layer', String(params.layer))
  if (params?.target_lang) qs.set('target_lang', params.target_lang)
  if (params?.q) qs.set('q', params.q)
  if (params?.page) qs.set('page', String(params.page))
  if (params?.page_size) qs.set('page_size', String(params.page_size))
  if (params?.count) qs.set('count', '1')
  return bizResp(() => request(`/api/admin/kb-entries?${qs.toString()}`, { headers: authHeaders() }))
}

/** 获取品牌术语（module=brand AND layer=1，如 极石→ROX；package_id 必填）——品牌名设置面板用 */
export async function brandTerms(packageId: number): Promise<AdminResp> {
  return bizResp(() => request(`/api/admin/brand-terms?package_id=${packageId}`, { headers: authHeaders() }))
}

/** 新增 KB 条目（层级/原文/目标语言/译文/模块） */
export async function kbEntryAdd(data: { package_id: number; layer: number; source_text: string; target_lang: string; target_text: string; module: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-entries/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 更新 KB 条目（层级/原文/目标语言/译文/模块；不可改包归属） */
export async function kbEntryUpdate(data: { id: number; layer: number; source_text: string; target_lang: string; target_text: string; module: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-entries/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 删除 KB 条目 */
export async function kbEntryDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-entries/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 批量导入 KB 条目（租户管理员） */
export async function kbEntriesImport(data: { package_id: number; entries: { source_text: string; target_lang: string; target_text: string; layer?: number; module?: string }[] }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/import', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== KB 文件上传（后台，租户管理员及以上） ====================

/** 识别 KB 文件（multipart 上传，返回预览/语言列/temp_id） */
export async function kbRecognizeFile(file: File, mergedName?: string, onProgress?: (pct: number) => void): Promise<AdminResp> {
  if (mergedName) {
    // ★ H5：分片已合并，recognize 直读服务端合并产物
    // F-64②：后端该端点失败已改诚实状态码（400 文件无有效数据），包 bizResp 还原 success/message
    return bizResp(() => fetchJSON(`${API_BASE}/api/translation/recognize-kb?merged=${encodeURIComponent(mergedName)}`, {
      method: 'POST', headers: authHeaders(),
    }))
  }
  if (file.size > CHUNK_UPLOAD_MIN) {
    const merged = await uploadFileChunked(file, onProgress)
    if (!merged) return { success: false, message: apiMsg('common.chunkFail', '分片上传失败') }
    return bizResp(() => fetchJSON(`${API_BASE}/api/translation/recognize-kb?merged=${encodeURIComponent(merged)}`, {
      method: 'POST', headers: authHeaders(),
    }))
  }
  const formData = new FormData()
  formData.append('file', file)
  return bizResp(() => fetchJSON(`${API_BASE}/api/translation/recognize-kb`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
  }))
}

// ==================== ★ H5 大文件断点续传（分片上传） ====================

// 分片大小常量：4MB/片，须小于服务端 8MB 上限；改这里要同步检查后端分片校验，否则整批上传被拒
export const CHUNK_SIZE = 4 * 1024 * 1024 // 4MB/片（服务端上限 8MB）
/** CHUNK_UPLOAD_MIN 分片上传阈值：文件 ≥4MB 自动走分片通道 */
export const CHUNK_UPLOAD_MIN = 4 * 1024 * 1024

// 生成分片上传 ID（优先 crypto.randomUUID，降级随机串）
function newUploadId(): string {
  const c = globalThis.crypto as Crypto | undefined
  if (c?.randomUUID) return c.randomUUID().replace(/-/g, '')
  let s = ''
  for (let i = 0; i < 32; i++) s += Math.floor(Math.random() * 16).toString(16)
  return s
}

/** 查询已收分片（续传定位；网络失败按 0 处理不影响主流程） */
export async function uploadStatus(uploadId: string): Promise<{ received: number[]; total: number }> {
  try {
    const r = await fetchJSON(`${API_BASE}/api/upload/status?upload_id=${uploadId}`, { headers: authHeaders() })
    return r.success ? { received: r.received || [], total: r.total || 0 } : { received: [], total: 0 }
  } catch { return { received: [], total: 0 } }
}

// ★ P1-17 修复（2026-09-14）：uploadId 按「文件名+大小」持久化到 localStorage——
// 旧实现每次调用 newUploadId() 内存新生成，页面刷新后已收分片定位恒为空，
// 「断点续传」实际退化为全量重传。合并成功/最终失败后清除，避免脏复用。
function uploadIdKey(file: File): string {
  return `kb_upload_id:${file.name}:${file.size}`
}

/** 分片上传整个文件并合并；返回合并产物名（recognize-kb merged 参数用）。onProgress 0-100 */
export async function uploadFileChunked(file: File, onProgress?: (pct: number) => void, uploadId?: string): Promise<string | null> {
  let uid = uploadId || ''
  if (!uid) {
    try { uid = localStorage.getItem(uploadIdKey(file)) || '' } catch { /* 隐私模式忽略 */ }
    if (!uid) uid = newUploadId()
    try { localStorage.setItem(uploadIdKey(file), uid) } catch { /* 忽略 */ }
  }
  const total = Math.max(1, Math.ceil(file.size / CHUNK_SIZE))
  const have = new Set((await uploadStatus(uid)).received)
  for (let i = 0; i < total; i++) {
    if (have.has(i)) { onProgress?.(Math.round(((i + 1) / total) * 95)); continue }
    const blob = file.slice(i * CHUNK_SIZE, Math.min(file.size, (i + 1) * CHUNK_SIZE))
    const fd = new FormData()
    fd.append('upload_id', uid)
    fd.append('index', String(i))
    fd.append('total', String(total))
    fd.append('chunk', blob, `${file.name}.part${i}`)
    let ok = false
    for (let retry = 0; retry < 3 && !ok; retry++) {
      try {
        const r = await fetchJSON(`${API_BASE}/api/upload/chunk`, { method: 'POST', headers: authHeaders(), body: fd })
        ok = !!r.success
      } catch { /* 断网重试 */ }
      if (!ok) await new Promise((res) => setTimeout(res, 500 * (retry + 1)))
    }
    if (!ok) return null
    onProgress?.(Math.round(((i + 1) / total) * 95))
  }
  try {
    const mr = await fetchJSON(`${API_BASE}/api/upload/merge`, {
      method: 'POST', headers: { ...authHeaders(), 'Content-Type': 'application/json' },
      body: JSON.stringify({ upload_id: uid, filename: file.name }),
    })
    if (!mr.success) return null
    try { localStorage.removeItem(uploadIdKey(file)) } catch { /* 忽略 */ }
    onProgress?.(100)
    return String(mr.merged)
  } catch { return null }
}

/** 双语语料对齐导入：xlsx/csv 两列以上，直接写入翻译记忆库 */
export async function bitextImport(file: File): Promise<AdminResp & { added?: number; skipped?: number }> {
  const formData = new FormData()
  formData.append('file', file)
  // F-64②：400「文件无有效数据」经 bizResp 回到 success/message，调用点（KbP.tsx 无 catch）不再吞成未捕获拒绝
  return bizResp(() => fetchJSON(`${API_BASE}/api/translation/import-bitext`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
  }))
}

/** TMX 翻译记忆标准格式导入（xml），写入翻译记忆库 */
export async function tmxImport(file: File): Promise<AdminResp & { tus?: number; added?: number; skipped?: number }> {
  const formData = new FormData()
  formData.append('file', file)
  // F-64②：同上，保住后端原文（「TMX 无有效双语单元…」）
  return bizResp(() => fetchJSON(`${API_BASE}/api/translation/import-tmx`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
  }))
}

/**
 * tmxExport 导出翻译记忆为 TMX 1.4（Trados / memoQ 桥，与 tmxImport 成双向闭环）。
 * 走 fetch→blob 而非 <a href>：端点要求鉴权头，裸链接会被 401 挡下；
 * 文件名优先取响应 Content-Disposition，取不到再回落本地时间戳命名。
 * opts.module='approved' 只导已审核句对；opts.lang 只导该目标语非空的句对（增量迁移常用）。
 */
export async function tmxExport(opts?: { lang?: string; module?: string }): Promise<void> {
  const q = new URLSearchParams()
  if (opts?.lang) q.set('lang', opts.lang)
  if (opts?.module) q.set('module', opts.module)
  const url = `${API_BASE}/api/translation/export-tmx${q.toString() ? '?' + q.toString() : ''}`
  const r = await fetch(url, { headers: authHeaders() })
  if (r.status === 401) { handleUnauthorized(url); throw new Error(apiMsg('common.notLogged', '未登录')) }
  if (!r.ok) {
    // 失败时后端回的是 JSON 而非文件：解析出 message 抛给调用方，避免「点了没反应」
    let msg = `HTTP ${r.status}`
    try { msg = (await r.json()).message || msg } catch { /* 非 JSON 响应保留状态码 */ }
    throw new Error(msg)
  }
  const cd = r.headers.get('Content-Disposition') || ''
  const m = cd.match(/filename="?([^";]+)"?/)
  const blob = await r.blob()
  const objUrl = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = objUrl
  a.download = m ? m[1] : `langcross_tm_${new Date().toISOString().slice(0, 19).replace(/[-:T]/g, '')}.tmx`
  a.click()
  // 立刻 revoke 在部分浏览器会截断下载，交给下一轮事件循环
  setTimeout(() => URL.revokeObjectURL(objUrl), 1000)
}

/** 导入已识别的 KB 文件到指定包（按包隔离写入） */
export async function kbImportFile(data: { temp_id: string; package_id: number }): Promise<AdminResp> {
  // F-64②：导入阶段二次解析 0 行 → 400，包 bizResp 保住「文件无有效数据」原文
  return bizResp(() => request('/api/translation/import-kb', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}
/** 启用/停用知识库包（停用后不参与翻译命中） */
export async function kbPackageStatus(id: number, enabled: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-packages/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, enabled }) }))
}

/** ★ H3 知识库包级授权：列某包 read/write/manage 授权清单 */
export async function kbPackGrants(packId: number): Promise<AdminResp> {
  return bizResp(() => request(`/api/admin/kb-packages/grants?pack_id=${packId}`, { headers: authHeaders() }))
}

/** ★ H3 设置包级授权（role: read|write|manage；空串=撤销） */
export async function kbPackGrantSet(data: { pack_id: number; user_id: number; role: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-packages/grants', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** ★ H3 当前用户的包级授权清单（KB 管理导航门控用） */
export async function kbPackMine(): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-packages/mine', { headers: authHeaders() }))
}

/** 部门包跨部门共享开关：share=1 共享 / 0 仅限归属链内 */
export async function kbPackageShare(id: number, share: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-packages/share', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, share }) }))
}

/** 手动触发向量索引全量重建（超管） */
export async function kbIndexRebuild(): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/kb-index/rebuild', { method: 'POST', headers: authHeaders() }))
}

// ==================== 语言文化规范（安全句 / Gate 闸门） ====================

/** 安全句实体：语言文化规范（风格/禁用词/替换对），含审核状态 */
/** SafetyPhrase 安全短语（风格/避雷/替换词，语言文化包） */
export interface SafetyPhrase {
  id: number
  tenant_id: number
  package_id: number
  lang: string
  phrase: string
  kind?: string
  replacement?: string
  status?: string
  source?: string
  created_at: string
}

/** 列出安全句（可按语言文化包/语言/类型/状态过滤 + 关键词搜索 + 服务端分页） */
export async function safetyPhrases(params?: { package_id?: number; lang?: string; kind?: string; status?: string; q?: string; page?: number; page_size?: number }): Promise<AdminResp> {
  const qs = new URLSearchParams()
  if (params?.package_id) qs.set('package_id', String(params.package_id))
  if (params?.lang) qs.set('lang', params.lang)
  if (params?.kind) qs.set('kind', params.kind)
  if (params?.status) qs.set('status', params.status)
  if (params?.q) qs.set('q', params.q)
  if (params?.page) qs.set('page', String(params.page))
  if (params?.page_size) qs.set('page_size', String(params.page_size))
  const qstr = qs.toString()
  return bizResp(() => request(`/api/admin/safety-phrases${qstr ? `?${qstr}` : ''}`, { headers: authHeaders() }))
}

/** 新增安全句（结构化：类型+替换词） */
export async function safetyPhraseAdd(data: { package_id: number; lang: string; phrase: string; kind?: string; replacement?: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/safety-phrases/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 删除安全句 */
export async function safetyPhraseDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/safety-phrases/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 审核安全句（approved/rejected/pending） */
export async function safetyPhraseStatus(id: number, status: string): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/safety-phrases/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, status }) }))
}

/** LLM 投喂批量导入（统一落 pending 待人工审核） */
export async function safetyBulkImport(packageId: number, items: { lang: string; phrase: string; kind: string; replacement?: string }[]): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/safety-phrases/bulk-import', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ package_id: packageId, items }) }))
}
