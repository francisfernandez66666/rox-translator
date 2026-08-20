// ============================================================================
// api/core.ts — API 基础设施
// 职责：后端地址、登录态（token/租户）、通用请求封装、文件下载地址等
// 所有域模块（auth/tenant/tickets/admin/...）均基于本文件提供的能力
// ============================================================================

// 后端地址配置：默认同源，可用 VITE_API_BASE 覆盖
const API_BASE = import.meta.env.VITE_API_BASE || ''
export { API_BASE }

// 登录态：所有请求自动带 Authorization Bearer，租户由后端从 JWT 解析
let authToken = localStorage.getItem('auth_token') || ''
// 超管生效租户：以 X-Tenant-ID 下发（仅超级管理员使用租户切换器）
let activeTenantId = Number(localStorage.getItem('active_tenant_id') || 0)

// 设置登录 token 并持久化
export function setAuthToken(token: string) {
  authToken = token
  try { localStorage.setItem('auth_token', token) } catch {}
}

// 读取登录 token
export function getAuthToken(): string {
  return authToken
}

// 设置生效租户 ID 并持久化（超管租户切换器使用）
export function setActiveTenantId(tid: number) {
  activeTenantId = tid
  try { localStorage.setItem('active_tenant_id', String(tid)) } catch {}
}

// 读取生效租户 ID
export function getActiveTenantId(): number {
  return activeTenantId
}

// 组装认证请求头（Authorization + X-Tenant-ID）
export function authHeaders(): Record<string, string> {
  const h: Record<string, string> = {}
  if (authToken) h['Authorization'] = `Bearer ${authToken}`
  if (activeTenantId > 0) h['X-Tenant-ID'] = String(activeTenantId)
  return h
}

// 通用 JSON 请求封装：自动附带认证头，非 2xx 抛出错误，返回解析后的 JSON
export async function request<T>(url: string, options?: RequestInit): Promise<T> {
  const fullUrl = `${API_BASE}${url}`
  try {
    const response = await fetch(fullUrl, {
      headers: { 'Content-Type': 'application/json', ...authHeaders(), ...options?.headers },
      ...options,
    })
    if (!response.ok) {
      const errorText = await response.text()
      throw new Error(`请求失败 (${response.status}): ${errorText}`)
    }
    return await response.json()
  } catch (error) {
    if (error instanceof TypeError && error.message.includes('fetch')) {
      throw new Error('无法连接后端服务')
    }
    throw error
  }
}

// 后台接口通用返回结构
export interface AdminResp {
  success: boolean
  message?: string
  [key: string]: unknown
}

// 获取文件下载URL
export function getDownloadUrl(filePath: string): string {
  return `${API_BASE}/api/download/?path=${encodeURIComponent(filePath)}`
}

// 开放 API 文档地址（同源）
export function openAPIDocsUrl(): string {
  return `${API_BASE}/openapi/docs`
}