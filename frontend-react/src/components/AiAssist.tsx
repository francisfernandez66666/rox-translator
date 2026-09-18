// ============================================================================
// components/AiAssist.tsx — AI 销售/客服常驻挂件
// 常驻范围：除登录/注册页外全站显示（官网落地页/前台工作台/管理后台）。
// 能力：会话引导（欢迎词+快捷提问）、AI 对话、推荐功能入口按钮（深链跳转）、
//       历史恢复（localStorage 会话复用）、离线降级提示。
// 接口：api/assist.ts（ai-assist 独立服务，同源 /assist-api 反代）
//
// 动效（2026-09-18 接入 css/motion.css）：
//   - 面板从 FAB 方向（右下 origin）放大登场 200ms，收起 180ms 快退（原则 7：先有锚点再有面板）
//   - 气泡按「离最新一条越近越晚」的固定节拍现身，新消息立即到位不等待
//   - 等待态改成三枚等速圆点（原则 8：三次以内等速），不再是一句半透明文字
//   - FAB 按压 scale(.92) / 悬浮 1.06，白圆内是 22px 线性图标（原来是空圆）
// ============================================================================

import { useCallback, useEffect, useRef, useState } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import {
  assistChat, assistGreet, assistHistory,
  getAssistSid, setAssistSid, setAssistTok,
  type AssistAction, type AssistChatResp, type AssistMsg,
} from '@/api/assist'
import { Icon, ArrowRightIcon } from '@/ui/langcross/src'
import { useT } from '@/i18n'

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

/** 面板收起时长（与 .na-panel--out 的动画时长必须一致，否则会闪帧） */
const OUT_MS = 180

