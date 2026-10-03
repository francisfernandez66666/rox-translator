// ============================================================================
// components/ChatHistory.tsx — 工作台翻译对话历史记录页
// 左侧：对话列表（按活跃时间降序，点击展开）
// 右侧：选中对话的消息列表（用户/AI 气泡式展示）
// ============================================================================

import { useState, useEffect } from 'react'
import { getChatHistory, getChatMessages, type ChatConv, type ChatMsg } from '@/api/chat'
import { useT } from '@/i18n'
import { Icon } from '@/ui/langcross/src'

// ★ 〇-P 运行时锁细则：描边 1.2px + 字号 +2px。本文件全部走 --lc-* 令牌。

/** ChatHistory 主组件：对话历史列表 + 消息面板。*/
export default function ChatHistory() {
  const [, , tpl] = useT() // [lang, t, tpl] — 只取 tpl 做带参数的模板取词
  const [convos, setConvoes] = useState<ChatConv[]>([])
  const [loading, setLoading] = useState(true)
  const [selectedConv, setSelectedConv] = useState<ChatConv | null>(null)
  const [messages, setMessages] = useState<ChatMsg[]>([])
  const [msgLoading, setMsgLoading] = useState(false)

  // 挂载时拉取对话列表
  useEffect(() => { void loadList() }, [])

  async function loadList() {
    try {
      setLoading(true)
      const resp = await getChatHistory(50)
      if (resp.success && Array.isArray(resp.data)) {
        setConvoes(resp.data as unknown as ChatConv[])
      }
    } catch { /* 加载失败保持空列表 */ }
    finally { setLoading(false) }
  }

  // 选中某个对话 → 加载消息
  async function handleSelect(conv: ChatConv) {
    setSelectedConv(conv)
    try {
      setMsgLoading(true)
      const resp = await getChatMessages(conv.id)
      if (resp.success && Array.isArray(resp.data)) {
        setMessages(resp.data as unknown as ChatMsg[])
      } else {
        setMessages([])
      }
    } catch {
      setMessages([])
    }
    finally { setMsgLoading(false) }
  }

  return (
    <div style={{ height: '100%', display: 'flex', gap: 0 }}>
      {/* 左侧：对话列表 */}
      <ChatListPanel
        convos={convos}
        loading={loading}
        selectedId={selectedConv?.id ?? ''}
        onSelect={handleSelect}
        refresh={loadList}
        tpl={tpl}
      />
      {/* 右侧：消息面板 */}
      <MessagePanel
        conv={selectedConv}
        messages={messages}
        msgLoading={msgLoading}
        tpl={tpl}
      />
    </div>
  )
}

// ---- 左侧面板：对话列表 ----

type tplFn = ReturnType<typeof useT>[2]

// ChatListPanelProps 左侧对话列表的入参。
// convos/loading 由父层统一拉取后下传（列表与消息面板共用同一份刷新时机，
// 各自再发一次请求会出现「刚删完一边还在显示另一边」的错位）；
// selectedId 用字符串比较而非对象引用，父层每次刷新都会重建数组，引用不稳定。
interface ChatListPanelProps {
  convos: ChatConv[]
  loading: boolean
  selectedId: string
  onSelect: (c: ChatConv) => void
  refresh: () => void
  tpl: tplFn
}

// ChatListPanel 左侧对话列表面板：空态、加载中、单条删除入口都在这里出。
// 刻意不做本地过滤——列表的排序与筛选口径以服务端返回为准（两处各排一次必然漂移）。
function ChatListPanel({ convos, loading, selectedId, onSelect, refresh, tpl }: ChatListPanelProps) {

  return (
    <div style={{
      width: 320, minWidth: 260, borderRight: '1.2px solid var(--lc-border-faint)',
      display: 'flex', flexDirection: 'column', background: 'var(--lc-panel)',
    }}>
      {/* 头部 */}
      <div style={{ padding: '16px 16px 8px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span style={{ fontSize: 18, fontWeight: 600, color: 'var(--lc-text-1)' }}>
          {tpl('chat.historyTitle')}
        </span>
        <button onClick={refresh} style={{
          background: 'transparent', border: 'none', cursor: 'pointer', padding: 4,
          color: 'var(--lc-text-2)', transition: 'color 0.15s',
        }} onMouseEnter={e => e.currentTarget.style.color = 'var(--lc-text)'}
                onMouseLeave={e => e.currentTarget.style.color = 'var(--lc-text-2)'}>
          <Icon n="refresh" style={{ fontSize: 14, verticalAlign: '-1px' }} />
        </button>
      </div>
      {/* 列表 */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '4px 8px' }}>
        {loading ? (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--lc-text-2)' }}>
            {tpl('app.loading')}
          </div>
        ) : convos.length === 0 ? (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--lc-text-2)' }}>
            {tpl('chat.empty')}
          </div>
        ) : (
          convos.map(c => (
            <div
              key={c.id}
              onClick={() => onSelect(c)}
              style={{
                padding: '10px 12px', borderRadius: 8, cursor: 'pointer',
                marginBottom: 4,
                background: c.id === selectedId ? 'var(--lc-raised)' : 'transparent',
                border: c.id === selectedId ? '1.2px solid var(--lc-border-done)' : '1.2px solid transparent',
                transition: 'background 0.15s, border 0.15s',
              }}
              onMouseEnter={e => {
                if (c.id !== selectedId) e.currentTarget.style.background = 'rgba(255,255,255,0.04)'
              }}
              onMouseLeave={e => {
                if (c.id !== selectedId) e.currentTarget.style.background = 'transparent'
              }}
            >
              <div style={{ fontSize: 15, color: 'var(--lc-text-1)', whiteSpace: 'nowrap', overflow: 'hidden', textOverflow: 'ellipsis' }}>
                {c.title || tpl('chat.unknown')}
              </div>
              <div style={{ fontSize: 12, color: 'var(--lc-text-2)', marginTop: 2 }}>
                {formatTime(c.updated_at, tpl)}
              </div>
            </div>
          ))
        )}
      </div>
    </div>
  )
}

