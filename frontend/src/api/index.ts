import type { ChatResponse, HealthResponse, ProgressEvent } from '@/types'

// ---- 后端地址配置 ----
const API_BASE = import.meta.env.VITE_API_BASE || ''

// 登录态：所有请求自动带 Authorization Bearer，租户由后端从 JWT 解析
let authToken = localStorage.getItem('auth_token') || ''
// 超管生效租户：以 X-Tenant-ID 下发（仅超级管理员使用租户切换器）
let activeTenantId = Number(localStorage.getItem('active_tenant_id') || 0)

export function setAuthToken(token: string) {
  authToken = token
  try { localStorage.setItem('auth_token', token) } catch {}
}

export function getAuthToken(): string {
  return authToken
}

export function setActiveTenantId(tid: number) {
  activeTenantId = tid
  try { localStorage.setItem('active_tenant_id', String(tid)) } catch {}
}

export function getActiveTenantId(): number {
  return activeTenantId
}

function authHeaders(): Record<string, string> {
  const h: Record<string, string> = {}
  if (authToken) h['Authorization'] = `Bearer ${authToken}`
  if (activeTenantId > 0) h['X-Tenant-ID'] = String(activeTenantId)
  return h
}

async function request<T>(url: string, options?: RequestInit): Promise<T> {
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

/** SSE 流式聊天接口 */
export async function chatStream(
  message: string,
  skill?: string,
  options?: Record<string, unknown>,
  onProgress?: (event: ProgressEvent) => void,
  signal?: AbortSignal,
): Promise<ChatResponse> {
  const body = JSON.stringify({ message, skill: skill || '', options: options || {} })
  const response = await fetch(`${API_BASE}/api/chat/stream`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', ...authHeaders() },
    body,
    signal,
  })

  if (!response.ok) {
    const errorText = await response.text()
    throw new Error(`请求失败 (${response.status}): ${errorText}`)
  }

  const reader = response.body?.getReader()
  if (!reader) throw new Error('无法读取流式响应')

  const decoder = new TextDecoder()
  let buffer = ''
  let finalResult: ChatResponse | null = null

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''
    for (const line of lines) {
      const trimmed = line.trim()
      if (!trimmed.startsWith('data: ')) continue
      const jsonStr = trimmed.slice(6)
      if (jsonStr === '[DONE]') continue
      try {
        const event: ProgressEvent = JSON.parse(jsonStr)
        if (event.type === 'progress' && onProgress) {
          onProgress(event)
        } else if (event.type === 'done') {
          finalResult = event.result || null
        } else if (event.type === 'error') {
          throw new Error(event.error || '翻译出错')
        }
      } catch (e) {
        if (e instanceof Error && !e.message.includes('JSON')) throw e
      }
    }
  }
  if (!finalResult) throw new Error('未收到翻译结果')
  return finalResult
}

/** 健康检查 */
export async function healthCheck(): Promise<HealthResponse> {
  return request('/api/health')
}

/** SSE 流式文件翻译 */
export async function translateFileStream(
  file: File,
  targetLangs?: string[],
  useOnline: boolean = true,
  onProgress?: (event: ProgressEvent) => void,
  signal?: AbortSignal,
  userMessage: string = "",
): Promise<ChatResponse> {
  const formData = new FormData()
  formData.append('file', file)
  if (targetLangs && targetLangs.length > 0) {
    formData.append('target_langs', targetLangs.join(','))
  }
  formData.append('use_online', String(useOnline))
  if (userMessage) formData.append('message', userMessage)

  const response = await fetch(`${API_BASE}/api/translate/stream`, {
    method: 'POST',
    headers: tenantHeaders(),
    body: formData,
    signal,
  })

  if (!response.ok) {
    const errorText = await response.text()
    throw new Error(`文件翻译失败 (${response.status}): ${errorText}`)
  }

  const reader = response.body?.getReader()
  if (!reader) throw new Error('无法读取流式响应')

  const decoder = new TextDecoder()
  let buffer = ''
  let finalResult: ChatResponse | null = null

  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    const lines = buffer.split('\n')
    buffer = lines.pop() || ''
    for (const line of lines) {
      const trimmed = line.trim()
      if (!trimmed.startsWith('data: ')) continue
      const jsonStr = trimmed.slice(6)
      if (jsonStr === '[DONE]') continue
      try {
        const event: ProgressEvent = JSON.parse(jsonStr)
        if (event.type === 'progress' && onProgress) {
          onProgress(event)
        } else if (event.type === 'done') {
          finalResult = event.result || null
        } else if (event.type === 'error') {
          throw new Error(event.error || '文件翻译出错')
        }
      } catch (e) {
        if (e instanceof Error && !e.message.includes('JSON')) throw e
      }
    }
  }
  if (!finalResult) throw new Error('未收到翻译结果')
  return finalResult
}

