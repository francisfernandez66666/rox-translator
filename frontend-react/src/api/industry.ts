// ============================================================================
// api/industry.ts — 行业字典管理接口（2026-09-10 超管可创建/维护行业）
// 职责：行业 CRUD（列表/新建/编辑/启停/删除）+ 公开注册行业字典
// 说明：行业以 kb_packages pack_type=industry 的平台行业包为承载（宿主租户0），
//      超管在「行业管理」面板维护；注册页/租户表单/数据采集下拉均动态拉取。
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

/** 行业字典条目（后端 KBPackage 精简） */
export interface IndustryItem {
  id: number
  code: string
  name: string
  pack_type: string
  role: string
  enabled: number
  sort_order: number
  entry_count: number
  created_at: string
  updated_at: string
}

/** 获取行业字典（超管/租户管理员以上可见；供「行业管理」面板与各下拉动态拉取） */
export async function industries(): Promise<AdminResp & { industries?: IndustryItem[] }> {
  return request('/api/admin/industries', { headers: authHeaders() })
}

/** 新建行业（仅超管；code=小写字母/数字/下划线，全局唯一） */
export async function industryCreate(data: { code: string; name: string }): Promise<AdminResp> {
  return request('/api/admin/industries/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 编辑行业显示名（仅超管） */
export async function industryUpdate(id: number, name: string): Promise<AdminResp> {
  return request('/api/admin/industries/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, name }) })
}

/** 启用/停用行业（仅超管；enabled=1 启用 / 0 停用） */
export async function industryStatus(id: number, enabled: number): Promise<AdminResp> {
  return request('/api/admin/industries/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, enabled }) })
}

/** 删除行业（仅超管；被租户引用时后端拒绝） */
export async function industryDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/industries/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 公开注册行业字典（无需登录；来自 /api/auth/register-config，仅含启用行业） */
export async function publicIndustries(): Promise<AdminResp & { industries?: Array<{ code: string; name: string }> }> {
  return request('/api/auth/register-config')
}
