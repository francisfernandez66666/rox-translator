// ============================================================================
// hooks/useChat.tsx — 聊天全局状态（Pinia useChatStore 的 React 实现）
// 行为对齐清单：localStorage 恢复/持久化(chat_msgs_v1, 上限200, >2MB裁50, 持久化剥离
// progress)、selectedLangs(chat_langs)、SSE 发送/停止(占位气泡收尾文案)、健康检查
// （离线后 30×1s 重试循环、可停止）、清空消息。
// ============================================================================

/**
 * hooks/useChat.tsx · 职责说明
 * 聊天全局状态 Hook，提供以下功能：
 * - 消息管理：消息列表的增删改查、持久化到 localStorage
 * - SSE 收发：文本翻译和文件翻译的流式请求、进度回调、中断控制
 * - 健康检查：后端服务状态检测、离线自动重试（30×1s）
 * - 语言选择：目标语言列表的管理和持久化
 */

// 依赖引入：React 基础 Hooks、API（SSE 流式聊天/文件翻译/健康检查）与 ChatMessage 类型
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { chatStream, translateFileStream, healthCheck } from '@/api'
import type { ChatMessage } from '@/types'

// 单会话消息数量上限（超出时从尾部截断）
const MAX_MESSAGES = 200

// 生成消息唯一 id：时间戳 + 随机字符串
function generateId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

// 从 localStorage 恢复历史消息；数据损坏或不存在时返回空数组
function loadMsgs(): ChatMessage[] {
  try {
    const raw = localStorage.getItem('chat_msgs_v1')
    if (raw) {
      const arr = JSON.parse(raw) as ChatMessage[]
      if (Array.isArray(arr)) return arr.slice(-MAX_MESSAGES)
    }
  } catch { /* 损坏忽略 */ }
  return []
}

// 从 localStorage 恢复目标语言列表；解析失败时使用默认值 ["en"]
function loadLangs(): string[] {
  try { return JSON.parse(localStorage.getItem('chat_langs') || '["en"]') } catch { return ['en'] }
}

// ChatContext 对外暴露的状态与方法类型
interface ChatCtx {
  messages: ChatMessage[]
  isLoading: boolean
  selectedLangs: string[]
  setSelectedLangs: (l: string[]) => void
  isBackendOnline: boolean
  isBackendLoading: boolean
  isBackendChecking: boolean
  errorMessage: string
  sendMessage: (text: string, options?: Record<string, unknown>) => Promise<void>
  sendFile: (file: File, langs?: string[], userMessage?: string, maxLength?: number) => Promise<void>
  stopGeneration: () => void
  clearMessages: () => void
  retryHealth: () => Promise<void>
}

// 内部 Context 实例；null 默认值用于检测是否在 Provider 内使用
const Ctx = createContext<ChatCtx | null>(null)

/** 聊天全局状态 Provider：封装消息列表、目标语言、后端健康检查、SSE 发送与停止等生命周期
 * @param children - 需要访问聊天上下文的子组件树
 */