/** 获取文件下载URL */
export function getDownloadUrl(filePath: string): string {
  return `${API_BASE}/api/download/?path=${encodeURIComponent(filePath)}`
}

// ==================== SaaS 租户管理 ====================

export interface TenantInfo {
  id: number
  code: string
  name: string
  status: string
  expires_at: string
  permissions: string
  created_at: string
  updated_at: string
}

interface TenantResp {
  success: boolean
  message?: string
  tenants?: TenantInfo[]
  tenant?: TenantInfo
}

// 租户管理：统一走 JWT 登录认证（管理后台 admin 角色）
export async function tenantList(): Promise<TenantResp> {
  return request('/api/tenant/list', { headers: authHeaders() })
}

export async function tenantCreate(
  data: { code: string; name: string; expires_at: string; permissions: string; admin_user?: string; admin_pass?: string },
): Promise<TenantResp> {
  return request('/api/tenant/create', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

export async function tenantUpdate(
  data: { id: number; name: string; expires_at: string; permissions: string },
): Promise<TenantResp> {
  return request('/api/tenant/update', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

export async function tenantSetStatus(id: number, status: string): Promise<TenantResp> {
  return request('/api/tenant/status', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, status }),
  })
}

export async function tenantDelete(id: number): Promise<TenantResp> {
  return request('/api/tenant/delete', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id }),
  })
}

// ==================== 认证（JWT 后台） ====================

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

export async function login(username: string, password: string): Promise<LoginResp> {
  return request('/api/auth/login', { method: 'POST', body: JSON.stringify({ username, password }) })
}

export async function authMe(): Promise<LoginResp> {
  return request('/api/auth/me', { headers: authHeaders() })
}

// ==================== 工单 ====================

export interface Ticket {
  id: number
  tenant_id: number
  ticket_no: string
  title: string
  status: string
  source_text: string
  file_path: string
  target_langs: string
  created_by: number
  approver_id: number
  reviewer_id: number
  reject_reason: string
  final_result: string
  created_at: string
  updated_at: string
}

interface TicketResp {
  success: boolean
  message?: string
  tickets?: Ticket[]
  ticket?: Ticket
  states?: unknown[]
}

export async function ticketList(mine?: boolean): Promise<TicketResp> {
  return request(`/api/tickets${mine ? '?mine=1' : ''}`, { headers: authHeaders() })
}

