// ============================================================================
// hooks/useChat.tsx — 聊天全局状态（★ H8 Zustand 版，路由级实例 store）
// 行为对齐清单：localStorage 恢复/持久化(chat_msgs_v1:<uid>, 上限200, >2MB裁50,
// 持久化剥离 progress)、selectedLangs(chat_langs)、SSE 发送/停止(占位气泡收尾文案)、
// 健康检查（离线后 30×1s 重试循环、可停止）、清空消息。
// 实现说明：ChatProvider 用 zustand vanilla createStore 建「每路由实例一份」的
// 独立 store（避免多实例/测试间串状态），Context 仅传递 store 句柄；
// useChat() 公开签名与旧版一致。
// ============================================================================

/**
 * hooks/useChat.tsx · 职责说明
 * 聊天全局状态（Zustand 路由级 store）：
 * - 消息管理：消息列表的增删改查、持久化到 localStorage
 * - SSE 收发：文本翻译和文件翻译的流式请求、进度回调、中断控制
 * - 健康检查：后端服务状态检测、离线自动重试（30×1s）
 * - 语言选择：目标语言列表的管理和持久化
 */

// 依赖引入：React 基础 Hooks、zustand、API（SSE 流式聊天/文件翻译/健康检查）与类型
import { createContext, useContext, useEffect, useMemo, useRef, type ReactNode } from 'react'
import { createStore, useStore } from 'zustand'
import { msgsKeyFor, loadMsgs, serializeForPersist, MAX_MESSAGES } from '@/lib/chatStorage' // ★ F11 持久化纯函数
import { chatStream, translateFileStream, healthCheck, ApiError } from '@/api'
import { useNavigate, type NavigateFunction } from 'react-router-dom'
import { confirmDialog } from '@/components/uiDialogs'
import { useAuth } from '@/stores/auth'
import { t as gt, tpl as gtpl } from '@/i18n'
import type { ChatMessage } from '@/types'

// ★ E2：聊天记录存储键按账号隔离（chat_msgs_v1:<uid>）。
// 旧全局键 chat_msgs_v1 无法归属、历史上跨账号可见——一次性清除，不做迁移（避免错误归属他人记录）。
try { localStorage.removeItem('chat_msgs_v1') } catch { /* ignore */ }

// 生成消息唯一 id：时间戳 + 随机字符串
function generateId(): string {
  return Date.now().toString(36) + Math.random().toString(36).slice(2, 8)
}

// 从 localStorage 恢复目标语言列表；解析失败时使用默认值 ["en"]
function loadLangs(): string[] {
  try { return JSON.parse(localStorage.getItem('chat_langs') || '["en"]') } catch { return ['en'] }
}

// ChatCtx 对外暴露的状态与方法类型（公开契约保持不变）
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

// ChatState：zustand store 形状（含非响应式旁挂字段：abort/持久化定时器/导航句柄）
interface ChatState extends ChatCtx {
  msgsKey: string
  navigate: NavigateFunction | null
  abort: AbortController | null
  persistTimer: number | null
  healthStop: boolean
  bind: (nav: NavigateFunction) => void
  switchAccount: (key: string) => void
  patchMsg: (id: string, patch: Partial<ChatMessage>) => void
  schedulePersist: (msgs: ChatMessage[], langs: string[]) => void
  setMessagesNow: (m: ChatMessage[]) => void
  setFlags: (f: Partial<Pick<ChatState, 'isLoading' | 'isBackendOnline' | 'isBackendLoading' | 'isBackendChecking' | 'errorMessage'>>) => void
}

// 从 localStorage 恢复历史消息（解析规则见 lib/chatStorage.loadMsgs）
function loadMsgsStored(msgsKey: string): ChatMessage[] {
  let raw: string | null = null
  try { raw = localStorage.getItem(msgsKey) } catch { return [] }
  return loadMsgs(raw)
}