// ---- 右侧面板：消息展示 ----

interface MessagePanelProps {
  conv: ChatConv | null
  messages: ChatMsg[]
  msgLoading: boolean
  tpl: tplFn
}

// MessagePanel 右侧消息面板：未选中对话时给一句引导，选中后按时间序平铺气泡。
// msgLoading 与 conv 是两件事——conv 有值但消息还在路上时必须显示加载态而不是空态，
// 否则「历史一条都没有」和「还没取到」在界面上长得一样，用户会以为对话被删了。
function MessagePanel({ conv, messages, msgLoading, tpl }: MessagePanelProps) {

  return (
    <div style={{
      flex: 1, display: 'flex', flexDirection: 'column', background: 'var(--npz-page-bg, #000)',
    }}>
      {/* 消息区域 */}
      <div style={{ flex: 1, overflowY: 'auto', padding: '24px 32px' }}>
        {!conv ? (
          <div style={{ textAlign: 'center', paddingTop: 80, color: 'var(--lc-text-2)' }}>
            <Icon n="chat" style={{ fontSize: 48, opacity: 0.3, display: 'block', margin: '0 auto 16px' }} />
            <div style={{ fontSize: 16 }}>{tpl('chat.selectHint')}</div>
          </div>
        ) : msgLoading ? (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--lc-text-2)' }}>
            {tpl('app.loading')}
          </div>
        ) : messages.length === 0 ? (
          <div style={{ textAlign: 'center', padding: 40, color: 'var(--lc-text-2)' }}>
            {tpl('chat.noMsgs')}
          </div>
        ) : (
          messages.map((m, i) => (
            <MessageBubble key={i} msg={m} isLast={i === messages.length - 1} />
          ))
        )}
      </div>
      {/* 底部信息条 */}
      {conv && (
        <div style={{
          padding: '8px 16px', borderTop: '1px solid var(--lc-border-faint)',
          fontSize: 13, color: 'var(--lc-text-2)',
        }}>
          {conv.title} · {messages.length} 条消息
        </div>
      )}
    </div>
  )
}

// ---- 消息气泡 ----

interface MessageBubbleProps {
  msg: ChatMsg
  isLast: boolean
}

// MessageBubble 单条气泡：用户右、助手左，isLast 只用于末尾那条给一点呼吸空间。
// 内容一律按纯文本渲染（模型回的是 Markdown 原文，这里不引渲染器＝少一个 XSS 面）。
function MessageBubble({ msg, isLast }: MessageBubbleProps) {
  const isUser = msg.role === 'user'

  return (
    <div style={{
      display: 'flex',
      justifyContent: isUser ? 'flex-end' : 'flex-start',
      marginBottom: isLast ? 0 : 16,
    }}>
      <div style={{
        maxWidth: '75%', padding: '12px 16px',
        borderRadius: isUser ? '16px 16px 4px 16px' : '16px 16px 16px 4px',
        background: isUser
          ? 'linear-gradient(135deg, rgba(59,130,246,0.3), rgba(99,102,241,0.25))'
          : 'rgba(255,255,255,0.06)',
        border: isUser ? '1.2px solid rgba(59,130,246,0.25)' : '1.2px solid var(--lc-border-faint)',
        color: 'var(--lc-text-1)',
        wordBreak: 'break-word',
      }}>
        <div style={{ whiteSpace: 'pre-wrap', lineHeight: 1.6, fontSize: 15 }}>
          {msg.content || '—'}
        </div>
      </div>
    </div>
  )
}

// ---- 工具函数 ----

/** formatTime 格式化时间戳（相对时间 + 精确到分钟）。*/
function formatTime(ts: string, tpl: tplFn): string {
  if (!ts) return ''
  const d = new Date(ts)
  if (isNaN(d.getTime())) return ts.slice(0, 16)
  const now = new Date()
  const diffMs = now.getTime() - d.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return tpl('chat.now')
  if (diffMin < 60) return `${diffMin} ${tpl('chat.minAgo')}`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr} ${tpl('chat.hrAgo')}`
  const diffDay = Math.floor(diffHr / 24)
  if (diffDay < 7) return `${diffDay} ${tpl('chat.dayAgo')}`
  return d.toLocaleDateString()
}
