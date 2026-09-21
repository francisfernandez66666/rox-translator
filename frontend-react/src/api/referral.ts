// ============================================================================
// api/referral.ts — 邀请裂变域接口
// 职责：我的邀请码/邀请链接/邀请记录/奖励统计、二维码拉取（带鉴权）、运营参数（超管）
// ============================================================================

/**
 * api/referral.ts · 职责说明
 * 封装邀请裂变（推荐奖励）相关的所有接口，包括：
 * - 邀请主页：获取我的邀请码、邀请链接、邀请记录、奖励统计
 * - 二维码获取：带鉴权的邀请二维码图片下载
 * - 运营参数（超管）：总开关、奖励积分数、有效期等配置
 */

import { request, API_BASE, authHeaders, handleUnauthorized, handleForbidden, ApiError, type AdminResp } from './core'

/** 单条邀请奖励记录（对应后端 store.ReferralRecord） */
/** ReferralRecord 单条邀请记录（被邀人/奖励/到账态） */
export interface ReferralRecord {
  invitee_uid: number
  invitee_name: string
  invitee_email?: string // 被邀人注册邮箱快照（2026-08-26 前台记录需求）
  type: string // trial_stack=体验叠加 | paid_perm=付费永久奖励
  reward_points: number
  days: number
  paid: boolean
  created_at: string
}

/** 我的邀请主页数据响应（含邀请码/链接/记录/奖励统计） */
/** ReferralMyResp 我的邀请出参（记录列表 + 汇总） */
export interface ReferralMyResp extends AdminResp {
  ref_code?: string
  invite_url?: string
  records?: ReferralRecord[]
  invited?: number
  trial_count?: number
  trial_points?: number
  paid_points?: number
}

/** ReferralFunnel 邀请漏斗（注册→付费→奖励转化） */
export interface ReferralFunnel {
  l1_invited: number; l1_paid: number
  l2_invited: number; l2_paid: number
  reward_points_l1: number; reward_points_l2: number
  reg_rewards: number
}

/** ★ H9 我的 2 级邀请归因漏斗（登录即可，仅本人维度） */
export async function referralFunnel(): Promise<AdminResp & { funnel?: ReferralFunnel; l2_pct?: number }> {
  return request('/api/referral/funnel', { headers: authHeaders() })
}

/** 拉取我的邀请码与邀请记录（懒生成个人码） */
export async function referralMy(): Promise<ReferralMyResp> {
  return request('/api/referral/my', { headers: authHeaders() })
}

/**
 * 拉取邀请二维码 PNG Blob（需鉴权）。
 * 此前裸 <img>/<a> 引用无法携带 Authorization 头导致 401，现改为 fetch + authHeaders 取 Blob。
 * ★ §4.2-2：出图走 fetch→blob（二进制响应，正当裸用），但 401/403 与统一 client 同源处理——
 *   旧实现把一切非 2xx 静默折叠成 null，登录失效/越权都不提示；现补齐状态语义。
 */
export async function fetchReferralQrBlob(): Promise<Blob | null> {
  const url = `${API_BASE}/api/referral/qrcode`
  try {
    const resp = await fetch(url, { headers: authHeaders() })
    if (resp.status === 401) handleUnauthorized(url)
    // 403 越权：抛带本地化文案的稳定码错误，交由调用方（面板）提示，而非静默返回 null
    if (resp.status === 403) {
      let msg = ''
      try { msg = (await resp.json()).message || '' } catch { /* 非 JSON 错误体 */ }
      throw new ApiError(handleForbidden(msg), 403, 'FORBIDDEN')
    }
    if (!resp.ok) return null
    return await resp.blob()
  } catch (e) {
    // 403 显式上抛（越权要让用户看到），其余网络/解析异常保持旧的「返回 null 静默降级」口径不变
    if (e instanceof ApiError && e.status === 403) throw e
    return null
  }
}

/** 邀请裂变运营参数（仅超管可读写）：总开关/奖励积分额度/有效期等 */
/** ReferralConfig 邀请奖励运营配置（开关/奖励额度/日上限，均为积分口径） */
export interface ReferralConfig {
  enabled: boolean // 总开关（关闭后绑定与奖励全部停发）
  reward_points: number // 受邀注册→邀请人体验叠加积分
  paid_reward_points: number // 受邀人首笔付费→邀请人奖励积分
  reward_days: number // 注册邀请奖励有效期（天）；register.go 读取
  paid_reward_days: number // 付费邀请奖励有效期（天）；0=永久
}

/** 读取邀请运营参数（超管） */
export async function referralConfigGet(): Promise<ReferralConfig & AdminResp> {
  return request('/api/admin/referral/config', { headers: authHeaders() })
}

/** 保存邀请运营参数（超管；可选字段增量更新） */
export async function referralConfigSave(cfg: Partial<ReferralConfig>): Promise<AdminResp> {
  return request('/api/admin/referral/config', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({
      enabled: cfg.enabled,
      reward_points: cfg.reward_points,
      paid_reward_points: cfg.paid_reward_points,
      reward_days: cfg.reward_days,
      paid_reward_days: cfg.paid_reward_days,
    }),
  })
}
