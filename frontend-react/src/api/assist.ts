// ============================================================================
// api/assist.ts — AI 销售/客服助手（独立 ai-assist 服务）前端接口
// 职责：访客会话引导、消息收发、历史恢复（服务端 history + 本地消息缓存双路）、功能入口拉取
// 服务：ai-assist（默认经同源 /assist-api 反代；可用 VITE_ASSIST_API 覆盖）
// ============================================================================

/**
 * 服务基址：默认走同源反代 /assist-api，部署时由 Caddy/nginx 转发到 ai-assist 服务。
 * ★ 2026-09-22：该前缀新增第三条兜底路径——主服务二进制自己转发（internal/api/assist_open_proxy.go，
 *   只放 greeting/chat/history/features 四个访客端点）。此前它只活在 vite dev proxy 与 Caddy 里，
 *   于是「主服务直出 dist」（单二进制本地跑、发布闸门 run_uat）形态下挂件全部落进 SPA 兜底、
 *   拿回一整个 index.html，助手链路在闸门里从未真跑通。
 */
const ASSIST_API = import.meta.env.VITE_ASSIST_API || '/assist-api'
export { ASSIST_API }

/** 会话 ID 本地持久化键（同一浏览器复用会话，保证上下文连续） */
const SID_KEY = 'ny_assist_sid'
/** ★ P0-1（2026-09-18）：会话能力令牌持久化键——history/chat 需 sid+tok 成对提交 */
const TOK_KEY = 'ny_assist_tok'

/** 动作按钮：route=站内路由（SPA 跳转）| link=外链（新窗口） */
export interface AssistAction {
  key: string
  name: string
  url: string
  ftype: string
  icon?: string
}

/** 单条消息（历史恢复用） */
export interface AssistMsg {
  id?: number
  role: 'user' | 'assistant' | string
  content: string
  actions?: AssistAction[] | string
  created_at?: string
}

/** 引导响应：欢迎词 + 会话 id + 能力令牌（P0-1）+ 快捷提问 chips */
export interface AssistGreet {
  session: string
  tok?: string
  greeting: string
  chips?: string[]
}

/** 聊天响应 */
export interface AssistChatResp {
  reply: string
  actions?: AssistAction[]
  model?: string
  source?: string
}

/** 读本地会话 ID */
export function getAssistSid(): string {
  try { return localStorage.getItem(SID_KEY) || '' } catch { return '' }
}

/** 写本地会话 ID（清空时同步清令牌，防半对状态） */
export function setAssistSid(sid: string) {
  try { sid ? localStorage.setItem(SID_KEY, sid) : localStorage.removeItem(SID_KEY) } catch { /* ignore */ }
  if (!sid) setAssistTok('')
}

/** 读本地会话能力令牌 */
export function getAssistTok(): string {
  try { return localStorage.getItem(TOK_KEY) || '' } catch { return '' }
}

/** 写本地会话能力令牌（greet 响应落存） */
export function setAssistTok(tok: string) {
  try { tok ? localStorage.setItem(TOK_KEY, tok) : localStorage.removeItem(TOK_KEY) } catch { /* ignore */ }
}

// ----------------------------------------------------------------------------
// 会话消息本地缓存（★ 2026-09-22 用户反馈「ai 助手要带缓存，刷新一次页面就没了很尴尬」）
// 为什么在服务端 history 之外再加一层本地缓存：history 需要 sid+tok 成对才拿得回来，
// 而 tok 可能失效——历史成因是 sessKey 由管理 Token 派生（换 Token/重启即全线作废），
// 该根因已于 〇-LK 在服务端修掉（sess_key 随机生成并落库，见 backend-go/internal/assist/api），
// 但跨 origin 访问、清理站点数据、服务端不可达等情况仍在，所以缓存作为**即时 + 离线兜底**保留。
// 定位是**先即时出内容**（零等待、断网也能看），服务端 history 回来后再以服务端为准对账；
// 它不替代服务端台账（messages 表仍是权威存储，管理台与审计都读它）。
// ----------------------------------------------------------------------------

/** 会话消息缓存键（存 {sid, msgs}，sid 用于判断这份缓存属于哪个会话） */
const MSG_KEY = 'ny_assist_msgs'
/** 缓存条数上限：挂件是轻量对话，超出只留最近若干条（避免 localStorage 被长会话撑爆） */
const MSG_CAP = 40

/** 缓存里的单条消息：只存渲染气泡需要的字段，服务端下发的 actions 原样保留 */
export interface AssistCacheMsg {
  role: 'user' | 'assistant'
  content: string
  actions?: AssistAction[]
}

