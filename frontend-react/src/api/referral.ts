// ============================================================================
// api/referral.ts — 邀请裂变域接口
// 职责：我的邀请码/邀请链接/邀请记录/奖励统计、二维码拉取（带鉴权）、运营参数（超管）
// ============================================================================

import { request, API_BASE, authHeaders, type AdminResp } from './core'

// ReferralRecord 单条邀请奖励记录（对应后端 store.ReferralRecord）
export interface ReferralRecord {
  invitee_uid: number
  invitee_name: string
  invitee_email?: string // 被邀人注册邮箱快照（2026-08-26 前台记录需求）
  type: string // trial_stack=体验叠加 | paid_perm=付费永久奖励
  tokens: number
  days: number
  paid: boolean
  created_at: string
}

// 我的邀请主页数据响应
export interface ReferralMyResp extends AdminResp {
  ref_code?: string
  invite_url?: string
  records?: ReferralRecord[]
  invited?: number
  trial_count?: number
  trial_tokens?: number
  paid_tokens?: number
}

// referralMy 拉取我的邀请码与邀请记录（懒生成个人码）
export async function referralMy(): Promise<ReferralMyResp> {
  return request('/api/referral/my', { headers: authHeaders() })
}

// fetchReferralQrBlob 拉取邀请二维码 PNG Blob。
// ★ 修复（2026-08-26 U2）：此前用裸 <img src>/<a href> 引用该端点——浏览器原生请求
// 无法携带 Authorization 头，后端强制 JWT 返回 401，导致预览裂图、下载报「需要授权」。
// 现改为 fetch + authHeaders 取 Blob，由调用方经 URL.createObjectURL 展示/下载。
export async function fetchReferralQrBlob(): Promise<Blob | null> {
  try {
    const resp = await fetch(`${API_BASE}/api/referral/qrcode`, { headers: authHeaders() })
    if (!resp.ok) return null
    return await resp.blob()
  } catch {
    return null
  }
}

// 邀请裂变运营参数（仅超管可读写）
export interface ReferralConfig {
  enabled: boolean // 总开关（关闭后绑定与奖励全部停发）
  reward_tokens: number // 受邀注册→邀请人体验叠加 token
  paid_reward_tokens: number // 受邀人首笔付费→邀请人奖励 token
  reward_days: number // 注册邀请奖励有效期（天）；register.go 读取
  paid_reward_days: number // 付费邀请奖励有效期（天）；0=永久
}

// referralConfigGet 读取邀请运营参数（超管）
export async function referralConfigGet(): Promise<ReferralConfig & AdminResp> {
  return request('/api/admin/referral/config', { headers: authHeaders() })
}

// referralConfigSave 保存邀请运营参数（超管；可选字段增量更新）
export async function referralConfigSave(cfg: Partial<ReferralConfig>): Promise<AdminResp> {
  return request('/api/admin/referral/config', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({
      enabled: cfg.enabled,
      reward_tokens: cfg.reward_tokens,
      paid_reward_tokens: cfg.paid_reward_tokens,
      reward_days: cfg.reward_days,
      paid_reward_days: cfg.paid_reward_days,
    }),
  })
}
