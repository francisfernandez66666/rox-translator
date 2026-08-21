// ============================================================================
// api/kb.ts — 知识库（行业包）域接口
// 职责：行业包 CRUD、包内条目管理、批量导入
// ============================================================================

import { request, authHeaders, API_BASE, type AdminResp } from './core'

// 行业包列表
export async function kbPackages(): Promise<AdminResp> {
  return request('/api/admin/kb-packages', { headers: authHeaders() })
}

// 创建行业包
export async function kbPackageCreate(data: { code: string; name: string; pack_type: string; role: string }): Promise<AdminResp> {
  return request('/api/admin/kb-packages/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 删除行业包
export async function kbPackageDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-packages/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 行业包内条目列表
export async function kbEntries(packageId: number): Promise<AdminResp> {
  return request(`/api/admin/kb-entries?package_id=${packageId}`, { headers: authHeaders() })
}

// 添加 KB 条目
export async function kbEntryAdd(data: { package_id: number; layer: number; source_text: string; target_lang: string; target_text: string; module: string }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 删除 KB 条目
export async function kbEntryDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-entries/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 批量导入 KB 条目（租户管理员）
export async function kbEntriesImport(data: { package_id: number; entries: { source_text: string; target_lang: string; target_text: string; layer?: number; module?: string }[] }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/import', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== KB 文件上传（后台，租户管理员及以上） ====================

// 识别 KB 文件（multipart 上传，返回预览/语言列/temp_id）
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

// 导入已识别的 KB 文件到指定包（按包隔离写入）
export async function kbImportFile(data: { temp_id: string; package_id: number }): Promise<AdminResp> {
  return request('/api/translation/import-kb', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}
// 启用/停用知识库包（停用后不参与翻译命中）
export async function kbPackageStatus(id: number, enabled: number): Promise<AdminResp> {
  return request('/api/admin/kb-packages/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, enabled }) })
}

// 手动触发向量索引全量重建（超管；使用知识库 Embed 阶段模型）
export async function kbIndexRebuild(): Promise<AdminResp> {
  return request('/api/admin/kb-index/rebuild', { method: 'POST', headers: authHeaders() })
}

// ==================== 语言文化规范（安全句 / Gate 闸门） ====================

// 安全句实体（kind: style 风格规范/forbidden 禁用词/replace 替换对；status: pending/approved/rejected）
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

// 列出安全句（可按语言文化包过滤）
export async function safetyPhrases(): Promise<AdminResp> {
  return request('/api/admin/safety-phrases', { headers: authHeaders() })
}

// 新增安全句（结构化：类型+替换词）
export async function safetyPhraseAdd(data: { package_id: number; lang: string; phrase: string; kind?: string; replacement?: string }): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 删除安全句
export async function safetyPhraseDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 审核安全句（approved 通过 / rejected 驳回 / pending 回退待审）
export async function safetyPhraseStatus(id: number, status: string): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, status }) })
}

// LLM 投喂批量导入（统一落 pending 待人工审核）
export async function safetyBulkImport(packageId: number, items: { lang: string; phrase: string; kind: string; replacement?: string }[]): Promise<AdminResp> {
  return request('/api/admin/safety-phrases/bulk-import', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ package_id: packageId, items }) })
}
