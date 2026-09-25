// ============================================================================
// hooks/useChat.tsx — 聊天全局状态（★ H8 Zustand 版，路由级实例 store）
// 行为对齐清单：localStorage 恢复/持久化(chat_msgs_v1:<uid>, 上限200, >2MB裁50,
// 持久化剥离 progress)、selectedLangs(chat_langs)、SSE 发送/停止(占位气泡收尾文案)、
// 健康检查（离线后 30×1s 重试循环、可停止）、清空消息。
// 实现说明：ChatProvider 用 zustand vanilla createStore 建「每路由实例一份」的
// 独立 store（避免多实例/测试间串状态），Context 仅传递 store 句柄；
// useChat() 公开签名与旧版一致。
// 2026-09-17/18 纯黑换肤：行为链路未变，只动了两处表现层——
// ① 消息持久化的纯函数（lib/chatStorage：msgsKeyFor/loadMsgs/serializeForPersist）
//    继续从独立模块引入，本文件只负责何时落盘；
// ② 错误气泡文案不再前缀 emoji（见 h6HandleErr 注释）。
// ★ 2026-09-19 B1 流式双态：delta 不再只消费单语言、也不再写进 content 被量尺挡住——
//   逐语言累积原始流并按帧合批写入 message.draft（清洗见 lib/draftClean），
//   done/error/停止统一清空；useChat() 消费端按字段拆分订阅（详见该函数注释）。
// ★ 2026-09-21 #36：即时翻译不再支持文件翻译——本 store 的 sendFile（SSE 文件流）与
//   ChatWindow 的上传入口一并移除；逐段上屏的展示层（MessageBubble 读 message.segments）
//   保留，历史会话里已落盘的逐段数据仍可正常回看。文件翻译走「文档翻译」工单页。
// ★ 2026-09-25 批G：F-11 done 帧后 debounce 2s 经 PkgRefreshCtx 回调顶栏积分刷新；
//   F-29 错误收尾识别网关 HTML 错误页（非 SSE 整页回吐），气泡替换为 chat.timeoutTicket 文案。
// ============================================================================

/**
 * hooks/useChat.tsx · 职责说明
 * 聊天全局状态（Zustand 路由级 store）：
 * - 消息管理：消息列表的增删改查、持久化到 localStorage
 * - SSE 收发：文本翻译的流式请求、进度回调、中断控制
 * - 健康检查：后端服务状态检测、离线自动重试（30×1s）
 * - 语言选择：目标语言列表的管理和持久化
 */

// 依赖引入：React 基础 Hooks、zustand、API（SSE 流式聊天/健康检查）与类型
import { createContext, useContext, useEffect, useMemo, useRef, type ReactNode } from 'react'
import { createStore, useStore } from 'zustand'
import { msgsKeyFor, loadMsgs, serializeForPersist, MAX_MESSAGES } from '@/lib/chatStorage' // ★ F11 持久化纯函数
import { cleanDraft } from '@/lib/draftClean' // ★ B1 流式双态：初译草稿的 <t> 契约清洗
import { chatStream, healthCheck, ApiError } from '@/api'
import { useNavigate, type NavigateFunction } from 'react-router-dom'
import { confirmDialog } from '@/components/uiDialogs'
import { useAuth, useAuthStore, roleLevel } from '@/stores/auth'
import { useAdminStore } from '@/stores/admin'
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
  // ★ F-11：挂载/摘除顶栏积分刷新句柄（ChatProvider 随 PkgRefreshCtx 的提供变化调用）
  setPkgHandle: (h: PkgRefreshHandle | null) => void
  // ★ F-11：取消尚未到点的顶栏刷新定时器（Provider 卸载时清理，防止卸载后 setState 与幽灵请求）
  cancelPkgRefresh: () => void
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

// ★ F-11（批G 2026-09-25）：顶栏积分刷新句柄——可变对象，App 根持有并经 PkgRefreshCtx 下发；
// FrontShell 挂载时把真正的 refreshPkgLine 注册进 .refresh，聊天层 done 帧后延迟 2s 回调它。
// 用「旁挂可变句柄」而非把函数塞进 store：ChatProvider 在 FrontShell 之上，
// 直接传回调会形成「下层注册、上层读取」的时序倒挂，句柄对象则两侧都只拿引用。
export interface PkgRefreshHandle {
  /** 顶栏「当前套餐/积分行」刷新函数；FrontShell 未挂载（如后台路由）时为 null */
  refresh: (() => void) | null
}

// ★ F-11：积分刷新句柄的 Context——Provider 挂在 ChatProvider 之外（App.tsx），
// FrontShell（消费注册方）与 ChatProvider（聊天触发方）都在其子树内。
export const PkgRefreshCtx = createContext<PkgRefreshHandle | null>(null)

