// ============================================================================
// api/auth.ts — 认证域接口
// 职责：账号密码登录、会话恢复（me）、自助注册（可带邀请码/租户信息）
// ============================================================================

/**
 * api/auth.ts · 职责说明
 * 封装用户认证相关的所有接口，包括：
 * - 登录认证：账号密码登录、会话恢复（me 接口）
 * - 自助注册：支持邀请码、租户信息、行业、邮箱验证码、人机验证
 * - 密码管理：忘记密码、重置密码、自助修改密码
 * - 邮箱管理：发送验证码、绑定/修改邮箱
 * - 账号注销：自助注销账号
 */

import { request, authHeaders, type AdminResp } from './core'

/** 登录用户信息结构：含 id/用户名/显示名/角色/所属租户 */
export interface AuthUser {
  id: number
  username: string
  display_name: string
  role: string
  tenant_id: number
  [key: string]: unknown
}

// LoginResp 登录接口响应结构：token 为 JWT 凭证、user 为当前用户信息。
export interface LoginResp {
  success: boolean
  message?: string
  token?: string
  /** 品牌专属域名：当用户所属租户配置了独立子域且本次登录不在该子域时返回，前端据此带 token 跳转过去 */
  brand_host?: string
  user?: AuthUser
  /** ★ S1 积分制：1 积分 = N 内部 token（authMe 下发，供前端统一换算展示） */
  points_tokens_rate?: number
}

/** 账号密码登录，成功返回 JWT token 与用户信息 */
/** 登录：用户名+密码，返回会话 token 与用户/组织上下文 */
export async function login(username: string, password: string): Promise<LoginResp> {
  return request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
}

/** 登录后自助修改密码（校验原密码）。首登强制改密（must_change_pwd=1）时用于设置新密码 */
/** 修改密码（校验旧密码） */
export async function changePassword(old_password: string, new_password: string): Promise<AdminResp> {
  return request('/api/auth/change-password', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ old_password, new_password }) })
}

/** 校验当前 token 对应的用户信息（用于会话恢复） */
/** 当前用户信息（会话恢复、积分汇率注入源） */
export async function authMe(): Promise<LoginResp> {
  return request('/api/auth/me', { headers: authHeaders() })
}

/**
 * 自助注册：可带邀请码/租户信息/行业/邮箱验证码/人机验证 token。
 * @param data 注册字段（username/password 必填，其余可选；ref 为邀请裂变个人码）
 */
/** 自助注册：组织名/邮箱验证码/Turnstile/邀请码/UTM 归因一并上报 */
export async function authRegister(data: { username: string; password: string; type?: string; code?: string; name?: string; invite?: string; email?: string; email_code?: string; captcha_token?: string; industry?: string; role_choice?: string; ref?: string; agreed?: boolean; brand_name?: string; brand_name_en?: string; brand_names?: string; landing_path?: string; utm_source?: string; utm_medium?: string; utm_campaign?: string; utm_term?: string; utm_content?: string }): Promise<AdminResp> {
  return request('/api/auth/register', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 发送注册邮箱验证码（noop=true 表示服务端邮件未配置，验证码打印在服务端日志） */
/** 注册邮箱验证码（开启 Turnstile 时须带人机 token） */
export async function sendEmailCode(email: string, captchaToken?: string): Promise<AdminResp & { noop?: boolean }> {
  return request('/api/auth/email-code', { method: 'POST', body: JSON.stringify({ email, captcha_token: captchaToken }) })
}

/** 获取公开注册配置（email_verify_enabled，前端据以显隐验证码输入） */
/** 注册页公开配置（邮箱验证/人机验证开关等） */
export async function registerConfig(): Promise<AdminResp & { email_verify_enabled?: boolean }> {
  return request('/api/auth/register-config')
}

/** 忘记密码：发送验证码到绑定邮箱 */
/** 忘记密码：向绑定邮箱发送重置验证码 */
export async function forgotPassword(data: { username?: string; email?: string }): Promise<AdminResp> {
  return request('/api/auth/forgot-password', { method: 'POST', body: JSON.stringify(data) })
}

/** 重置密码：校验验证码并设置新密码 */
/** 忘记密码：验证码核验 */
export async function resetPassword(data: { username: string; code: string; new_password: string }): Promise<AdminResp> {
  return request('/api/auth/reset-password', { method: 'POST', body: JSON.stringify(data) })
}

/** 获取注册行业列表（无需登录，来自超管维护的行业包） */
/** 注册可选行业清单（超管配置驱动） */
export async function registerIndustries(): Promise<AdminResp> {
  return request('/api/register/industries')
}

// ============================================================================
// 自助修改密码（邮箱校验流程，复用找回密码通道）
// ============================================================================

/** sendPwdCode 向账号绑定邮箱发送改密验证码（username/email 二选一定位）。 */
export async function sendPwdCode(data: { username?: string; email?: string }): Promise<AdminResp> {
  return forgotPassword(data)
}

/** submitNewPassword 校验验证码并设置新密码。 */
/** 忘记密码尾步：验证码通过后设置新密码 */
export async function submitNewPassword(data: { username: string; code: string; new_password: string }): Promise<AdminResp> {
  return resetPassword(data)
}

/** meEmailCode 向新邮箱发送变更验证码（需登录） */
/** 换绑邮箱：向新邮箱发验证码 */
export async function meEmailCode(email: string): Promise<AdminResp & { noop?: boolean }> {
  return request('/api/me/email-code', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ email }) })
}

/** updateEmail 登录用户自助绑定/修改邮箱（需携带发往新邮箱的验证码） */
/** 换绑邮箱（新旧双向验证码） */
export async function updateEmail(email: string, code: string, oldCode = ''): Promise<AdminResp> {
  return request('/api/me/update-email', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ email, new_code: code, old_code: oldCode }) })
}

/** deactivateAccount 自助注销：当日宽限、次日失效；名下 API Key 立即停用；数据保留 */
/** 账号自助注销（数据按合规策略处理） */
export async function deactivateAccount(): Promise<AdminResp> {
  return request('/api/me/deactivate', { method: 'POST' })
}
