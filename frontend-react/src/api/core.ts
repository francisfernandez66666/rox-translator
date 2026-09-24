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
 * - 403 统一处理：越权访问集中识别，对外文案走既有 i18n 键、附稳定错误码 FORBIDDEN
 * - 文件下载地址：生成带认证的文件下载 URL
 */

/** 后端地址配置：默认同源，可用 VITE_API_BASE 环境变量覆盖 */
const API_BASE = import.meta.env.VITE_API_BASE || ''
export { API_BASE }

// 登录态：所有请求自动带 Authorization Bearer，租户由后端从 JWT 解析
let authToken = sessionStorage.getItem('auth_token') || ''
// 超管生效租户：以 X-Tenant-ID 下发（仅超级管理员使用租户切换器）
// 存储键 v2：v1 键作废（防历史残留把超管误挂到具体租户，导致前台误命中该租户知识库）
const TENANT_KEY = 'active_tenant_id_v2'
let activeTenantId = Number(sessionStorage.getItem(TENANT_KEY) || 0)

/** 设置登录 token 并持久化到 sessionStorage（比 localStorage 更安全：不跨标签页、关闭即清） */
export function setAuthToken(token: string) {
  authToken = token
  try { sessionStorage.setItem('auth_token', token) } catch {}
}

/** 读取当前登录 token */
export function getAuthToken(): string {
  return authToken
}

// 全局 401 拦截：登录态失效时清 token 并落回登录页。
// 登录/注册等自身接口返回 401（如凭证错误）不触发，避免循环。
// ★ P0-6（2026-09-21 评估报告 E8 整改）：旧实现 `href='/'` 与注释「跳登录页」自相矛盾，
//   且把用户从当前页踢回首页丢掉了回跳目标。现改为：清 token + 调 AuthProvider 注册的
//   状态复位钩子（user→null），Root 未登录守卫在**当前路径**原地渲染 Login——
//   登录成功即回到失效前页面，回跳天然成立，无需 URL 参数；营销门面（/、/pricing 等）
//   只清态不强改视图，访客浏览不受打扰。
let resetAuthState: (() => void) | null = null
/** 注册 401 时的登录态复位钩子（由 stores/auth AuthProvider 挂载时注入，避免 api→stores 反向依赖） */
export function setUnauthorizedHandler(fn: (() => void) | null) {
  resetAuthState = fn
}
// handleUnauthorized 统一处理 401 响应：清除登录态并落回登录页（登录/注册接口自身除外，避免循环）
// ★ E5：导出给 SSE 等手工 fetch 通道复用（chat/translate stream 此前无 401 处理）
export function handleUnauthorized(url: string) {
  if (url.includes('/api/auth/login') || url.includes('/api/auth/register')) return
  setAuthToken('') // 清除本地 token（同步清空内存与 sessionStorage）
  resetAuthState?.() // 复位全局 user → Root 守卫在当前路径出登录页（回跳=原路径）
}

// ============================================================================
// 403 统一处理（★ 2026-09-22 §4.2-3 前端质量债批）
// 背景：旧 core 只判 401，403（已登录但越权）与其余非 2xx 混在一起，前端无从区分、
//   也拿不到统一的本地化「无权限」文案。这里补齐集中识别：
//   - request() 及各裸 fetch 通道命中 403 时统一走 handleForbidden()，产出对外文案 +
//     稳定错误码 'FORBIDDEN'，调用方 catch 到的 ApiError.status=403 可据此分支。
// ★ WHY 文案不在此直接 import i18n：core 是 api 基础设施、被所有域模块引用；
//   i18n 模块加载期即读 localStorage（见 i18n/index.ts），而 core.test.ts 跑在 **node 环境**
//   （仅 stub sessionStorage/window），静态 import i18n 会让整个 api 层测试在模块解析期即崩。
//   且「基础设施层」不应反向依赖「UI 文案层」。故沿用 401 的钩子注入口径：由上层（ToastBridge）
//   在运行时注册解析器，用既有键 admin.forbid 提供本地化文案；未注册（单测/SSR）时回落入参/后端 message。
// ★ 刻意不新增 i18n 键（本批硬约束），复用全站权限文案 admin.forbid；若后端 message 更具体可并入 desc。
// ============================================================================
let forbiddenCopy: ((backendMessage: string) => string) | null = null
/** 注册 403 对外文案解析器（由 ToastBridge 挂载时注入，避免 api→i18n 反向依赖；传 null 复位） */
export function setForbiddenCopyResolver(fn: ((backendMessage: string) => string) | null) {
  forbiddenCopy = fn
}

