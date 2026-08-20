// ============================================================================
// api/index.ts — 后端 API 封装层
// 职责：统一封装所有后端 HTTP 请求（fetch + SSE 流式）
// - 自动附加登录认证头（Authorization: Bearer）与租户头（X-Tenant-ID）
// - 覆盖：聊天翻译、健康检查、认证、租户、用户、行业包、流程、模型路由、
//         策略、看板、审计、告警、evals、工单、审批、计费/充值/配额/发票、
//         API Key、邀请码、GDPR 导出/清除等全部功能接口
// ============================================================================

// 类型定义：聊天响应 / 健康检查 / 流式进度事件
import type { ChatResponse, HealthResponse, ProgressEvent } from '@/types'

// ---- 后端地址配置 ----
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
function authHeaders(): Record<string, string> {
  const h: Record<string, string> = {}
  if (authToken) h['Authorization'] = `Bearer ${authToken}`
  if (activeTenantId > 0) h['X-Tenant-ID'] = String(activeTenantId)
  return h
}

// 通用 JSON 请求封装：自动附带认证头，非 2xx 抛出错误，返回解析后的 JSON
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

  // 文件上传用登录令牌认证头（不带租户头），与后端文件翻译接口对齐
  const response = await fetch(`${API_BASE}/api/translate/stream`, {
    method: 'POST',
    headers: authHeaders(),
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

// 租户信息数据结构
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
// 获取租户列表
export async function tenantList(): Promise<TenantResp> {
  return request('/api/tenant/list', { headers: authHeaders() })
}

// 创建新租户（可附带租户管理员账号）
export async function tenantCreate(
  data: { code: string; name: string; expires_at: string; permissions: string; admin_user?: string; admin_pass?: string },
): Promise<TenantResp> {
  return request('/api/tenant/create', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

// 更新租户信息（名称/有效期/权限）
export async function tenantUpdate(
  data: { id: number; name: string; expires_at: string; permissions: string },
): Promise<TenantResp> {
  return request('/api/tenant/update', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify(data),
  })
}

// 启用/停用租户
export async function tenantSetStatus(id: number, status: string): Promise<TenantResp> {
  return request('/api/tenant/status', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id, status }),
  })
}

// 删除租户（连同数据一并删除）
export async function tenantDelete(id: number): Promise<TenantResp> {
  return request('/api/tenant/delete', {
    method: 'POST',
    headers: authHeaders(),
    body: JSON.stringify({ id }),
  })
}

