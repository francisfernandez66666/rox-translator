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

import { request, authHeaders, API_BASE, type AdminResp } from './core'

/** 获取行业知识库包列表（不带头条目数 entry_count，后端一次 GROUP BY 附带） */
export async function kbPackages(): Promise<AdminResp> {
  return request('/api/admin/kb-packages', { headers: authHeaders() })
}

/** 创建行业知识库包 */
export async function kbPackageCreate(data: { code: string; name: string; pack_type: string; role: string; cross_all?: boolean; cross_orgs?: number[] }): Promise<AdminResp> {
  return request('/api/admin/kb-packages/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 更新知识库包（名称 / 跨部门范围：全公司或涵盖部门） */
export async function kbPackageUpdate(data: { id: number; name?: string; cross_all?: boolean; cross_orgs?: number[] }): Promise<AdminResp> {
  return request('/api/admin/kb-packages/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 删除行业知识库包 */
export async function kbPackageDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-packages/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
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
  return request(`/api/admin/kb-entries?${qs.toString()}`, { headers: authHeaders() })
}

/** 新增 KB 条目（层级/原文/目标语言/译文/模块） */
export async function kbEntryAdd(data: { package_id: number; layer: number; source_text: string; target_lang: string; target_text: string; module: string }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 更新 KB 条目（层级/原文/目标语言/译文/模块；不可改包归属） */
export async function kbEntryUpdate(data: { id: number; layer: number; source_text: string; target_lang: string; target_text: string; module: string }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 删除 KB 条目 */
export async function kbEntryDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-entries/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 批量导入 KB 条目（租户管理员） */
export async function kbEntriesImport(data: { package_id: number; entries: { source_text: string; target_lang: string; target_text: string; layer?: number; module?: string }[] }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/import', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== KB 文件上传（后台，租户管理员及以上） ====================

/** 识别 KB 文件（multipart 上传，返回预览/语言列/temp_id） */
export async function kbRecognizeFile(file: File): Promise<AdminResp> {
  const formData = new FormData()
  formData.append('file', file)
  const resp = await fetch(`${API_BASE}/api/translation/recognize-kb`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
  })
  return resp.json()
}

/** 双语语料对齐导入：xlsx/csv 两列以上，直接写入翻译记忆库 */
export async function bitextImport(file: File): Promise<AdminResp & { added?: number; skipped?: number }> {
  const formData = new FormData()
  formData.append('file', file)
  const resp = await fetch(`${API_BASE}/api/translation/import-bitext`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
  })
  return resp.json()
}

/** TMX 翻译记忆标准格式导入（xml），写入翻译记忆库 */
export async function tmxImport(file: File): Promise<AdminResp & { tus?: number; added?: number; skipped?: number }> {
  const formData = new FormData()
  formData.append('file', file)
  const resp = await fetch(`${API_BASE}/api/translation/import-tmx`, {
    method: 'POST',
    headers: authHeaders(),
    body: formData,
  })
  return resp.json()
}

/** 导入已识别的 KB 文件到指定包（按包隔离写入） */
export async function kbImportFile(data: { temp_id: string; package_id: number }): Promise<AdminResp> {
  return request('/api/translation/import-kb', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}
/** 启用/停用知识库包（停用后不参与翻译命中） */
export async function kbPackageStatus(id: number, enabled: number): Promise<AdminResp> {
  return request('/api/admin/kb-packages/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, enabled }) })
}

/** 部门包跨部门共享开关：share=1 共享 / 0 仅限归属链内 */
export async function kbPackageShare(id: number, share: number): Promise<AdminResp> {
  return request('/api/admin/kb-packages/share', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, share }) })
}

/** 手动触发向量索引全量重建（超管） */
export async function kbIndexRebuild(): Promise<AdminResp> {
  return request('/api/admin/kb-index/rebuild', { method: 'POST', headers: authHeaders() })
}

// ==================== 语言文化规范（安全句 / Gate 闸门） ====================

/** 安全句实体：语言文化规范（风格/禁用词/替换对），含审核状态 */
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
  return request(`/api/admin/safety-phrases${qstr ? `?${qstr}` : ''}`, { headers: authHeaders() })
}

/** 新增安全句（结构化：类型+替换词） */
export async function safetyPhraseAdd(data: { package_id: number; lang: string; phrase: string; kind?: string; replacement?: string }): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 删除安全句 */
export async function safetyPhraseDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 审核安全句（approved/rejected/pending） */
export async function safetyPhraseStatus(id: number, status: string): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, status }) })
}

/** LLM 投喂批量导入（统一落 pending 待人工审核） */
export async function safetyBulkImport(packageId: number, items: { lang: string; phrase: string; kind: string; replacement?: string }[]): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/bulk-import', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ package_id: packageId, items }) })
}
