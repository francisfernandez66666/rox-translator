// ============================================================================
// api/auth.ts — 认证域接口
// 职责：账号密码登录、会话恢复（me）、自助注册（可带邀请码/租户信息）
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// ============ 本文件职责中文说明 ============
// 封装登录、会话恢复(me)、自助注册/找回密码/改绑邮箱等认证接口
// ========================================

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
}

/** 账号密码登录，成功返回 JWT token 与用户信息 */
export async function login(username: string, password: string): Promise<LoginResp> {
  return request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
}

/** 校验当前 token 对应的用户信息（用于会话恢复） */
export async function authMe(): Promise<LoginResp> {
  return request('/api/auth/me', { headers: authHeaders() })
}

/**
 * 自助注册：可带邀请码/租户信息/行业/邮箱验证码/人机验证 token。
 * @param data 注册字段（username/password 必填，其余可选；ref 为邀请裂变个人码）
 */
export async function authRegister(data: { username: string; password: string; type?: string; code?: string; name?: string; invite?: string; email?: string; email_code?: string; captcha_token?: string; industry?: string; role_choice?: string; ref?: string; agreed?: boolean }): Promise<AdminResp> {
  return request('/api/auth/register', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

/** 发送注册邮箱验证码（noop=true 表示服务端邮件未配置，验证码打印在服务端日志） */
export async function sendEmailCode(email: string, captchaToken?: string): Promise<AdminResp & { noop?: boolean }> {
  return request('/api/auth/email-code', { method: 'POST', body: JSON.stringify({ email, captcha_token: captchaToken }) })
}

/** 获取公开注册配置（email_verify_enabled/registration_review，前端据以显隐验证码输入） */
export async function registerConfig(): Promise<AdminResp & { email_verify_enabled?: boolean; registration_review?: boolean }> {
  return request('/api/auth/register-config')
}

/** 忘记密码：发送验证码到绑定邮箱 */
export async function forgotPassword(data: { username?: string; email?: string }): Promise<AdminResp> {
  return request('/api/auth/forgot-password', { method: 'POST', body: JSON.stringify(data) })
}

/** 重置密码：校验验证码并设置新密码 */
export async function resetPassword(data: { username: string; code: string; new_password: string }): Promise<AdminResp> {
  return request('/api/auth/reset-password', { method: 'POST', body: JSON.stringify(data) })
}

/** 获取注册行业列表（无需登录，来自超管维护的行业包） */
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
export async function submitNewPassword(data: { username: string; code: string; new_password: string }): Promise<AdminResp> {
  return resetPassword(data)
}

/** meEmailCode 向新邮箱发送变更验证码（需登录） */
export async function meEmailCode(email: string): Promise<AdminResp & { noop?: boolean }> {
  return request('/api/me/email-code', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ email }) })
}

/** updateEmail 登录用户自助绑定/修改邮箱（需携带发往新邮箱的验证码） */
export async function updateEmail(email: string, code: string, oldCode = ''): Promise<AdminResp> {
  return request('/api/me/update-email', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ email, new_code: code, old_code: oldCode }) })
}

/** deactivateAccount 自助注销：当日宽限、次日失效；名下 API Key 立即停用；数据保留 */
export async function deactivateAccount(): Promise<AdminResp> {
  return request('/api/me/deactivate', { method: 'POST' })
}
