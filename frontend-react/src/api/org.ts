// ============================================================================
// api/org.ts — 组织层级域接口（管理结构展示层）
// 职责：组织/部门树的 CRUD、组织下用户视图（超管/租户管理员按组织下钻）
// ============================================================================

/**
 * api/org.ts · 职责说明
 * 封装组织架构管理相关的所有接口，包括：
 * - 组织树管理：创建、重命名、移动、删除组织/部门
 * - 用户视图：查询组织下的用户列表（支持子孙组织归集）
 * - 部门预算：设置部门月度 token 预算、查看预算总览
 */

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 组织实体：含父子关系/名称/类型（root/org/dept） */
export interface OrgInfo {
  id: number
  tenant_id: number
  parent_id: number
  name: string
  type: string // root(根组织)/org(组织)/dept(部门)
  created_at: string
  updated_at: string
}

/** 组织接口统一响应结构：orgs 列表/org 单对象/root 根组织行/tenant_id 归属租户 */
export interface OrgResp {
  success: boolean
  message?: string
  orgs?: OrgInfo[]
  org?: OrgInfo
  root?: OrgInfo
  tenant_id?: number
}

/** 获取组织列表（扁平结构，前端组装树；含根组织行） */
export async function orgList(): Promise<OrgResp> {
  return bizResp(() => request('/api/admin/orgs', { headers: authHeaders() }))
}

/** 创建组织/部门（parent_id=0 为组织；>0 为部门） */
export async function orgCreate(data: { name: string; parent_id: number; type?: string }): Promise<OrgResp> {
  return bizResp(() => request('/api/admin/orgs/create', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  }))
}

/** 重命名组织 */
export async function orgRename(id: number, name: string): Promise<OrgResp> {
  return bizResp(() => request('/api/admin/orgs/rename', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, name }),
  }))
}

/** 移动组织/部门到新父节点（拖拽调整层级；parent_id=0 为根组织下） */
export async function orgMove(id: number, parentId: number): Promise<OrgResp> {
  return bizResp(() => request('/api/admin/orgs/move', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, parent_id: parentId }),
  }))
}

/** 删除组织（子孙上移、用户回收至根组织） */
export async function orgDelete(id: number): Promise<OrgResp> {
  return bizResp(() => request('/api/admin/orgs/delete', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id }),
  }))
}

/** 组织下用户视图（含子孙组织归集）；org_id 缺省/0 = 租户全部用户 */
export async function orgUsers(orgId?: number): Promise<any> {
  const q = orgId ? `?org_id=${orgId}` : ''
  return bizResp(() => request(`/api/admin/orgs/users${q}`, { headers: authHeaders() }))
}

// ============================================================================
// 部门预算（四期）：租管为部门分配月度积分预算（∑部门预算=租户总预算）
// ============================================================================

/** OrgBudgetDept 预算总览中的部门项（积分口径） */
export interface OrgBudgetDept {
  org_id: number
  name: string
  limit_points: number // 部门月度预算（0=未启用）
  used_points: number  // 含子树本月消耗
}

/** orgBudgetSummary 预算总览（积分口径）：总预算=∑部门预算；全租户本月已用 */
export async function orgBudgetSummary(): Promise<AdminResp & {
  summary?: { total_limit_points: number; used_this_month_points: number; depts: OrgBudgetDept[] }
}> {
  return request('/api/admin/org-budget', { headers: authHeaders() })
}

/** orgTokenLimit 设置部门月度积分预算（0=关闭该部门的部门墙）。 */
export async function orgTokenLimit(orgId: number, limitPoints: number): Promise<AdminResp> {
  return request('/api/admin/orgs/token-limit', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ org_id: orgId, limit_points: limitPoints }),
  })
}