// ★ 2026-09-24 后台去写死中文批：api 层通用「本地化文案注入器」——与 403 解析器同口径，
//   core 及兄弟 api 模块（kb/translate/billing/admin）不静态 import i18n（会崩 api 层 node 单测），
//   由 ToastBridge 运行时注入 tpl()；未注册（单测/SSR）回落传入的中文原句，行为与旧版一致。
let msgCopy: ((key: string, fallback: string, vars?: Record<string, string | number>) => string) | null = null
/** 注册/清空 api 层文案注入器（传 null 恢复未注入态，用于卸载或测试复位） */
export function setApiMsgCopier(fn: typeof msgCopy) { msgCopy = fn }
/** api 层取词：已注入翻译器按键取当前界面语言文案，取不到（缺键/未注入）用 fallback 原句 */
export function apiMsg(key: string, fallback: string, vars?: Record<string, string | number>): string {
  if (!msgCopy) return fallback
  const v = msgCopy(key, fallback, vars)
  return v || fallback
}
/**
 * 统一处理 403：返回对外可读文案，与 handleUnauthorized 不同，403 不清登录态
 * （用户仍在线，只是无该资源权限），仅把文案收口到一处，保证任一请求通道
 * （client / 裸 fetch）的越权提示一致。
 * ★ 2026-09-24 匿名认证链路豁免：/api/auth/* 的请求本就没有登录态，其 403 是
 *   人机验证/注册关闭一类拒绝（后端文案），再套「当前账号无管理权限，请使用管理员
 *   账号登录」就是把排查往歧路上带（AI 注册发码 403 事故的真实表现）。故该前缀
 *   一律透出后端 message，仅在其缺失时回落通用兜底。
 */
export function handleForbidden(backendMessage = '', url = ''): string {
  if (url.startsWith('/api/auth/')) {
    return backendMessage || apiMsg('common.forbidden', '无权限访问该资源')
  }
  return forbiddenCopy ? forbiddenCopy(backendMessage) : (backendMessage || apiMsg('common.forbidden', '无权限访问该资源'))
}

/** 设置并持久化超管生效租户 ID（用于租户切换器） */
export function setActiveTenantId(tid: number) {
  activeTenantId = tid
  try { sessionStorage.setItem(TENANT_KEY, String(tid)) } catch {}
}

/** 读取当前生效租户 ID */
export function getActiveTenantId(): number {
  return activeTenantId
}

/** 读取界面语言（★ 〇-S #12 后端语言识别）：直接读 localStorage 的 app_lang，
 *  不 import i18n 模块——api 层的 node 环境单测没有 window，且语种键是稳定契约。
 *  读不到/异常返回空串（请求头不带，后端按 Accept-Language→默认中文降级）。 */
export function currentUiLang(): string {
  try {
    return typeof localStorage === 'undefined' ? '' : (localStorage.getItem('app_lang') || '')
  } catch { return '' }
}

