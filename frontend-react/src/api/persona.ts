// ============================================================================
// api/persona.ts — 职业角色字典接口（2026-09-19 需求：用户角色 + 角色知识库包）
// 职责：角色 CRUD（超管）+ 公开角色字典 + 本人角色维护（转岗/转行）
// 说明：角色以 kb_packages pack_type=persona 的平台角色包为承载（宿主租户0），
//      job_role 列只挂在用户上，与企业无关（退出企业角色仍在）。
// ============================================================================

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 角色字典条目（后端 KBPackage 精简，结构同 IndustryItem） */
export interface PersonaItem {
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

/** 获取角色字典（超管/租户管理员以上可见；供「角色管理」面板与注册下拉动态拉取） */
export async function personas(): Promise<AdminResp & { personas?: PersonaItem[] }> {
  return bizResp(() => request('/api/admin/personas', { headers: authHeaders() }))
}

/** 新建角色（仅超管；code=小写字母/数字/下划线，全局唯一） */
export async function personaCreate(data: { code: string; name: string }): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/personas/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 编辑角色显示名（仅超管） */
export async function personaUpdate(id: number, name: string): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/personas/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, name }) }))
}

/** 启用/停用角色（仅超管；enabled=1 启用 / 0 停用，停用后注册下拉消失） */
export async function personaStatus(id: number, enabled: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/personas/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, enabled }) }))
}

/** 删除角色（仅超管；被用户 job_role 引用时后端拒绝） */
export async function personaDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/personas/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 公开角色字典（注册页用，仅启用中，无需登录） */
export async function registerPersonas(): Promise<AdminResp & { personas?: Array<{ code: string; name: string }> }> {
  return bizResp(() => request('/api/register/personas'))
}

/** 本人维护职业角色（个人/企业通用；传空串清除；转岗即改一次值） */
export async function setMyJobRole(job_role: string): Promise<AdminResp & { job_role?: string }> {
  return bizResp(() => request('/api/me/job-role', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ job_role }) }))
}
