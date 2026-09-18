// ============================================================================
// api/assist.ts — AI 销售/客服助手（独立 ai-assist 服务）前端接口
// 职责：访客会话引导、消息收发、历史恢复、功能入口拉取
// 服务：ai-assist（默认经同源 /assist-api 反代；可用 VITE_ASSIST_API 覆盖）
// ============================================================================

/** 服务基址：默认走同源反代 /assist-api，部署时由 Caddy/nginx 转发到 ai-assist 服务 */
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

/** 开场引导：取欢迎词/会话 id+令牌/快捷提问（幂等；sid+tok 成对提交才复用，令牌缺失服务端会换新会话） */
export async function assistGreet(page = ''): Promise<AssistGreet> {
  const sid = getAssistSid()
  const tok = getAssistTok()
  const q = new URLSearchParams()
  if (sid) q.set('session', sid)
  if (tok) q.set('tok', tok)
  if (page) q.set('page', page)
  const res = await fetch(`${ASSIST_API}/api/assist/greeting?${q.toString()}`)
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
  if (!res.ok) throw new Error(`chat ${res.status}`)
  return res.json()
}

/** 恢复历史消息（刷新后回显）；401 视为令牌失效并清本地会话，返回空数组走重新 greet 路径 */
export async function assistHistory(session: string, limit = 30): Promise<AssistMsg[]> {
  const res = await fetch(`${ASSIST_API}/api/assist/history?session=${encodeURIComponent(session)}&tok=${encodeURIComponent(getAssistTok())}&limit=${limit}`)
  if (res.status === 401) { setAssistSid(''); return [] }
  if (!res.ok) throw new Error(`history ${res.status}`)
  const data = await res.json() as { messages?: AssistMsg[] }
  return data.messages || []
}

/** 功能入口列表（快捷入口行） */
export async function assistFeatures(): Promise<AssistAction[]> {
  const res = await fetch(`${ASSIST_API}/api/assist/features`)
  if (!res.ok) return []
  const data = await res.json() as { features?: (AssistAction & { enabled?: number })[] }
  return (data.features || []) as AssistAction[]
}