// createChatStore 路由级实例工厂：每个 ChatProvider 一份，互不串扰
function createChatStore(msgsKey: string) {
  return createStore<ChatState>()((set, get) => ({
    messages: loadMsgsStored(msgsKey),
    isLoading: false,
    selectedLangs: loadLangs(),
    isBackendOnline: false,
    isBackendLoading: true,
    isBackendChecking: false,
    errorMessage: '',
    msgsKey,
    navigate: null,
    abort: null,
    persistTimer: null,
    healthStop: false,

    bind: (nav) => set({ navigate: nav }),
    switchAccount: (key) => {
      set({ msgsKey: key, messages: loadMsgsStored(key) })
    },
    setMessagesNow: (m) => set({ messages: m }),
    setFlags: (f) => set(f),

    // 按 id 局部更新某条消息的字段（★ E1：回填同样触发持久化；进度剥离 + 400ms 防抖）
    patchMsg: (id, patch) => {
      set((s) => {
        const next = s.messages.map((m) => (m.id === id ? { ...m, ...patch } : m))
        get().schedulePersist(next, s.selectedLangs)
        return { messages: next }
      })
    },

    // persistChat：消息与语言写回——节流防流式期间抖动
    schedulePersist: (msgs, langs) => {
      const st = get()
      if (st.persistTimer) window.clearTimeout(st.persistTimer)
      const timer = window.setTimeout(() => {
        try {
          // ★ E2：未登录（anon 键）不落聊天记录，仅记住语言偏好
          const blob = serializeForPersist(get().msgsKey, msgs)
          if (blob !== null) localStorage.setItem(get().msgsKey, blob)
        } catch { /* 存储满静默 */ }
        localStorage.setItem('chat_langs', JSON.stringify(langs))
      }, 400)
      set({ persistTimer: timer })
    },

    setSelectedLangs: (l) => {
      set({ selectedLangs: l })
      get().schedulePersist(get().messages, l)
    },

    retryHealth: async () => {
      set({ isBackendChecking: true })
      const ok = await (async () => { try { await healthCheck(); return true } catch { return false } })()
      set({ isBackendOnline: ok, isBackendLoading: !ok, isBackendChecking: false })
    },

    // 发送骨架（★ H8：文本/文件两通道在 store 动作内自足，占位气泡 + SSE 进度回填）
    sendMessage: async (text: string, options: Record<string, unknown> = {}) => {
      if (!text.trim()) return
      const s0 = get()
      if (s0.isLoading) return
      const opts = { ...options }
      if (!('mode' in opts)) opts.mode = localStorage.getItem('translate_mode') || 'pro'
      s0.setFlags({ errorMessage: '' })
      const abort = new AbortController()
      set({ abort })
      const userMsg: ChatMessage = { id: generateId(), role: 'user', content: text.trim(), timestamp: Date.now() }
      const assistantId = generateId()
      const assistantMsg: ChatMessage = { id: assistantId, role: 'assistant', content: '', skill: '', timestamp: Date.now(), progress: { step: gt('chat.preparing'), percent: 0 } }
      set((s) => {
        const next = [...s.messages, userMsg, assistantMsg].slice(-MAX_MESSAGES)
        s.schedulePersist(next, s.selectedLangs) // ★ E1：发送即落盘
        return { messages: next, isLoading: true }
      })
      try {
        // ★ D20：单目标语言时消费 token 级增量，逐字渲染进助手气泡
        const onlyLang = Array.isArray(opts.target_langs) && opts.target_langs.length === 1 ? String(opts.target_langs[0]) : null
        let streamed = ''
        const res = await chatStream(text, 'translation', opts, (ev) => {
          if (ev.type === 'progress') get().patchMsg(assistantId, { progress: { step: ev.step || '', percent: ev.percent ?? 0 } })
        }, abort.signal, (lang, delta) => {
          if (!onlyLang || lang !== onlyLang) return
          streamed += delta
          get().patchMsg(assistantId, { content: streamed })
        })
        get().patchMsg(assistantId, { ...res, progress: undefined } as Partial<ChatMessage>)
      } catch (e) {
        h6HandleErr(get(), assistantId, e)
      } finally {
        set({ isLoading: false, abort: null })
      }
    },

    sendFile: async (file: File, langs?: string[], userMessage = '', maxLength = 0) => {
      const s0 = get()
      if (s0.isLoading) return
      const label = userMessage || file.name
      const targetLangs = langs && langs.length > 0 ? langs : s0.selectedLangs
      const mode = localStorage.getItem('translate_mode') || 'pro'
      s0.setFlags({ errorMessage: '' })
      const abort = new AbortController()
      set({ abort })
      const userMsg: ChatMessage = { id: generateId(), role: 'user', content: label.trim(), timestamp: Date.now() }
      const assistantId = generateId()
      const assistantMsg: ChatMessage = { id: assistantId, role: 'assistant', content: '', skill: '', timestamp: Date.now(), progress: { step: gt('chat.preparing'), percent: 0 } }
      set((s) => {
        const next = [...s.messages, userMsg, assistantMsg].slice(-MAX_MESSAGES)
        s.schedulePersist(next, s.selectedLangs)
        return { messages: next, isLoading: true }
      })
      try {
        const res = await translateFileStream(file, targetLangs, true, (ev) => {
          if (ev.type === 'progress') get().patchMsg(assistantId, { progress: { step: ev.step || '', percent: ev.percent ?? 0 } })
        }, abort.signal, userMessage, mode, maxLength)
        get().patchMsg(assistantId, { ...res, progress: undefined } as Partial<ChatMessage>)
      } catch (e) {
        h6HandleErr(get(), assistantId, e)
      } finally {
        set({ isLoading: false, abort: null })
      }
    },

    // 中断当前 SSE 请求，并为未完成的 AI 气泡设置停止文案
    stopGeneration: () => {
      get().abort?.abort()
      set({ abort: null, isLoading: false })
      set((s) => {
        const next = [...s.messages]
        for (let i = next.length - 1; i >= 0; i--) {
          if (next[i].role === 'assistant') {
            if (!next[i].content) next[i] = { ...next[i], content: gt('chat.stopped'), progress: undefined }
            else next[i] = { ...next[i], progress: undefined }
            break
          }
        }
        s.schedulePersist(next, s.selectedLangs)
        return { messages: next }
      })
    },

    // 清空本地消息列表与 localStorage 中的聊天记录
    clearMessages: () => {
      set({ messages: [] })
      try { localStorage.removeItem(get().msgsKey) } catch { /* ignore */ }
    },
  }))
}

