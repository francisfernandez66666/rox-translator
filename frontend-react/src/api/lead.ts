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

// ★ bizResp 豁免登记（★ D-3 批 2026-09-29，设计而非疏漏，代码保持原样）：
//   蜜罐「假成功」设计——bot/被拦请求也必须拿到与真人一致的静默成功语义，
//   bizResp 会把结构化失败还原成 {success:false} 显式回执，等于向提交方「揭穿」拦截，
//   与本接口反垃圾语义相悖，故本文件**刻意不接 bizResp**（白名单锁见 api/bizRespGate.test.ts）。
// createLead 提交留资。后端 429（限流）/400（校验）/403（验证码）均抛 ApiError。
export async function createLead(payload: LeadPayload): Promise<AdminResp> {
  return request('/api/lead', {
    method: 'POST',
    body: JSON.stringify(payload),
  })
}