// 租户数据导出（数据主权）/ 清除（GDPR 删除权，super_admin）
// 导出租户全部数据（JSON 文件下载）
export async function tenantExport(id: number): Promise<AdminResp> {
  return request('/api/tenant/export', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// GDPR 清除租户全部业务数据（不可恢复）
export async function tenantErase(id: number): Promise<AdminResp> {
  return request('/api/tenant/erase', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// ==================== 认证（JWT 后台） ====================

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

// ==================== 工单 ====================

// 翻译工单信息
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

// 工单列表（mine=true 仅查看自己的）
export async function ticketList(mine?: boolean): Promise<TicketResp> {
  return request(`/api/tickets${mine ? '?mine=1' : ''}`, { headers: authHeaders() })
}

// 创建翻译工单
export async function ticketCreate(data: { title: string; source_text: string; target_langs: string }): Promise<TicketResp> {
  return request('/api/tickets/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 运行工单翻译流程
export async function ticketRun(id: number): Promise<TicketResp> {
  return request('/api/tickets/run', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 工单详情
export async function ticketDetail(id: number): Promise<TicketResp> {
  return request(`/api/tickets/detail?id=${id}`, { headers: authHeaders() })
}

// ==================== 审批 ====================

// 待审批工单列表
export async function approveList(): Promise<TicketResp> {
  return request('/api/approve/list', { headers: authHeaders() })
}

// 审批操作：批准或驳回（附原因/建议/审定译文）
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

// 用户列表
export async function adminUsers(): Promise<AdminResp> {
  return request('/api/admin/users', { headers: authHeaders() })
}

// 创建用户
export async function adminUserCreate(data: { username: string; password: string; display_name: string; role: string }): Promise<AdminResp> {
  return request('/api/admin/users/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 更新用户（显示名称/角色/状态）
export async function adminUserUpdate(id: number, data: { display_name: string; role: string; status: string }): Promise<AdminResp> {
  return request('/api/admin/users/update', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, ...data }) })
}

// 重置用户密码
export async function adminUserResetPassword(id: number, password: string): Promise<AdminResp> {
  return request('/api/admin/users/reset-password', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, password }) })
}

// ==================== 行业包（KB 包） ====================

// 行业包列表
export async function kbPackages(): Promise<AdminResp> {
  return request('/api/admin/kb-packages', { headers: authHeaders() })
}

// 创建行业包
export async function kbPackageCreate(data: { code: string; name: string; pack_type: string; role: string }): Promise<AdminResp> {
  return request('/api/admin/kb-packages/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 删除行业包
export async function kbPackageDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-packages/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 行业包内条目列表
export async function kbEntries(packageId: number): Promise<AdminResp> {
  return request(`/api/admin/kb-entries?package_id=${packageId}`, { headers: authHeaders() })
}

// 添加 KB 条目
export async function kbEntryAdd(data: { package_id: number; layer: number; source_text: string; target_lang: string; target_text: string; module: string }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/add', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 删除 KB 条目
export async function kbEntryDelete(id: number): Promise<AdminResp> {
  return request('/api/admin/kb-entries/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 批量导入 KB 条目（租户管理员）
export async function kbEntriesImport(data: { package_id: number; entries: { source_text: string; target_lang: string; target_text: string; layer?: number; module?: string }[] }): Promise<AdminResp> {
  return request('/api/admin/kb-entries/import', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 流程引擎 ====================

// 流程步骤配置项
export interface FlowStepItem {
  key: string
  name: string
  enable: boolean
}

// 读取流程引擎配置
export async function flowConfig(): Promise<AdminResp> {
  return request('/api/admin/flow', { headers: authHeaders() })
}

// 保存流程引擎配置（各步骤启停）
export async function flowSave(steps: FlowStepItem[]): Promise<AdminResp> {
  return request('/api/admin/flow/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ steps }) })
}

// ==================== 模型/策略 ====================

// 读取在线模型配置
export async function adminModels(): Promise<AdminResp> {
  return request('/api/admin/models', { headers: authHeaders() })
}

// 保存在线模型配置
export async function adminModelsSave(data: { api_base: string; api_key: string; model: string }): Promise<AdminResp> {
  return request('/api/admin/models/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 模型路由策略（super_admin） ====================

// 读取模型路由策略
export async function modelRoutes(): Promise<AdminResp> {
  return request('/api/admin/models/routes', { headers: authHeaders() })
}

// 保存模型路由策略（多供应商按权重路由 + 降级）
export async function modelRoutesSave(data: { routes: { provider: string; api_base: string; api_key: string; model: string; weight: number }[] }): Promise<AdminResp> {
  return request('/api/admin/models/routes/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 读取匹配策略参数
export async function adminPolicy(): Promise<AdminResp> {
  return request('/api/admin/policy', { headers: authHeaders() })
}

// 保存匹配策略参数
export async function adminPolicySave(policy: Record<string, number>): Promise<AdminResp> {
  return request('/api/admin/policy/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ policy }) })
}

// ==================== 系统/看板 ====================

// 系统健康状态（知识库/余额/流程/用量/模型状态等）
export async function systemHealth(): Promise<AdminResp> {
  return request('/api/system/health', { headers: authHeaders() })
}

// 审计日志列表
export async function systemAudit(): Promise<AdminResp> {
  return request('/api/system/audit', { headers: authHeaders() })
}

// ==================== 监控告警 ====================

// 告警列表（可按状态过滤）
export async function systemAlerts(status?: string): Promise<AdminResp> {
  const q = status ? `?status=${status}` : ''
  return request(`/api/system/alerts${q}`, { headers: authHeaders() })
}

// 解决告警
export async function alertResolve(id: number): Promise<AdminResp> {
  return request('/api/system/alerts/resolve', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// evals 评估记录列表
export async function evalsList(): Promise<AdminResp> {
  return request('/api/evals/list', { headers: authHeaders() })
}

// ==================== 计费/余额/充值 ====================

// 租户余额查询
export async function billingBalance(): Promise<AdminResp> {
  return request('/api/billing/balance', { headers: authHeaders() })
}

// 租户用量查询
export async function billingUsage(): Promise<AdminResp> {
  return request('/api/billing/usage', { headers: authHeaders() })
}

// 充值订单列表
export async function billingOrders(): Promise<AdminResp> {
  return request('/api/billing/orders', { headers: authHeaders() })
}

// 创建充值订单
export async function adminOrderCreate(data: { tenant_id: number; tokens: number; money: number }): Promise<AdminResp> {
  return request('/api/admin/orders/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 确认收款（订单状态置为已支付）
export async function adminOrderPay(id: number): Promise<AdminResp> {
  return request('/api/admin/orders/pay', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// ==================== API Key ====================

// 开放 API Key 列表
export async function apiKeys(): Promise<AdminResp> {
  return request('/api/apikeys', { headers: authHeaders() })
}

// 签发新 API Key
export async function apiKeyCreate(data: { name: string; perms: string }): Promise<AdminResp> {
  return request('/api/apikeys/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 启用/停用 API Key
export async function apiKeyStatus(id: number, status: string): Promise<AdminResp> {
  return request('/api/apikeys/status', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id, status }) })
}

// 轮换 API Key（旧 Key 立即失效）
export async function apiKeyRotate(id: number): Promise<AdminResp> {
  return request('/api/apikeys/rotate', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 删除 API Key
export async function apiKeyDelete(id: number): Promise<AdminResp> {
  return request('/api/apikeys/delete', { method: 'POST', headers: authHeaders(), body: JSON.stringify({ id }) })
}

// 开放 API 文档地址（同源）
export function openAPIDocsUrl(): string {
  return `${API_BASE}/openapi/docs`
}

// ==================== 自助注册/邀请码 ====================

// 自助注册（可带邀请码/租户信息）
export async function authRegister(data: { username: string; password: string; code?: string; name?: string; invite?: string }): Promise<AdminResp> {
  return request('/api/auth/register', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// 邀请码列表
export async function inviteCodes(): Promise<AdminResp> {
  return request('/api/admin/invite-codes', { headers: authHeaders() })
}

// 生成邀请码（绑定租户或留空新建租户）
export async function inviteCodeCreate(data: { code: string; tenant_id: number }): Promise<AdminResp> {
  return request('/api/admin/invite-codes/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 计费配置（super_admin） ====================

// 读取计费配置（是否强制计费）
export async function billingConfig(): Promise<AdminResp> {
  return request('/api/billing/config', { headers: authHeaders() })
}

// 保存计费配置
export async function billingConfigSave(data: { billing_enforced: boolean }): Promise<AdminResp> {
  return request('/api/billing/config/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 租户配额（QPS/并发/每日上限） ====================

// 读取当前租户配额
export async function billingQuota(): Promise<AdminResp> {
  return request('/api/billing/quota', { headers: authHeaders() })
}

// 保存当前租户配额
export async function billingQuotaSave(data: { qps: number; concurrent: number; max_daily_chars: number }): Promise<AdminResp> {
  return request('/api/billing/quota/save', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}

// ==================== 发票 ====================

// 发票列表
export async function billingInvoices(): Promise<AdminResp> {
  return request('/api/billing/invoices', { headers: authHeaders() })
}

// 创建发票（关联已支付订单）
export async function billingInvoiceCreate(data: { order_id: number; title: string; tax_no: string }): Promise<AdminResp> {
  return request('/api/billing/invoices/create', { method: 'POST', headers: authHeaders(), body: JSON.stringify(data) })
}
