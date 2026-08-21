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
