// ============================================================================
// api/kb.ts — 知识库（行业包）域接口
// 职责：行业包 CRUD、包内条目管理、批量导入
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

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