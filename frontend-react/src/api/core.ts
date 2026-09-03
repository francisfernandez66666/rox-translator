// ============================================================================
// api/core.ts — API 基础设施
// 职责：后端地址、登录态（token/租户）、通用请求封装、文件下载地址等
// 所有域模块（auth/tenant/tickets/admin/...）均基于本文件提供的能力
// ============================================================================

/**
 * api/core.ts · 职责说明
 * 提供 API 请求的基础设施，包括：
 * - 后端地址配置：支持环境变量覆盖
 * - 登录态管理：token 和租户 ID 的读写与持久化
 * - 通用请求封装：自动附带认证头、超时控制、错误处理
 * - 401 拦截：登录态失效时自动清 token 并跳转登录页
 * - 文件下载地址：生成带认证的文件下载 URL
 */

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
// 注意：token 存于 localStorage（XSS 风险已由安全团队单独跟踪，本处不改动存储机制）。
export function setAuthToken(token: string) {
  authToken = token
  try { localStorage.setItem('auth_token', token) } catch {}
}

/** 读取当前登录 token */
export function getAuthToken(): string {
  return authToken
}

// 全局 401 拦截：登录态失效时清 token 并跳回登录页。
// 登录/注册等自身接口返回 401（如凭证错误）不触发跳转，避免循环。
let authRedirecting = false
// handleUnauthorized 统一处理 401 响应：清除登录态并跳转回登录页（登录/注册接口自身除外，避免循环）
function handleUnauthorized(url: string) {
  if (url.includes('/api/auth/login') || url.includes('/api/auth/register')) return
  setAuthToken('') // 清除本地 token（同步清空内存与 localStorage）
  if (!authRedirecting) {
    authRedirecting = true
    window.location.href = '/'
  }
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
    if (response.status === 401) handleUnauthorized(url)
    if (!response.ok) {
      // 优先解析后端结构化错误体（{message}）作为用户可读信息；解析失败回退状态码 + 原文
      let message = ''
      try {
        const body = await response.json()
        if (body && typeof body === 'object') {
          const m = (body as { message?: string; error?: string }).message || (body as { error?: string }).error
          if (typeof m === 'string' && m) message = m
        }
      } catch { /* 非 JSON 错误体 */ }
      if (!message) {
        const text = await response.text().catch(() => '')
        message = `请求失败 (${response.status}): ${text}`
      }
      throw new Error(message)
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