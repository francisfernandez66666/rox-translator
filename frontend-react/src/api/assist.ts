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

/** 引导响应：欢迎词 + 快捷提问 chips */
export interface AssistGreet {
  session: string
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

/** 写本地会话 ID */
export function setAssistSid(sid: string) {
  try { sid ? localStorage.setItem(SID_KEY, sid) : localStorage.removeItem(SID_KEY) } catch { /* ignore */ }
}

/** 开场引导：取欢迎词/会话 id/快捷提问（幂等，复用已有会话） */
export async function assistGreet(page = ''): Promise<AssistGreet> {
  const sid = getAssistSid()
  const q = new URLSearchParams()
  if (sid) q.set('session', sid)
  if (page) q.set('page', page)
  const res = await fetch(`${ASSIST_API}/api/assist/greeting?${q.toString()}`)
  if (!res.ok) throw new Error(`greeting ${res.status}`)
  return res.json()
}

/** 发消息：返回 AI 回复 + 推荐功能入口 */
export async function assistChat(session: string, message: string, page = ''): Promise<AssistChatResp> {
  const res = await fetch(`${ASSIST_API}/api/assist/chat`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session, message, page }),
  })
  if (!res.ok) throw new Error(`chat ${res.status}`)
  return res.json()
}

/** 恢复历史消息（刷新后回显） */
export async function assistHistory(session: string, limit = 30): Promise<AssistMsg[]> {
  const res = await fetch(`${ASSIST_API}/api/assist/history?session=${encodeURIComponent(session)}&limit=${limit}`)
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
