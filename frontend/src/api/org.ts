// ============================================================================
// api/org.ts — 组织层级域接口（管理结构展示层）
// 职责：组织/部门树的 CRUD、组织下用户视图（超管/租户管理员按组织下钻）
// ============================================================================

import { request, authHeaders } from './core'

// 组织实体
export interface OrgInfo {
  id: number
  tenant_id: number
  parent_id: number
  name: string
  created_at: string
  updated_at: string
}

// OrgResp 组织接口统一响应结构：orgs 为组织列表、org 为单个对象、tenant_id 为归属租户。
export interface OrgResp {
  success: boolean
  message?: string
  orgs?: OrgInfo[]
  org?: OrgInfo
  tenant_id?: number
}

// 组织列表（扁平，前端组装树）
export async function orgList(): Promise<OrgResp> {
  return request('/api/admin/orgs', { headers: authHeaders() })
}

// 创建子组织/部门（parent_id=0 为根组织下）
export async function orgCreate(data: { name: string; parent_id: number }): Promise<OrgResp> {
  return request('/api/admin/orgs/create', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

// 重命名组织
export async function orgRename(id: number, name: string): Promise<OrgResp> {
  return request('/api/admin/orgs/rename', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, name }),
  })
}

// 删除组织（子孙上移、用户回收至根组织）
export async function orgDelete(id: number): Promise<OrgResp> {
  return request('/api/admin/orgs/delete', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id }),
  })
}

// 组织下用户视图（含子孙组织归集）；org_id 缺省/0 = 租户全部用户
export async function orgUsers(orgId?: number): Promise<any> {
  const q = orgId ? `?org_id=${orgId}` : ''
  return request(`/api/admin/orgs/users${q}`, { headers: authHeaders() })
}