// ★ F-11：done 帧后刷新顶栏积分的 debounce 间隔（毫秒）。
// 取 2s：连续多条即时翻译快速完成时合并成一次 myPackage 查询，避免每帧都打顶栏接口。
const PKG_REFRESH_DEBOUNCE_MS = 2000

// ★ F-29（批G 2026-09-25）：判定错误文案是否为「网关/代理回吐的 HTML 错误页」。
// 场景：网关 ~90s 超时返回整页 HTML（Cloudflare 524 之类），api/chatStream 的 !response.ok
// 分支会把响应体原文拼进 Error message（`请求失败 (524): <!DOCTYPE html>...`）。
// 口径：不区分大小写地在消息前 200 字节内找 `<!doctype`——HTML 错误页的 DOCTYPE 声明必然在
// 体首，正常业务错误文案（几十个字）不可能命中；误伤面为零。
function isHtmlErrorBody(msg: string): boolean {
  return msg.slice(0, 200).toLowerCase().includes('<!doctype')
}

// createChatStore 路由级实例工厂：每个 ChatProvider 一份，互不串扰
function createChatStore(msgsKey: string) {
  // ★ F-11：顶栏积分刷新的 debounce 状态——刻意放闭包而非 ChatState：
  // 两者都是纯副作用句柄，进 store 会让每次 schedule/触发都白刷一轮订阅者。
  let pkgHandle: PkgRefreshHandle | null = null
  let pkgRefreshTimer: number | null = null
  // ★ F-11：done 帧后延迟 2s 调顶栏刷新（debounce：窗口内多次 done 只合并成一次刷新，
  // 计时器重置式；句柄未注册（未进工作台）时静默跳过，不打无谓的 myPackage）
  const schedulePkgRefresh = () => {
    if (!pkgHandle?.refresh) return
    if (pkgRefreshTimer !== null) window.clearTimeout(pkgRefreshTimer)
    pkgRefreshTimer = window.setTimeout(() => {
      pkgRefreshTimer = null
      try { pkgHandle?.refresh?.() } catch { /* 顶栏刷新失败不影响聊天收尾 */ }
    }, PKG_REFRESH_DEBOUNCE_MS)
  }
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
    // ★ F-11：注册/摘除顶栏刷新句柄（闭包变量，不进 store state，见工厂顶部注释）
    setPkgHandle: (h) => { pkgHandle = h },
    // ★ F-11：卸载清理——未到点的 debounce 刷新直接作废（Provider 都没了，刷新也没有归属）
    cancelPkgRefresh: () => {
      if (pkgRefreshTimer !== null) { window.clearTimeout(pkgRefreshTimer); pkgRefreshTimer = null }
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
        const next = [...s.messages, userMsg, assistantMsg].slice(-MAX_MESSAGES) // 超上限（200）即裁掉最旧消息，保留尾部
        s.schedulePersist(next, s.selectedLangs) // ★ E1：发送即落盘
        return { messages: next, isLoading: true }
      })
      // ★ B1 流式双态：逐语言 token 增量全量收集 → draft 初译层（取代旧 D20「仅单语言消费」方案——
      //   旧方案把 streamed 写进 content，却被气泡里 progress↔content 互斥开关挡住实际不可见，
      //   且多语言时只有一门能流。现在：按语言累积原始流，最多每帧一次刷入 store（rAF 合帧，
      //   无 rAF 环境退 16ms 定时器；vitest stub 即走此分支），展示前按后端 <t> 契约清洗
      //   （lib/draftClean，口径锚点 postprocess.go）。浑元/熔断/流式失败三条降级路径无 delta
      //   时 draft 为空，UI 自动回退三关量尺。）
      const rawByLang: Record<string, string> = {}
      let flushQueued = false   // 合帧标志：同帧内多条 delta 只触发一次 store 写入
      let streamClosed = false  // 收尾标记：done/stop/error 落定后在途帧不得再回灌 draft
      const scheduleFlush = () => {
        if (flushQueued || streamClosed) return
        flushQueued = true
        const run = () => { flushQueued = false; if (!streamClosed) flushDraft() }
        if (typeof window !== 'undefined' && typeof window.requestAnimationFrame === 'function') {
          window.requestAnimationFrame(run)
        } else {
          setTimeout(run, 16) // 无 rAF 环境（vitest 的 window 桩）退 16ms 定时器，语义等价
        }
      }
      const flushDraft = () => {
        const draft: Record<string, string> = {}
        for (const [lang, raw] of Object.entries(rawByLang)) {
          const cleaned = cleanDraft(raw) // 半截标签已剥除，空进度（契约刚开个头）不占行
          if (cleaned) draft[lang] = cleaned
        }
        get().patchMsg(assistantId, { draft })
      }
      try {
        const res = await chatStream(text, 'translation', opts, (ev) => {
          if (ev.type === 'progress') get().patchMsg(assistantId, { progress: { step: ev.step || '', percent: ev.percent ?? 0 } })
        }, abort.signal, (lang, delta) => {
          rawByLang[lang] = (rawByLang[lang] ?? '') + delta
          scheduleFlush()
        })
        streamClosed = true
        // ★ 修复（2026-09-22）：旧实现整包 `{ ...res }` 把后端 ChatResponse 的 `reply`
        //   原样并进消息，而展示层读的是 `message.content`（ChatMessage 根本没有 reply 字段，
        //   靠 `as Partial<ChatMessage>` 断言绕过 excess-property 检查才没报类型错）。
        //   后果：无 token 增量的降级路径（浑元/熔断/流式失败）done 后 content 仍是建气泡时的
        //   空串 ⇒ 纯文本问答（如「支持哪些语言」）气泡一片空白。故显式 reply→content 映射，
        //   其余结构化字段按名取，不再整包 spread（防止再混入未定义字段污染持久化）。
        get().patchMsg(assistantId, {
          content: res.reply || '',
          skill: res.skill,
          data: res.data,
          files: res.files,
          points_used: res.points_used,
          progress: undefined,
          draft: undefined,
        })
        // ★ F-11：done 即本条已完成扣点（points_used 落进气泡的同时顶栏余额已过期），
        // debounce 2s 后回调 FrontShell 注册的 refreshPkgLine 重拉 myPackage，积分行不再停在旧值。
        schedulePkgRefresh()
      } catch (e) {
        streamClosed = true
        h6HandleErr(get(), assistantId, e)
      } finally {
        streamClosed = true
        set({ isLoading: false, abort: null })
      }
    },

    // 中断当前 SSE 请求，并为未完成的 AI 气泡设置停止文案
    // ★ B1：停止同样清空 draft——草稿是未定稿初译，停在一半的尖括号/残句不适合留存展示
    stopGeneration: () => {
      get().abort?.abort()
      set({ abort: null, isLoading: false })
      set((s) => {
        const next = [...s.messages]
        for (let i = next.length - 1; i >= 0; i--) {
          if (next[i].role === 'assistant') {
            // ★ B3：停止同 done/error 收尾口径——逐段实时行与 draft 一并清空（半途中断的段落不是交付物；
            //   #36 后即时翻译无文件流，此清理对历史会话中残留的 segments 字段同样生效）
            if (!next[i].content) next[i] = { ...next[i], content: gt('chat.stopped'), progress: undefined, draft: undefined, segments: undefined }
            else next[i] = { ...next[i], progress: undefined, draft: undefined, segments: undefined }
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

// 错误码规范化（★ 2026-09-16 P2 整改）：后端存在两套命名口径——业务级大写
// （errors/codes.go：INSUFFICIENT_BALANCE/QUOTA_EXCEEDED）与 OpenAPI/SSE 小写系
// （insufficient_balance/daily_quota_exceeded）。统一折叠到小写规范形再判定，
// 避免大写路径下「余额不足」退化为通用红字、丢失充值引导。
function normErrCode(raw: string | undefined): string {
  if (!raw) return ''
  const k = raw.toLowerCase()
  if (k === 'insufficient_balance') return 'insufficient_balance'
  if (k === 'quota_exceeded' || k === 'daily_quota_exceeded') return 'daily_quota_exceeded'
  return k
}

// SSE 错误收尾（余额不足给充值引导；其余气泡提示）——从 startSend 抽出复用
// ★ B1：三条错误分支同 done/停止口径，收尾一律清空 draft（在途合帧由 streamClosed 挡住）
function h6HandleErr(st: ChatState, assistantId: string, e: unknown) {
  const msg = e instanceof Error ? e.message : String(e)
  if (msg === 'AbortError' || String(e).includes('abort')) return // 用户主动 stop（AbortController）不算错误：直接返回，保留气泡已生成内容与停止文案
  const code = normErrCode(e instanceof ApiError ? e.code : undefined)
  // ★ F-29（批G 2026-09-25）：网关 ~90s 超时回吐的 HTML 错误页会经 chatStream 的 !response.ok
  // 分支把整页正文拼进 Error message（典型 Cloudflare 524：含 `<!DOCTYPE html>` 与「Ray ID」）。
  // 这种响应根本不是 SSE（content-type 非 text/event-stream 的整页 HTML），原文塞进气泡就是一屏
  // 网关错误码小作文——这里在进气泡之前整条替换为超时文案，与后端 90s 超时帧文案对齐；
  // errorMessage 同步换成友好文案，禁止原始 HTML 从顶部提示条二次泄漏。
  if (isHtmlErrorBody(msg)) {
    st.setFlags({ errorMessage: gt('chat.timeoutTicket') })
    st.patchMsg(assistantId, { content: gt('chat.timeoutTicket'), progress: undefined, draft: undefined })
    return
  }
  if (code === 'insufficient_balance') {
    st.patchMsg(assistantId, { content: gt('chat.quotaExhausted'), progress: undefined, draft: undefined })
    void confirmDialog({ header: gt('chat.insufficientTitle'), body: gt('chat.insufficientBody'), confirmText: gt('chat.gotoTopUp') })
      .then((ok) => {
        if (!ok) return
        // ★ 2026-09-16：收银台在后台计费 Hub（/admin，租管+可达）——旧版一律跳
        //   /packages 只读页，对有权付费的租户管理员是死胡同。
        if (roleLevel(useAuthStore.getState().user?.role) >= 3) {
          useAdminStore.getState().gotoPanel('billing')
          st.navigate?.('/admin')
        } else st.navigate?.('/packages')
      })
  } else if (code === 'daily_quota_exceeded') {
    // 日额度超限≠余额耗尽：明日自动重置，充值解决不了，故只透出后端原因（词条带「明日自动恢复」），
    // 不弹充值引导、也不走通用红字兜底，避免把限额说成欠费。
    st.setFlags({ errorMessage: msg })
    st.patchMsg(assistantId, { content: gtpl('chat.dailyQuotaTpl', { msg }), progress: undefined, draft: undefined })
  } else {
    st.setFlags({ errorMessage: msg })
    // 通用失败兜底：气泡正文只放错误文案本身——旧版带 ❌ 前缀，纯黑换肤后不再用
    // emoji 表意（字符串里只剩一个占位空格），错误态由 errorMessage 与顶部提示承担。
    st.patchMsg(assistantId, { content: ` ${msg}`, progress: undefined, draft: undefined })
  }
}

/** ChatStore 单会话 chat store 类型（Context 只传句柄不传状态） */
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
  // ★ F-11：顶栏积分刷新句柄（App 根提供；缺省时聊天层不调刷新，行为同旧版）
  const pkgHandle = useContext(PkgRefreshCtx)
  const storeRef = useRef<ChatStore | null>(null)
  if (storeRef.current === null) storeRef.current = createChatStore(msgsKey)
  const store = storeRef.current

  useEffect(() => { store.getState().bind(navigate) }, [store, navigate])
  // ★ F-11：把句柄交给 store（done 帧后经它回调顶栏刷新）；卸载时先作废在途 debounce
  // 定时器再摘句柄——顺序反过的话，定时器可能抢在句柄置空前对已卸载的 FrontShell 发请求。
  useEffect(() => {
    store.getState().setPkgHandle(pkgHandle ?? null)
    return () => {
      store.getState().cancelPkgRefresh()
      store.getState().setPkgHandle(null)
    }
  }, [store, pkgHandle])
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

/** 在函数组件中读取聊天状态；必须在 <ChatProvider> 内使用，否则抛出错误
 *  ★ B1 订阅收敛：旧版 useStore(store) 无选择器——store 任意切片变化（含流式期间
 *  每 token 一次的 messages 更新）都会让所有 useChat() 消费方整页重渲染。
 *  现按字段拆分订阅：动作函数在 store 创建后永不替换，从 getState() 直取；
 *  消费方仍随 messages 重渲染（这是渲染流式文本的必要条件），但语言偏好、
 *  健康标记等旁路字段不再互相牵连。公开契约 ChatCtx 保持不变。 */
export function useChat(): ChatCtx {
  const store = useContext(Ctx)
  if (!store) throw new Error(gt('chat.providerGuard'))
  const messages = useStore(store, (s) => s.messages)
  const isLoading = useStore(store, (s) => s.isLoading)
  const selectedLangs = useStore(store, (s) => s.selectedLangs)
  const isBackendOnline = useStore(store, (s) => s.isBackendOnline)
  const isBackendLoading = useStore(store, (s) => s.isBackendLoading)
  const isBackendChecking = useStore(store, (s) => s.isBackendChecking)
  const errorMessage = useStore(store, (s) => s.errorMessage)
  // 动作引用稳定（建店时一次性 set），无需参与订阅
  const { setSelectedLangs, sendMessage, stopGeneration, clearMessages, retryHealth } = store.getState()
  return useMemo<ChatCtx>(() => ({
    messages, isLoading, selectedLangs, setSelectedLangs,
    isBackendOnline, isBackendLoading, isBackendChecking, errorMessage,
    sendMessage, stopGeneration, clearMessages, retryHealth,
  }), [messages, isLoading, selectedLangs, isBackendOnline, isBackendLoading, isBackendChecking, errorMessage])
}