/** 读本地消息缓存；无缓存或解析失败一律回空数组（绝不因为缓存坏掉而让挂件白屏） */
export function loadAssistMsgs(): { sid: string; msgs: AssistCacheMsg[] } {
  try {
    const raw = localStorage.getItem(MSG_KEY)
    if (!raw) return { sid: '', msgs: [] }
    const p = JSON.parse(raw) as { sid?: string; msgs?: AssistCacheMsg[] }
    const msgs = Array.isArray(p.msgs)
      ? p.msgs.filter((m) => m && (m.role === 'user' || m.role === 'assistant') && typeof m.content === 'string')
      : []
    return { sid: typeof p.sid === 'string' ? p.sid : '', msgs }
  } catch { return { sid: '', msgs: [] } }
}

/** 写本地消息缓存（按上限截断保留最近若干条；配额异常静默忽略，缓存丢失不影响功能） */
export function saveAssistMsgs(sid: string, msgs: AssistCacheMsg[]): void {
  try {
    localStorage.setItem(MSG_KEY, JSON.stringify({ sid, msgs: msgs.slice(-MSG_CAP) }))
  } catch { /* 隐私模式/配额满：忽略 */ }
}

// ============================================================================
// §4.2-2 正当豁免（2/2）：为什么 ai-assist 这几个请求必须裸用 fetch、
// 且绝不能收敛进统一 client（api/core.request）或复用 handleUnauthorized——
//   1) 不同服务/不同基址：assist 指向独立二进制 ai-assist（默认经同源 /assist-api 反代，
//      可用 VITE_ASSIST_API 覆盖），而 core.request 打的是主后台 API_BASE 并会附带全站
//      JWT(X-Tenant-ID) 认证头；套上去等于把主站登录凭证发给另一个服务，既错又危险。
//   2) 不同鉴权模型：assist 用「会话能力令牌 tok」（sid+tok 成对，greet 下发），与 app 的
//      登录 token 完全解耦。它的 401 只代表「这次会话令牌失效」，自愈方式是重新 greet 换新会话，
//      而不是把用户从整个应用登出——所以只能做「会话级」失效处理，不能复用 401 的 app 级清态。
// 结论：这里保留裸 fetch，但把此前散落、不一致的 401/403 处理统一收口到一个 helper。
// ============================================================================
/**
 * 会话级鉴权失效归一：assist 的 401（令牌过期）/403（会话被拒）都意味着本地 sid+tok 这对着陆失效，
 * 清掉本地会话即可让调用方的「重新 greet」路径自然重建（与 assistHistory 既有 401 处理口径一致）。
 * 刻意不清全站登录态、不改返回值语义（各函数原有的 throw / 返回空数组契约保持不变）。
 */
function noteAssistAuthFailure(status: number) {
  if (status === 401 || status === 403) setAssistSid('') // setAssistSid('') 同步清 tok，避免半对状态
}

/** 开场引导：取欢迎词/会话 id+令牌/快捷提问（幂等；sid+tok 成对提交才复用，令牌缺失服务端会换新会话） */
export async function assistGreet(page = ''): Promise<AssistGreet> {
  const sid = getAssistSid()
  const tok = getAssistTok()
  const q = new URLSearchParams()
  if (sid) q.set('session', sid)
  if (tok) q.set('tok', tok)
  if (page) q.set('page', page)
  const res = await fetch(`${ASSIST_API}/api/assist/greeting?${q.toString()}`)
  noteAssistAuthFailure(res.status) // 401/403：清本地会话，调用方重走 greet（见上方豁免说明）
  if (!res.ok) throw new Error(`greeting ${res.status}`)
  return res.json()
}

/** 发消息：返回 AI 回复 + 推荐功能入口；★ P0-1 需随会话携带能力令牌 tok（401=令牌失效，调用方重新 greet 自愈） */
export async function assistChat(session: string, message: string, page = ''): Promise<AssistChatResp> {
  const res = await fetch(`${ASSIST_API}/api/assist/chat`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session, tok: getAssistTok(), message, page }),
  })
  if (!res.ok) { noteAssistAuthFailure(res.status); throw new Error(`chat ${res.status}`) }
  return res.json()
}

/** 恢复历史消息（刷新后回显）；401 视为令牌失效并清本地会话，返回空数组走重新 greet 路径 */
export async function assistHistory(session: string, limit = 30): Promise<AssistMsg[]> {
  const res = await fetch(`${ASSIST_API}/api/assist/history?session=${encodeURIComponent(session)}&tok=${encodeURIComponent(getAssistTok())}&limit=${limit}`)
  if (res.status === 401) { noteAssistAuthFailure(res.status); return [] }
  if (!res.ok) { noteAssistAuthFailure(res.status); throw new Error(`history ${res.status}`) }
  const data = await res.json() as { messages?: AssistMsg[] }
  return data.messages || []
}

/** 功能入口列表（快捷入口行） */
export async function assistFeatures(): Promise<AssistAction[]> {
  const res = await fetch(`${ASSIST_API}/api/assist/features`)
  if (!res.ok) { noteAssistAuthFailure(res.status); return [] }
  const data = await res.json() as { features?: (AssistAction & { enabled?: number })[] }
  return (data.features || []) as AssistAction[]
}
