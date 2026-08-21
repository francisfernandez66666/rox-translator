// ============================================================================
// api/auth.ts — 认证域接口
// 职责：账号密码登录、会话恢复（me）、自助注册（可带邀请码/租户信息）
// ============================================================================

import { request, authHeaders, type AdminResp } from './core'

// 登录用户信息
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
  user?: AuthUser
}

// 账号密码登录，成功返回 JWT token 与用户信息
export async function login(username: string, password: string): Promise<LoginResp> {
  return request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
}

// 校验当前 token 对应的用户信息（会话恢复用）
export async function authMe(): Promise<LoginResp> {
  return request('/api/auth/me', { headers: authHeaders() })
}

// 自助注册（可带邀请码/租户信息/行业）
export async function authRegister(data: { username: string; password: string; code?: string; name?: string; invite?: string; email?: string; industry?: string }): Promise<AdminResp> {
  return request('/api/auth/register', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 忘记密码：发送验证码到绑定邮箱
export async function forgotPassword(data: { username?: string; email?: string }): Promise<AdminResp> {
  return request('/api/auth/forgot-password', { method: 'POST', body: JSON.stringify(data) })
}

// 重置密码：校验验证码并设置新密码
export async function resetPassword(data: { username: string; code: string; new_password: string }): Promise<AdminResp> {
  return request('/api/auth/reset-password', { method: 'POST', body: JSON.stringify(data) })
}

// 注册行业列表（无需登录，来自超管维护的行业包）
export async function registerIndustries(): Promise<AdminResp> {
  return request('/api/register/industries')
}