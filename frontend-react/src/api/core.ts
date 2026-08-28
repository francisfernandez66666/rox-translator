// ============ 本文件职责中文说明 ============
// 封装后端地址、登录态(token/租户)与通用请求/文件下载地址等 API 基础设施
// ========================================

// ============================================================================
// api/core.ts — API 基础设施
// 职责：后端地址、登录态（token/租户）、通用请求封装、文件下载地址等
// 所有域模块（auth/tenant/tickets/admin/...）均基于本文件提供的能力
// ============================================================================

/** 后端地址配置：默认同源，可用 VITE_API_BASE 环境变量覆盖 */
const API_BASE = import.meta.env.VITE_API_BASE || ''
export { API_BASE }

// 登录态：所有请求自动带 Authorization Bearer，租户由后端从 JWT 解析
let authToken = localStorage.getItem('auth_token') || ''
// 超管生效租户：以 X-Tenant-ID 下发（仅超级管理员使用租户切换器）
// 存储键 v2：v1 键作废（防历史残留把超管误挂到具体租户，导致前台误命中该租户知识库）
const TENANT_KEY = 'active_tenant_id_v2'
let activeTenantId = Number(localStorage.getItem(TENANT_KEY) || 0)

/** 设置登录 token 并持久化到 localStorage */
export function setAuthToken(token: string) {
  authToken = token
  try { localStorage.setItem('auth_token', token) } catch {}
}

/** 读取当前登录 token */
export function getAuthToken(): string {
  return authToken
}

/** 设置并持久化超管生效租户 ID（用于租户切换器） */
export function setActiveTenantId(tid: number) {
  activeTenantId = tid
  try { localStorage.setItem(TENANT_KEY, String(tid)) } catch {}
}

/** 读取当前生效租户 ID */
export function getActiveTenantId(): number {
  return activeTenantId
}

/** 组装认证请求头（Authorization Bearer + X-Tenant-ID 租户头） */
export function authHeaders(): Record<string, string> {
  const h: Record<string, string> = {}
  if (authToken) h['Authorization'] = `Bearer ${authToken}`
  if (activeTenantId > 0) h['X-Tenant-ID'] = String(activeTenantId)
  return h
}

/**
 * 通用 JSON 请求封装：自动附带认证头，非 2xx 抛出错误，返回解析后的 JSON。
 * 支持 options.timeoutMs 设置请求超时（默认 30 秒），超时自动 Abort 并抛出明确错误。
 */
export async function request<T>(url: string, options?: RequestInit & { timeoutMs?: number }): Promise<T> {
  const fullUrl = `${API_BASE}${url}`
  // 组装 AbortController：外部 signal 与超时信号合并，任一触发即中断
  const timeoutMs = options?.timeoutMs ?? 30000
  const controller = new AbortController()
  const externalSignal = options?.signal
  // ★ 修复（2026-08-26 全仓评审 D4）：保存 handler 引用——旧实现 removeEventListener
  //   传入新建箭头函数，与 addEventListener 的不是同一引用，监听器永远移除不掉，
  //   每个带外部 signal 的请求泄漏一个 AbortSignal 监听器。
  const onExternalAbort = () => controller.abort()
  if (externalSignal) {
    if (externalSignal.aborted) controller.abort()
    else externalSignal.addEventListener('abort', onExternalAbort)
  }
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    const response = await fetch(fullUrl, {
      headers: { 'Content-Type': 'application/json', ...authHeaders(), ...options?.headers },
      ...options,
      signal: controller.signal,
    })
    if (!response.ok) {
      const errorText = await response.text()
      throw new Error(`请求失败 (${response.status}): ${errorText}`)
    }
    return await response.json()
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      if (externalSignal?.aborted) throw error
      throw new Error('请求超时，请检查后端服务是否正常')
    }
    if (error instanceof TypeError && error.message.includes('fetch')) {
      throw new Error('无法连接后端服务')
    }
    throw error
  } finally {
    clearTimeout(timer)
    externalSignal?.removeEventListener('abort', onExternalAbort)
  }
}

/** 后台接口通用返回结构：success 标记结果，message 为可选提示，其余字段透传 */
export interface AdminResp {
  success: boolean
  message?: string
  [key: string]: unknown
}

/** 获取文件下载 URL（带 path 编码查询参数） */
export function getDownloadUrl(filePath: string): string {
  return `${API_BASE}/api/download/?path=${encodeURIComponent(filePath)}`
}

/** 获取开放 API 文档地址（同源） */
export function openAPIDocsUrl(): string {
  return `${API_BASE}/openapi/docs`
}