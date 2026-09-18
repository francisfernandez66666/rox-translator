// ============================================================================
// api/lead.ts — 营销留资接口（官网 / 定价页 → POST /api/lead）
// 职责：匿名提交销售线索；后端复用 feedbacks 通道（target_type='lead'）落库，
//       带 IP 限流 + 蜜罐 + 可选 Turnstile（★ P1-3，2026-09-18）。
// 注意：匿名调用，不走 authHeaders 之外的任何登录态；401 兜底跳转对本题无意义
//       （接口本身不鉴权），错误统一以 ApiError 抛出由表单层展示文案。
// ============================================================================

import { request, type AdminResp } from './core'

/** 留资表单请求体（与后端 leadReq json 字段一一对应，改名必须两头同步） */
export interface LeadPayload {
  company: string // 公司/团队名称（必填）
  email: string // 联系邮箱（必填）
  langs?: string // 意向语言，逗号分隔
  message?: string // 补充留言
  source?: string // 来源页：landing | pricing | footer
  captcha_token?: string // Turnstile token（后台开启人机验证时必填）
  site?: string // ★ 蜜罐：真人恒为空，bot 全会填（后端静默吞掉）
}

// createLead 提交留资。后端 429（限流）/400（校验）/403（验证码）均抛 ApiError。
export async function createLead(payload: LeadPayload): Promise<AdminResp> {
  return request('/api/lead', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}
