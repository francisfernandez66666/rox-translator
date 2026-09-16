// ============================================================================
// components/AiAssist.tsx — AI 销售/客服常驻挂件
// 常驻范围：除登录/注册页外全站显示（官网落地页/前台工作台/管理后台）。
// 能力：会话引导（欢迎词+快捷提问）、AI 对话、推荐功能入口按钮（深链跳转）、
//       历史恢复（localStorage 会话复用）、离线降级提示。
// 接口：api/assist.ts（ai-assist 独立服务，同源 /assist-api 反代）
// ============================================================================

import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  assistChat, assistGreet, assistHistory,
  getAssistSid, setAssistSid,
  type AssistAction, type AssistMsg,
} from '@/api/assist'

/** 站内路由集合：route 类型按钮据此 SPA 跳转（其余按外链新开窗口） */
const ROUTE_PATHS = new Set(['/', '/tickets', '/editor', '/billing', '/packages', '/invites', '/pricing', '/register', '/my', '/admin'])

// Bubble 挂件气泡：单条对话消息（role 区分用户/AI，actions 为推荐功能入口按钮）
interface Bubble {
  role: 'user' | 'assistant'
  content: string
  actions?: AssistAction[]
}

/** 挂件默认隐藏的路径：登录/注册页（导出便于单测与未来路由守卫复用） */
export function isHiddenPath(path: string): boolean {
  return path === '/login' || path === '/register' || path.startsWith('/login/') || path.startsWith('/register/')
}

