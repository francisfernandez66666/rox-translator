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

// ============================================================================
// 错误体统一解析（★ 2026-09-26 〇-U 批 I-8 · F-52）
// ----------------------------------------------------------------------------
// 缺陷本体：SSE 通道（api/translate.ts 的 !response.ok 分支）自己写了
//   `throw new Error(\`请求失败 (${status}): ${await response.text()}\`)`，
//   后端 400 现在是**结构化 JSON**（{success,code,message,details,trace_id}，见
//   internal/errors/codes.go 与批 I-7 的状态码诚实改造），于是整段 JSON 被当正文塞进
//   聊天气泡——用户看到一屏 `{"success":false,"code":"chat_text_too_long",...,"trace_id":"..."}`。
//   useChat 只对 `<!doctype` 开头的 HTML 网关页做了过滤（F-29 批G），对 JSON 无判，
//   所以「错误体格式升级」这一侧的收益被前端静默吞掉。
//
// 手法：把「怎么读一个失败响应」收成一个函数，两条通道（统一 client / 裸 fetch 的 SSE）
//   共用同一判据，而不是在 SSE 里再抄一份 if/else（抄一份就意味着以后只改一处）。
//   规则按「能不能给人看」分两档：
//   ① JSON 对象 → message/error 作正文、code/error_code 作稳定码、整个对象留作 body，
//      **display 一律空串**：整段 JSON 永不进兜底文案（trace_id 因此也进不去，只在 body 里供排查）；
//   ② 其余（HTML 网关页 / 坏 JSON / 纯文本）→ 截前 200 字作 display 兜底。
//      ★ HTML 这一档**必须保留原文**，不是遗漏：useChat 的 isHtmlErrorBody() 就是靠气泡文案里
//      的 `<!doctype` 认出「网关把长请求掐成 524 错误页」并换成超时文案（F-29 批G，
//      useChat.dom.test.tsx 有锁）。在这里把它抹白，那条识别链就断了——反而把超时提示退化成
//      「请求失败 (524)」空壳。200 字截断不影响判据（doctype 在开头）。
//
// ★ 顺带修一个「读侧自伤」：旧 request() 先 `await response.json()`，失败后在 catch 里
//   再 `await response.text()`——Response 体只能消费一次，第二次直接抛
//   `body already consumed`，被 `.catch(()=> '')` 吞成空串，于是非 JSON 错误体的兜底文案
//   一直是 `请求失败 (400): `（后面空的）。这里改成**先读一次 text()、再对文本做 parse**，
//   兜底文案才真能带上后端原文。
// ============================================================================
export interface ErrEnvelope {
  /** 后端 message / error 字段（给人看的那句），取不到为空串 */
  message: string
  /** 稳定错误码（code 优先，error_code 为 OpenAPI/SDK 兼容别名） */
  code?: string
  /** parse 成功且是 JSON 对象时的原样对象（bizResp 据此还原历史响应体） */
  body?: Record<string, unknown>
  /** 允许拼进兜底文案的原文（HTML/JSON 一律空串，见上面三档规则） */
  display: string
}

/** 解析失败响应体文本（纯函数，喂字符串即可单测） */
export function parseErrEnvelope(raw: string): ErrEnvelope {
  const text = (raw ?? '').trim()
  if (!text) return { message: '', display: '' }
  if (text.startsWith('{') || text.startsWith('[')) {
    try {
      const parsed: unknown = JSON.parse(text)
      if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
        const b = parsed as Record<string, unknown>
        const m = b.message ?? b.error
        return {
          message: typeof m === 'string' ? m : '',
          code: (typeof b.code === 'string' && b.code) || (typeof b.error_code === 'string' && b.error_code) || undefined,
          body: b,
          display: '', // ★ 整段 JSON 不进正文（本缺陷的落点：这里给原文就是回归）
        }
      }
      // JSON 数组/标量：不是错误契约，按原文兜底（截断后）
      return { message: '', display: text.slice(0, 200) }
    } catch { /* 以 { 开头的坏 JSON：落到下面的原文兜底 */ }
  }
  return { message: '', display: text.slice(0, 200) }
}

/** 读一次响应体并解析（★ 只能调用一次——同一 Response 的 body 不可重复消费） */
export async function readErrEnvelope(response: { text(): Promise<string> }): Promise<ErrEnvelope> {
  const raw = await response.text().catch(() => '')
  return parseErrEnvelope(raw)
}

/**
 * 从任意 catch 到的错误里取 trace_id（★ F-52③：trace_id 只做「复制排查信息」，不进气泡正文）。
 * 后端统一错误体带 trace_id（observability 的 slog trace），前端此前零消费方——
 * 既没展示也没地方复制。这里收一个取值器：展示层要复制排查信息时调它，
 * **禁止**把它拼进 message/content（那正是本缺陷的形态）。
 */