// AiAssist AI 销售/客服常驻挂件主组件：FAB 悬浮球 + 对话面板，展开时拉引导/恢复历史。
export default function AiAssist() {
  // t 只用于取词（不关心当前语言值）：挂件文案此前硬编码中文，英文站整块漏翻，故统一走词典
  const [, t] = useT()
  const location = useLocation()
  const navigate = useNavigate()
  const open = !isHiddenPath(location.pathname)
  const [expanded, setExpanded] = useState(false)
  const [closing, setClosing] = useState(false)
  const [bubbles, setBubbles] = useState<Bubble[]>([])
  const [chips, setChips] = useState<string[]>([])
  const [input, setInput] = useState('')
  const [busy, setBusy] = useState(false)
  const [offline, setOffline] = useState(false)
  const sidRef = useRef('')
  const listRef = useRef<HTMLDivElement>(null)
  const initedRef = useRef(false)
  const outTimerRef = useRef<number | null>(null)

  // 滚动到底部
  const scrollBottom = useCallback(() => {
    requestAnimationFrame(() => {
      if (listRef.current) listRef.current.scrollTop = listRef.current.scrollHeight
    })
  }, [])

  // 收起：先播退场动画，时长到点再卸载（不等动画结束就卸载会看不到收起）
  const collapse = useCallback(() => {
    if (outTimerRef.current !== null) return
    setClosing(true)
    outTimerRef.current = window.setTimeout(() => {
      outTimerRef.current = null
      setExpanded(false)
      setClosing(false)
    }, OUT_MS)
  }, [])

  // 卸载兜底：清掉未触发的收起定时器，否则路由切换（挂件被摘）后仍会回调 setState 报警告
  useEffect(() => () => { if (outTimerRef.current !== null) window.clearTimeout(outTimerRef.current) }, [])

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
        // 新会话：拉欢迎词 + chips（★ P0-1：落存服务端下发的能力令牌 tok，后续 history/chat 随带）
        const g = await assistGreet(location.pathname + location.search)
        sidRef.current = g.session
        setAssistSid(g.session)
        setAssistTok(g.tok || '')
        setChips(g.chips || [])
        setBubbles([{ role: 'assistant', content: g.greeting }])
        setOffline(false)
        scrollBottom()
      } catch {
        setOffline(true)
        setBubbles([{ role: 'assistant', content: t('chat.assistOffline') }])
      }
    })()
  }, [expanded, location.pathname, location.search, scrollBottom, t])

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
        setAssistTok(g.tok || '')
        setChips(g.chips || [])
      }
      let rep: AssistChatResp
      try {
        rep = await assistChat(sid, msg, location.pathname + location.search)
      } catch (e) {
        // ★ P0-1 自愈：令牌失效（401）→ 重新 greet 拿新 sid+tok 后重发一次
        if (!String((e as Error)?.message || '').includes('401')) throw e
        const g = await assistGreet(location.pathname)
        sid = g.session
        sidRef.current = sid
        setAssistSid(sid)
        setAssistTok(g.tok || '')
        rep = await assistChat(sid, msg, location.pathname + location.search)
      }
      setBubbles((b) => [...b, { role: 'assistant', content: rep.reply, actions: rep.actions }])
      setOffline(false)
    } catch {
      setOffline(true)
      setBubbles((b) => [...b, { role: 'assistant', content: t('chat.assistSendFail') }])
    } finally {
      setBusy(false)
      scrollBottom()
    }
  }, [busy, location.pathname, location.search, scrollBottom, t])

  // 动作按钮点击：route 类型走 SPA 跳转并收起面板，link 新窗口
  const doAction = useCallback((a: AssistAction) => {
    if (a.ftype === 'route' && ROUTE_PATHS.has(a.url)) {
      collapse()
      navigate(a.url)
    } else {
      window.open(a.url, '_blank', 'noopener')
    }
  }, [navigate, collapse])

  // 隐藏路径直接不渲染（登录/注册页）
  if (!open) return null

  return (
    <>
      {/* 样式：作用域类名 na-*，避免与全站样式冲突 */}
      <style>{`
        .na-fab{position:fixed;right:22px;bottom:22px;z-index:99990;width:56px;height:56px;border-radius:50%;
          border:none;cursor:pointer;background:#E7E9EA;color:#000;display:flex;align-items:center;justify-content:center;
          box-shadow:0 6px 20px rgba(231,233,234,.16)}
        .na-fab:hover{transform:scale(1.06)}
        .na-panel{position:fixed;right:22px;bottom:88px;z-index:99991;width:380px;max-width:calc(100vw - 24px);
          height:min(620px,78vh);background:#0E1014;border-radius:16px;box-shadow:0 12px 48px rgba(0,0,0,.5);
          display:flex;flex-direction:column;overflow:hidden;border:1.2px solid #464C58;
          --lc-mo-origin:100% 100%;
          animation:lc-mo-pop 200ms cubic-bezier(.16,1,.3,1) both}
        .na-panel--out{animation:lc-mo-pop-out ${OUT_MS}ms cubic-bezier(.4,0,.2,1) both}
        /* 头部改为深色 + 1px 分隔线：纯黑体系里的浮层不出现整块白条 */
        .na-head{background:#16181C;color:#E7E9EA;padding:12px 14px;display:flex;align-items:center;gap:10px;
          border-bottom:1.2px solid #2A2F3A}
        .na-head-ic{width:28px;height:28px;border-radius:9px;background:#0A0B0D;border:1.2px solid #464C58;
          display:flex;align-items:center;justify-content:center;color:#E7E9EA;flex:none}
        .na-head .na-sub{font-size:11px;color:#71767B;line-height:1.3}
        .na-close{margin-left:auto;background:none;border:1.2px solid #464C58;color:#9AA0AA;
          width:26px;height:26px;border-radius:8px;cursor:pointer;display:flex;align-items:center;justify-content:center;flex:none}
        .na-list{flex:1;overflow-y:auto;padding:14px 12px;background:#0E1014;display:flex;flex-direction:column;gap:12px}
        .na-row{display:flex}
        .na-row.me{justify-content:flex-end}
        .na-bubble{max-width:82%;padding:9px 12px;border-radius:12px;font-size:13.5px;line-height:1.65;white-space:pre-wrap;word-break:break-word}
        .na-row.ai .na-bubble{background:#0A0B0D;border:1.2px solid #31363D;color:#C8CCD1;border-top-left-radius:4px}
        .na-row.me .na-bubble{background:#E7E9EA;color:#000;border-top-right-radius:4px}
        .na-acts{display:flex;flex-wrap:wrap;gap:6px;margin-top:8px}
        /* 推荐入口：深底面板上必须是浅字浅描边（原来 #0A0B0D 文字在 #0E1014 底上等于不可见） */
        .na-act{display:inline-flex;align-items:center;gap:5px;border:1.2px solid #464C58;background:#16181C;
          color:#C8CCD1;border-radius:999px;padding:4px 11px;font-size:12px;cursor:pointer;white-space:nowrap;
          font-family:inherit}
        .na-act:hover{background:#E7E9EA;border-color:#E7E9EA;color:#000}
        .na-chips{display:flex;flex-wrap:wrap;gap:6px;padding:10px 12px;border-top:1.2px solid #2A2F3A;background:#0E1014}
        .na-chip{border:1.2px dashed #464C58;background:transparent;color:#9AA0AA;border-radius:999px;padding:4px 11px;
          font-size:12px;cursor:pointer;font-family:inherit}
        .na-chip:hover{border-color:#5A6270;color:#E7E9EA}
        .na-input{display:flex;gap:8px;padding:10px 12px;border-top:1.2px solid #2A2F3A;background:#0E1014}
        .na-input input{flex:1;background:#0A0B0D;border:1.2px solid #5A6270;border-radius:10px;padding:8px 12px;
          font-size:13px;outline:none;color:#E7E9EA;font-family:inherit}
        .na-input input::placeholder{color:#71767B}
        .na-send{border:none;background:#E7E9EA;color:#000;border-radius:10px;padding:8px 18px;cursor:pointer;font-size:13px;font-family:inherit}
        .na-send:disabled{opacity:.42;cursor:not-allowed}
        .na-offline{font-size:11px;color:#D29922;background:rgba(210,153,34,0.10);border:1.2px solid rgba(210,153,34,0.32);border-radius:8px;padding:2px 8px;margin-right:6px}
        .na-offline + .na-close{margin-left:8px}
        @media (max-width:640px){
          .na-panel{right:8px;left:8px;bottom:78px;width:auto;height:min(70vh,560px)}
          .na-fab{right:14px;bottom:14px}
        }
        @media (prefers-reduced-motion: reduce){
          .na-panel{animation:none!important}
          .na-row.lc-mo-up{animation:none!important}
        }
      `}</style>

      {!expanded && (
        <button className="na-fab lc-mo-tap" onClick={() => setExpanded(true)} aria-label={t('chat.assistFabLabel')} title={t('chat.assistFabLabel')}>
          <Icon n="chat" size={22} />
                        </button>
      )}

      {expanded && (
        <div className={`na-panel${closing ? ' na-panel--out' : ''}`}>
          <div className="na-head">
            <span className="na-head-ic"><Icon n="robot" size={16} /></span>
            <div>
              <b>{t('chat.assistTitle')}</b>
              <div className="na-sub">{t('chat.assistSub')}</div>
            </div>
            {offline && <span className="na-offline">{t('chat.offlineBadge')}</span>}
            <button className="na-close lc-mo-tap" onClick={collapse} aria-label={t('chat.assistClose')}><Icon n="close" size={13} /></button>
          </div>

          <div className="na-list" ref={listRef}>
            {bubbles.map((b, i) => {
              // 节拍：离最新一条越近越晚（新消息永远 0 延迟到位，历史消息错落差 60ms）
              const delay = Math.min(Math.max(0, 3 - (bubbles.length - 1 - i)), 3) * 60
  return (
                <div key={i} className={`na-row ${b.role === 'user' ? 'me' : 'ai'} lc-mo-up`}
                     style={{ animationDelay: `${delay}ms` }}>
                <div className="na-bubble">
                  {b.content}
                  {b.role === 'assistant' && !!b.actions?.length && (
                    <div className="na-acts">
                      {b.actions.map((a) => (
                          <button key={a.key} className="na-act lc-mo-press" onClick={() => doAction(a)}>
                            {a.name}<ArrowRightIcon size={12} />
                        </button>
                        ))}
                      </div>
                    )}
                  </div>
                </div>
              )
            })}
            {busy && (
              <div className="na-row ai lc-mo-up">
                <div className="na-bubble" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span className="lc-mo-typing"><i /><i /><i /></span>
                </div>
              </div>
            )}
          </div>

          {chips.length > 0 && (
            <div className="na-chips">
              {chips.map((c) => (
                <button key={c} className="na-chip lc-mo-press" onClick={() => { void send(c) }}>{c}</button>
              ))}
            </div>
          )}

          {/* 回车即发，但 isComposing 时必须放行：中日韩输入法选词的回车不是「发送」，
              否则候选词还没上屏就把半截拼音发出去了 */}
          <div className="na-input">
            <input
              value={input}
              placeholder={t('chat.assistPlaceholder')}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter' && !e.nativeEvent.isComposing) void send(input) }}
              disabled={busy}
            />
            <button className="na-send lc-mo-press" onClick={() => { void send(input) }} disabled={busy || !input.trim()}>{t('chat.send')}</button>
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
