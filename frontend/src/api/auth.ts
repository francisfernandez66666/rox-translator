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

// 自助注册（可带邀请码/租户信息）
export async function authRegister(data: { username: string; password: string; code?: string; name?: string; invite?: string }): Promise<AdminResp> {
  return request('/api/auth/register', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}