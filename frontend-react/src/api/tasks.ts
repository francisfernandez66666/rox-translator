// ============================================================================
// api/tasks.ts — 任务中心域接口
// 职责：超管任务定义管理（每日/一次性任务 + 永久积分奖励）+ 用户领取
// 对应后端：/api/admin/tasks*（超管）与 /api/me/tasks*（登录用户）
// ============================================================================

/**
 * api/tasks.ts · 职责说明
 * 封装「任务中心」相关接口：
 * - 超管：任务列表 / 新增/更新任务（task_type=daily|once + reward_points 永久积分奖励）/ 删除任务
 * - 用户：我的任务列表（含领取状态）/ 一键领取奖励（奖励入永久余额）
 */

// ★ F-64②（2026-09-26 批 I-10）：本文件所有接口统一经 core.ts 的 bizResp 接线——
//   HTTP 200 但业务体 success:false 会被如实降级为异常口径，调用方不再拿到「假成功」；
//   新增接口一律写 bizResp(() => request(...))，禁止直返裸 request。
import { bizResp, request, authHeaders, type AdminResp } from './core'

/** 计数周期四档（后端 period 列的合法值，也是 task_type 的取值域 —— 见下方 F-60 注释） */
export type TaskCycle = 'daily' | 'weekly' | 'once' | 'event'

/** 任务定义（对应后端 store.UserTask） */
export interface UserTask {
  id: number // 任务 ID
  // ★ F-60（2026-09-26 〇-U 批 I-8）：task_type 自 #33 起只是 period 的**恒等别名**——
  //   后端 store.normalizeTaskCycle（写侧）＋ RepairTaskCycleColumns（存量订正，随迁移自愈）
  //   保证两列恒等，故 auto 任务会出现 'weekly'/'event' 这类取值，取值域与 TaskCycle 一致。
  //   判周期请读 period；本字段仅为兼容既有调用方保留（历史形态 task_type='daily' + period='weekly'
  //   会把周任务算成日任务，正是本轮 UAT 的 F-60）。
  task_type: TaskCycle | string
  title: string // 任务标题
  description: string // 任务说明（可空）
  reward_points: number // 奖励积分数（临时/永久由 valid_days 决定，见 reward_kind）
  enabled: number // 1=启用 0=停用
  sort_order: number // 排序
  created_at: string
  updated_at: string
  // ★ #33（2026-09-21）任务系统：事件自动发放口径（手工任务 grant_mode=manual，其余字段为默认值）
  task_key?: string // 事件任务标识（空=超管自定义手工任务）
  grant_mode?: 'manual' | 'auto' // manual=用户点击领取 / auto=事件自动发放
  period?: TaskCycle // ★ 计数周期真值（决定去重键粒度与日/周上限口径）
  valid_days?: number // >0=临时积分有效天数；0=永久积分
  stack_expiry?: number // 1=到期叠加（日/周叠加）；0=固定 now+valid_days
  cap_per_day?: number // 每日发放上限（0=不限）
  cap_per_week?: number // 每周发放上限（0=不限）
  reward_kind?: 'temporary' | 'permanent' | string // 出参派生：临时/永久积分
}

/** 事件自动发放任务的本期进度（对应后端 store.TaskRewardStat） */
export interface TaskRewardStat {
  today_count: number // 今日已发放次数
  week_count: number // 本周已发放次数
  total_count: number // 历史累计发放次数
  last_expiry: string // 最近一次临时积分到期时间（永久类为空）
  last_granted: string // 最近一次发放时间
}

/** 用户视角任务（含本人领取状态） */
export interface UserTaskView extends UserTask {
  claimed: boolean // 是否已领取（手工）/ 本周期是否已自动发放
  claimed_at: string // 最近领取时间（空=未领取）
  reward?: TaskRewardStat // ★ #33 事件任务的周期进度（手工任务无此字段）
}

/** 超管任务列表响应 */
export async function adminTasks(): Promise<AdminResp & { tasks?: UserTask[] }> {
  return request('/api/admin/tasks', { headers: authHeaders() })
}

/** 超管新增/更新任务（id=0 新增；>0 更新） */
export async function adminTaskSave(data: Partial<UserTask>): Promise<AdminResp & { id?: number }> {
  return bizResp(() => request('/api/admin/tasks/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) }))
}

/** 超管删除任务（连带清理领取记录） */
export async function adminTaskDelete(id: number): Promise<AdminResp> {
  return bizResp(() => request('/api/admin/tasks/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/** 我的任务列表（启用任务 + 本人领取状态） */
export async function myTasks(): Promise<AdminResp & { tasks?: UserTaskView[] }> {
  return request('/api/me/tasks', { headers: authHeaders() })
}

/** 一键领取任务奖励（奖励入永久余额） */
export async function claimTask(id: number): Promise<AdminResp & { points?: number }> {
  return bizResp(() => request('/api/me/tasks/claim', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) }))
}

/**
 * ★ #33 特殊任务（仅超管）：重置「已订阅全部用户」的任务积分消耗量。
 * 把未过期的任务临时积分台账拉回满额（已消耗完的同样重置），有效期一律不改写。
 * 参数 subscribedOnly=true（默认）只影响有未过期订阅台账的租户；false 覆盖全部租户。
 */
export async function adminTaskResetConsumption(subscribedOnly = true): Promise<AdminResp & { tenants?: number; reset_rows?: number }> {
  return bizResp(() => request('/api/admin/tasks/reset-consumption', {
    method: 'POST', headers: authHeaders(), body: JSON.stringify({ subscribed_only: subscribedOnly }),
  }))
}