export function ChatProvider({ children }: { children: ReactNode }) {
  // 聊天消息列表（从 localStorage 恢复）
  const [messages, setMessages] = useState<ChatMessage[]>(loadMsgs)
  // 是否正在生成 AI 回复
  const [isLoading, setLoading] = useState(false)
  // 当前选中的目标语言列表
  const [selectedLangs, setLangs] = useState<string[]>(loadLangs)
  // 后端服务是否在线
  const [isBackendOnline, setOnline] = useState(false)
  // 首屏是否仍在检查后端健康
  const [isBackendLoading, setBLoading] = useState(true)
  // 是否正在手动重试健康检查
  const [isBackendChecking, setChecking] = useState(false)
  // 最近一次发送失败的错误提示
  const [errorMessage, setError] = useState('')
  // 当前 SSE 请求的 AbortController，用于中断生成
  const abortRef = useRef<AbortController | null>(null)
  // 健康检查离线重试循环的停止标志
  const healthStopRef = useRef(false)

  // persistChat：消息与语言写回（进度字段剥离；>2MB 裁到最近 50 条）——节流防流式期间抖动
  const persistTimer = useRef<number | null>(null)
  const schedulePersist = useCallback((msgs: ChatMessage[], langs: string[]) => {
    if (persistTimer.current) window.clearTimeout(persistTimer.current)
    persistTimer.current = window.setTimeout(() => {
      try {
        let arr = msgs
        if (arr.length > 50 && JSON.stringify(arr).length > 2 * 1024 * 1024) arr = arr.slice(-50)
        localStorage.setItem('chat_msgs_v1', JSON.stringify(arr.map((m) => ({ ...m, progress: undefined }))))
      } catch { /* 存储满静默 */ }
      localStorage.setItem('chat_langs', JSON.stringify(langs))
    }, 400)
  }, [])

  // 追加一条消息并在尾部截断，同时触发持久化
  const pushMsg = useCallback((m: ChatMessage) => {
    setMessages((prev) => {
      const next = [...prev, m].slice(-MAX_MESSAGES)
      schedulePersist(next, selectedLangs)
      return next
    })
  }, [schedulePersist, selectedLangs])

  // 按 id 局部更新某条消息的字段
  const patchMsg = useCallback((id: string, patch: Partial<ChatMessage>) => {
    setMessages((prev) => prev.map((m) => (m.id === id ? { ...m, ...patch } : m)))
  }, [])

  // 健康检查：首查 + 离线重试循环（30×1s，可被新一次成功打断）
  const runHealth = useCallback(async (): Promise<boolean> => {
    try {
      await healthCheck()
      return true
    } catch { return false }
  }, [])

  // 首屏健康检查：后端离线时每 1s 重试最多 30 次；卸载或重试成功即停止
  useEffect(() => {
    let alive = true
    ;(async () => {
      const ok = await runHealth()
      if (!alive) return
      setOnline(ok)
      setBLoading(!ok)
      if (!ok) {
        healthStopRef.current = false
        for (let i = 0; i < 30 && alive && !healthStopRef.current; i++) {
          await new Promise((r) => setTimeout(r, 1000))
          if (await runHealth()) { if (alive) { setOnline(true); setBLoading(false) } break }
        }
      }
    })()
    return () => { alive = false; healthStopRef.current = true }
  }, [runHealth])

  // 手动重试健康检查，更新在线状态与加载态
  const retryHealth = useCallback(async () => {
    setChecking(true)
    const ok = await runHealth()
    setOnline(ok)
    setBLoading(!ok)
    setChecking(false)
  }, [runHealth])

  // 设置目标语言并持久化到 localStorage
  const setSelectedLangs = useCallback((l: string[]) => {
    setLangs(l)
    schedulePersist(messages, l)
  }, [messages, schedulePersist])

  // 公共发送骨架：占位 AI 气泡 + SSE 进度回填
  const startSend = useCallback(async (
    userText: string,
    invoke: (assistantId: string, signal: AbortSignal) => Promise<void>,
  ) => {
    if (isLoading) return
    setError('')
    abortRef.current = new AbortController()
    const userMsg: ChatMessage = { id: generateId(), role: 'user', content: userText.trim(), timestamp: Date.now() }
    const assistantId = generateId()
    const assistantMsg: ChatMessage = { id: assistantId, role: 'assistant', content: '', skill: '', timestamp: Date.now(), progress: { step: '准备中', percent: 0 } }
    setMessages((prev) => [...prev, userMsg, assistantMsg].slice(-MAX_MESSAGES))
    setLoading(true)
    try {
      await invoke(assistantId, abortRef.current.signal)
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e)
      if (msg !== 'AbortError' && !String(e).includes('abort')) {
        setError(msg)
        patchMsg(assistantId, { content: `❌ ${msg}`, progress: undefined })
      }
    } finally {
      setLoading(false)
      abortRef.current = null
    }
  }, [isLoading, patchMsg])

  // ★ 双模式：快速/专业校对随请求透传（localStorage 记忆，默认 pro）——对齐 Vue store.sendMessage
  // 读取当前翻译模式：fast（快速）或 pro（专业），默认 professional
  const currentMode = (): string => localStorage.getItem('translate_mode') || 'pro'

  // sendMessage 发送文本翻译：未指定模式时透传当前翻译模式（默认 pro），以 SSE 流式回填助手气泡
  const sendMessage = useCallback((text: string, options: Record<string, unknown> = {}) => {
    if (!text.trim()) return Promise.resolve()
    const opts = { ...options }
    if (!('mode' in opts)) opts.mode = currentMode()
    return startSend(text, async (assistantId, signal) => {
      const res = await chatStream(text, 'translation', opts, (ev) => {
        if (ev.type === 'progress') patchMsg(assistantId, { progress: { step: ev.step || '', percent: ev.percent ?? 0 } })
      }, signal)
      patchMsg(assistantId, { ...res, progress: undefined } as Partial<ChatMessage>)
    })
  }, [startSend, patchMsg])

  // ★ 对齐 Vue：sendFile(file, langs?, userMessage?, maxLength?)，langs 优先于 selectedLangs 透传后端
  const sendFile = useCallback((file: File, langs?: string[], userMessage = '', maxLength = 0) => {
    const label = userMessage || file.name
    const targetLangs = langs && langs.length > 0 ? langs : selectedLangs
    const mode = currentMode()
    return startSend(label, async (assistantId, signal) => {
      const res = await translateFileStream(file, targetLangs, true, (ev) => {
        if (ev.type === 'progress') patchMsg(assistantId, { progress: { step: ev.step || '', percent: ev.percent ?? 0 } })
      }, signal, userMessage, mode, maxLength)
      patchMsg(assistantId, { ...res, progress: undefined } as Partial<ChatMessage>)
    })
  }, [startSend, patchMsg, selectedLangs])

  // 中断当前 SSE 请求，并为未完成的 AI 气泡设置停止文案
  const stopGeneration = useCallback(() => {
    abortRef.current?.abort()
    abortRef.current = null
    setLoading(false)
    setMessages((prev) => {
      const next = [...prev]
      for (let i = next.length - 1; i >= 0; i--) {
        if (next[i].role === 'assistant') {
          if (!next[i].content) next[i] = { ...next[i], content: '⏹ 生成已停止', progress: undefined }
          else next[i] = { ...next[i], progress: undefined }
          break
        }
      }
      schedulePersist(next, selectedLangs)
      return next
    })
  }, [schedulePersist, selectedLangs])

  // 清空本地消息列表与 localStorage 中的聊天记录
  const clearMessages = useCallback(() => {
    setMessages([])
    localStorage.removeItem('chat_msgs_v1')
  }, [])

  // 聚合所有状态与方法，作为 Provider 值；缓存以减少不必要重渲染
  const value = useMemo<ChatCtx>(() => ({
    messages, isLoading, selectedLangs, setSelectedLangs,
    isBackendOnline, isBackendLoading, isBackendChecking, errorMessage,
    sendMessage, sendFile, stopGeneration, clearMessages, retryHealth,
  }), [messages, isLoading, selectedLangs, setSelectedLangs, isBackendOnline, isBackendLoading,
       isBackendChecking, errorMessage, sendMessage, sendFile, stopGeneration, clearMessages, retryHealth])

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

/** 在函数组件中读取聊天上下文；必须在 <ChatProvider> 内使用，否则抛出错误 */
export function useChat(): ChatCtx {
  const v = useContext(Ctx)
  if (!v) throw new Error('useChat 必须在 <ChatProvider> 内使用')
  return v
}