export async function ticketCreate(data: { title: string; source_text: string; target_langs: string }): Promise<TicketResp> {
  return request('/api/tickets/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

export async function ticketRun(id: number): Promise<TicketResp> {
  return request('/api/tickets/run', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

export async function ticketDetail(id: number): Promise<TicketResp> {
  return request(`/api/tickets/detail?id=${id}`, { headers: authHeaders() })
}

// ==================== 审批 ====================

export async function approveList(): Promise<TicketResp> {
  return request('/api/approve/list', { headers: authHeaders() })
}

export async function approveAction(id: number, action: 'approve' | 'reject', reason: string, suggestion: string, approvedText: string): Promise<TicketResp> {
  return request('/api/approve/action', {
    method: 'POST', headers: authHeaders(),
    body: JSON.stringify({ id, action, reason, suggestion, approved_text: approvedText }),
  })
}

// ==================== 后台（admin） ====================

interface AdminResp {
  success: boolean
  message?: string
  [key: string]: unknown
}

export async function adminUsers(): Promise<AdminResp> {
  return request('/api/admin/users', { headers: authHeaders() })
}

export async function adminUserCreate(data: { username: string; password: string; display_name: string; role: string }): Promise<AdminResp> {
  return request('/api/admin/users/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

export async function adminUserUpdate(id: number, data: { display_name: string; role: string; status: string }): Promise<AdminResp> {
  return request('/api/admin/users/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, ...data }) })
}

export async function adminUserResetPassword(id: number, password: string): Promise<AdminResp> {
  return request('/api/admin/users/reset-password', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, password }) })
}

// 行业包（KB 包）
export async function kbPackages(): Promise<AdminResp> {
  return request('/api/admin/kb-packages', { headers: authHeaders() })
}

export async function kbPackageCreate(data: { code: string; name: string; pack_type: string; role: string }): Promise<AdminResp> {
  return request('/api/admin/kb-packages/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

export async function kbPackageDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-packages/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

export async function kbEntries(packageId: number): Promise<AdminResp> {
  return request(`/api/admin/kb-entries?package_id=${packageId}`, { headers: authHeaders() })
}

export async function kbEntryAdd(data: { package_id: number; layer: number; source_text: string; target_lang: string; target_text: string; module: string }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

export async function kbEntryDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-entries/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 流程引擎
export interface FlowStepItem {
  key: string
  name: string
  enable: boolean
}

export async function flowConfig(): Promise<AdminResp> {
  return request('/api/admin/flow', { headers: authHeaders() })
}

export async function flowSave(steps: FlowStepItem[]): Promise<AdminResp> {
  return request('/api/admin/flow/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ steps }) })
}

// 模型/策略
export async function adminModels(): Promise<AdminResp> {
  return request('/api/admin/models', { headers: authHeaders() })
}

export async function adminModelsSave(data: { api_base: string; api_key: string; model: string }): Promise<AdminResp> {
  return request('/api/admin/models/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

export async function adminPolicy(): Promise<AdminResp> {
  return request('/api/admin/policy', { headers: authHeaders() })
}

export async function adminPolicySave(policy: Record<string, number>): Promise<AdminResp> {
  return request('/api/admin/policy/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ policy }) })
}

// 系统/看板
export async function systemHealth(): Promise<AdminResp> {
  return request('/api/system/health', { headers: authHeaders() })
}

export async function systemAudit(): Promise<AdminResp> {
  return request('/api/system/audit', { headers: authHeaders() })
}

export async function evalsList(): Promise<AdminResp> {
  return request('/api/evals/list', { headers: authHeaders() })
}

// 计费/余额/充值
export async function billingBalance(): Promise<AdminResp> {
  return request('/api/billing/balance', { headers: authHeaders() })
}

export async function billingUsage(): Promise<AdminResp> {
  return request('/api/billing/usage', { headers: authHeaders() })
}

export async function billingOrders(): Promise<AdminResp> {
  return request('/api/billing/orders', { headers: authHeaders() })
}

export async function adminOrderCreate(data: { tenant_id: number; tokens: number; money: number }): Promise<AdminResp> {
  return request('/api/admin/orders/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

export async function adminOrderPay(id: number): Promise<AdminResp> {
  return request('/api/admin/orders/pay', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// API Key
export async function apiKeys(): Promise<AdminResp> {
  return request('/api/apikeys', { headers: authHeaders() })
}

export async function apiKeyCreate(data: { name: string; perms: string }): Promise<AdminResp> {
  return request('/api/apikeys/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

export async function apiKeyStatus(id: number, status: string): Promise<AdminResp> {
  return request('/api/apikeys/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, status }) })
}

export async function apiKeyDelete(id: number): Promise<AdminResp> {
  return request('/api/apikeys/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}