// AiAssist AI 销售/客服常驻挂件主组件：FAB 悬浮球 + 对话面板，展开时拉引导/恢复历史。
export default function AiAssist() {
  const location = useLocation()
  const navigate = useNavigate()
  const open = !isHiddenPath(location.pathname)
  const [expanded, setExpanded] = useState(false)
  const [bubbles, setBubbles] = useState<Bubble[]>([])
  const [chips, setChips] = useState<string[]>([])
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [offline, setOffline] = useState(false)
  const sidRef = useRef('')
  const listRef = useRef<HTMLDivElement>(null)
  const initedRef = useRef(false)

  // 滚动到底部
  const scrollBottom = useCallback(() => {
    requestAnimationFrame(() => {
      if (listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight
    })
  }, [])

  // 初始化：展开时拉引导/恢复历史（一次）
  useEffect(() => {
    if (!expanded || initedRef.current) return
    initedRef.current = true
    ;(async () => {
      try {
        const sid = getAssistSid()
        if (sid) {
          const msgs = await assistHistory(sid)
          if (msgs.length > 0) {
            setBubbles(msgs.filter((m: AssistMsg) => m.role === 'user' || m.role === 'assistant').map((m) => ({
              role: m.role as 'user' | 'assistant',
              content: m.content,
              actions: typeof m.actions === 'string' ? safeParse(m.actions) : m.actions,
            })))
            sidRef.current = sid
            setOffline(false)
            scrollBottom()
            return
          }
        }
        // 新会话：拉欢迎词 + chips
        const g = await assistGreet(location.pathname + location.search)
        sidRef.current = g.session
        setAssistSid(g.session)
        setChips(g.chips || [])
        setBubbles([{ role: 'assistant', content: g.greeting }])
        setOffline(false)
        scrollBottom()
      } catch {
        setOffline(true)
        setBubbles([{ role: 'assistant', content: '助手暂时联系不上（服务未启动），请稍后再试。你可以先直接使用各个功能页面。' }])
      }
    })()
  }, [expanded, location.pathname, location.search, scrollBottom])

  // 发送消息
  const send = useCallback(async (text: string) => {
    const msg = text.trim()
    if (!msg || busy) return
    setInput('')
    setBubbles((b) => [...b, { role: 'user', content: msg }])
    setBusy(true)
    scrollBottom()
    try {
      let sid = sidRef.current || getAssistSid()
      if (!sid) {
        const g = await assistGreet(location.pathname)
        sid = g.session
        sidRef.current = sid
        setAssistSid(sid)
        setChips(g.chips || [])
      }
      const rep = await assistChat(sid, msg, location.pathname + location.search)
      setBubbles((b) => [...b, { role: 'assistant', content: rep.reply, actions: rep.actions }])
      setOffline(false)
    } catch {
      setOffline(true)
      setBubbles((b) => [...b, { role: 'assistant', content: '这条没发出去（网络/服务异常），再试一次？' }])
    } finally {
      setBusy(false)
      scrollBottom()
    }
  }, [busy, location.pathname, location.search, scrollBottom])

  // 动作按钮点击：route 类型走 SPA 跳转并收起面板，link 新窗口
  const doAction = useCallback((a: AssistAction) => {
    if (a.ftype === 'route' && ROUTE_PATHS.has(a.url)) {
      setExpanded(false)
      navigate(a.url)
    } else {
      window.open(a.url, '_blank', 'noopener')
    }
  }, [navigate])

  // 隐藏路径直接不渲染（登录/注册页）
  if (!open) return null

  return (
    <>
      {/* 样式：作用域类名 na-*，避免与全站样式冲突 */}
      <style>{`
        .na-fab{position:fixed;right:22px;bottom:22px;z-index:99990;width:56px;height:56px;border-radius:50%;
          border:none;cursor:pointer;background:linear-gradient(135deg,#2f47f5,#6a5cff);color:#fff;font-size:26px;
          box-shadow:0 6px 20px rgba(47,71,245,.35);transition:transform .18s}
        .na-fab:hover{transform:scale(1.08)}
        .na-panel{position:fixed;right:22px;bottom:88px;z-index:99991;width:380px;max-width:calc(100vw - 24px);
          height:min(620px,78vh);background:#fff;border-radius:16px;box-shadow:0 12px 48px rgba(0,0,0,.18);
          display:flex;flex-direction:column;overflow:hidden;border:1px solid #eceef2}
        .na-head{background:linear-gradient(135deg,#2f47f5,#6a5cff);color:#fff;padding:12px 16px;display:flex;align-items:center;gap:8px}
        .na-head b{font-size:15px}
        .na-head .na-sub{font-size:11px;opacity:.85}
        .na-close{margin-left:auto;background:rgba(255,255,255,.18);border:none;color:#fff;width:26px;height:26px;
          border-radius:6px;cursor:pointer;font-size:14px}
        .na-list{flex:1;overflow-y:auto;padding:12px;background:#f7f8fb;display:flex;flex-direction:column;gap:10px}
        .na-row{display:flex}
        .na-row.me{justify-content:flex-end}
        .na-bubble{max-width:82%;padding:9px 12px;border-radius:12px;font-size:13.5px;line-height:1.65;white-space:pre-wrap;word-break:break-word}
        .na-row.ai .na-bubble{background:#fff;border:1px solid #e8eaf0;border-top-left-radius:4px}
        .na-row.me .na-bubble{background:#2f47f5;color:#fff;border-top-right-radius:4px}
        .na-acts{display:flex;flex-wrap:wrap;gap:6px;margin-top:7px}
        .na-act{border:1px solid #d8defc;background:#f3f5ff;color:#2f47f5;border-radius:14px;padding:3px 11px;
          font-size:12px;cursor:pointer;white-space:nowrap}
        .na-act:hover{background:#2f47f5;color:#fff}
        .na-chips{display:flex;flex-wrap:wrap;gap:6px;padding:8px 12px;border-top:1px solid #f0f1f5;background:#fff}
        .na-chip{border:1px dashed #c9d2f5;background:#fbfcff;color:#4a5cf0;border-radius:14px;padding:3px 11px;
          font-size:12px;cursor:pointer}
        .na-chip:hover{background:#eef1ff}
        .na-input{display:flex;gap:8px;padding:10px 12px;border-top:1px solid #eceef2;background:#fff}
        .na-input input{flex:1;border:1px solid #dfe3ea;border-radius:10px;padding:8px 12px;font-size:13px;outline:none}
        .na-input input:focus{border-color:#2f47f5}
        .na-send{border:none;background:#2f47f5;color:#fff;border-radius:10px;padding:8px 18px;cursor:pointer;font-size:13px}
        .na-send:disabled{opacity:.5;cursor:not-allowed}
        .na-offline{font-size:11px;color:#c2410c;background:#fff7ed;border:1px solid #fed7aa;border-radius:8px;padding:2px 8px;margin-right:6px}
        @media (max-width:640px){
          .na-panel{right:8px;left:8px;bottom:78px;width:auto;height:min(70vh,560px)}
          .na-fab{right:14px;bottom:14px}
        }
      `}</style>

      {!expanded && (
        <button className="na-fab" onClick={() => setExpanded(true)} aria-label="AI 助手" title="AI 助手">🤖</button>
      )}

      {expanded && (
        <div className="na-panel">
          <div className="na-head">
            <span style={{ fontSize: 20 }}>🤖</span>
            <div>
              <b>能言 AI 助手</b>
              <div className="na-sub">接待 · 指导 · 快速直达功能</div>
            </div>
            {offline && <span className="na-offline">离线</span>}
            <button className="na-close" onClick={() => setExpanded(false)} aria-label="收起">✕</button>
          </div>

          <div className="na-list" ref={listRef}>
            {bubbles.map((b, i) => (
              <div key={i} className={`na-row ${b.role === 'user' ? 'me' : 'ai'}`}>
                <div className="na-bubble">
                  {b.content}
                  {b.role === 'assistant' && !!b.actions?.length && (
                    <div className="na-acts">
                      {b.actions.map((a) => (
                        <button key={a.key} className="na-act" onClick={() => doAction(a)}>
                          {a.icon ? a.icon + ' ' : ''}{a.name} →
                        </button>
                      ))}
                    </div>
                  )}
                </div>
              </div>
            ))}
            {busy && <div className="na-row ai"><div className="na-bubble" style={{ opacity: .6 }}>正在输入…</div></div>}
          </div>

          {chips.length > 0 && (
            <div className="na-chips">
              {chips.map((c) => (
                <button key={c} className="na-chip" onClick={() => { void send(c) }}>{c}</button>
              ))}
            </div>
          )}

          <div className="na-input">
            <input
              value={input}
              placeholder="想了解什么？比如：怎么翻译一份 PDF"
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && !e.nativeEvent.isComposing) void send(input) }}
              disabled={busy}
            />
            <button className="na-send" onClick={() => { void send(input) }} disabled={busy || !input.trim()}>发送</button>
          </div>
        </div>
      )}
    </>
  )
}

/** 安全解析历史消息中的 actions JSON（导出便于单测） */
export function safeParse(s: string): AssistAction[] | undefined {
  try {
    const v = JSON.parse(s)
    return Array.isArray(v) ? v : undefined
  } catch { return undefined }
}