/** 组装认证请求头（Authorization Bearer + X-Tenant-ID 租户头 + X-App-Lang 界面语种） */
export function authHeaders(): Record<string, string> {
  const h: Record<string, string> = {}
  if (authToken) h['Authorization'] = `Bearer ${authToken}`
  if (activeTenantId > 0) h['X-Tenant-ID'] = String(activeTenantId)
  // ★ 2026-09-24 〇-S #12：让后端提示语按用户界面语言返回（zh 系/英文/其余翻英，后端归一）
  const lang = currentUiLang()
  if (lang) h['X-App-Lang'] = lang
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
  // ★ E4：headers 合并必须在 ...options 展开【之后】——旧写法 options 自带的
  //   headers 字段会后盖掉合并结果，凡传 headers 的调用全部丢失 Content-Type/认证头。
  const { headers: optHeaders, timeoutMs: _omitTimeout, signal: _omitSignal, ...restOptions } = options ?? {}
  // ★ multipart/二进制上传修复：body 为 FormData 时禁止预设 Content-Type，
  //   否则浏览器不会生成 multipart/form-data 的 boundary，后端 ParseMultipartForm
  //   瞬间 400（曾表现为「文件解析失败或超过大小上限（40MB）」误导文案）。
  //   FormData/Blob 交由 fetch 自动设置带 boundary 的 Content-Type；调用方显式传入者优先。
  const isFormBody = typeof FormData !== 'undefined' && options?.body instanceof FormData
  const baseHeaders: Record<string, string> = isFormBody ? {} : { 'Content-Type': 'application/json' }
  try {
    // ★ §4.2-2 正当豁免（1/2）：此处 fetch 就是「统一 client」本体，全站 request() 经此出口，
    //   不能再自我收敛到 request()，故裸用 fetch 是唯一正确写法。SSE 通道见 api/translate.ts 的豁免说明。
    const response = await fetch(fullUrl, {
      ...restOptions,
      headers: { ...baseHeaders, ...authHeaders(), ...(optHeaders as Record<string, string>) },
      signal: controller.signal,
    })
    if (response.status === 401) handleUnauthorized(url)
    if (!response.ok) {
      // 优先解析后端结构化错误体（{message}）作为用户可读信息；解析失败回退状态码 + 原文
      let message = ''
      let errCode: string | undefined
      try {
        const body = await response.json()
        if (body && typeof body === 'object') {
          const m = (body as { message?: string; error?: string }).message || (body as { error?: string }).error
          if (typeof m === 'string' && m) message = m
          // 统一错误码透传：code 为稳定码（errors 包），error_code 为 OpenAPI/SDK 兼容别名
          errCode = (body as { code?: string }).code || (body as { error_code?: string }).error_code || undefined
        }
      } catch { /* 非 JSON 错误体 */ }
      if (!message) {
        const text = await response.text().catch(() => '')
        message = apiMsg('common.reqFailDetail', `请求失败 (${response.status}): ${text}`, { status: response.status, text })
      }
      // ★ §4.2-3：403 越权集中识别——对外文案走统一解析器（本地化既有键），并钉稳定错误码
      //   FORBIDDEN（后端未回 code 时补），调用方据此分支；不清登录态（用户仍在线）。
      //   ★ 2026-09-24：url 一并传入——/api/auth/* 匿名链路豁免（详见 handleForbidden 注释）。
      if (response.status === 403) {
        throw new ApiError(handleForbidden(message, url), 403, errCode || 'FORBIDDEN')
      }
      const err = new ApiError(message, response.status, errCode)
      throw err
    }
    return await response.json()
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') {
      if (externalSignal?.aborted) throw error
      throw new Error(apiMsg('common.timeout', '请求超时，请检查后端服务是否正常'))
    }
    if (error instanceof TypeError && error.message.includes('fetch')) {
      throw new Error(apiMsg('common.netFail', '无法连接后端服务'))
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

/**
 * ApiError 携带稳定错误码的请求错误（统一错误码体系）。
 * code 来源：HTTP 非 2xx 响应体 {code}/{error_code}；业务错误（HTTP 200 + success:false）
 * 不抛异常，由调用方用 bizErrorCode() 从响应体提取。
 */
export class ApiError extends Error {
  /** 稳定错误码（如 INSUFFICIENT_BALANCE / insufficient_balance） */
  readonly code?: string
  /** HTTP 状态码 */
  readonly status?: number

  constructor(message: string, status?: number, code?: string) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
  }
}

/** 从业务错误响应体（HTTP 200 + success:false）提取稳定错误码（兼容 code / error_code 双字段） */
export function bizErrorCode(body: unknown): string | undefined {
  if (body && typeof body === 'object') {
    const b = body as { code?: unknown; error_code?: unknown }
    const c = typeof b.code === 'string' ? b.code : undefined
    if (c) return c
    return typeof b.error_code === 'string' ? b.error_code : undefined
  }
  return undefined
}


/** 获取开放 API 文档地址（同源） */
export function openAPIDocsUrl(): string {
  return `${API_BASE}/openapi/docs`
}