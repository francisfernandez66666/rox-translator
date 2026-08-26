// ============================================================================
// api/referral.ts — 邀请裂变域接口
// 职责：我的邀请码/邀请链接/邀请记录/奖励统计、二维码图片地址
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

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

// referralQrUrl 二维码图片地址（img 标签直接引用；download=1 触发浏览器下载）
export function referralQrUrl(download = false): string {
  return `/api/referral/qrcode${download ? '?download=1' : ''}`
}
