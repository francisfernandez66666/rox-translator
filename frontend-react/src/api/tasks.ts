// ============================================================================
// api/tasks.ts — 任务中心域接口
// 职责：超管任务定义管理（每日/一次性任务 + 永久 token 奖励）+ 用户领取
// 对应后端：/api/admin/tasks*（超管）与 /api/me/tasks*（登录用户）
// ============================================================================

/**
 * api/tasks.ts · 职责说明
 * 封装「任务中心」相关接口：
 * - 超管：任务列表 / 新增/更新任务（task_type=daily|once + reward_tokens 永久 token 奖励）/ 删除任务
 * - 用户：我的任务列表（含领取状态）/ 一键领取奖励（奖励入永久余额）
 */

import { request, authHeaders, type AdminResp } from './core'

/** 任务定义（对应后端 store.UserTask） */
export interface UserTask {
  id: number // 任务 ID
  task_type: 'daily' | 'once' // daily=每日任务 / once=一次性任务
  title: string // 任务标题
  description: string // 任务说明（可空）
  reward_tokens: number // 奖励 token 数（永久余额）
  enabled: number // 1=启用 0=停用
  sort_order: number // 排序
  created_at: string
  updated_at: string
}

/** 用户视角任务（含本人领取状态） */
export interface UserTaskView extends UserTask {
  claimed: boolean // 是否已领取（daily=今日；once=曾领取）
  claimed_at: string // 最近领取时间（空=未领取）
}

/** 超管任务列表响应 */
export async function adminTasks(): Promise<AdminResp & { tasks?: UserTask[] }> {
  return request('/api/admin/tasks', { headers: authHeaders() })
}

/** 超管新增/更新任务（id=0 新增；>0 更新） */
export async function adminTaskSave(data: Partial<UserTask>): Promise<AdminResp & { id?: number }> {
  return request('/api/admin/tasks/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 超管删除任务（连带清理领取记录） */
export async function adminTaskDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/tasks/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

/** 我的任务列表（启用任务 + 本人领取状态） */
export async function myTasks(): Promise<AdminResp & { tasks?: UserTaskView[] }> {
  return request('/api/me/tasks', { headers: authHeaders() })
}

/** 一键领取任务奖励（奖励入永久余额） */
export async function claimTask(id: number): Promise<AdminResp & { tokens?: number }> {
  return request('/api/me/tasks/claim', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