// SSE 错误收尾（余额不足给充值引导；其余气泡提示）——从 startSend 抽出复用
function h6HandleErr(st: ChatState, assistantId: string, e: unknown) {
  const msg = e instanceof Error ? e.message : String(e)
  if (msg === 'AbortError' || String(e).includes('abort')) return
  const code = e instanceof ApiError ? e.code : undefined
  if (code === 'insufficient_balance') {
    st.patchMsg(assistantId, { content: gt('chat.quotaExhausted'), progress: undefined })
    void confirmDialog({ header: gt('chat.insufficientTitle'), body: gt('chat.insufficientBody'), confirmText: gt('chat.gotoTopUp') })
      .then((ok) => { if (ok) st.navigate?.('/packages') })
  } else if (code === 'daily_quota_exceeded') {
    st.setFlags({ errorMessage: msg })
    st.patchMsg(assistantId, { content: gtpl('chat.dailyQuotaTpl', { msg }), progress: undefined })
  } else {
    st.setFlags({ errorMessage: msg })
    st.patchMsg(assistantId, { content: `❌ ${msg}`, progress: undefined })
  }
}

type ChatStore = ReturnType<typeof createChatStore>

// Context 只传 store 句柄（不传状态）——zustand 官方多实例模式
const Ctx = createContext<ChatStore | null>(null)

/** 聊天全局状态 Provider：创建路由级 zustand 实例并编排副作用
 * @param children - 需要访问聊天上下文的子组件树
 */
export function ChatProvider({ children }: { children: ReactNode }) {
  const { user } = useAuth()
  const msgsKey = msgsKeyFor(user?.id)
  const navigate = useNavigate()
  const storeRef = useRef<ChatStore | null>(null)
  if (storeRef.current === null) storeRef.current = createChatStore(msgsKey)
  const store = storeRef.current

  useEffect(() => { store.getState().bind(navigate) }, [store, navigate])
  // 切换账号（含登录/登出）时重新加载对应键的记录——不读取上一账号的残留
  useEffect(() => { store.getState().switchAccount(msgsKey) }, [store, msgsKey])

  // 首屏健康检查：后端离线时每 1s 重试最多 30 次；卸载或重试成功即停止
  useEffect(() => {
    let alive = true
    const runHealth = async (): Promise<boolean> => {
      try { await healthCheck(); return true } catch { return false }
    }
    ;(async () => {
      const ok = await runHealth()
      if (!alive) return
      store.getState().setFlags({ isBackendOnline: ok, isBackendLoading: !ok })
      if (!ok) {
        store.getState().healthStop = false
        for (let i = 0; i < 30 && alive && !store.getState().healthStop; i++) {
          await new Promise((r) => setTimeout(r, 1000))
          if (await runHealth()) { if (alive) store.getState().setFlags({ isBackendOnline: true, isBackendLoading: false }); break }
        }
      }
    })()
    return () => { alive = false; store.getState().healthStop = true }
  }, [store])

  return <Ctx.Provider value={store}>{children}</Ctx.Provider>
}

/** 在函数组件中读取聊天状态；必须在 <ChatProvider> 内使用，否则抛出错误 */
export function useChat(): ChatCtx {
  const store = useContext(Ctx)
  if (!store) throw new Error(gt('chat.providerGuard'))
  const s = useStore(store)
  return useMemo<ChatCtx>(() => ({
    messages: s.messages, isLoading: s.isLoading, selectedLangs: s.selectedLangs,
    setSelectedLangs: s.setSelectedLangs,
    isBackendOnline: s.isBackendOnline, isBackendLoading: s.isBackendLoading,
    isBackendChecking: s.isBackendChecking, errorMessage: s.errorMessage,
    sendMessage: s.sendMessage, sendFile: s.sendFile,
    stopGeneration: s.stopGeneration, clearMessages: s.clearMessages, retryHealth: s.retryHealth,
  }), [s])
}