export function errTraceId(e: unknown): string {
  if (e instanceof ApiError && e.body && typeof e.body.trace_id === 'string') return e.body.trace_id
  return ''
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
      // ★ F-52（批 I-8）：失败体的读法/判据收进 readErrEnvelope()，与 SSE 通道同一函数
      //   （旧写法在这里 json() 失败后再 text()，第二次消费必抛 → 兜底原文恒为空串）。
      //   message＝后端那句；structBody＝结构化对象（bizResp 靠它区分业务失败/网关坏体）；
      //   display＝可拼进兜底文案的原文（JSON/HTML 为空，不会把整段体送进界面）。
      const env = await readErrEnvelope(response)
      let message = env.message
      const errCode = env.code
      // ★ F-64①（批 I-7）：把解析成功的错误对象体留住，供 bizResp() 判定「这是后端主动下发的
      //   结构化业务失败」还是「网关/HTML/坏体」——前者可以还原成 success:false 响应体，
      //   后者必须照旧抛出。只有带结构化体的错误才可能被 bizResp 收敛。
      const structBody = env.body
      if (!message) {
        message = apiMsg('common.reqFailDetail', `请求失败 (${response.status})${env.display ? `: ${env.display}` : ''}`,
          { status: response.status, text: env.display })
      }
      // ★ §4.2-3：403 越权集中识别——对外文案走统一解析器（本地化既有键），并钉稳定错误码
      //   FORBIDDEN（后端未回 code 时补），调用方据此分支；不清登录态（用户仍在线）。
      //   ★ 2026-09-24：url 一并传入——/api/auth/* 匿名链路豁免（详见 handleForbidden 注释）。
      if (response.status === 403) {
        throw new ApiError(handleForbidden(message, url), 403, errCode || 'FORBIDDEN')
      }
      const err = new ApiError(message, response.status, errCode, structBody)
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
  /**
   * ★ F-64①（批 I-7）：后端下发的**结构化错误体**原样（{success:false, code, message, details…}）。
   * 只在「非 2xx 且响应体是合法 JSON 对象」时有值；网络层失败、超时、HTML 错误页一律为 undefined。
   * 用途：bizResp() 据此把「业务失败」还原成历史响应体形态，见该函数注释。
   */
  readonly body?: Record<string, unknown>

  constructor(message: string, status?: number, code?: string, body?: Record<string, unknown>) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.body = body
  }
}

/**
 * bizResp —— 把「HTTP 诚实状态码」还原成历史 `{success, message, ...}` 响应体的收敛器。
 *
 * ★ 背景（F-64① 批 I-7，2026-09-26）：后端充值/订阅/券/账单等接口过去用 **HTTP 200 承载失败**
 *   （`{success:false,...}`），客户与 SDK 只看状态码时全部判成成功——这是缺陷，已改为按语义
 *   给 400/404/409/500/503。但前端全站约定是 `if (!r.success)` / `toastResp(r)`（数百处），
 *   而 request() 对非 2xx 一律抛异常：只改后端就会让「点订阅失败」变成
 *   **未捕获的 promise rejection + 界面毫无提示**，等于把一个契约缺陷换成一个线上故障。
 *
 * ★ 手法（AGENTS §三「优先薄委托 + 零改动调用点」）：在**收款/账务域接口函数**这一层用
 *   bizResp 包一层，把后端结构化失败体还原成原响应形态（含 details 里的 order/order_no/
 *   coupon_error 附加字段），调用点零改动即恢复既有语义；HTTP 线上仍是诚实状态码，
 *   对外契约（SDK / 第三方）不受影响。
 *
 * ★ 不收敛的两类，照旧抛出（与改前行为完全一致，刻意保留）：
 *   ① 401：request() 已触发全局清登录态 + 回登录页，调用方必须感知「这次没做成」；
 *   ② 403：本批未翻状态码（历史上就是 403），收敛它等于顺手改掉另一批的口径；
 *   ③ 无结构化体（网络断开、超时、CF/网关 HTML 页）：不是业务失败，不能伪装成 success:false。
 *
 * 用法：`return bizResp(() => request('/api/pay/create', {...}))`
 */
export async function bizResp<T = AdminResp>(fn: () => Promise<T>): Promise<T> {
  try {
    return await fn()
  } catch (e) {
    if (e instanceof ApiError && e.body && e.status !== 401 && e.status !== 403) {
      const b = e.body
      // details 里是后端随错误附带的数据（order_no 等），摊平到顶层保持与旧响应体同形；
      // 摊平顺序：先 details 再信封字段，确保 success/message/code 以错误信封为准不被覆盖。
      const { details, ...rest } = b
      const flat = (details && typeof details === 'object' ? (details as Record<string, unknown>) : {})
      return { ...flat, ...rest, success: false, status: e.status, code: e.code } as unknown as T
    }
    throw e
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